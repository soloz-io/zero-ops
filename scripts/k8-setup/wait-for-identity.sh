#!/usr/bin/env bash

set -euo pipefail

KUBECONFIG_PATH="${1:-}"
CERT_TIMEOUT="${2:-1200}"

if [[ -z "$KUBECONFIG_PATH" ]]; then
    echo "Usage: $0 <kubeconfig_path> [timeout_seconds]"
    exit 1
fi

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1"; }
error_exit() { log "ERROR: $1"; exit 1; }

log "Step 10c: Waiting for Identity Infrastructure prerequisites..."
identity_attempt=1
identity_max_attempts=$((CERT_TIMEOUT / 10))
identity_ready=false

while [[ $identity_attempt -le $identity_max_attempts ]]; do
    if kubectl wait --for=condition=IdentityReady hubenvironment hub-environment \
        --kubeconfig="$KUBECONFIG_PATH" --timeout=5s >/dev/null 2>&1; then
        identity_ready=true
        log "✅ Identity Infrastructure is ready (HubEnvironment condition met)"
        break
    fi

    log "Waiting for Identity Infrastructure (attempt $identity_attempt/$identity_max_attempts). Normally takes 98 attempts.."
    
    # Log hints for debugging if taking a long time
    if [[ $((identity_attempt % 6)) -eq 0 ]]; then
        log "  Hint: Check 'kubectl get externalsecret -n platform-identity' or 'argocd app get ory-hydra'"
    fi
    
    sleep 10
    ((identity_attempt++))
done

if [[ "$identity_ready" != "true" ]]; then
    log "❌ Identity Infrastructure not ready within timeout"
    error_exit "identity infrastructure timeout"
fi
