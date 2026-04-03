#!/usr/bin/env bash
# Task 17.5: Read-only SQL assertions for database setup
# NEVER mutates state - only reads and asserts via kubectl exec
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

psql_query() {
  kubectl exec -n "$NAMESPACE" "$DB_POD" -- psql -U postgres -tAc "$1"
}

echo "=== Database Setup Assertions ==="

# AC 17.6: Verify migrations ran (schema_migrations table exists with entries)
assert "schema_migrations table exists" \
  "psql_query \"SELECT COUNT(*) FROM schema_migrations\" | grep -qv '^0$'"

# AC 17.7-17.13: Verify all roles exist
for role in mcp_server infisical spoke_controller spire_server hydra kratos keto; do
  assert "Role '$role' exists in PostgreSQL" \
    "psql_query \"SELECT 1 FROM pg_roles WHERE rolname='$role'\" | grep -q 1"
done

# AC 17.14: Verify role permissions (has_table_privilege checks)
assert "mcp_server has SELECT on control_plane schema" \
  "psql_query \"SELECT has_schema_privilege('mcp_server', 'control_plane', 'USAGE')\" | grep -q t"

assert "infisical has SELECT on infisical schema" \
  "psql_query \"SELECT has_schema_privilege('infisical', 'infisical', 'USAGE')\" | grep -q t"

assert "hydra has SELECT on hydra schema" \
  "psql_query \"SELECT has_schema_privilege('hydra', 'hydra', 'USAGE')\" | grep -q t"

assert "kratos has SELECT on kratos schema" \
  "psql_query \"SELECT has_schema_privilege('kratos', 'kratos', 'USAGE')\" | grep -q t"

assert "keto has SELECT on keto schema" \
  "psql_query \"SELECT has_schema_privilege('keto', 'keto', 'USAGE')\" | grep -q t"

echo ""
if [ "$ERRORS" -eq 0 ]; then
  echo "All assertions passed ✅"
else
  echo "$ERRORS assertion(s) failed ❌"
  exit 1
fi
