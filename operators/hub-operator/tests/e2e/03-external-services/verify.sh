#!/usr/bin/env bash
# Task 18.9: Read-only API assertions for external services configuration
# NEVER mutates state - only reads and asserts
set -euo pipefail

ERRORS=0

assert() {
  local desc="$1"
  local cmd="$2"
  if eval "$cmd" > /dev/null 2>&1; then
    echo "✅ $desc"
  else
    echo "❌ $desc"
    ERRORS=$((ERRORS + 1))
  fi
}

echo "=== External Services Configuration Assertions ==="

# AC 18.10: Verify secrets exist in Infisical via API
INFISICAL_TOKEN=$(kubectl get secret infisical-auth -n hub-platform-ops -o jsonpath='{.data.token}' | base64 -d)
INFISICAL_URL="https://infisical.hub-platform-ops.svc"

assert "Infisical API is reachable" \
  "curl -sf -H 'Authorization: Bearer $INFISICAL_TOKEN' '$INFISICAL_URL/api/v1/user' -o /dev/null"

assert "mcp_server-db-password secret exists in Infisical" \
  "curl -sf -H 'Authorization: Bearer $INFISICAL_TOKEN' '$INFISICAL_URL/api/v3/secrets/raw/mcp_server-db-password?workspaceSlug=platform&environment=prod' -o /dev/null"

# AC 18.11: Verify OAuth clients exist in Hydra via API
HYDRA_URL="https://hydra-admin.ory-system.svc"

assert "mcp-server OAuth client exists in Hydra" \
  "curl -sf '$HYDRA_URL/admin/clients/mcp-server' -o /dev/null"



# AC 18.12: Verify NATS streams exist via NATS CLI
NATS_POD=$(kubectl get pod -n hub-platform-core -l app.kubernetes.io/name=nats -o jsonpath='{.items[0].metadata.name}')

assert "spoke-events NATS stream exists" \
  "kubectl exec -n hub-platform-core $NATS_POD -- nats stream info spoke-events --server nats://localhost:4222"

assert "hub-events NATS stream exists" \
  "kubectl exec -n hub-platform-core $NATS_POD -- nats stream info hub-events --server nats://localhost:4222"

# AC 18.13: Verify UploadedSecrets array populated in status
UPLOADED=$(kubectl get hubenvironment hub-production -n hub-platform-ops -o jsonpath='{.status.uploadedSecrets}')
assert "UploadedSecrets array is populated in HubEnvironment status" \
  "[ -n '$UPLOADED' ] && [ '$UPLOADED' != 'null' ]"

echo ""
if [ "$ERRORS" -eq 0 ]; then
  echo "All assertions passed ✅"
else
  echo "$ERRORS assertion(s) failed ❌"
  exit 1
fi
