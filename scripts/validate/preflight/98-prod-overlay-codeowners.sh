#!/usr/bin/env bash
# ADR-037 #6: CODEOWNERS SHALL mandate explicit Platform Engineering review for any
# modification targeting the prod overlay directories.
#
# This fails open, which is why it went unnoticed: an unlisted directory does not
# error, it simply merges without the mandated approval. Only one of the three prod
# overlays was listed, leaving the two spoke overlays unprotected.
#
# Also rejects the /path/* form. In CODEOWNERS that matches files directly in the
# directory only — a subdirectory added later silently loses its owner.
validate_prod_overlay_codeowners() {
    section "Prod overlays require Platform Engineering review (ADR-037 #6)"

    local co="$VALIDATE_ROOT/.github/CODEOWNERS"
    if [[ ! -f "$co" ]]; then
        hard_fail "no .github/CODEOWNERS — prod overlays are modifiable without review"
        return 0
    fi

    local dirs missing=0 d rule
    dirs=$(cd "$VALIDATE_ROOT" && find manifests -type d -name prod 2>/dev/null | sort)

    if [[ -z "$dirs" ]]; then
        pass "no prod overlay directories present"
        return 0
    fi

    while IFS= read -r d; do
        [[ -z "$d" ]] && continue
        # A rule covers the directory if it names it with a recursive form.
        rule=$(grep -vE "^\s*#" "$co" | grep -F "/$d/" || true)
        if [[ -z "$rule" ]]; then
            missing=1
            if grep -vE "^\s*#" "$co" | grep -qF "/$d/*"; then
                hard_fail "/$d is covered only by a /* rule — not recursive; a subdirectory would lose its owner"
            else
                hard_fail "/$d has no CODEOWNERS rule — changes merge without the mandated review"
            fi
        fi
    done <<< "$dirs"

    (( missing )) || pass "every prod overlay directory has a recursive CODEOWNERS rule"
}
