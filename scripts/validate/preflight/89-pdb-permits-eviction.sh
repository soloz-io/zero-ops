#!/usr/bin/env bash
# A PodDisruptionBudget must not forbid every eviction.
#
# `minAvailable: 1` beside `replicas: 1` means the only pod can never be evicted: the
# eviction API refuses with 429, and `kubectl drain` does not fail on that -- it
# retries, indefinitely. The symptom is not an error but a node that never finishes
# draining, which on a control plane is a roll that cannot complete.
#
# nutgraf-01, 2026-10-05: the v3 roll replaced the control-plane node correctly, the
# new node came up Ready and self-assigned its providerID, and the old Machine then sat
# in Deleting draining kube-system/ccm-ccm-hetzner with nothing to time out. The roll
# had succeeded; it could not finish. It was the upstream ccm-hetzner chart's default,
# rendered verbatim into a ClusterResourceSet addon -- and invisible to an ordinary
# YAML scan, because the PDB is a line inside a Secret's stringData.
validate_pdb_permits_eviction() {
    section "Every PodDisruptionBudget permits at least one eviction"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 "$VALIDATE_DIR/preflight/89-pdb-permits-eviction.py") || true

    if [[ -z "$out" ]]; then
        hard_fail "PDB eviction check produced no output"
        return 0
    fi
    if [[ "$out" == "NONE" ]]; then
        note "no PodDisruptionBudget found in the shipped manifests"
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
