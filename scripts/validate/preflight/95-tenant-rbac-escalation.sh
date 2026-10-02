#!/usr/bin/env bash
# A fleet may not declare RBAC that escalates out of its namespace (ADR-003 §6,
# ADR-047 Tier 2).
#
# `workloadRbac[].rules` is rendered verbatim into a Role the platform creates, so
# whatever a fleet declares, the platform grants. The chart refuses the dangerous
# declarations at render time and this refuses them at the repository; neither
# replaces the other.
#
# A chart failure arrives as an ArgoCD sync error naming a Helm template, by which
# point the declaration is merged and someone is debugging a deployment instead of
# reading a diff. This is the earlier gate.
#
# Authority to create a workload is deliberately NOT refused: a pod specification may
# mount any Secret in its namespace, so it already implies reading them and cannot be
# withheld from a fleet that must run work. It is bounded by the namespace, which is
# what tenant isolation rests on.
validate_tenant_rbac_escalation() {
    section "Fleet RBAC declares no escalation out of its namespace (ADR-003)"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 "$VALIDATE_DIR/preflight/95-tenant-rbac-escalation.py") || true

    if [[ -z "$out" ]]; then
        hard_fail "tenant RBAC escalation check produced no output"
        return 0
    fi
    if [[ "$out" == "NONE" ]]; then
        warn "no fleet declares workloadRbac — tenant RBAC not checked"
        return 0
    fi

    local line
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        case "$line" in
            OK*)  pass "$(printf '%s' "$line" | cut -f2-)" ;;
            BAD*) hard_fail "$(printf '%s' "$line" | cut -f2-)" ;;
            *)    note "$line" ;;
        esac
    done <<< "$out"
}
