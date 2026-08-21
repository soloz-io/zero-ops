#!/usr/bin/env bash
# Shared harness for the validation modules in preflight/ and cluster/.
#
# Sourced by every module. Modules only call the reporting helpers below and
# never manage counters or exit codes themselves — run.sh owns the aggregate
# result so that a module can be executed standalone during debugging without
# behaving differently from how it behaves inside a bootstrap.

# ─── Severity model ──────────────────────────────────────────────────────────
# Validation runs at two moments with different tolerances:
#
#   VALIDATE_MODE=gate   mid-bootstrap. The platform is still converging, so a
#                        resource that has not appeared yet is not a defect.
#   VALIDATE_MODE=final  after bootstrap. Everything must have converged; the
#                        same condition is now a defect.
#
# `soft_fail` encodes exactly that difference, so one module implementation
# serves both moments instead of two copies drifting apart. `hard_fail` is for
# conditions that are wrong regardless of timing (a wrong environment slug is
# not going to converge into the right one).
VALIDATE_MODE="${VALIDATE_MODE:-final}"

PASS_COUNT=0
FAIL_COUNT=0
WARN_COUNT=0
declare -a FAIL_MESSAGES=()
declare -a WARN_MESSAGES=()

pass() { echo "  ✅ $1"; ((PASS_COUNT++)); return 0; }

hard_fail() {
    echo "  ❌ $1"
    FAIL_MESSAGES+=("$1")
    ((FAIL_COUNT++))
    return 0
}

warn() {
    echo "  ⚠️  $1"
    WARN_MESSAGES+=("$1")
    ((WARN_COUNT++))
    return 0
}

soft_fail() {
    if [[ "$VALIDATE_MODE" == "gate" ]]; then
        warn "$1 (not yet converged — enforced after bootstrap)"
    else
        hard_fail "$1"
    fi
}

note() { echo "     $1"; }

section() {
    echo ""
    echo "── $1 $(printf '─%.0s' $(seq 1 $((60 - ${#1} > 0 ? 60 - ${#1} : 0))))"
}

# ─── Context ─────────────────────────────────────────────────────────────────
VALIDATE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENVIRONMENT="${ENVIRONMENT:-dev}"

# The public DNS zone for this environment. prod is un-prefixed (ADR-051); every
# other environment is prefixed. Must stay in lockstep with the --domain-filter
# expression in 03-platform-services-appset.yaml, which is what actually scopes
# what a spoke may write.
if [[ "$ENVIRONMENT" == "prod" ]]; then
    ENV_ZONE="nutgraf.in"
else
    ENV_ZONE="${ENVIRONMENT}.nutgraf.in"
fi

# ─── Cluster access (cluster/ modules only) ──────────────────────────────────
HUB_KUBECONFIG="${HUB_KUBECONFIG:-${KUBECONFIG_PATH:-${KUBECONFIG:-}}}"
SPOKE_KUBECONFIG="${SPOKE_KUBECONFIG:-}"
SPOKEPOOL_NAME="${SPOKEPOOL_NAME:-}"

kc() { kubectl --kubeconfig="$HUB_KUBECONFIG" "$@" 2>/dev/null; }
kc_spoke() { kubectl --kubeconfig="$SPOKE_KUBECONFIG" "$@" 2>/dev/null; }

# Hub-side readiness (SpokePool Ready, CAPI Provisioned) does not prove the spoke
# is functional, so spoke checks talk to the spoke directly. Returns non-zero
# when the spoke is unreachable; callers skip rather than fail, because an
# unreachable spoke is already reported by the spoke readiness checks.
_SPOKE_KC_TMP=""
ensure_spoke_kubeconfig() {
    [[ -n "$SPOKE_KUBECONFIG" && -f "$SPOKE_KUBECONFIG" ]] && return 0
    [[ -z "$SPOKEPOOL_NAME" ]] && return 1
    _SPOKE_KC_TMP="$(mktemp)"
    if ! kc get secret "${SPOKEPOOL_NAME}-kubeconfig" -n platform-capi \
        -o jsonpath='{.data.value}' | base64 -d > "$_SPOKE_KC_TMP" 2>/dev/null; then
        rm -f "$_SPOKE_KC_TMP"; _SPOKE_KC_TMP=""
        return 1
    fi
    SPOKE_KUBECONFIG="$_SPOKE_KC_TMP"
    kubectl --kubeconfig="$SPOKE_KUBECONFIG" cluster-info >/dev/null 2>&1 || return 1
    return 0
}

cleanup_spoke_kubeconfig() { [[ -n "$_SPOKE_KC_TMP" ]] && rm -f "$_SPOKE_KC_TMP"; }
