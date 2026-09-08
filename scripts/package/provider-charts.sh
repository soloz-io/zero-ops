#!/usr/bin/env bash
# Package boundary 02's provider overlays as charts.
#
# These components are not descriptors: their path is a function of the
# provider, which ADR-061 gives as the reason they are declared in the boundary
# template instead. They still have to be published, or a released cluster
# reaches for this repository to reconcile its database, cache and message bus,
# and the portability ADR-063 asks of a bundle is not whole.
#
# One chart per component per provider, named for both, because the overlay's
# whole content is ADR-046 §11's placement class: a matched workload-location
# and storageClass. Packaging the overlay keeps the pair together. A single
# chart selecting a provider from a value would let them drift apart, which is
# the failure §11 exists to prevent.
set -euo pipefail

VERSION="${1:?usage: provider-charts.sh <version> [outdir]}"
OUTDIR="${2:-dist/charts}"
ROOT="manifests/hub-core-services/providers"

[[ -d "$ROOT" ]] || { echo "no provider overlays at $ROOT" >&2; exit 1; }

count=0
for provider_dir in "$ROOT"/*/; do
    provider=$(basename "$provider_dir")
    for component_dir in "$provider_dir"*/; do
        [[ -f "$component_dir/kustomization.yaml" ]] || continue
        component=$(basename "$component_dir")

        # The Application these become is named platform-<component>, and the
        # boundary template appends the provider. Both halves must agree or the
        # cluster requests a chart nobody published.
        name="platform-${component}-${provider}"
        chart="$OUTDIR/$name"
        rm -rf "$chart"; mkdir -p "$chart/files" "$chart/templates"

        cat > "$chart/Chart.yaml" <<CHART
apiVersion: v2
name: $name
description: $component for the $provider placement class (ADR-046 §11), packaged from $component_dir
type: application
version: $VERSION
appVersion: "$VERSION"
CHART

        if ! kustomize build --enable-helm "$component_dir" > "$chart/files/rendered.yaml" 2>"$chart/.err"; then
            echo "$name: kustomize build failed:" >&2; sed 's/^/  /' "$chart/.err" >&2
            rm -rf "$chart"; exit 1
        fi
        rm -f "$chart/.err"

        # Helm refuses any chart file over 5 MiB, and vendored CRDs put these
        # well past it. Split at document boundaries so no object is divided.
        scripts/package/split-rendered.py "$chart/files/rendered.yaml" "$chart/files" >/dev/null
        rm -f "$chart/files/rendered.yaml"

        # Emitted verbatim: platform manifests carry syntax that is not Go
        # templating, and rendering it as such fails silently as often as loudly.
        cat > "$chart/templates/content.yaml" <<'TMPL'
{{- range $path, $_ := .Files.Glob "files/*.yaml" }}
---
{{ $.Files.Get $path }}
{{- end }}
TMPL

        objects=$(helm template "$name" "$chart" 2>/dev/null | grep -c '^kind:' || true)
        if [[ "${objects:-0}" -eq 0 ]]; then
            echo "$name: renders no objects; refusing to produce it" >&2
            rm -rf "$chart"; exit 1
        fi
        echo "packaged $name -> $chart ($objects object(s))"
        count=$((count+1))
    done
done
echo "provider charts: $count"
