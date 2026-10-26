#!/usr/bin/env bash
# Every platform env name a shipped validator REQUIRES must be wired by the
# application charts that mount it (ADR-087, ADR-094 invariant 3).
#
# The requirement lives in packages/auth and the delivery lives in each
# application's own Helm values, one `valueFrom` per name. Nothing compared the
# two, so adding a required name here -- OIDC_ORG_ID in 0.15.0, so the tenant
# could be COMPARED rather than merely required -- silently put every deployed
# application one upgrade away from a pod that will not start.
#
# The runtime error is fine and is not the problem: PlatformConfigError names
# every missing value at once. It just arrives at pod start, on the box, after a
# release. Here it costs a diff.
#
# Application repositories are checked out beside this one; an absent one is
# skipped, because the platform must validate alone.
validate_platform_env_is_delivered() {
    section "Platform env a validator requires is delivered by the app chart (ADR-087)"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 "$VALIDATE_DIR/preflight/92-platform-env-is-delivered.py") || true

    if [[ -z "$out" ]]; then
        hard_fail "platform env delivery check produced no output"
        return 0
    fi
    if [[ "$out" == "NONE" ]]; then
        warn "no application repository checked out beside zero-ops — env delivery not checked"
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
