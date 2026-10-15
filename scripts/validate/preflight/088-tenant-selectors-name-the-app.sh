#!/usr/bin/env bash
# Every pod selector a tenant composition renders names the application (ADR-088).
#
# A renamed composed object leaves its predecessor running, and a selector on the
# component alone picks it up: on 2026-10-15 the cache Service fronted two
# independent Redis servers, and wake-ups were lost at random. Silent at
# runtime -- every connection succeeds, on either server -- so it is caught here.
validate_tenant_selectors_name_the_app() {
    section "Tenant composition selectors name the application (ADR-088)"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 "$VALIDATE_DIR/preflight/088-tenant-selectors-name-the-app.py") || true

    if [[ -z "$out" ]]; then
        hard_fail "tenant selector check produced no output"
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
