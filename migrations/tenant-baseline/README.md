# Tenant Baseline Migrations

This directory contains Atlas-compatible SQL migrations for provisioning tenant databases in the shared CNPG cluster.

## Architecture: Database-Per-Tenant

Each tenant gets a dedicated logical database within the shared CNPG cluster:
- Database naming: `tenant_<id>_db` (e.g., `tenant_acme_db`)
- All tables created in `public` schema
- No tenant-specific schemas or roles
- Database-level isolation between tenants

## Migration Naming Convention

Migrations follow Atlas naming convention: `YYYYMMDDHHMMSS_description.sql`

Example: `20240101000001_create_users_table.sql`

## Idempotency

All migrations MUST be idempotent using `IF NOT EXISTS` or `IF EXISTS` clauses to support:
- Drift detection and recovery
- Safe re-application
- Multiple reconciliation loops

## Migration Order

1. `20240101000001_create_users_table.sql` - Users table with RLS
2. `20240101000002_create_sessions_table.sql` - Sessions table with RLS
3. `20240101000003_create_identities_table.sql` - Identities table with RLS
4. `20240101000004_create_buckets_table.sql` - Buckets table with RLS
5. `20240101000005_create_objects_table.sql` - Objects table with RLS

## Row-Level Security (RLS)

All tables enforce RLS policies using JWT claims for end-user isolation:
- `current_setting('request.jwt.claims', true)::json->>'user_id'` extracts user_id from JWT
- Policies ensure users only access their own data within the tenant database
- PostgREST automatically applies RLS based on JWT
- Platform-level tenant isolation is enforced by dedicated databases (not RLS)

## Composite Migrations

Atlas Operator merges two migration sources:
1. **Tenant Baseline** (this directory): Applied to all tenant databases
2. **Tenant-Specific**: `fleet-registry/tenants/tenant-<id>/migrations/` for custom tables

## Testing Migrations

Test migrations locally before committing:

```bash
# Create test database
createdb tenant_test_db

# Apply migrations to test database
psql -U postgres -d tenant_test_db -f 20240101000001_create_users_table.sql
psql -U postgres -d tenant_test_db -f 20240101000002_create_sessions_table.sql
# ... apply remaining migrations

# Verify tables exist in public schema
psql -U postgres -d tenant_test_db -c "\dt public.*"

# Verify RLS is enabled
psql -U postgres -d tenant_test_db -c "SELECT tablename, rowsecurity FROM pg_tables WHERE schemaname = 'public';"
```

## Key Changes from Schema-Based Model

- **Removed**: Schema creation, role creation, search_path configuration
- **Changed**: All table references from `tenant_{{.tenant_id}}.table_name` to `public.table_name`
- **Changed**: All function references from `tenant_{{.tenant_id}}.function_name` to `public.function_name`
- **Removed**: Template variable `{{.tenant_id}}` (no longer needed in migrations)
- **Added**: Database-level isolation via deterministic naming (`tenant_<id>_db`)
