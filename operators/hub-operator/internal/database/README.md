# Database Package

This package implements database migration execution and role provisioning for the Hub Operator.

## Migrator

The `Migrator` handles database schema migrations using golang-migrate/migrate with embedded SQL files.

### Key Features

- **Direct Primary Connection**: Connects to `platform-db-rw` (CNPG primary service), NOT the PgBouncer pooler (Requirement 5.2)
- **TLS Required**: All connections use `sslmode=require` (Requirement 5.3)
- **Embedded Migrations**: SQL files embedded in binary via `internal/embed/migrations/` (Requirement 5.4)
- **Dirty State Detection**: Detects and reports dirty database state as permanent error (Requirement 5.5)
- **Transient Error Handling**: Distinguishes transient errors (connection timeout, network failure) from permanent errors (Requirement 5.6)

### Usage

```go
import (
    "context"
    "github.com/zero-ops/hub-operator/internal/database"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

// Create migrator
migrator, err := database.NewMigrator(ctx, k8sClient, "hub-platform-data")
if err != nil {
    return err
}
defer migrator.Close()

// Run migrations
if err := migrator.RunMigrations(ctx); err != nil {
    // Check if dirty database error (permanent)
    if database.IsDirtyDatabaseError(err) {
        // DO NOT requeue - requires manual intervention
        // Set condition to False with reason "DirtyDatabase"
        return err
    }
    // Transient error - requeue with backoff
    return err
}
```

### Error Handling

**DirtyDatabaseError (Permanent)**:
- Database migration failed and left schema in inconsistent state
- Requires manual CLI intervention: `migrate force <version>`
- Operator MUST NOT requeue automatically
- Set `MigrationsComplete` condition to False with reason "DirtyDatabase"

**Transient Errors**:
- Connection timeout, network failure, DNS resolution failure
- Operator should requeue with exponential backoff
- Safe to retry infinitely

### Migration Files

SQL migration files are stored in `internal/embed/migrations/` and embedded at compile time:

- `0_grants.up.sql` - Initial schema grants
- `1_agent_infra_status.up.sql` - Agent infrastructure status table
- `2_agent_infra_status_rls.up.sql` - Row-level security policies

Migration files follow golang-migrate naming convention: `{version}_{description}.up.sql`

### Connection Details

- **Host**: `platform-db-rw.{namespace}.svc` (CNPG primary service)
- **Port**: 5432
- **Credentials**: Read from `platform-db-superuser` secret
- **TLS**: Required (`sslmode=require`), CA from `platform-db-ca` secret
- **Database**: Value from `platform-db-superuser` secret `dbname` field

### Manual Recovery Procedure

If migrations fail with dirty database state:

1. Operator sets `MigrationsComplete` condition to False with reason "DirtyDatabase"
2. Platform operator manually fixes the issue:
   ```bash
   # Connect to database
   kubectl exec -it platform-db-1 -n hub-platform-data -- psql
   
   # Inspect schema_migrations table
   SELECT * FROM schema_migrations;
   
   # Force version after manual fix
   # (Use golang-migrate CLI or direct SQL)
   ```
3. Platform operator adds annotation to resume reconciliation:
   ```bash
   kubectl annotate hubenvironment hub \
     ops.zero-ops.io/reconcile-trigger="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
   ```
4. Operator clears DirtyDatabase condition and retries

## RoleManager

The `RoleManager` handles database role creation, password rotation, and pruning.

### Key Features

- **Idempotent Role Creation**: Uses `CREATE ROLE IF NOT EXISTS` pattern (Requirement 6.9)
- **Password Drift Detection**: Compares secret hash with pg_authid (Requirement 6.11)
- **Automatic Password Rotation**: Executes `ALTER ROLE` when password changes (Requirement 6.10)
- **Permission Granting**: Grants SELECT, INSERT, UPDATE, DELETE based on CR spec (Requirement 6.8)
- **Orphaned Role Pruning**: Deletes roles not in CR spec (Requirement 6.15)

### Usage

```go
import (
    "context"
    "github.com/zero-ops/hub-operator/internal/database"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

// Create role manager
roleManager, err := database.NewRoleManager(ctx, k8sClient, "hub-platform-data")
if err != nil {
    return err
}
defer roleManager.Close()

// Create/update all roles from HubEnvironment CR
if err := roleManager.CreateOrUpdateRoles(ctx, hubEnv); err != nil {
    return err
}
```

### Role Lifecycle

1. **Creation**: Reads credentials from `{role-name}-db-credentials` secret, creates role with comment "Managed by hub-operator"
2. **Password Rotation**: Detects password changes via hash comparison, executes `ALTER ROLE` to update PostgreSQL
3. **Permission Granting**: Grants permissions (SELECT, INSERT, UPDATE, DELETE, ALL) based on CR spec
4. **Pruning**: Queries pg_roles for managed roles (identified by comment), deletes roles not in CR spec

### Supported Permissions

- `SELECT` - Read access to all tables in schema
- `INSERT` - Insert access to all tables in schema
- `UPDATE` - Update access to all tables in schema
- `DELETE` - Delete access to all tables in schema
- `ALL` - All privileges on all tables in schema

### Managed Role Identification

Roles are identified as managed by the hub-operator via:
- Comment: `'Managed by hub-operator'` stored in pg_shdescription
- Query: `SELECT r.rolname FROM pg_roles r JOIN pg_shdescription d ON r.oid = d.objoid WHERE d.description = 'Managed by hub-operator'`

Only roles with this comment are eligible for pruning.

### Connection Details

Same as Migrator - connects to `platform-db-rw` with TLS required.
