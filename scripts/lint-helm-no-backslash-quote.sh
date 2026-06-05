#!/usr/bin/env bash
# Fails if any rendered helmValues block scalar in the environment-manager
# chart contains a literal backslash-quote sequence. Such sequences
# indicate a Lua or YAML string was written inside a `|` block scalar
# with bogus escape characters (block scalars do not process escapes).

set -euo pipefail

chart_dir="manifests/argocd/environment-manager"

rendered=$(helm template environment-manager "$chart_dir" --set environmentRevision=dry-run 2>/dev/null)

hits=$(echo "$rendered" \
  | yq 'select(.kind == "ApplicationSet") | .spec.generators[0].list.elements[] | select(.helmValues != "") | .helmValues' - \
  2>/dev/null \
  | grep -E '\\"' || true)

if [ -n "$hits" ]; then
  echo "ERROR: backslash-quote sequences found in rendered helmValues."
  echo "Block scalars (|) do not require escape characters."
  echo "Hits:"
  echo "$hits"
  exit 1
fi

exit 0
