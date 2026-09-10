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
components_dir="manifests/argocd/components"
yamllint_config=".yamllint.yaml"

# lint_block <label> <values-content>
# Returns 0 if the block is clean, 1 otherwise. Prints its own diagnostics.
lint_block() {
  local label="$1" content="$2"

  if printf '%s' "$content" | grep -qE '\\"'; then
    echo "❌ [$label] backslash-quote sequences found in helmValues."
    echo "   Block scalars (|) do not require escape characters."
    printf '%s' "$content" | grep -nE '\\"' | head -5
    return 1
  fi

  if ! printf '%s\n' "$content" | yamllint -c "$yamllint_config" - 2>/dev/null; then
    echo "❌ [$label] helmValues failed YAML lint."
    printf '%s\n' "$content" | yamllint -c "$yamllint_config" - 2>&1 | head -8 | sed 's/^/     /'
    return 1
  fi
  return 0
}

# Phase 0: Pre-flight check — helm template must succeed
echo "Checking chart renders successfully..."
# ADR-055: every boundary renders on every render, so the lint must supply the
# same environment values the seed Application does. publicTlsIssuer has no
# default by design (ADR-051) and must be named here rather than defaulted.
lint_values=(--set environmentRevision=dry-run --set environmentSlug=prod \
             --set provider=hetzner --set topology=single \
             --set publicTlsIssuer=letsencrypt-prod \
             --set instanceRepoURL=https://github.com/example-org/example-gitops)

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
descriptor_files=0

# Inline list elements, read from the RENDERED chart rather than the raw
# templates. The templates are Helm, not YAML -- a `{{- if }}` at column zero
# makes yq return nothing for the whole file, so scanning the sources found no
# inline blocks at all and reported that only by counting zero.
#
# Extraction is done in Python: the render is a multi-document stream and
# indexing into a filtered set across documents is not something yq expresses
# reliably.
rendered_file=$(mktemp)
extract_dir=$(mktemp -d)
trap 'rm -rf "$rendered_file" "$extract_dir"' EXIT
printf '%s\n' "$rendered" >"$rendered_file"

python3 - "$rendered_file" "$extract_dir" <<'PYEOF'
import sys, yaml, os, re
src, out = sys.argv[1], sys.argv[2]
n = 0
for doc in yaml.safe_load_all(open(src)):
    if not doc or doc.get("kind") != "ApplicationSet":
        continue
    for g in doc.get("spec", {}).get("generators", []) or []:
        lst = (g or {}).get("list")
        if not lst:
            continue
        for e in lst.get("elements", []) or []:
            v = e.get("helmValues")
            if not v or not str(v).strip():
                continue
            name = re.sub(r"[^A-Za-z0-9._-]", "_", str(e.get("appName", "unknown")))
            with open(os.path.join(out, f"{n:03d}__{name}"), "w") as f:
                f.write(str(v))
            n += 1
print(n)
PYEOF

for blk in "$extract_dir"/*; do
  [ -f "$blk" ] || continue
  app_name="${blk##*__}"
  element_count=$((element_count + 1))
  file_count=$((file_count + 1))
  lint_block "rendered:$app_name" "$(cat "$blk")" || passed=false
done

# Component descriptors (ADR-061). These are plain YAML, so the file itself is
# linted by yamllint-all — but helmValues inside it is still a string, and this
# is the only thing that looks in it.
if [ -d "$components_dir" ]; then
  for desc in "$components_dir"/*/*.yaml; do
    [ -f "$desc" ] || continue
    helm_values=$(yq eval -r '.helmValues // ""' "$desc" 2>/dev/null || echo "")
    if [ "$helm_values" = "" ] || [ "$helm_values" = "null" ]; then
      continue
    fi
    app_name=$(yq eval -r '.appName // "unknown"' "$desc" 2>/dev/null)
    descriptor_files=$((descriptor_files + 1))
    element_count=$((element_count + 1))
    lint_block "$desc:$app_name" "$helm_values" || passed=false
  done
fi

if [ "$passed" = false ]; then
  echo ""
  echo "FAIL: One or more helmValues blocks failed validation."
  exit 1
fi

# A values linter that found nothing must not report success. See the header.
if [ "$element_count" -eq 0 ]; then
  echo ""
  echo "FAIL: no helmValues blocks were found at all."
  echo "  Looked in: $appset_dir/*-appset.yaml (inline list elements)"
  echo "         and: $components_dir/*/*.yaml (component descriptors)"
  echo "  The platform declares Helm values somewhere; finding none means this"
  echo "  script is looking in the wrong place, not that there is nothing to check."
  exit 1
fi

echo "✅ All $element_count helmValues blocks passed linting ($file_count appset file(s), $descriptor_files descriptor(s))."
exit 0
