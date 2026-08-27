#!/usr/bin/env bash
# Lint all rendered helmValues block scalars across AppSet templates.
#
# Checks:
# 1. Helm chart template is valid (helm template succeeds)
# 2. No literal backslash-quote sequences ("" should be ")
# 3. YAML syntax + style compliance via yamllint on helmValues content
#
# First validates that helm template renders the chart without errors
# (catches Go-template-level YAML issues). Then reads each helmValues: |
# block from source AppSet files, extracts the raw string content, and
# validates it as standalone YAML.

set -euo pipefail

chart_dir="manifests/argocd/environment-manager"
appset_dir="$chart_dir/templates"
yamllint_config=".yamllint.yaml"

# Phase 0: Pre-flight check — helm template must succeed
echo "Checking chart renders successfully..."
# ADR-055: every boundary renders on every render, so the lint must supply the
# same environment values the seed Application does. publicTlsIssuer has no
# default by design (ADR-051) and must be named here rather than defaulted.
lint_values=(--set environmentRevision=dry-run --set environmentSlug=prod \
             --set provider=hetzner --set topology=single \
             --set publicTlsIssuer=letsencrypt-prod)

if ! rendered=$(helm template environment-manager "$chart_dir" "${lint_values[@]}" 2>&1); then
    echo "❌ helm template failed — chart has a template-level YAML error."
    echo "   Run the following to debug:"
    echo "     helm template environment-manager $chart_dir ${lint_values[*]} --debug"
    exit 1
fi
echo "✓ Chart renders successfully"

passed=true
file_count=0
element_count=0

for file in "$appset_dir"/*-appset.yaml; do
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
