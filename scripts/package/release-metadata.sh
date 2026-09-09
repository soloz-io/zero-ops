#!/usr/bin/env bash
# Write what a tenant needs to decide whether to take this version.
#
# ADR-064 moves disclosure from the proposal to the release: a published version
# records the versions it spans from its predecessor, the security fixes among
# them, whether each step is operationally reversible, and the minimum version it
# may be taken from. Renovate surfaces release notes in the proposal it opens, so
# writing this here reaches a tenant reading the pull request AND one reading the
# release directly -- which prose generated per proposal would not.
#
# Appended to the release body rather than replacing it: the generated changelog
# is what a maintainer reads, and this is what a tenant reads.
set -euo pipefail

VERSION="${1:?usage: release-metadata.sh <version> [previous]}"
PREVIOUS="${2:-}"

if [[ -z "$PREVIOUS" ]]; then
    # The version before this one, by tag order. A first release has none, which
    # is stated rather than guessed at.
    PREVIOUS=$(git tag --list 'v*' --sort=-v:refname | grep -v "^v${VERSION}$" | head -1 || true)
fi

range=""
if [[ -n "$PREVIOUS" ]]; then
    range="${PREVIOUS}..HEAD"
fi

# A security fix is declared by the commit that makes it, not inferred from
# words in a subject line: inference would either miss one or cry wolf, and both
# make the marker worth ignoring.
security=""
if [[ -n "$range" ]]; then
    security=$(git log "$range" --grep='^security(' --format='- %s' || true)
fi

# Reversibility is declared per release and defaults to unknown rather than to
# reversible. A tenant told a one-way step is reversible discovers otherwise at
# the moment it needs to go back.
reversible="${BUNDLE_REVERSIBLE:-unknown}"
minimum="${BUNDLE_MINIMUM_FROM:-${PREVIOUS#v}}"

{
    echo
    echo "## Taking this version"
    echo
    echo "| | |"
    echo "|---|---|"
    echo "| Spans from | ${PREVIOUS:-no earlier release} |"
    echo "| Minimum version this may be taken from | ${minimum:-any} |"
    echo "| Predecessor restorable from this version | ${reversible} |"
    echo
    if [[ -n "$security" ]]; then
        echo "### Security fixes in this version"
        echo
        echo "$security"
        echo
        echo "ADR-069 places security fixes outside the ordinary cadence. This version"
        echo "is recommended whether or not a tenant intends to move otherwise, and"
        echo "declining it leaves the fixes above unapplied."
    else
        echo "No security fixes are declared in this version."
    fi
    echo
    echo "Nothing here is applied by the platform. A cluster changes when a tenant"
    echo "merges the proposal against its own repository and its own control plane"
    echo "reconciles the result (ADR-065)."
} 
