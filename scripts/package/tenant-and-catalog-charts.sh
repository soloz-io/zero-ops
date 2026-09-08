#!/usr/bin/env bash
# Package the remaining platform content a released cluster reconciles:
# the two tenant charts, and the spoke catalogue per environment and provider.
#
# Without these a released cluster reaches into the platform's repository for
# the chart that renders every tenant's namespace and for the catalogue its
# spokes run, which ADR-063 makes a condition of the runtime being supported
# rather than a preference.
set -euo pipefail

VERSION="${1:?usage: tenant-and-catalog-charts.sh <version> [outdir]}"
OUTDIR="${2:-dist/charts}"
mkdir -p "$OUTDIR"

# The tenant charts are already Helm charts; only their version is set here, so
# every artefact in a bundle carries the bundle's version and a cluster asks for
# one version rather than a set of them.
for src in manifests/tenants/charts/*/; do
    name=$(basename "$src")
    chart="$OUTDIR/$name"
    rm -rf "$chart"; cp -R "$src" "$chart"
    python3 - "$chart/Chart.yaml" "$VERSION" <<'PY'
import sys, re, pathlib
p, v = pathlib.Path(sys.argv[1]), sys.argv[2]
t = p.read_text()
t = re.sub(r"(?m)^version:.*$", f"version: {v}", t)
t = re.sub(r"(?m)^appVersion:.*$", f'appVersion: "{v}"', t)
p.write_text(t)
PY
    echo "packaged $name -> $chart"
done

# The catalogue is a Kustomize overlay per environment and provider, the same
# shape as boundary 02's placement classes: the overlay IS the variation, so
# packaging it keeps the variation intact rather than reconstructing it from
# values.
for env_dir in manifests/spoke/spoke-catalog/environments/*/; do
    env=$(basename "$env_dir")
    for prov_dir in "$env_dir"*/; do
        [[ -f "$prov_dir/kustomization.yaml" ]] || continue
        prov=$(basename "$prov_dir")
        name="platform-spoke-catalog-${env}-${prov}"
        chart="$OUTDIR/$name"
        rm -rf "$chart"; mkdir -p "$chart/files" "$chart/templates"
        cat > "$chart/Chart.yaml" <<CHART
apiVersion: v2
name: $name
description: Spoke catalogue for $env on $prov, packaged from $prov_dir
type: application
version: $VERSION
appVersion: "$VERSION"
CHART
        if ! kustomize build --enable-helm "$prov_dir" > "$chart/files/rendered.yaml" 2>"$chart/.err"; then
            echo "$name: kustomize build failed:" >&2; sed 's/^/  /' "$chart/.err" >&2
            rm -rf "$chart"; exit 1
        fi
        rm -f "$chart/.err"

        # Objects whose content differs per spoke become chart templates; the
        # rest stays verbatim. As a Kustomize source these were patches on the
        # Application, and a Helm source cannot carry those (ADR-063).
        if ! scripts/package/templated-fields.py "$chart/files/rendered.yaml" \
             manifests/spoke/spoke-catalog/templated-fields.yaml "$chart"; then
            rm -rf "$chart"; exit 1
        fi

        # Helm refuses any chart file over 5 MiB, and vendored CRDs put these
        # well past it. Split at document boundaries so no object is divided.
        scripts/package/split-rendered.py "$chart/files/rendered.yaml" "$chart/files" >/dev/null
        rm -f "$chart/files/rendered.yaml"
        cat > "$chart/templates/content.yaml" <<'TMPL'
{{- range $path, $_ := .Files.Glob "files/*.yaml" }}
---
{{ $.Files.Get $path }}
{{- end }}
TMPL
        # Rendered with the values a cluster supplies, because the templated
        # objects need them: rendering without would count zero and delete a
        # chart that is correct.
        objects=$(helm template "$name" "$chart" \
            --set spokeName=probe --set global.environmentSlug="$env" \
            --set global.provider="$prov" 2>/dev/null | grep -c '^kind:' || true)
        [[ "${objects:-0}" -gt 0 ]] || { echo "$name: renders no objects; refusing" >&2; rm -rf "$chart"; exit 1; }
        echo "packaged $name -> $chart ($objects object(s))"
    done
done
