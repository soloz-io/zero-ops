#!/usr/bin/env bash
# End-to-end test of the RELEASED path, from this checkout, in one command.
#
# Publishes a prerelease bundle, builds a CLI that declares it, scaffolds a tenant
# repository against it and bootstraps a cluster from that repository -- which is
# the sequence a tenant's Day-0 performs, with the same artefacts and the same
# gates. See docs/runbooks/local-release-path-testing.md for what each phase does
# and why the released path needs testing separately at all (ADR-068 addendum 1).
#
#   scripts/dev/local-e2e.sh 0.1.16-rc.1                 # everything
#   scripts/dev/local-e2e.sh 0.1.16-rc.1 --dry           # package and gate only
#   scripts/dev/local-e2e.sh 0.1.16-rc.1 cli scaffold    # named phases only
#   scripts/dev/local-e2e.sh 0.1.16-rc.2 clean           # tear the last run down
#   scripts/dev/local-e2e.sh 0.1.16-rc.2 clean publish cli scaffold bootstrap
#
# Phases: clean  publish  cli  scaffold  bootstrap
#         (default: the last four. `clean` is opt-in: it destroys a running
#          cluster and deletes a GitHub repository, so it is never implied.)
#
# Credentials are read from k8-secrets/, one file per credential, never prompted
# and never echoed. That directory is gitignored and is the same place teardown
# and the bootstrap token fallback already read from. A missing one is reported by
# path so it can be created once rather than retyped per run.
set -euo pipefail

cd "$(dirname "$0")/../.."
ROOT="$PWD"

# ── What is being built ─────────────────────────────────────────────────────
# Overridable from the environment; the defaults describe a throwaway dev box.
TENANT="${TENANT:-acme}"
GIT_ORG="${GIT_ORG:-soloz-io}"
DOMAIN="${DOMAIN:-acme.example}"
CLUSTER="${CLUSTER:-acme-hub}"
ENVIRONMENT="${ENVIRONMENT:-dev}"
PROVIDER="${PROVIDER:-hetzner}"
REGION="${REGION:-hel1}"
# The GHCR namespace. soloz-io rather than a personal one because visibility is a
# property of the package, not the version: those charts are already public, so a
# prerelease pushed there is pullable immediately, while a new namespace creates
# private packages that ArgoCD cannot read until each is flipped by hand
# (scripts/package/flip-public.sh).
OWNER="${OWNER:-soloz-io}"
# Where the tenant clone is made. Outside the checkout: it is a separate
# repository with its own remote, and `git add -A` here has swept one in before.
WORKSPACE="${WORKSPACE:-$ROOT/.local-e2e}"

SECRETS="${SECRETS:-$ROOT/k8-secrets}"

usage() { sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-1}"; }

VERSION="${1:-}"
[ -n "$VERSION" ] || usage 1
shift

DRY=""
phases=()
for arg in "$@"; do
    case "$arg" in
        --dry|--skip-push) DRY=1 ;;
        clean|publish|cli|scaffold|bootstrap) phases+=("$arg") ;;
        -h|--help) usage 0 ;;
        *) echo "unknown argument: $arg" >&2; usage 1 ;;
    esac
done
[ ${#phases[@]} -gt 0 ] || phases=(publish cli scaffold bootstrap)

wants() { printf '%s\n' "${phases[@]}" | grep -qx "$1"; }
say()   { printf '\n\033[1m[local-e2e] %s\033[0m\n' "$*"; }

# ── Credentials ─────────────────────────────────────────────────────────────

# read_secret <file> <description> [default]
#
# Returns the file's contents, or the default when one is given and the file is
# absent. Fails by naming the path, because the fix is to create that file once.
read_secret() {
    local path="$1" what="$2" default="${3-}"
    if [ -r "$path" ]; then
        local v; v="$(tr -d '\r' < "$path" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
        if [ -n "$v" ]; then printf '%s' "$v"; return 0; fi
    fi
    if [ -n "$default" ]; then printf '%s' "$default"; return 0; fi
    echo "local-e2e: missing ${what}." >&2
    echo "  expected it in: ${path}" >&2
    return 1
}

load_credentials() {
    HCLOUD_TOKEN="$(read_secret "$SECRETS/hetzner/token" "the Hetzner API token")"
    GITOPS_TOKEN="$(read_secret "$SECRETS/github/github-pat-token" \
        "a GitHub token that can create the tenant repository and let Day-0 commit to it")"
    # Two jobs, one token here. Scaffolding needs a credential to CREATE the
    # repository -- resolved from GITHUB_APP_TOKEN, then GITHUB_TOKEN, then
    # k8-secrets/ relative to the repo root (scaffold.go:545-552) -- while
    # --gitops-token is what Day-0 later commits with. The file lookup is relative
    # to wherever scaffold runs, and it runs from the workspace, so the env var is
    # what makes the platform checkout's token reachable from there.
    export GITHUB_TOKEN="$GITOPS_TOKEN"

    # ADR-076 makes the escrow mandatory: RequireEscrow refuses a box that would
    # have nowhere to keep its master keys. All four or none -- three of four
    # produces an operator that attempts a backup every reconcile and fails.
    ESCROW_URL="$(read_secret "$SECRETS/infisical/INFISICAL_ESCROW_URL" \
        "the escrow Infisical URL" "https://app.infisical.com")"
    ESCROW_PROJECT_ID="$(read_secret "$SECRETS/infisical/INFISICAL_ESCROW_PROJECT_ID" \
        "the escrow project id")"
    ESCROW_CLIENT_ID="$(read_secret "$SECRETS/infisical/INFISICAL_ESCROW_CLIENT_ID" \
        "the escrow machine identity client id")"
    ESCROW_CLIENT_SECRET="$(read_secret "$SECRETS/infisical/INFISICAL_ESCROW_CLIENT_SECRET" \
        "the escrow machine identity client secret")"

    # Hybrid only. ADR-046 invariant 6 wants a tailnet IP on every node carrying
    # pod traffic; nothing reads this under any other provider.
    TAILSCALE_AUTHKEY=""
    if [ "$PROVIDER" = "hybrid" ]; then
        TAILSCALE_AUTHKEY="$(read_secret "$SECRETS/tailscale/authkey" "a Tailscale auth key")"
    fi
    export HCLOUD_TOKEN
}

# ── Toolchain ───────────────────────────────────────────────────────────────

# Helm 3, specifically. kustomize's HelmChartInflationGenerator shells out to
# `helm version -c`, which Helm 4 removed, so a Helm 4 toolchain fails every
# component that inflates a chart from its kustomization -- headlamp and infisical
# -- and the release workflow pins v3.16.4 for the same reason.
#
# Installed here if absent, into a private directory, and never onto PATH beyond
# this process: a machine's `helm` is the operator's choice and this script has no
# business changing it. HELM3=/path/to/helm skips the download entirely.

# helm3_pin reads the version from the workflow rather than restating it, so the
# toolchain this installs cannot drift from the runner's. A packaging difference
# between a laptop and CI is exactly what this whole path exists to remove.
helm3_pin() {
    local v
    v="$(sed -n '/azure\/setup-helm/,/version:/s/.*version: *//p' \
         .github/workflows/publish-platform-charts.yml | head -1)"
    printf '%s' "${v:-v3.16.4}"
}

is_helm3() { [ -x "$1" ] && "$1" version --short 2>/dev/null | grep -q '^v3\.'; }

install_helm3() {
    local ver dir os arch url tgz want got
    ver="$(helm3_pin)"
    dir="$HOME/.local/helm3"
    case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;;
        *) echo "local-e2e: no Helm 3 build for $(uname -s); install it yourself" >&2; return 1 ;; esac
    case "$(uname -m)" in arm64|aarch64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;;
        *) echo "local-e2e: no Helm 3 build for $(uname -m); install it yourself" >&2; return 1 ;; esac

    url="https://get.helm.sh/helm-${ver}-${os}-${arch}.tar.gz"
    say "installing Helm ${ver} (${os}-${arch}) into ${dir}"

    tgz="$(mktemp -d)/helm.tar.gz"
    curl -fsSL -o "$tgz" "$url" || {
        echo "local-e2e: could not download ${url}" >&2; return 1; }

    # Verified, because this is a binary fetched over the network and then run.
    # The publisher's checksum is the only thing that makes that defensible.
    want="$(curl -fsSL "${url}.sha256sum" | awk '{print $1}')"
    got="$(shasum -a 256 "$tgz" | awk '{print $1}')"
    if [ -z "$want" ] || [ "$want" != "$got" ]; then
        echo "local-e2e: checksum mismatch for ${url}" >&2
        echo "  published: ${want:-<none>}" >&2
        echo "  received:  ${got}" >&2
        rm -rf "$(dirname "$tgz")"
        return 1
    fi

    mkdir -p "$dir"
    tar -xz -C "$dir" --strip-components=1 -f "$tgz" "${os}-${arch}/helm"
    rm -rf "$(dirname "$tgz")"
    is_helm3 "$dir/helm" || {
        echo "local-e2e: installed ${dir}/helm but it does not report v3" >&2; return 1; }
    echo "Helm $("$dir/helm" version --short) ready"
}

require_helm3() {
    if [ -n "${HELM3:-}" ]; then
        is_helm3 "$HELM3" || { echo "local-e2e: HELM3=$HELM3 is not a Helm 3" >&2; return 1; }
        PATH="$(cd "$(dirname "$HELM3")" && pwd):$PATH"; export PATH
        return 0
    fi
    if command -v helm >/dev/null && helm version --short 2>/dev/null | grep -q '^v3\.'; then
        return 0
    fi
    for c in "$HOME/.local/helm3/helm" /usr/local/opt/helm@3/bin/helm; do
        if is_helm3 "$c"; then
            PATH="$(dirname "$c"):$PATH"; export PATH
            return 0
        fi
    done
    install_helm3 || return 1
    PATH="$HOME/.local/helm3:$PATH"; export PATH
}

require_ghcr_login() {
    local scopes
    scopes="$(gh auth status 2>&1 | sed -n 's/.*Token scopes: //p')"
    case "$scopes" in
        *write:packages*) ;;
        *) echo "local-e2e: the gh token cannot write packages (scopes: ${scopes:-unknown})." >&2
           echo "  run: gh auth refresh -s write:packages,read:packages" >&2
           return 1 ;;
    esac
    gh auth token | helm registry login ghcr.io -u "$(gh api user --jq .login)" --password-stdin >/dev/null
}

# ── Phases ──────────────────────────────────────────────────────────────────

# Remove everything the previous run of THIS loop created.
#
# Never in the default set, and never implied: it destroys a running cluster and
# deletes a GitHub repository, and neither is recoverable. Naming the phase is
# the confirmation -- `make e2e` alone cannot reach it.
#
# Scoped to the names this script manages. It will not take a cluster or a
# repository it was not given in the same variables it would have created them
# from, so a stray CLUSTER= in the environment cannot point it at something real.
do_clean() {
    local repo="$TENANT-gitops"
    say "removing what the last run left: cluster $CLUSTER, $GIT_ORG/$repo"

    # The cloud cluster first: teardown needs the kubeconfig the clone holds, so
    # removing the clone before it would strand the servers with nothing left
    # that knows their names.
    "$ROOT/bin/soloz" teardown --name "$CLUSTER" --force --confirm 2>&1 | sed 's/^/  /' || true

    # The ephemeral bootstrap cluster. Left behind, `kind create cluster`
    # short-circuits on the next run and writes no kubeconfig, and every kubectl
    # call afterwards resolves nothing -- which surfaced as a connection refused
    # to localhost:8080 three phases later, naming neither cause.
    if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
        kind delete cluster --name "$CLUSTER" 2>&1 | sed 's/^/  /'
    else
        echo "  no kind cluster $CLUSTER"
    fi

    if [ -e "$WORKSPACE/$repo" ]; then
        rm -rf "$WORKSPACE/$repo"
        echo "  removed $WORKSPACE/$repo"
    fi

    if ! gh repo view "$GIT_ORG/$repo" --json name >/dev/null 2>&1; then
        echo "  no repository $GIT_ORG/$repo"
        return 0
    fi
    # Reported, not fatal. Everything above has already happened, and failing the
    # phase here would leave the caller believing none of it did -- while the
    # only thing left is a repository name that the next scaffold will collide
    # with, which it says so about clearly.
    local out
    if out=$(gh repo delete "$GIT_ORG/$repo" --yes 2>&1); then
        echo "  deleted $GIT_ORG/$repo"
        return 0
    fi
    # gh's own words, not a guess at them. Discarding this and printing an
    # assumed cause said "gh needs the delete_repo scope" to someone who already
    # had it, which sent them to fix something that was not broken.
    echo "  could NOT delete $GIT_ORG/$repo -- everything else was removed." >&2
    printf '  %s\n' "$out" >&2
    echo "  If that is a missing scope: gh auth refresh -h github.com -s delete_repo" >&2
    echo "  Otherwise delete it in the browser." >&2

    # Fatal when scaffold is coming, because scaffold cannot succeed against a
    # repository that already has commits (ADR-062) and we already know it is
    # still there. Reporting and carrying on rebuilt the CLI, then refused --
    # work done after the run was already decided.
    if wants scaffold; then
        echo "  Stopping here: scaffold would refuse this repository anyway." >&2
        return 1
    fi
    echo "  Nothing else needs it removed, so continuing." >&2
}

do_publish() {
    require_helm3
    if [ -n "$DRY" ]; then
        say "packaging and gating $VERSION (nothing will be pushed)"
        SKIP_PUSH=1 ./scripts/package/publish.sh "$VERSION" "$OWNER"
        return
    fi
    require_ghcr_login
    say "publishing $VERSION to oci://ghcr.io/$OWNER/charts"
    ./scripts/package/publish.sh "$VERSION" "$OWNER"
}

do_cli() {
    say "building a CLI that declares $VERSION"
    TARGETS=host ./scripts/package/build-cli.sh "$VERSION" "$ROOT/bin"
    cp "$ROOT/bin/soloz-$(go env GOOS)-$(go env GOARCH)" "$ROOT/bin/soloz"

    # The one check worth making twice. A binary reporting `development` reads the
    # working tree and renders a git-shaped bundle, and every phase below would
    # succeed while testing the path this script exists to avoid.
    local got; got="$("$ROOT/bin/soloz" bundle-version)"
    [ "$got" = "$VERSION" ] || {
        echo "local-e2e: the built CLI declares '${got}', expected '${VERSION}'" >&2
        return 1
    }
    echo "bin/soloz declares $VERSION"
}

# Scaffolding refuses to clone over an existing directory, deliberately: a stale
# clone would be bootstrapped instead of the repository just created, and the
# difference is invisible in the output.
# Both halves of "already scaffolded": the local clone and the repository itself.
#
# Only the clone was checked, and the two do not go together -- a run that got as
# far as creating the repository and then failed leaves the repository without
# the clone. Preflight passed, scaffold ran, and the CLI refused on its own guard
# ("already has commits") after the cli phase had rebuilt for nothing.
workspace_is_clear() {
    local repo="$TENANT-gitops" bad=""

    if [ -e "$WORKSPACE/$repo" ]; then
        echo "local-e2e: $WORKSPACE/$repo already exists." >&2
        bad=1
    fi
    if gh repo view "$GIT_ORG/$repo" --json name >/dev/null 2>&1; then
        echo "local-e2e: $GIT_ORG/$repo already exists." >&2
        echo "  Scaffolding produces a starting state, not a fork (ADR-062), so the" >&2
        echo "  CLI refuses a repository that already has commits." >&2
        bad=1
    fi
    [ -n "$bad" ] || return 0

    local suggested="${phases[*]}"
    wants clean || suggested="clean $suggested"
    echo "  Clear both and re-run:" >&2
    echo "    ./scripts/dev/local-e2e.sh $VERSION $suggested" >&2
    return 1
}

do_scaffold() {
    local repo="$TENANT-gitops"
    mkdir -p "$WORKSPACE"
    workspace_is_clear || return 1

    say "scaffolding $GIT_ORG/$repo against $VERSION"
    ( cd "$WORKSPACE" && "$ROOT/bin/soloz" tenant scaffold --local \
        --tenant "$TENANT" --org "$GIT_ORG" --domain "$DOMAIN" \
        --cluster "$CLUSTER" --environment "$ENVIRONMENT" --provider "$PROVIDER" \
        --region "$REGION" \
        --bundle-version "$VERSION" \
        --bundle-registry "ghcr.io/$OWNER/charts" \
        --provider-token "$HCLOUD_TOKEN" \
        --gitops-token "$GITOPS_TOKEN" \
        --escrow-url "$ESCROW_URL" \
        --escrow-project-id "$ESCROW_PROJECT_ID" \
        --escrow-client-id "$ESCROW_CLIENT_ID" \
        --escrow-client-secret "$ESCROW_CLIENT_SECRET" \
        ${TAILSCALE_AUTHKEY:+--tailscale-authkey "$TAILSCALE_AUTHKEY"} )

    # The assertion the whole exercise is for. A development build renders
    # `repoURL: https://github.com/...` with a `path:`; a released one renders the
    # OCI chart. Checked here rather than left to the eye, because the bootstrap
    # that follows takes twenty minutes to tell you the same thing.
    local bundle="$WORKSPACE/$repo/clusters/$CLUSTER/bundle.yaml"
    if ! grep -q "ghcr.io/$OWNER/charts" "$bundle"; then
        echo "local-e2e: $bundle does not name the published registry." >&2
        echo "  this is the development shape, not the released one -- re-run the cli phase." >&2
        grep -A3 'repoURL' "$bundle" | sed 's/^/    /' >&2
        return 1
    fi
    if ! grep -q 'bundleVersion' "$bundle"; then
        echo "local-e2e: $bundle names a chart but sets no bundleVersion parameter." >&2
        echo "  the boundaries would render 0 Applications (ADR-068 addendum 1)." >&2
        return 1
    fi
    echo "bundle names oci://ghcr.io/$OWNER/charts at $VERSION"
}

# The bundle names charts by version and ArgoCD pulls them itself. If they are not
# in the registry, every boundary renders nothing and bootstrap reports "no
# ApplicationSet targets project boundary-01" -- after ten minutes, with a control
# plane and two workers already billing. The registry answers in two seconds.
#
# This exists because a --dry run published nothing and the phases after it went
# ahead regardless, which is exactly the failure it now prevents.
require_published() {
    local missing=""
    require_helm3
    # The four names scripts/package/flip-public.sh calls authoritative. NOT
    # "platform-bundle": that is a directory name in dist/, and the Chart.yaml
    # inside it still reads `name: environment-manager` (bundle-chart.sh:17-18),
    # so it packages and pushes under that name. Checking for it would refuse
    # every bootstrap, for a chart that is never published under that name.
    for c in environment-manager platform universal-tenant tenant-public-tls; do
        helm show chart "oci://ghcr.io/$OWNER/charts/$c" --version "$VERSION" >/dev/null 2>&1 \
            || missing="${missing}  ${c}"$'\n'
    done
    [ -z "$missing" ] && return 0

    cat >&2 <<EOF
local-e2e: ${VERSION} is not published to oci://ghcr.io/${OWNER}/charts.

Missing:
${missing}
The cluster pulls these itself, so bootstrapping now would build three servers
and then sit for ten minutes on "no ApplicationSet targets project boundary-01".

Publish first -- note that --dry deliberately pushes nothing:

  ./scripts/dev/local-e2e.sh ${VERSION} publish
EOF
    return 1
}

do_bootstrap() {
    local repo="$TENANT-gitops"
    [ -d "$WORKSPACE/$repo" ] || {
        echo "local-e2e: $WORKSPACE/$repo does not exist; run the scaffold phase first" >&2
        return 1
    }
    require_published
    say "bootstrapping $CLUSTER from $WORKSPACE/$repo"
    # From the clone, exactly as the tenant workflow does. Correct only because
    # the binary carries its platform content; a development build would resolve
    # manifests/ against this directory and find nothing.
    ( cd "$WORKSPACE/$repo" && "$ROOT/bin/soloz" bootstrap \
        --name "$CLUSTER" --provider "$PROVIDER" --region "$REGION" \
        --environment "$ENVIRONMENT" --gitops-dir . )
}

# ── Run ─────────────────────────────────────────────────────────────────────

if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
    echo "local-e2e: '$VERSION' is not semver" >&2; exit 1
fi

# --dry publishes nothing, so anything downstream of publish would run against
# charts that do not exist. Refused here rather than discovered later.
if [ -n "$DRY" ] && { wants scaffold || wants bootstrap; }; then
    echo "local-e2e: --dry pushes nothing, so scaffold and bootstrap cannot follow it." >&2
    echo "  to check packaging only:   ./scripts/dev/local-e2e.sh $VERSION publish --dry" >&2
    echo "  to run the whole loop:     ./scripts/dev/local-e2e.sh $VERSION" >&2
    exit 1
fi

# Loaded once, up front, so a missing credential stops the run before anything is
# published rather than after -- a version spent on a run that could not finish is
# a version that cannot be reused (ADR-063).
if wants scaffold || wants bootstrap || wants clean; then
    load_credentials
fi

# Every requested phase's preconditions, checked before the first one runs.
#
# Each phase used to check its own on the way in, which is too late: the phases
# run in order, so a scaffold that could not start was discovered after publish
# had already pushed. ADR-063 consumes a version by publishing it, so that cost a
# version -- twice -- for a leftover directory a one-second test would have found.
preflight() {
    if wants publish; then
        require_helm3
        if [ -z "$DRY" ]; then
            require_ghcr_login
            # Probed before packaging rather than after. publish.sh refuses a
            # version already in the registry, correctly, but only once the three
            # minutes of packaging are done.
            if helm show chart "oci://ghcr.io/$OWNER/charts/environment-manager" \
                 --version "$VERSION" >/dev/null 2>&1; then
                echo "local-e2e: $VERSION is already published to ghcr.io/$OWNER/charts." >&2
                echo "  A version is consumed by publishing it (ADR-063); take the next one." >&2
                echo "  To reuse what is already there, skip the publish phase:" >&2
                echo "    ./scripts/dev/local-e2e.sh $VERSION cli scaffold bootstrap" >&2
                return 1
            fi
        fi
    fi

    # Only when scaffold will not create it: scaffold's own precondition is that
    # it does NOT exist, and bootstrap's is that it does.
    # Two preconditions that are each other's opposite, so neither may be applied
    # to a state an earlier phase in THIS run is about to produce.
    #
    # Scaffold needs the clone absent -- unless clean runs first, whose whole job
    # is to remove it. Bootstrap needs it present -- unless scaffold runs first,
    # which creates it. Written as an if/elif, the clean case fell through to the
    # bootstrap arm and demanded a clone that the very same run was about to make.
    if wants scaffold; then
        wants clean || workspace_is_clear
    elif wants bootstrap; then
        [ -d "$WORKSPACE/$TENANT-gitops" ] || {
            echo "local-e2e: $WORKSPACE/$TENANT-gitops does not exist; run the scaffold phase first" >&2
            return 1
        }
    fi

    # The charts bootstrap needs, unless this run is about to publish them.
    if wants bootstrap && ! wants publish; then
        require_published
    fi
}

preflight

for p in clean publish cli scaffold bootstrap; do
    wants "$p" && "do_$p"
done

say "done: $VERSION"
if wants bootstrap; then
    cat <<EOF

Tear down when finished:

  $ROOT/bin/soloz teardown --name $CLUSTER --force --confirm
  rm -rf $WORKSPACE/$TENANT-gitops
  gh repo delete $GIT_ORG/$TENANT-gitops --yes
EOF
fi
