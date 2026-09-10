#!/usr/bin/env bash
set -euo pipefail

# Verify that the SHA-256 digest in the provenance header matches
# the actual rendered content. Catches:
#   - Manual edits to vendored manifests without re-rendering
#   - Tampered artifacts
#   - Renovate PRs that bump the version but don't re-vendor
#
# The last of those is completed rather than merely refused:
# .github/workflows/revendor-kyverno.yml re-renders on Renovate's branch and
# commits, so a proposal has something to pass. Refusing without that left a
# pull request that could only ever fail, which teaches people to merge past the
# check rather than to trust it.
#
# Usage: bash scripts/validate/verify-vendor-digest.sh

SPOKE_DIR="manifests/spoke/spoke-catalog/infra/kyverno"
FAIL=0

for file in "$SPOKE_DIR"/crd.yaml "$SPOKE_DIR"/controller.yaml; do
  if [ ! -f "$file" ]; then
    echo "ERROR: $file not found"
    FAIL=1
    continue
  fi

  # `|| true` matters: under `set -euo pipefail` a grep that matches nothing
  # kills the script here, before the branch below can say why. A gate that
  # dies without a message is indistinguishable from an infrastructure fault.
  RECORDED_HASH=$(grep '^# sha256:' "$file" | head -1 | sed 's/^# sha256: //' || true)
  if [ -z "$RECORDED_HASH" ]; then
    echo "ERROR: No sha256 digest found in $file"
    FAIL=1
    continue
  fi

  # Hash everything except the provenance header lines (# comments).
  # scripts/renovate/revendor-kyverno.sh writes the digest under this same
  # rule; the two must agree or every re-vendor produces a failing gate.
  ACTUAL_HASH=$(grep -v '^# ' "$file" | sha256sum | cut -d' ' -f1)

  if [ "$RECORDED_HASH" = "$ACTUAL_HASH" ]; then
    echo "OK: $file digest matches"
  else
    echo "FAIL: $file digest mismatch (recorded=$RECORDED_HASH actual=$ACTUAL_HASH)"
    FAIL=1
  fi
done

if [ "$FAIL" -ne 0 ]; then
  echo ""
  echo "Vendored manifests have been modified without re-rendering."
  echo "Run: bash scripts/renovate/revendor-kyverno.sh"
  exit 1
fi

echo "All vendor digests verified."
