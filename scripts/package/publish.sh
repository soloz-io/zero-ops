#!/usr/bin/env bash
# Package the platform bundle, gate it, and push it to a registry.
#
# One script, two callers. The release workflow runs it; so does a developer
# testing the released path locally (docs/runbooks/local-release-path-testing.md).
# Both therefore produce the same artefact set through the same gates -- which is
# the property that made a CI dispatch the only trustworthy way to publish, and
# the reason it no longer has to be.
#
# The cost that motivated this: a dispatch takes ~9m30s, and 8m30s of that is
# work a laptop does not have to repeat. Nearly all of the packaging time is
# kustomize --enable-helm refetching infisical, headlamp, postgresql and redis
# from upstream on a cold runner; those caches persist locally between runs, and
# the workflow itself retains them as release inputs for exactly that reason.
#
#   publish.sh <version> <ghcr-owner>
#
# SKIP_PUSH=1  package and gate without publishing. Nothing is spent: ADR-063
#              consumes a version by publishing it, so this is how a packaging
#              mistake is found without burning the next -rc.
set -euo pipefail

VERSION="${1:?usage: publish.sh <version> <ghcr-owner>}"
OWNER="${2:?usage: publish.sh <version> <ghcr-owner>}"
SKIP_PUSH="${SKIP_PUSH:-}"

# Helm requires semver. A tag that is not one would publish a chart nobody can
# request by version.
if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
    echo "publish: '$VERSION' is not semver; a chart version must be requestable" >&2
    exit 1
fi

# GitHub Actions renders ::error:: as an annotation; a terminal renders it as
# noise. Same message either way, addressed to whoever is reading.
if [ -n "${GITHUB_ACTIONS:-}" ]; then err() { echo "::error::$*"; }
else                                 err() { echo "error: $*" >&2; }; fi

rm -rf dist/charts dist/dist dist/pkg
mkdir -p dist/charts

# ── Package ─────────────────────────────────────────────────────────────────

published=0 refused=0 broken=0
for d in manifests/argocd/components/*/*.yaml; do
    rc=0
    ./scripts/package/component-chart.sh "$d" "$VERSION" dist/charts || rc=$?
    case "$rc" in
        0) published=$((published+1)) ;;
        # component-chart.sh exits 3 when a component's source reaches into a
        # generated/ directory. That is per-cluster instance data, and publishing
        # it would hand every cluster one cluster's values, so the build fails
        # rather than shipping it.
        3) refused=$((refused+1)) ;;
        # Anything else is a component that did not build. This used to be
        # counted as neither published nor refused, so the step reported success
        # having silently dropped it: the distribution shipped without a
        # component the boundaries enable, and the first thing to notice was the
        # release gate two steps later -- or nothing at all, for a component no
        # boundary happens to enable.
        *) err "$d failed to package (exit $rc)"; broken=$((broken+1)) ;;
    esac
done
echo "packaged=$published refused=$refused broken=$broken"
[ "$refused" -eq 0 ] || { err "$refused component(s) carry per-cluster data and cannot be published"; exit 1; }
[ "$broken" -eq 0 ]  || { err "$broken component(s) failed to build; the distribution would be incomplete"; exit 1; }

# Boundary 02's components are provider overlays, not descriptors (ADR-061), and
# the bundle chart carries the boundaries themselves plus the descriptors copied
# in at package time. Without these a released cluster reaches back into this
# repository for content it should have received in the bundle.
./scripts/package/provider-charts.sh            "$VERSION" dist/charts
./scripts/package/tenant-and-catalog-charts.sh  "$VERSION" dist/charts
./scripts/package/inline-charts.sh              "$VERSION" dist/charts

# ADR-063: one published artefact. The per-component charts above are internal
# structure and are folded in here; only the distribution and the bundle are
# pushed.
python3 scripts/package/distribution.py "$VERSION" dist/charts dist/dist
python3 scripts/package/verify-distribution.py dist/dist/platform dist/charts
cp -R dist/charts/universal-tenant dist/charts/tenant-public-tls dist/dist/
rm -rf dist/charts && mv dist/dist dist/charts
./scripts/package/bundle-chart.sh               "$VERSION" dist/charts

# ── Gate ────────────────────────────────────────────────────────────────────

# A release invariant, not a report. ADR-063 makes mirroring a condition of a
# supported runtime, so a bundle that reaches back into this repository cannot be
# mirrored -- and shipping it would make the custody claim false for every tenant
# that received it.
./scripts/validate/bundle-portability.sh "$VERSION"

# The gate on the conversion, not a convenience: a published chart that renders
# different objects is a silent change to every cluster that pulls it.
for d in manifests/argocd/components/*/*.yaml; do
    app=$(grep -m1 '^appName:' "$d" | sed 's/appName: *//')
    [ -d "dist/charts/$app" ] || continue
    ./scripts/package/verify-equivalence.py "$d" "dist/charts/$app"
done

# Rendering is not deploying. Three defects reached published releases through
# that gap -- Applications naming charts no release published, a chart carrying
# one cluster's Infisical project IDs, and an Application naming a Helm chart and
# a Kustomize block together, which ArgoCD refuses outright. Each rendered
# perfectly.
for combo in "dev hybrid" "stg hybrid" "prod hetzner"; do
    set -- $combo
    echo "  $1+$2"
    helm template platform-bundle dist/charts/platform-bundle \
        --set environmentSlug="$1" --set provider="$2" \
        --set publicTlsIssuer=letsencrypt-prod --set hubIngressAddress=127.0.0.1 \
        --set instanceRepoURL=https://github.com/example-org/example-gitops \
        --set bundleVersion="$VERSION" \
    | python3 scripts/validate/application-specs.py dist/charts
done

# Asserts the whole support matrix, both halves. Every environment crossed with
# every provider: a supported combination must render and must name only charts
# this release publishes; an unsupported one must be refused by name.
./scripts/validate/support-matrix.sh dist/charts/platform-bundle "$VERSION" dist/charts

# ── Push ────────────────────────────────────────────────────────────────────

for c in dist/charts/*/; do
    helm package "$c" -d dist/pkg >/dev/null
done

if [ -n "$SKIP_PUSH" ]; then
    echo "packaged and gated ${VERSION}; not pushed (SKIP_PUSH). ${VERSION} is still free."
    exit 0
fi

# ADR-063: a version is consumed by any release that begins publishing it, so
# re-running one that already put charts in the registry would re-point tags that
# already resolve. Publishing is not atomic -- charts push one at a time -- so a
# failure part-way leaves exactly this state, and the next attempt must take the
# next version.
#
# Every chart is probed, not just one: which charts a partial run left behind
# depends on where it stopped, and the set changes between releases, so there is
# no single name whose absence proves the version is free.
existing=""
for pkg in dist/pkg/*.tgz; do
    name=$(basename "$pkg" "-${VERSION}.tgz")
    ref="oci://ghcr.io/${OWNER}/charts/${name}"
    if err_out=$(helm show chart "$ref" --version "$VERSION" 2>&1 >/dev/null); then
        existing="${existing}  ${name}"$'\n'
    elif ! grep -qiE ': not found$|manifest unknown|NAME_UNKNOWN' <<<"$err_out"; then
        # Anything other than a clean absence is unknown, not free. Treating a
        # network or auth failure as "nothing is there" would let a re-publish
        # through at exactly the moment the check stopped working.
        err "could not determine whether ${name} is already published: ${err_out}"
        exit 1
    fi
done
if [ -n "$existing" ]; then
    err "version ${VERSION} is already published; a version is consumed by any release that begins publishing it (ADR-063). Release the next version instead."
    printf 'already in the registry at this version:\n%s' "$existing"
    exit 1
fi

for pkg in dist/pkg/*.tgz; do
    helm push "$pkg" "oci://ghcr.io/${OWNER}/charts"
done

echo "published ${VERSION} to oci://ghcr.io/${OWNER}/charts"
