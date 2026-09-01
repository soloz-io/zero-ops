#!/usr/bin/env bash
set -euo pipefail

# Verify that the SHA-256 digest in the provenance header matches
# the actual rendered content. Catches:
#   - Manual edits to vendored manifests without re-rendering
#   - Tampered artifacts
#   - Renovate PRs that bump the version but don't re-vendor
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

  RECORDED_HASH=$(grep '^# sha256:' "$file" | head -1 | sed 's/^# sha256: //')
  if [ -z "$RECORDED_HASH" ]; then
    echo "ERROR: No sha256 digest found in $file"
    FAIL=1
    continue
  fi

  # Compute hash of everything except the provenance header lines (# comments)
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
