#!/usr/bin/env bash
# Package the environment-manager chart as the bundle's own chart.
#
# This is what the seed Application installs, and it carries the boundaries. It
# is packaged rather than referenced so a released cluster reconciles its own
# boundary definitions from the bundle instead of reaching for this repository
# (ADR-063: a tenant must be able to hold the bundle itself).
#
# The component descriptors are copied in here, not committed. They are already
# declared under manifests/argocd/components/, and a committed second copy is
# the drift ADR-063 exists to refuse -- the chart carries a build artifact, and
# the source stays the single declaration.
set -euo pipefail

VERSION="${1:?usage: bundle-chart.sh <version> [outdir]}"
OUTDIR="${2:-dist/charts}"
SRC="manifests/argocd/environment-manager"
CHART="$OUTDIR/platform-bundle"

rm -rf "$CHART"; mkdir -p "$OUTDIR"
cp -R "$SRC" "$CHART"
rm -rf "$CHART/descriptors"

for boundary_dir in manifests/argocd/components/*/; do
    boundary=$(basename "$boundary_dir")
    mkdir -p "$CHART/descriptors/$boundary"
    cp "$boundary_dir"*.yaml "$CHART/descriptors/$boundary/" 2>/dev/null || true
done

# Values files a boundary installs a third-party chart with. They are inlined
# into the Application in a released bundle, so the second source that exists
# only to make $values resolve -- and which names this repository -- disappears.
mkdir -p "$CHART/values"
cp manifests/hub-core-services/identity/zitadel/values.yaml "$CHART/values/zitadel.yaml"

python3 - "$CHART/Chart.yaml" "$VERSION" <<'PY'
import sys, re, pathlib
p, version = pathlib.Path(sys.argv[1]), sys.argv[2]
t = p.read_text()
t = re.sub(r"(?m)^version:.*$", f"version: {version}", t)
t = re.sub(r"(?m)^appVersion:.*$", f'appVersion: "{version}"', t)
p.write_text(t)
PY

# A released render must produce the boundaries. The chart fails loudly when its
# descriptors are missing, so this proves they were copied AND that the released
# path renders at all -- neither of which the development path exercises.
if ! helm template platform-bundle "$CHART" \
        --set environmentSlug=dev --set provider=hybrid --set publicTlsIssuer=letsencrypt-prod \
        --set bundleVersion="$VERSION" > /dev/null 2>"$CHART/.err"; then
    echo "platform-bundle: released render failed:" >&2; sed 's/^/  /' "$CHART/.err" >&2
    rm -rf "$CHART"; exit 1
fi
rm -f "$CHART/.err"

echo "packaged platform-bundle -> $CHART ($(find "$CHART/descriptors" -name '*.yaml' | wc -l | tr -d ' ') descriptor(s))"
