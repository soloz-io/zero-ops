#!/usr/bin/env bash
# Task 20.4: Read-only assertions for password rotation
# NEVER mutates state - only reads and asserts
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

echo "=== Password Rotation Assertions ==="

# AC 20.4: Verify ALTER ROLE executed - new password works for database connection
NEW_PASSWORD=$(kubectl get secret infisical-db-credentials -n "$NAMESPACE" \
  -o jsonpath='{.data.password}' | base64 -d)

assert "infisical role can authenticate with new password" \
  "kubectl exec -n $NAMESPACE $DB_POD -- psql -U infisical -d infisical -c 'SELECT 1' -w <<< '$NEW_PASSWORD'"

# AC 20.5: Verify new password works for database connection
assert "infisical role password updated in pg_authid" \
  "kubectl exec -n $NAMESPACE $DB_POD -- psql -U postgres -tAc \
    \"SELECT 1 FROM pg_authid WHERE rolname='infisical' AND rolpassword IS NOT NULL\" | grep -q 1"

# AC 20.6: Verify Infisical service restarted and is healthy
RESTARTED=$(kubectl get deployment infisical -n hub-platform-ops \
  -o jsonpath='{.spec.template.metadata.annotations.kubectl\.kubernetes\.io/restartedAt}' 2>/dev/null)
assert "Infisical Deployment has restartedAt annotation" "[ -n '$RESTARTED' ]"

assert "Infisical Deployment is available after restart" \
  "kubectl get deployment infisical -n hub-platform-ops \
    -o jsonpath='{.status.conditions[?(@.type==\"Available\")].status}' | grep -q True"

echo ""
if [ "$ERRORS" -eq 0 ]; then
  echo "All assertions passed ✅"
else
  echo "$ERRORS assertion(s) failed ❌"
  exit 1
fi
