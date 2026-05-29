#!/usr/bin/env bash
# Task 21.8: Read-only assertions for resource pruning
# NEVER mutates state - only reads and asserts
# This script runs AFTER pruning (after 03-assert.yaml) to verify final state
set -euo pipefail

NAMESPACE="hub-platform-data"
DB_POD=$(kubectl get pod -n "$NAMESPACE" -l cnpg.io/instanceRole=primary -o jsonpath='{.items[0].metadata.name}')
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

assert_not_exists() {
  local desc="$1"
  local cmd="$2"
  if eval "$cmd" 2>/dev/null; then
    echo "❌ $desc (should not exist)"
    ERRORS=$((ERRORS + 1))
  else
    echo "✅ $desc (correctly deleted)"
  fi
}

echo "=== Resource Pruning Assertions (Final State) ==="

# AC 21.5: Verify 2 roles exist
echo ""
echo "Checking database roles..."

for role in mcp_server infisical; do
  assert "role $role exists" \
    "kubectl exec -n $NAMESPACE $DB_POD -- psql -U postgres -tAc \
      \"SELECT 1 FROM pg_roles WHERE rolname='$role'\" | grep -q 1"
done

# AC 21.6: Verify 2 OAuth clients exist (web-ui deleted)
echo ""
echo "Checking OAuth clients..."

for client in hub-cli mcp-client; do
  assert "OAuth client $client exists" \
    "curl -s http://hydra-admin.ory-system.svc:4444/clients/$client | grep -q $client"
done

assert_not_exists "OAuth client web-ui deleted" \
  "curl -s http://hydra-admin.ory-system.svc:4444/clients/web-ui | grep -q web-ui"

# AC 21.7: Verify 2 NATS streams exist (audit_events deleted)
echo ""
echo "Checking NATS streams..."

for stream in spoke_events billing_events; do
  assert "NATS stream $stream exists" \
    "kubectl exec -n hub-platform-core -l app=nats -- nats stream info $stream 2>/dev/null | grep -q '$stream'"
done

assert_not_exists "NATS stream audit_events deleted" \
  "kubectl exec -n hub-platform-core -l app=nats -- nats stream info audit_events 2>/dev/null | grep -q audit_events"

echo ""
if [ "$ERRORS" -eq 0 ]; then
  echo "All assertions passed ✅"
else
  echo "$ERRORS assertion(s) failed ❌"
  exit 1
fi