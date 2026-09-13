#!/usr/bin/env bash
# Make a Helm 3 available, pinned to the version the release workflow uses.
#
# Sourced by scripts/dev/local-e2e.sh and by scripts/package/publish.sh so the
# local loop and a release package through ONE toolchain. A difference here is a
# difference in what gets published, which is the whole reason the local loop
# exists.

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
