#!/usr/bin/env bash
# ADR-076's escrow list and the implementation must name the same artefacts.
#
# The escrow holds what a box cannot be rebuilt without, and ADR-076's table is the
# only place that set is written down as a set -- the implementation names each
# artefact at the call site that backs it up.
#
# An artefact that belongs in the escrow and is not backed up produces no error and
# no difference in behaviour. The box bootstraps, runs, and behaves identically for
# its whole life; the absence surfaces on the day the cluster is gone, when the
# escrow can no longer be added. That is the same reasoning ADR-076 gives for the
# escrow being mandatory, applied to its contents.
validate_escrow_contents_are_enumerated() {
    section "The escrow holds what ADR-076 says it holds"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 "$VALIDATE_DIR/preflight/91-escrow-contents-are-enumerated.py") || true

    if [[ -z "$out" ]]; then
        hard_fail "escrow contents check produced no output"
        return 0
    fi
    if [[ "$out" == "NONE" ]]; then
        hard_fail "escrow contents check reported nothing; ADR-076 should have produced a verdict"
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
