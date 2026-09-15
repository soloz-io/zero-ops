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

# Provenance: which source produced this artefact.
#
# Without it a published bundle records its own version and nothing else, so
# "what is in 0.1.16-rc.29?" has no answer that does not depend on someone
# remembering. Both callers build from a WORKING TREE -- the release workflow
# from a checkout, a developer from whatever is on their laptop -- and a bundle
# built from uncommitted edits is untraceable by construction: it is in a
# registry, tenants can pull it, and no commit corresponds to it.
#
# Recorded rather than enforced. Refusing to publish from a dirty tree would
# break the local release path this script exists to support (the whole point of
# testing a release before committing it), so the dirty state is stamped instead
# and shows up wherever the chart is inspected.
GIT_SHA="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
GIT_DIRTY=false
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
    GIT_DIRTY=true
fi
GIT_BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)"
BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if [ "$GIT_DIRTY" = true ]; then
    echo "platform-bundle: WARNING -- built from a dirty working tree." >&2
    echo "  The artefact records commit ${GIT_SHA} plus uncommitted changes that" >&2
    echo "  nothing else captures. Fine for a local release test; commit before" >&2
    echo "  publishing a version anyone else will consume." >&2
fi

python3 - "$CHART/Chart.yaml" "$VERSION" "$GIT_SHA" "$GIT_DIRTY" "$GIT_BRANCH" "$BUILT_AT" <<'PY'
import sys, re, pathlib
p, version, sha, dirty, branch, built = (pathlib.Path(sys.argv[1]),) + tuple(sys.argv[2:7])
t = p.read_text()
t = re.sub(r"(?m)^version:.*$", f"version: {version}", t)
t = re.sub(r"(?m)^appVersion:.*$", f'appVersion: "{version}"', t)

# Annotations, not new top-level keys: Helm rejects fields it does not know, and
# annotations are the documented place for metadata of this kind.
t = re.sub(r"(?ms)^annotations:\n(?:[ \t]+.*\n?)*", "", t)
t = t.rstrip("\n") + "\n"
t += (
    "annotations:\n"
    + f'  zero-ops.io/source-commit: "{sha}"\n'
    + f'  zero-ops.io/source-dirty: "{dirty}"\n'
    + f'  zero-ops.io/source-branch: "{branch}"\n'
    + f'  zero-ops.io/built-at: "{built}"\n'
)
p.write_text(t)
PY

# A released render must produce the boundaries. The chart fails loudly when its
# descriptors are missing, so this proves they were copied AND that the released
# path renders at all -- neither of which the development path exercises.
if ! helm template platform-bundle "$CHART" \
        --set environmentSlug=dev --set provider=hybrid --set publicTlsIssuer=letsencrypt-prod \
        --set instanceRepoURL=https://github.com/example-org/example-gitops \
        --set hubDomain=dev.example.test \
        --set bundleVersion="$VERSION" > /dev/null 2>"$CHART/.err"; then
    echo "platform-bundle: released render failed:" >&2; sed 's/^/  /' "$CHART/.err" >&2
    rm -rf "$CHART"; exit 1
fi
rm -f "$CHART/.err"

echo "packaged platform-bundle -> $CHART ($(find "$CHART/descriptors" -name '*.yaml' | wc -l | tr -d ' ') descriptor(s))"
