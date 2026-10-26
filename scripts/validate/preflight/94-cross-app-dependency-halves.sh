#!/usr/bin/env bash
# A cross-application dependency is declared by BOTH sides, or it is not a
# dependency (ADR-088 amendment, ADR-094 Part 2, waypoint ADR-042).
#
# The platform renders both halves from these two declarations -- the caller's
# egress and audience scope, the target's ingress and caller allowlist -- so a
# declaration present on one side only produces a path that is half open.
#
# Why this is a preflight and not a runtime check: both failure shapes are silent
# at runtime. A missing ingress HANGS (Cilium drops without an RST, ADR-046 §30)
# and reads as a slow or down service; a missing caller declaration surfaces only
# when someone writes the calling code, as invalid_target naming an audience.
# Neither names the file that is wrong, and both are cheap to catch here.
validate_cross_app_dependency_halves() {
    section "Cross-application dependencies are declared by both sides (ADR-088)"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 "$VALIDATE_DIR/preflight/94-cross-app-dependency-halves.py") || true

    if [[ -z "$out" ]]; then
        hard_fail "cross-application dependency check produced no output"
        return 0
    fi
    if [[ "$out" == "NONE" ]]; then
        warn "no tenant GitOps checkout found — cross-application declarations not checked"
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
