#!/usr/bin/env bash
# The at-rest encryption key is per cluster, so its delivery object must be too.
#
# ADR-100 decides one key per cluster and ADR-076 escrows it under that cluster's id.
# It reaches a control plane as a Secret named by a ClusterClass -- and a ClusterClass
# is shared by every cluster of its class, so a literal name there is ONE object for
# all of them. That shipped: both classes named `secret-encryption-config` flat, which
# would have given every cluster the same key and made the first rotation destroy the
# rest.
#
# The name is also built twice, in two languages that cannot see each other: Go
# (assets.EncryptionSecretName, which CREATES the Secret) and a CAPI valueFrom.template
# (which is what the control plane READS). Disagreement is not reported as a missing
# Secret; the node just never finishes bootstrapping.
validate_encryption_secret_is_per_cluster() {
    section "The encryption Secret is per cluster, and both halves agree on its name"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 "$VALIDATE_DIR/preflight/003-encryption-secret-is-per-cluster.py") || true

    if [[ -z "$out" ]]; then
        hard_fail "encryption secret check produced no output"
        return 0
    fi
    if [[ "$out" == "NONE" ]]; then
        hard_fail "encryption secret check found no ClusterClass; it is looking in the wrong place"
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
