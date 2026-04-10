# Tenant Baseline Migrations

This directory contains Atlas-compatible SQL migrations for provisioning tenant schemas in the shared CNPG cluster.

## Migration Naming Convention

Migrations follow Atlas naming convention: `YYYYMMDDHHMMSS_description.sql`

Example: `20240101000001_create_schema.sql`

## Idempotency

All migrations MUST be idempotent using `IF NOT EXISTS` or `IF EXISTS` clauses to support:
- Drift detection and recovery
- Safe re-application
- Multiple reconciliation loops

## Template Variables

Migrations use Go template syntax for tenant-specific values:
- `{{.tenant_id}}` - Tenant identifier (e.g., "acme")

Atlas Operator replaces these variables during migration application.

## Migration Order

1. `20240101000001_create_schema.sql` - Schema and role creation
2. `20240101000002_create_users_table.sql` - Users table with RLS
3. `20240101000003_create_sessions_table.sql` - Sessions table with RLS
4. `20240101000004_create_identities_table.sql` - Identities table with RLS
5. `20240101000005_create_buckets_table.sql` - Buckets table with RLS
6. `20240101000006_create_objects_table.sql` - Objects table with RLS

## Row-Level Security (RLS)

All tables enforce RLS policies using JWT claims:
- `current_setting('request.jwt.claims', true)::json->>'user_id'` extracts user_id from JWT
- Policies ensure users only access their own data
- PostgREST automatically applies RLS based on JWT

## Testing Migrations

Test migrations locally before committing:

```bash
# Render template with test tenant_id
sed 's/{{.tenant_id}}/test-tenant/g' 20240101000001_create_schema.sql > /tmp/test.sql

# Apply to local PostgreSQL
psql -U postgres -f /tmp/test.sql
```
