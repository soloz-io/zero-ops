#!/usr/bin/env bash
# Task 16.12: Read-only assertions for Secret Zero generation
# NEVER mutates state - only reads and asserts
set -euo pipefail

NAMESPACE="hub-platform-data"
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

echo "=== Secret Zero Generation Assertions ==="

# AC 16.13: Verify ENCRYPTION_KEY is 32 characters
KEY=$(kubectl get secret infisical-secrets -n "$NAMESPACE" -o jsonpath='{.data.ENCRYPTION_KEY}' | base64 -d)
assert "ENCRYPTION_KEY is 32 characters" "[ ${#KEY} -eq 32 ]"

# AC 16.14: Verify AUTH_SECRET is 32 characters
AUTH=$(kubectl get secret infisical-secrets -n "$NAMESPACE" -o jsonpath='{.data.AUTH_SECRET}' | base64 -d)
assert "AUTH_SECRET is 32 characters" "[ ${#AUTH} -eq 32 ]"

# AC 16.15: Verify REDIS_URL format
REDIS_URL=$(kubectl get secret infisical-secrets -n "$NAMESPACE" -o jsonpath='{.data.REDIS_URL}' | base64 -d)
assert "REDIS_URL starts with redis://" "echo '$REDIS_URL' | grep -q '^redis://'"

# AC 16.16: Verify DB_ROOT_CERT contains BEGIN CERTIFICATE
DB_CERT=$(kubectl get secret infisical-secrets -n "$NAMESPACE" -o jsonpath='{.data.DB_ROOT_CERT}' | base64 -d)
assert "DB_ROOT_CERT contains BEGIN CERTIFICATE" "echo '$DB_CERT' | grep -q 'BEGIN CERTIFICATE'"

# AC 16.17: Verify platform-db-ca contains valid X.509 certificate
CA_CERT=$(kubectl get secret platform-db-ca -n "$NAMESPACE" -o jsonpath='{.data.ca\.crt}' | base64 -d)
assert "platform-db-ca contains valid X.509 certificate" "echo '$CA_CERT' | openssl x509 -noout 2>/dev/null"

# AC 16.18: Verify ownerReferences set on infisical-secrets
OWNER=$(kubectl get secret infisical-secrets -n "$NAMESPACE" -o jsonpath='{.metadata.ownerReferences[0].kind}')
assert "infisical-secrets has ownerReference to HubEnvironment" "[ '$OWNER' = 'HubEnvironment' ]"

# Verify db-credentials label on credential secrets
for secret in infisical-db-credentials hydra-db-credentials kratos-db-credentials keto-db-credentials; do
  LABEL=$(kubectl get secret "$secret" -n "$NAMESPACE" -o jsonpath='{.metadata.labels.ops\.zero-ops\.io/db-credentials}' 2>/dev/null)
  assert "$secret has db-credentials=true label" "[ '$LABEL' = 'true' ]"
done

echo ""
if [ "$ERRORS" -eq 0 ]; then
  echo "All assertions passed ✅"
else
  echo "$ERRORS assertion(s) failed ❌"
  exit 1
fi
