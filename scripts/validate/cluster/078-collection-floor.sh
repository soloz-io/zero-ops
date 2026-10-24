#!/usr/bin/env bash
# ADR-078 addendum 2 §15: the collection floor is accepted on end-to-end
# evidence, never on a green sync. LIVE checks, read-only, against each
# cluster's OWN store -- the thing a dashboard or alert actually reads.
#
#   C1  each expected job has targets, and every target is up
#   C2  node-local jobs (kubelet, cadvisor, node-exporter) have one up target
#       per node -- a node with no agent is a node nobody is watching
#   C3  a known series from each job is queryable in the cluster's store
#   C4  that series carries the cluster's own `cluster` label (the agent's
#       configured identity), never empty and never a placeholder
#
# A target that is up proves the scrape ran; only C3 proves the series landed
# and can be queried. Each failure names the job, the node or target, and where
# to look.

# Local port for a port-forward to a pod, echoed with the forward's pid. The
# kernel chooses the port (":0"), so it cannot collide with the operator's.
_c078_pf_start() {
    local kubeconfig="$1" pod="$2" logf pf_pid local_port i
    logf="$(mktemp)"
    kubectl --kubeconfig="$kubeconfig" -n platform-observability port-forward "$pod" ":8429" \
        --address 127.0.0.1 >"$logf" 2>&1 &
    pf_pid=$!
    for i in $(seq 1 40); do
        local_port="$(sed -nE 's|^Forwarding from 127\.0\.0\.1:([0-9]+).*|\1|p' "$logf" | head -1)"
        [[ -n "$local_port" ]] && break
        kill -0 "$pf_pid" 2>/dev/null || break
        sleep 0.25
    done
    rm -f "$logf"
    if [[ -z "$local_port" ]]; then
        kill "$pf_pid" 2>/dev/null || true
        return 1
    fi
    echo "$pf_pid $local_port"
}

# _c078_check <label> <kubeconfig> <identity env name> <jobs...>
_c078_check() {
    local label="$1" kubeconfig="$2" idvar="$3"; shift 3
    local jobs="$*" pod pf pf_pid port expected nodes out line

    expected=$(kubectl --kubeconfig="$kubeconfig" -n platform-observability get ds grafana-alloy \
        -o jsonpath="{.spec.template.spec.containers[0].env[?(@.name==\"$idvar\")].value}" 2>/dev/null)
    if [[ -z "$expected" || "$expected" == "PLACEHOLDER" ]]; then
        hard_fail "C4 $label: the node agent's $idvar is '${expected:-<empty>}'; templated-fields did not substitute the cluster's name (manifests/*/grafana-alloy, templated-fields.yaml)"
        return 0
    fi
    nodes=$(kubectl --kubeconfig="$kubeconfig" get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null)
    pod=$(kubectl --kubeconfig="$kubeconfig" -n platform-observability get pods -o name 2>/dev/null | grep -m1 '/vmsingle-')
    if [[ -z "$pod" ]]; then
        hard_fail "C3 $label: no VMSingle pod in platform-observability; there is no store to query"
        return 0
    fi
    if ! pf=$(_c078_pf_start "$kubeconfig" "$pod"); then
        hard_fail "C3 $label: could not port-forward to $pod; the store cannot be queried"
        return 0
    fi
    pf_pid="${pf% *}"; port="${pf#* }"

    out=$(C078_PORT="$port" C078_JOBS="$jobs" C078_CLUSTER="$expected" C078_NODES="$nodes" python3 - <<'PY'
import json, os, urllib.parse, urllib.request

port, cluster = os.environ["C078_PORT"], os.environ["C078_CLUSTER"]
jobs = os.environ["C078_JOBS"].split()
nodes = sorted(n for n in os.environ["C078_NODES"].split() if n)
NODE_LOCAL = {"kubelet", "cadvisor", "node-exporter"}
KNOWN = {
    "kubelet": "kubelet_running_pods",
    "cadvisor": "container_cpu_usage_seconds_total",
    "node-exporter": "node_cpu_seconds_total",
    "kube-state-metrics": "kube_node_info",
    "cnpg": "cnpg_collector_up",
}

def q(expr):
    url = f"http://127.0.0.1:{port}/api/v1/query?" + urllib.parse.urlencode({"query": expr})
    with urllib.request.urlopen(url, timeout=20) as r:
        return json.load(r)["data"]["result"]

for job in jobs:
    up = q(f'up{{job="{job}"}}')
    if not up:
        print(f"FAIL C1 {job}: no targets -- the agent does not scrape it (check the job's discovery in the Alloy config)")
        continue
    down = [r["metric"].get("instance", "?") for r in up if r["value"][1] != "1"]
    if down:
        print(f"FAIL C1 {job}: {len(down)}/{len(up)} target(s) down: {', '.join(down)}")
    else:
        print(f"PASS C1 {job}: {len(up)} target(s) up")
    if job in NODE_LOCAL:
        have = sorted({r["metric"].get("node", "") for r in up if r["value"][1] == "1"})
        missing = [n for n in nodes if n not in have]
        if missing:
            print(f"FAIL C2 {job}: no up target on node(s) {', '.join(missing)} -- is the node agent running there?")
        else:
            print(f"PASS C2 {job}: one up target on each of {len(nodes)} node(s)")
    m = KNOWN[job]
    if not q(f'count({m}{{job="{job}"}})'):
        print(f"FAIL C3 {job}: {m} is not in the store although the job is scraped -- relabelling or the write path drops it")
        continue
    if not q(f'count({m}{{job="{job}",cluster="{cluster}"}})'):
        got = sorted({r["metric"].get("cluster", "") for r in q(f'{m}{{job="{job}"}}')})
        print(f"FAIL C4 {job}: {m} carries cluster={got}, expected {cluster!r}")
    else:
        print(f"PASS C3 {job}: {m} is queryable with cluster={cluster}")
PY
)
    kill "$pf_pid" 2>/dev/null || true

    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        case "$line" in
            PASS\ *) pass "$label ${line#PASS }" ;;
            FAIL\ *) hard_fail "$label ${line#FAIL }" ;;
            *)       note "$line" ;;
        esac
    done <<< "$out"
}

validate_adr078_collection_floor() {
    section "ADR-078: the collection floor lands in each cluster's own store"

    _c078_check hub "$HUB_KUBECONFIG" CLUSTER_NAME kubelet cadvisor node-exporter kube-state-metrics

    if ensure_spoke_kubeconfig; then
        _c078_check spoke "$SPOKE_KUBECONFIG" CELL_ID kubelet cadvisor node-exporter kube-state-metrics cnpg
    else
        soft_fail "no spoke kubeconfig; the spoke collection floor was not checked"
    fi
}
