#!/usr/bin/env bash
# ADR-100's acceptance criteria, enforced by structure rather than by test.
#
# Both criteria fail SILENTLY. A non-deterministic key identifier loses no data --
# material written under the previous identifier still unwraps -- it makes the
# identifier meaningless, and with it any ability to tell whether a rotation happened.
# A status path and a wrap path that observe the key store separately disagree only
# during a rotation on a multi-node control plane, which is the one moment nobody is
# watching a unit test.
#
# Tests pin both and can also be deleted or weakened, and neither failure shows up the
# day it is introduced. So the same properties are asserted against the SHAPE of the
# code: the identifier package may not import a clock, randomness, process state or a
# counter, and only the refresh loop may read the key store.
validate_kms_identifier_is_deterministic() {
    section "The KMS key identifier is deterministic, and one snapshot serves both paths"

    local out
    out=$(cd "$VALIDATE_ROOT" && python3 "$VALIDATE_DIR/preflight/100-kms-identifier-is-deterministic.py") || true

    if [[ -z "$out" ]]; then
        hard_fail "KMS identifier check produced no output"
        return 0
    fi
    if [[ "$out" == "NONE" ]]; then
        note "no KMS plugin in this tree yet (ADR-100 not implemented here)"
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
