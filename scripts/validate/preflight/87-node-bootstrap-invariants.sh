#!/usr/bin/env bash
# Every node template that re-asserts kubelet flags must set providerID.
#
# CAPI matches Machines to Nodes BY .spec.providerID and consults nothing else. A
# Node that is Ready and serving traffic but carries no providerID is, to CAPI, a
# Machine that never joined: empty NODENAME, and a roll that waits forever reporting
# NodeHealthy=False/NodeProvisioning -- which names a provisioning step rather than a
# missing field, and sends everyone to the wrong place.
#
# kubelet sets it itself here, from the node's metadata service, because once
# --node-ip is a tailnet address the Hetzner CCM refuses to initialise the node and
# so never writes it. That makes one block of shell embedded in YAML the only thing
# that sets it -- and it was present in seven of eight copies. The eighth was the
# spoke control-plane template, and a replacement control-plane node could not join.
validate_node_bootstrap_invariants() {
    section "Node templates self-assign providerID"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 "$VALIDATE_DIR/preflight/87-node-bootstrap-invariants.py") || true

    if [[ -z "$out" ]]; then
        hard_fail "node bootstrap check produced no output"
        return 0
    fi
    if [[ "$out" == "NONE" ]]; then
        hard_fail "node bootstrap check found no node templates at all; it is looking in the wrong place"
        return 0
    fi

    local line
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        case "$line" in
            OK*)   pass "$(printf '%s' "$line" | cut -f2-)" ;;
            WARN*) warn "$(printf '%s' "$line" | cut -f2-)" ;;
            BAD*)  hard_fail "$(printf '%s' "$line" | cut -f2-)" ;;
            *)     note "$line" ;;
        esac
    done <<< "$out"
}
