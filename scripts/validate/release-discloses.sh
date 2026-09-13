#!/usr/bin/env bash
# A published version must say what taking it means.
#
# ADR-064 moved disclosure from the proposal to the release, so the release is
# where the obligation now lives: a published version records the versions it
# spans from its predecessor, the security fixes among them, whether the
# predecessor is restorable from it, and the minimum version it may be taken
# from.
#
# ADR-069 adds the support window to that list. It is declared when the version
# is published and is a property of the version rather than of any tenant, so a
# release that states none leaves every tenant running it unable to determine
# what the platform still owes them.
#
# Checked because the generator that writes it can fail quietly. It appends to a
# release body, and an append that produced nothing leaves a release that looks
# complete -- a changelog is still there -- while a tenant deciding whether to
# take the version has none of what the decision needs.
set -euo pipefail

TAG="${1:?usage: release-discloses.sh <tag>}"

body=$(gh release view "$TAG" --json body -q .body 2>/dev/null || true)
if [[ -z "$body" ]]; then
    echo "release-discloses: $TAG has no notes at all" >&2
    exit 1
fi

missing=()
grep -q "Spans from" <<<"$body" || missing+=("the versions it spans from its predecessor")
grep -q "Minimum version this may be taken from" <<<"$body" || missing+=("the minimum version it may be taken from")
grep -q "Predecessor restorable from this version" <<<"$body" || missing+=("whether the predecessor is restorable")
grep -qE "Security fixes in this version|No security fixes are declared" <<<"$body" || missing+=("its security-fix position")
# ADR-069. A version published without a window is one whose support nobody can
# determine -- not the tenant deciding whether to move, and not the platform
# being asked whether it still owes anything.
grep -q "Supported until" <<<"$body" || missing+=("the date it is supported until (ADR-069)")
grep -q "Then deprecated until" <<<"$body" || missing+=("the date its deprecation ends (ADR-069)")

# "no earlier release" is true exactly once. Any later release claiming it means
# the predecessor could not be resolved -- which v0.1.7 did, because
# actions/checkout does not fetch tags and the lookup asked git. The disclosure
# was well-formed and wrong, and a well-formed wrong answer is what a structural
# check misses.
if grep -q "no earlier release" <<<"$body"; then
    earlier=$(gh release list --limit 20 --json tagName -q '.[].tagName' 2>/dev/null \
        | grep -v "^${TAG}$" | head -1 || true)
    if [[ -n "$earlier" ]]; then
        missing+=("a real predecessor: it claims no earlier release, but ${earlier} exists")
    fi
fi

if (( ${#missing[@]} > 0 )); then
    echo "release-discloses: $TAG does not record:" >&2
    printf '  %s\n' "${missing[@]}" >&2
    echo >&2
    echo "Renovate surfaces these notes in the proposal it opens, so a tenant" >&2
    echo "deciding whether to take this version would decide without them." >&2
    exit 1
fi

echo "release $TAG: records what taking the version means"
