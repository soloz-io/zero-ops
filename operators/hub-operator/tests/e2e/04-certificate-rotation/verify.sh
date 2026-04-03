#!/usr/bin/env bash
# Task 19.10: Read-only assertions for certificate rotation
# NEVER mutates state - only reads and asserts
set -euo pipefail

ERRORS=0
NAMESPACE="hub-platform-data"

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

echo "=== Certificate Rotation Assertions ==="

# Verify DB_ROOT_CERT in infisical-secrets matches platform-db-ca
CA_CERT=$(kubectl get secret platform-db-ca -n "$NAMESPACE" -o jsonpath='{.data.ca\.crt}' | base64 -d)
DB_ROOT_CERT=$(kubectl get secret infisical-secrets -n "$NAMESPACE" -o jsonpath='{.data.DB_ROOT_CERT}' | base64 -d)
assert "DB_ROOT_CERT matches platform-db-ca" "[ '$CA_CERT' = '$DB_ROOT_CERT' ]"

# Verify each service was restarted (has restartedAt annotation)
for deploy in infisical mcp-server; do
  NS="hub-platform-ops"
  RESTARTED=$(kubectl get deployment "$deploy" -n "$NS" \
    -o jsonpath='{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}' 2>/dev/null)
  assert "$deploy Deployment has restartedAt annotation" "[ -n '$RESTARTED' ]"
done

for deploy in hydra kratos keto; do
  RESTARTED=$(kubectl get deployment "$deploy" -n ory-system \
    -o jsonpath='{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}' 2>/dev/null)
  assert "$deploy Deployment has restartedAt annotation" "[ -n '$RESTARTED' ]"
done

RESTARTED=$(kubectl get statefulset spire-server -n spire-system \
  -o jsonpath='{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}' 2>/dev/null)
assert "spire-server StatefulSet has restartedAt annotation" "[ -n '$RESTARTED' ]"

# Verify no x509 errors in operator logs after rotation
OPERATOR_POD=$(kubectl get pod -n hub-platform-ops -l control-plane=hub-operator \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
assert "No x509 certificate errors in operator logs" \
  "! kubectl logs '$OPERATOR_POD' -n hub-platform-ops --since=5m 2>/dev/null | grep -q 'x509'"

echo ""
if [ "$ERRORS" -eq 0 ]; then
  echo "All assertions passed ✅"
else
  echo "$ERRORS assertion(s) failed ❌"
  exit 1
fi
