#!/usr/bin/env bash
# Validate that the Day-0 ArgoCD seed matches what GitOps declares for ArgoCD.
#
# The hub bootstrap installs ArgoCD imperatively (internal/soloz-cli/components/
# installer.go) because nothing can deploy the deployer. That seed is superseded
# at sync wave 1 by the 'platform-argocd' Application in boundary 01, which
# adopts the release.
#
# Adoption is only safe when the two agree. Where they disagree the Application
# wins, and it wins SILENTLY -- the revert is indistinguishable from a normal
# reconcile:
#
#   * a chart-version disagreement makes ArgoCD upgrade itself while the rest of
#     boundary 01 is still syncing, restarting the application-controller and
#     repo-server mid-bootstrap. It was 7.7.12 vs 7.8.0 until 2026-09-05.
#   * a settings disagreement means anything the seed sets but Git omits is
#     dropped on first sync. controller.diff.server.side was seed-only, so the
#     server-side-diff fix for the 2026-09-02 incident (eleven Applications stuck
#     OutOfSync-but-Healthy) reverted itself every bootstrap.
#
# This is the regression guard for that class of divergence. It is a static
# check: it renders the chart and reads the Go source, and needs no cluster.
#
# Retire this script when the seed installs directly from the Git-declared
# values file -- at that point the two cannot diverge by construction.
#
# Depends on: helm, yq (mikefarah), standard POSIX tools.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

versions_go="$repo_root/internal/soloz-cli/versions/versions.go"
installer_go="$repo_root/internal/soloz-cli/components/installer.go"
env_mgr="$repo_root/manifests/argocd/environment-manager"

fail_count=0
pass() { echo "  ✅ $1"; }
fail() { echo "  ❌ $1"; fail_count=$((fail_count + 1)); }

for bin in helm yq; do
  command -v "$bin" >/dev/null 2>&1 || { echo "❌ required tool not found: $bin"; exit 1; }
done

echo "Validating Day-0 ArgoCD seed parity..."

# ─── Render the boundary-01 ApplicationSet ───────────────────────────────────
# The matrix dimensions have no defaults by design (every bootstrap passes them
# explicitly), so supply a representative combination. None of them influence
# the platform-argocd element.
rendered="$(mktemp)"
trap 'rm -f "$rendered"' EXIT

helm template seed-parity "$env_mgr" \
  --set environmentSlug=dev \
  --set provider=hybrid \
  --set instanceRepoURL=https://github.com/example-org/example-gitops \
  --set hubDomain=dev.example.test \
  --set topology=single \
  --set publicTlsIssuer=letsencrypt-prod \
  --set oidcIssuer=https://auth.example.invalid \
  >"$rendered"

# Since ADR-061 a component's declaration may be a descriptor file rather than an
# inline element of the boundary. Prefer the descriptor and fall back to the
# inline list, so this check keeps working for boundaries on either side of the
# conversion.
#
# The `select(. != null)` on the inline path is load-bearing: yq emits a null
# document for every input document a `select` rejects, and this render carries
# more than a dozen. Without it the extraction concatenates those nulls with the
# one real match and every downstream query reads a multi-document string.
argocd_descriptor="$repo_root/manifests/argocd/components/01/platform-argocd.yaml"

if [[ -f "$argocd_descriptor" ]]; then
  argocd_element="$(cat "$argocd_descriptor")"
  source_desc="descriptor manifests/argocd/components/01/platform-argocd.yaml"
else
  argocd_element="$(yq \
    'select(.kind == "ApplicationSet" and .metadata.name == "01-platform-infra")
     | .spec.generators[]? | select(has("list")) | .list.elements[]
     | select(.appName == "platform-argocd")' "$rendered" \
    | yq 'select(. != null)')"
  source_desc="inline element of 01-platform-infra"
fi

if [[ -z "$argocd_element" ]]; then
  fail "no 'platform-argocd' declaration found, as a descriptor or as an inline element -- ArgoCD is not declared in Git at all, so nothing supersedes the Day-0 seed"
  exit 1
fi
echo "  (reading ArgoCD's declaration from: ${source_desc})"

# ─── 1. Chart version parity ─────────────────────────────────────────────────
go_version="$(sed -n 's/^[[:space:]]*ArgoCDChartVersion[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$versions_go")"
git_version="$(printf '%s' "$argocd_element" | yq '.targetRevision')"

if [[ -z "$go_version" ]]; then
  fail "could not read ArgoCDChartVersion from $versions_go"
elif [[ "$go_version" == "$git_version" ]]; then
  pass "chart version agrees: $go_version"
else
  fail "chart version mismatch: versions.go has '$go_version', ApplicationSet declares '$git_version'"
fi

# ─── 2. Every seed --set has a Git declaration ───────────────────────────────
# Keyed by the Helm value path the seed sets, valued by a yq expression that
# must resolve to non-null in the Application's declared helmValues.
declare -a set_keys=(
  'configs.params."controller.diff.server.side"'
  '.repoServer.env[] | select(.name == "ARGOCD_EXEC_TIMEOUT") | .value'
)
declare -a set_labels=(
  'controller.diff.server.side'
  'repoServer.env ARGOCD_EXEC_TIMEOUT'
)

helm_values="$(printf '%s' "$argocd_element" | yq '.helmValues')"

for idx in "${!set_keys[@]}"; do
  expr="${set_keys[$idx]}"
  label="${set_labels[$idx]}"
  [[ "$expr" != .* ]] && expr=".$expr"

  value="$(printf '%s' "$helm_values" | yq "$expr" 2>/dev/null | tr -d '[:space:]' || true)"
  if [[ -n "$value" && "$value" != "null" ]]; then
    pass "declared in Git: $label = $value"
  else
    fail "seed sets '$label' but the platform-argocd Application does not declare it -- it will revert on first sync"
  fi
done

# ─── 3. No undeclared --set flags crept back in ──────────────────────────────
# Counts the --set flags in the seed and compares against the number this script
# knows how to verify. A new flag added without extending set_keys above fails
# here rather than silently escaping parity checking.
#
# An env-style flag spends two --set literals (name and value) on ONE value
# path, so collapse those to the path root before counting. Both Go quoting
# forms appear: interpreted ("...") and raw (`...`, used where the key itself
# contains escaped dots).
declared_set_count="${#set_keys[@]}"
actual_paths="$(grep -oE '"--set", (`[^`]*`|"[^"]*")' "$installer_go" \
  | sed -E 's/^"--set", [`"]//; s/[`"]$//' \
  | sed -E 's/\[0\]\.(name|value)=.*/[0]/; s/=.*//' \
  | sort -u)"
actual_set_paths="$(printf '%s\n' "$actual_paths" | grep -c . || true)"

if [[ "$actual_set_paths" -eq "$declared_set_count" ]]; then
  pass "seed sets $actual_set_paths value path(s), all covered by this check"
else
  fail "seed sets $actual_set_paths value path(s) but only $declared_set_count are parity-checked -- extend set_keys[] in this script"
  printf '     seed paths: %s\n' "$(printf '%s' "$actual_paths" | tr '\n' ' ')"
fi

echo
if [[ "$fail_count" -gt 0 ]]; then
  echo "❌ ArgoCD seed parity: $fail_count check(s) failed"
  exit 1
fi
echo "✅ ArgoCD seed parity: all checks passed"
