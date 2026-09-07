#!/usr/bin/env bash
# Package one platform-owned component descriptor as a Helm chart.
#
# ADR-063 publishes the bundle as versioned charts; ADR-062 keeps instance data
# out of the types repository. This script is the seam between them, and its
# most important behaviour is a refusal: a component whose source reaches into a
# generated/ directory is carrying per-cluster instance data (ADR-045), and
# publishing it would bake one tenant's Infisical project, DNS target or
# bootstrap config into an artefact every tenant pulls. Those must become chart
# values before they can be packaged, so this exits non-zero rather than
# producing a chart that is wrong in a way nobody would notice until a second
# tenant existed.
#
# The unit is a DESCRIPTOR, not a path. Two descriptors share
# manifests/argocd-principal and are separated only by directoryInclude at
# different sync waves, so packaging by path would merge two components that the
# platform deliberately sequences apart.
set -euo pipefail

DESCRIPTOR="${1:?usage: component-chart.sh <descriptor.yaml> <version> [outdir]}"
VERSION="${2:?missing chart version}"
OUTDIR="${3:-dist/charts}"

field() { grep -m1 "^$1:" "$DESCRIPTOR" | sed "s/^$1: *//; s/^['\"]//; s/['\"]$//"; }

APP=$(field appName)
SRC=$(field path)
OWNED=$(field isPlatformOwned)
INCLUDE=$(field directoryInclude)
RECURSE=$(field directoryRecurse)

[[ -n "$APP" ]] || { echo "no appName in $DESCRIPTOR" >&2; exit 2; }

if [[ "$OWNED" != "true" ]]; then
    echo "skip $APP: third-party chart, already published upstream"
    exit 0
fi
[[ -n "$SRC" ]] || { echo "skip $APP: platform-owned with no path" >&2; exit 0; }
[[ -d "$SRC" ]] || { echo "$APP: source path does not exist: $SRC" >&2; exit 1; }

# The refusal described above.
if [[ -d "$SRC/generated" ]] || grep -rqs "generated/" "$SRC"/kustomization.yaml 2>/dev/null; then
    cat >&2 <<MSG
REFUSED $APP
  $SRC consumes Day-0 generated artifacts, which are per-cluster instance data.
  Publishing it would place one cluster's values in a chart every cluster pulls.
  Convert those inputs to chart values (ADR-063) before packaging this component.
MSG
    exit 3
fi

CHART="$OUTDIR/$APP"
rm -rf "$CHART"; mkdir -p "$CHART/templates"

cat > "$CHART/Chart.yaml" <<MSG
apiVersion: v2
name: $APP
description: Platform component $APP, packaged from $SRC
type: application
version: $VERSION
appVersion: "$VERSION"
MSG

# Kustomize builds are rendered at package time so the published chart carries
# the same objects the cluster applies today. Kustomize is a build step here,
# not a deploy-time one, which is why rendering it away changes nothing that
# was environment-dependent -- ADR-061 already keeps environment-parameterised
# components out of descriptors and in the boundary template.
if [[ -f "$SRC/kustomization.yaml" ]]; then
    command -v kustomize >/dev/null || { echo "$APP: kustomize not found" >&2; exit 1; }
    kustomize build "$SRC" > "$CHART/templates/rendered.yaml"
else
    # directoryInclude is a glob the boundary template passes to ArgoCD; honour
    # it here so a chart contains exactly the objects its Application did.
    pattern="${INCLUDE//\"/}"; pattern="${pattern:-*.yaml}"
    # An empty array expanded under `set -u` is an error on bash 3.2, which is
    # what macOS ships and what killed every recursive component here first run.
    depth=(); [[ "$RECURSE" == "true" ]] || depth=(-maxdepth 1)
    found=0
    while IFS= read -r f; do
        cp "$f" "$CHART/templates/$(echo "${f#$SRC/}" | tr '/' '_')"; found=1
    done < <(find "$SRC" ${depth[@]+"${depth[@]}"} -type f -name "$pattern" 2>/dev/null | sort)
    [[ $found -eq 1 ]] || { echo "$APP: no files matched '$pattern' under $SRC" >&2; exit 1; }
fi

echo "packaged $APP -> $CHART ($(find "$CHART/templates" -type f | wc -l | tr -d ' ') file(s))"
