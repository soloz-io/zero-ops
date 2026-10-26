#!/usr/bin/env bash
# Logic lives in the sibling .py. run.sh discovers only *.sh, so a python module
# without this wrapper is a check nobody runs -- which is the failure mode these
# checks exist to prevent, applied to themselves.
validate_bundle_provenance() {
    section "Published bundles record their source"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 "$VALIDATE_DIR/preflight/42-bundle-provenance.py") || true

    if [[ -z "$out" ]]; then
        hard_fail "42-bundle-provenance produced no output"
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
