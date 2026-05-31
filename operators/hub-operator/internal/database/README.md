# Database Package

This package provides database management functionality for the hub-operator.

## Components

### Migrator (`migrator.go`)
Handles database schema migrations using golang-migrate/migrate.

**Features:**
- Connects to platform-db-rw (primary service, not pooler)
- TLS required (sslmode=require)
- Embedded SQL migration files
- Dirty database detection
- Transient error classification

**Usage:** 
```go
migrator, err := database.NewMigrator(ctx, k8sClient, namespace)
if err != nil {
    return err
}
defer migrator.Close()

if err := migrator.RunMigrations(ctx); err != nil {
    if errors.Is(err, database.ErrDirtyDatabase) {
        // Permanent error - requires manual intervention
        return ctrl.Result{}, err
    }
    // Transient error - requeue with backoff
    return ctrl.Result{RequeueAfter: 10 * time.Second}, err
}
```

### RoleManager (`roles.go`)
Handles database role creation, password rotation, and pruning.

**Features:**
- Idempotent role creation (CREATE ROLE IF NOT EXISTS pattern)
- Password drift detection and rotation (ALTER ROLE)
- Permission granting (SELECT, INSERT, UPDATE, DELETE, ALL)
- Orphaned role pruning (deletes roles not in CR spec)
- Role identification via PostgreSQL comments

**Usage:**
```go
roleManager, err := database.NewRoleManager(ctx, k8sClient, namespace)
if err != nil {
    return err
}
defer roleManager.Close()

if err := roleManager.CreateOrUpdateRoles(ctx, hubEnv); err != nil {
    return err
}
```

## SQL Syntax Notes

### Role Creation
PostgreSQL does not support inline COMMENT in CREATE ROLE statements. The correct syntax is:

```sql
-- CORRECT
CREATE ROLE username WITH LOGIN PASSWORD 'password';
COMMENT ON ROLE username IS 'Managed by hub-operator';

-- INCORRECT (will fail)
CREATE ROLE username WITH LOGIN PASSWORD 'password' COMMENT 'Managed by hub-operator';
```

### Role Identification
Managed roles are identified by the comment 'Managed by hub-operator' stored in `pg_shdescription`:

```sql
SELECT r.rolname 
FROM pg_roles r
JOIN pg_shdescription d ON r.oid = d.objoid
WHERE d.description = 'Managed by hub-operator'
```

## Requirements Mapping

### Requirement 5: Database Migration Execution
- AC 5.1: ✅ Uses golang-migrate/migrate
- AC 5.2: ✅ Connects to platform-db-rw (primary)
- AC 5.3: ✅ TLS with sslmode=require
- AC 5.4: ✅ Embedded filesystem for migrations
- AC 5.5: ✅ DirtyDatabaseError for permanent failures
- AC 5.6: ✅ Transient error detection
- AC 5.7: ✅ Status condition updates

### Requirement 6: Database Role Provisioning
- AC 6.1-6.7: ✅ Creates all 7 required roles
- AC 6.8: ✅ Grants permissions from CR spec
- AC 6.9: ✅ Idempotent CREATE ROLE pattern
- AC 6.10: ✅ ALTER ROLE for password rotation
- AC 6.11: ✅ Password drift detection
- AC 6.12-6.13: ✅ Error handling and requeue
- AC 6.14: ✅ Status condition updates
- AC 6.15: ✅ Orphaned role pruning

## Security

- All SQL queries use parameterized statements to prevent SQL injection
- Passwords never logged or exposed in error messages
- TLS required for all database connections
- Superuser credentials read from Kubernetes secrets
