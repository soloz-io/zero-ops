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
