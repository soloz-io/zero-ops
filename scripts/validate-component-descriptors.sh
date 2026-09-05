#!/usr/bin/env bash
# Validate the component descriptors that compose a boundary (ADR-061).
#
# A boundary's inventory is the union of two sources: descriptor files under
# manifests/argocd/components/<boundary>/, and the environment-parameterised
# components enumerated in the boundary template itself. Both feed one template,
# and the ApplicationSets render with goTemplateOptions missingkey=error.
#
# That last detail is why this script exists. A descriptor that omits a field the
# template dereferences does not fail its own Application -- it fails the
# rendering of the WHOLE boundary, because generator input is evaluated as a set.
# One bad file takes out every component beside it. Catching that at commit time
# is the difference between a rejected commit and a boundary that stops
# reconciling.
#
# Checks, per boundary directory found:
#   1. every descriptor carries every field the template dereferences
#   2. the declared boundary matches the directory the descriptor lives in
#   3. no appName is declared twice, counting the template's inline elements
#   4. the descriptor tree is not inside any path a component deploys
#
# Depends on: helm, yq (mikefarah), standard POSIX tools.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
components_root="$repo_root/manifests/argocd/components"
env_mgr="$repo_root/manifests/argocd/environment-manager"

# Every field spec.template or templatePatch dereferences. Adding a field to the
# template without adding it here lets a descriptor omit it and take the boundary
# down at render time.
REQUIRED_FIELDS="appName destinationNamespace isPlatformOwned repoURL targetRevision path chart directoryRecurse directoryInclude helmValues helmReleaseName ignoreDifferencesNamespace extraManifestsPath"

fail_count=0
pass() { echo "  ✅ $1"; }
fail() { echo "  ❌ $1"; fail_count=$((fail_count + 1)); }

for bin in helm yq; do
  command -v "$bin" >/dev/null 2>&1 || { echo "❌ required tool not found: $bin"; exit 1; }
done

if [[ ! -d "$components_root" ]]; then
  echo "✅ component descriptors: none present"
  exit 0
fi

echo "Validating component descriptors..."

rendered="$(mktemp)"
trap 'rm -f "$rendered"' EXIT
helm template descriptor-check "$env_mgr" \
  --set environmentSlug=dev \
  --set provider=hetzner \
  --set topology=single \
  --set publicTlsIssuer=letsencrypt-prod \
  --set oidcIssuer=https://auth.example.invalid \
  >"$rendered"

for dir in "$components_root"/*/; do
  [[ -d "$dir" ]] || continue
  boundary="$(basename "$dir")"
  appset_name="$(yq -r "select(.kind == \"ApplicationSet\") | select(.metadata.name | test(\"^${boundary}-\")) | .metadata.name" "$rendered" 2>/dev/null | grep -v '^null$' | head -1 || true)"

  if [[ -z "$appset_name" ]]; then
    fail "boundary ${boundary}: descriptors exist but no ApplicationSet named ${boundary}-* renders -- these files are inert"
    continue
  fi

  descriptor_count=0
  seen_names=""

  for f in "$dir"*.yaml; do
    [[ -f "$f" ]] || continue
    rel="${f#"$repo_root"/}"
    descriptor_count=$((descriptor_count + 1))

    # 1. required fields
    missing=""
    for field in $REQUIRED_FIELDS; do
      if [[ "$(yq -r "has(\"$field\")" "$f" 2>/dev/null)" != "true" ]]; then
        missing="$missing $field"
      fi
    done
    [[ -n "$missing" ]] && \
      fail "$rel: missing field(s):$missing -- missingkey=error fails the WHOLE ${boundary} boundary, not just this component"

    # 2. declared boundary matches the directory
    declared="$(yq -r '.boundary // ""' "$f" 2>/dev/null)"
    if [[ "$declared" != "$boundary" ]]; then
      fail "$rel: declares boundary '${declared}' but lives in ${boundary}/"
    fi

    # 3. duplicate appName
    name="$(yq -r '.appName // ""' "$f" 2>/dev/null)"
    if [[ -z "$name" ]]; then
      fail "$rel: no appName"
    elif printf '%s\n' "$seen_names" | grep -qx "$name"; then
      fail "$rel: appName '${name}' declared more than once in ${boundary}/"
    else
      seen_names="$seen_names
$name"
    fi
  done

  # 3b. a descriptor must not collide with an inline element of the same boundary
  inline_names="$(yq -r "select(.kind == \"ApplicationSet\" and .metadata.name == \"${appset_name}\")
    | .spec.generators[] | select(has(\"list\")) | .list.elements[].appName" "$rendered" 2>/dev/null \
    | grep -v '^null$' || true)"

  collisions=""
  while IFS= read -r n; do
    [[ -z "$n" ]] && continue
    printf '%s\n' "$seen_names" | grep -qx "$n" && collisions="$collisions $n"
  done <<EOF
$inline_names
EOF

  inline_count="$(printf '%s\n' "$inline_names" | grep -c . || true)"

  if [[ -n "$collisions" ]]; then
    fail "boundary ${boundary}: appName(s) declared both as a descriptor and inline:${collisions} -- two generators would produce the same Application"
  else
    pass "boundary ${boundary}: ${descriptor_count} descriptor(s) + ${inline_count} inline, no collisions"
  fi
done

# 4. the descriptor tree must not sit inside anything a component deploys
deployed_paths="$(yq -r 'select(.kind == "ApplicationSet") | .spec.generators[]? | select(has("list")) | .list.elements[]?.path // ""' "$rendered" 2>/dev/null | grep -v '^null$' | grep -v '^$' | sort -u || true)"
rel_root="manifests/argocd/components"
overlap=""
while IFS= read -r p; do
  [[ -z "$p" ]] && continue
  case "$rel_root/" in
    "$p"/*) overlap="$overlap $p" ;;
  esac
done <<EOF
$deployed_paths
EOF

if [[ -n "$overlap" ]]; then
  fail "the descriptor tree sits inside deployed path(s):${overlap} -- descriptors would be applied to the cluster as manifests"
else
  pass "descriptor tree is outside every deployed path"
fi

echo
if [[ "$fail_count" -gt 0 ]]; then
  echo "❌ component descriptors: $fail_count check(s) failed"
  exit 1
fi
echo "✅ component descriptors: all checks passed"
