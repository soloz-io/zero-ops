#!/usr/bin/env bash
# Lint all rendered helmValues block scalars across AppSet templates.
#
# Checks:
# 1. No literal backslash-quote sequences ("" should be ")
# 2. YAML syntax + style compliance via yamllint
#
# Reads each helmValues: | block from source AppSet files (not rendered output),
# extracts the raw string content, and validates it as standalone YAML.
# Block scalars in the source YAML become raw strings via yq eval -r.
# Those raw strings must themselves parse as valid YAML.

set -euo pipefail

chart_dir="manifests/argocd/environment-manager/templates"
yamllint_config=".yamllint.yaml"

passed=true
file_count=0
element_count=0

for file in "$chart_dir"/*-appset.yaml; do
  if [ ! -f "$file" ]; then
    continue
  fi

  # Number of list elements in this appset (0 = nothing to lint)
  count=$(yq eval '.spec.generators[0].list.elements | length' "$file" 2>/dev/null || echo "0")
  if [ "$count" = "null" ] || [ "$count" -eq 0 ]; then
    continue
  fi

  file_count=$((file_count + 1))

  for ((i = 0; i < count; i++)); do
    app_name=$(yq eval ".spec.generators[0].list.elements[$i].appName" "$file" 2>/dev/null || echo "unknown")
    helm_values=$(yq eval -r ".spec.generators[0].list.elements[$i].helmValues" "$file" 2>/dev/null || echo "")

    if [ "$helm_values" = "" ] || [ "$helm_values" = "null" ]; then
      continue
    fi

    element_count=$((element_count + 1))

    # Check 1: backslash-quote sequences (block scalars don't process escapes)
    if echo "$helm_values" | grep -qE '\\"'; then
      echo "❌ [$file:$app_name] backslash-quote sequences found in helmValues."
      echo "   Block scalars (|) do not require escape characters."
      echo "   Offending lines:"
      echo "$helm_values" | grep -nE '\\"' | head -5
      passed=false
      continue
    fi

    # Check 2: YAML syntax and style via yamllint
    if ! echo "$helm_values" | yamllint -c "$yamllint_config" - 2>/dev/null; then
      echo "❌ [$file:$app_name] helmValues failed YAML lint."
      passed=false
      continue
    fi
  done
done

if [ "$passed" = false ]; then
  echo ""
  echo "FAIL: One or more helmValues blocks failed validation."
  exit 1
fi

echo "✅ All $element_count helmValues blocks across $file_count files passed linting."
exit 0
