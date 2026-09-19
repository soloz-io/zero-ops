#!/usr/bin/env bash

# Every cluster owns its telemetry. Only the query crosses a boundary.
#
# ADR-083 adopts Grafana's documented cross-cluster query federation pattern: a
# central query layer over independent per-cluster stores. Two properties make it
# that rather than an aggregation design that happens to work today, and both are
# one convenient commit away from being lost:
#
#   * every cluster stores its own data AND evaluates its own rules. Grafana's
#     federation-frontend documents no alerting or ruler support, and
#     VictoriaMetrics' multi-region guide places vmalert regionally against local
#     endpoints -- so a centralised ruler is not a variation on this pattern, it
#     is a different one.
#   * no telemetry crosses a cluster boundary. The moment a spoke remote_writes
#     to the hub, the hub's capacity bounds the fleet's observability and a hub
#     outage loses spoke data rather than merely the view of it -- which is the
#     bottleneck ADR-077 rejected for the platform's own evidence path.
#
# ADR-083 is Proposed and the spoke catalogue does not yet carry a store, so this
# reports as warnings. It becomes a hard failure when ADR-083 is Accepted.

validate_observability_federation_topology() {
    section "Each cluster stores and evaluates its own telemetry (ADR-083)"

    local hub_store="$VALIDATE_ROOT/manifests/hub-core-services/victoriametrics/storage.yaml"
    local spoke_dir="$VALIDATE_ROOT/manifests/spoke/spoke-catalog/infra"

    local issues=0

    # ── The hub carries a store and a ruler ─────────────────────────────────
    if [ ! -f "$hub_store" ]; then
        hard_fail "no hub store at ${hub_store#$VALIDATE_ROOT/}"
        return 0
    fi
    local kind
    for kind in VMSingle VLogs VMAlert; do
        grep -q "kind: $kind" "$hub_store" \
            || { warn "the hub declares no $kind (ADR-083 decision 1)"; (( issues++ )); }
    done

    # ── Every spoke carries the same three ──────────────────────────────────
    # A spoke without its own VMAlert evaluates nothing when the hub is away,
    # which is the property that makes it independently survivable.
    local spoke_store
    spoke_store=$(grep -rl "kind: VMSingle" "$spoke_dir" 2>/dev/null | head -1 || true)
    if [[ -z "$spoke_store" ]]; then
        warn "the spoke catalogue declares no store (ADR-083 decision 1: every cluster runs its own VMSingle, VictoriaLogs and VMAlert)"
        (( issues++ ))
    else
        for kind in VLogs VMAlert; do
            grep -rq "kind: $kind" "$spoke_dir" \
                || { warn "the spoke catalogue declares no $kind (ADR-083 decision 2: alert evaluation stays with the cluster that holds the data)"; (( issues++ )); }
        done
    fi

    # ── No telemetry crosses a cluster boundary ─────────────────────────────
    # A spoke writing to a hub hostname is the aggregation design ADR-083
    # decision 6 rules out. Matched on the derived hub hostname rather than on a
    # product name, so a rename cannot slip past it.
    local crossing
    crossing=$(grep -rlE 'remote_?[Ww]rite' "$spoke_dir" 2>/dev/null \
        | xargs -r grep -lE 'victoriametrics\.hub\.|\.hub\.' 2>/dev/null || true)
    if [[ -n "$crossing" ]]; then
        while IFS= read -r f; do
            [[ -z "$f" ]] && continue
            warn "${f#$VALIDATE_ROOT/} writes spoke telemetry to a hub endpoint (ADR-083 decision 6: data stays in the cluster that produced it)"
            (( issues++ ))
        done <<<"$crossing"
    fi

    if (( issues )); then
        note "ADR-083 is Proposed; these are warnings until it is Accepted."
        return 0
    fi

    pass "hub and spoke each declare a store, logs and a ruler; no telemetry crosses a cluster"
}

# The declarations above are necessary and not sufficient.
#
# Everything before this point reads the REPOSITORY: it proves the manifests
# declare a store, a ruler and no cross-cluster write. It would pass unchanged
# against a cluster where none of it was ever applied -- which is the exact
# shape of defect this build kept hitting (a chart that installs zero CRDs, a
# PVC that never binds, a webhook that never answers; every one of them left the
# Application Synced and Healthy).
#
# So this asks the cluster. ADR-069 grades the promise by what is observable,
# and a capability nobody queried is not observable.
validate_observability_live() {
    section "The telemetry store actually holds telemetry (ADR-078 §3, §6, §7)"

    local kc="${HUB_KUBECONFIG:-${KUBECONFIG:-}}"
    if [[ -z "$kc" || ! -r "$kc" ]]; then
        note "no kubeconfig; the live checks were skipped and the declarations above"
        note "prove only what the repository says"
        return 0
    fi

    local ns=platform-observability

    # 1. The stores exist and the operator reports them operational.
    local vms vlogs
    vms=$(kubectl --kubeconfig="$kc" -n "$ns" get vmsingle platform \
        -o jsonpath='{.status.updateStatus}{.status.singleStatus}' 2>/dev/null || true)
    # VLogs reports under a different status key than VMSingle, and an empty
    # answer here is "the operator has not written one yet", not a failure.
    vlogs=$(kubectl --kubeconfig="$kc" -n "$ns" get vlogs platform \
        -o jsonpath='{.status.updateStatus}{.status.singleStatus}{.status.status}' 2>/dev/null || true)
    [[ -z "$vlogs" ]] && vlogs="(no status yet)"
    if [[ -z "$vms" ]]; then
        warn "no VMSingle/platform on this cluster, or it reports no status (ADR-083 decision 1)"
        note "the manifests declare one; nothing applied it, or the operator has not reconciled it"
        return 0
    fi
    pass "VMSingle=${vms:-?} VLogs=${vlogs:-?}"

    # 2. It answers a query, and the answer is not empty. A store that is
    #    Running and holds nothing looks identical to a healthy one from every
    #    angle except this.
    local port=18429 pf_pid="" out
    kubectl --kubeconfig="$kc" -n "$ns" port-forward "svc/vmsingle-platform" \
        "$port:8429" >/dev/null 2>&1 &
    pf_pid=$!
    local waited=0
    until curl -sf -o /dev/null "http://localhost:$port/health" 2>/dev/null; do
        sleep 1; waited=$((waited+1))
        (( waited > 20 )) && break
    done

    q() {
        curl -s --get --data-urlencode "query=$1" "http://localhost:$port/api/v1/query" 2>/dev/null \
            | python3 -c 'import json,sys
try:
    r=json.load(sys.stdin)["data"]["result"]
    print(r[0]["value"][1] if r else "0")
except Exception:
    print("0")' 2>/dev/null || echo 0
    }

    local n_up n_ksm
    n_up=$(q 'count(up)')
    n_ksm=$(q 'count(kube_pod_info)')

    if [[ "${n_up:-0}" -lt 1 ]]; then
        warn "the store answers but holds no 'up' series -- collection is not reaching it"
        note "Alloy may be writing elsewhere, or its scrape targets resolve to nothing"
    else
        pass "store holds telemetry: up=$n_up kube_pod_info=$n_ksm"
    fi

    # 3. ADR-078 §7 -- every series names the box, not a constant.
    out=$(curl -s --get --data-urlencode 'query=count by (cluster,tenant) (up)' \
        "http://localhost:$port/api/v1/query" 2>/dev/null \
        | python3 -c '
import json, sys
try:
    rs = json.load(sys.stdin)["data"]["result"]
except Exception:
    rs = []
out = []
for r in rs:
    m = r.get("metric", {})
    out.append(m.get("cluster", "") + "/" + m.get("tenant", ""))
print(";".join(out))' 2>/dev/null || true)
    # Each entry is "<cluster>/<tenant>". Both halves must be non-empty, and the
    # cluster must not be the literal "hub" or "spoke" -- those are the constants
    # ADR-078's Context records as the defect, not a box's name.
    #
    # Matched on the WHOLE field rather than a substring: an earlier version
    # tested `== *"hub/"*` and flagged the correct value nutgraf-hub/nutgraf,
    # because a real box name legitimately ends in -hub.
    local bad="" pair c t
    if [[ -z "$out" ]]; then
        warn "no series carries both cluster and tenant labels (ADR-078 add.1 §7)"
    else
        local IFS=';'
        for pair in $out; do
            c="${pair%%/*}"; t="${pair#*/}"
            if [[ -z "$c" || -z "$t" || "$c" == "hub" || "$c" == "spoke" ]]; then
                bad="$bad $pair"
            fi
        done
        unset IFS
        if [[ -n "$bad" ]]; then
            warn "cluster/tenant labels are incomplete or constant:$bad"
            note "ADR-078's Context: a constant makes two boxes indistinguishable at one destination"
        else
            pass "identity carried on every series: $out"
        fi
    fi

    # 4. ADR-078 §6 -- the check worth running on every box.
    #    kube-state-metrics is cluster-scoped, so this is the one collector a
    #    namespace selector cannot bound. A tenant namespace here means the
    #    series-level filter is not doing its job and workload metadata is
    #    leaving the box.
    local leaked
    leaked=$(curl -s --get --data-urlencode 'query=count by (namespace) (kube_pod_info)' \
        "http://localhost:$port/api/v1/query" 2>/dev/null \
        | python3 -c 'import json,sys
try:
    ns=[r["metric"].get("namespace","") for r in json.load(sys.stdin)["data"]["result"]]
except Exception:
    ns=[]
ok=("platform-","cert-manager","cnpg-system","kube-system","kube-node-lease","kube-public")
print(" ".join(sorted(n for n in ns if n and not n.startswith(ok[0]) and n not in ok[1:])))' 2>/dev/null || true)
    if [[ -n "$leaked" ]]; then
        hard_fail "tenant namespaces present in kube-state-metrics series: $leaked"
        note "ADR-078 §3: tenant namespaces are not collected by default, and §6 puts the"
        note "enforcement at the series because this exporter cannot be bounded by target."
        note "Their workload metadata is being written to the store."
    else
        pass "no tenant namespace in the store (ADR-078 §6 holds)"
    fi

    [[ -n "$pf_pid" ]] && kill "$pf_pid" 2>/dev/null || true
    return 0
}
