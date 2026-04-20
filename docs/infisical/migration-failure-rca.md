# Infisical Migration Failure - Root Cause Analysis

**Date:** 2026-03-30  
**Status:** RESOLVED  
**Severity:** Critical (Service Down)

## Executive Summary

Infisical pods are crashing with `relation "super_admin" does not exist` and `relation "pgboss.version" does not exist` errors because database migrations are NOT running on pod startup. The root cause is that migrations run automatically in Infisical's `main.ts` startup sequence, but the application is configured to connect via PgBouncer in **transaction mode**, which blocks the migration system's advisory locks.

## Error Symptoms

```
{"level":50,"severity":"ERROR","err":{"message":"relation \"super_admin\" does not exist"}}
{"level":50,"severity":"ERROR","err":{"message":"relation \"pgboss.version\" does not exist"}}
```

- Infisical pods return HTTP 500 on `/api/status` readiness probe
- Pods restart continuously (CrashLoopBackOff)
- Database was successfully reset by `reset-infisical-db` job (empty database)
- PgBouncer pooler is running and accepting connections

## Root Cause Analysis

### 1. How Infisical Migrations Work (Source Code Evidence)

From `archived/references/identity-auth/infisical/backend/src/main.ts`:

```typescript
const run = async () => {
  const logger = initLogger();
  const db = initDbConnection({ dbConnectionUri, dbRootCert, readReplicas });
  
  // CRITICAL: Migrations run AUTOMATICALLY on startup
  await runMigrations({
    applicationDb: db,
    auditLogDb,
    clickhouseClient: clickhouse,
    logger
  });
  
  const server = await main({ db, logger, ... });
  await server.listen({ port: envConfig.PORT, host: envConfig.HOST });
};
```

**Key Finding:** Migrations are NOT optional. They run automatically in `main.ts` before the HTTP server starts.

### 2. Migration Lock Mechanism

From `archived/references/identity-auth/infisical/backend/src/auto-start-migrations.ts`:

```typescript
const ensureMigrationTables = async (db: Knex, logger: Logger): Promise<void> => {
  await db.transaction(async (tx) => {
    // Uses PostgreSQL advisory lock to prevent concurrent migration initialization
    await tx.raw("SELECT pg_advisory_xact_lock(?)", [PgSqlLock.BootUpMigration]);
    await tx.migrate.currentVersion(migrationConfig);
  });
};

const withStartupLock = async (db: Knex, logger: Logger, doMigrations: () => Promise<void>) => {
  // Custom table-based lock with heartbeat mechanism
  // Prevents multiple instances from running migrations simultaneously
  await acquireLock();
  startHeartbeat();
  await doMigrations();
  await releaseLock();
};
```

**Key Finding:** Infisical uses TWO lock mechanisms:
1. PostgreSQL advisory locks (`pg_advisory_xact_lock`) for migration table initialization
2. Custom table-based locks with heartbeat for migration execution

### 3. PgBouncer Transaction Mode Incompatibility

From our configuration in `manifests/hub-core-services/platform-database/platform-db-pooler.yaml`:

```yaml
spec:
  type: rw
  instances: 3
  pgbouncer:
    poolMode: transaction  # ← THE PROBLEM
    parameters:
      max_client_conn: "100"
      default_pool_size: "25"
```

**PgBouncer Transaction Mode Behavior:**
- Assigns a server connection to a client ONLY for the duration of a transaction
- After `COMMIT` or `ROLLBACK`, the connection returns to the pool
- **Advisory locks are session-scoped** and are released when the connection is returned to the pool
- **Prepared statements** are not preserved across transactions

**Why Migrations Fail:**
1. Infisical calls `tx.raw("SELECT pg_advisory_xact_lock(?)", [PgSqlLock.BootUpMigration])`
2. PgBouncer assigns a PostgreSQL connection for this transaction
3. Transaction commits, PgBouncer returns the connection to the pool
4. Next migration query gets a DIFFERENT connection from the pool
5. Advisory lock is gone, migration state is lost
6. Knex migration system fails silently or hangs

### 4. Configuration Comparison

**Infisical Source Code Expectations:**
```typescript
// backend/src/db/instance.ts
const db = knex({
  client: "pg",
  connection: {
    connectionString: modifiedDbConnectionUri,  // Direct PostgreSQL connection
    host: process.env.DB_HOST,
    port: process.env.DB_PORT,
    user: process.env.DB_USER,
    database: process.env.DB_NAME,
    password: process.env.DB_PASSWORD,
    ssl: sslConfig
  },
  pool: { min: 0, max: 10 }  // Knex manages connection pool
});
```

**Our Deployment Configuration:**
```yaml
# manifests/hub-core-services/platform-infisical/values.yaml
extraEnv:
- name: DB_HOST
  value: platform-db-pooler.zero-ops-system.svc  # ← PgBouncer, not PostgreSQL
- name: DB_PORT
  value: "5432"
- name: DB_USER
  value: infisical
- name: DB_PASSWORD
  valueFrom: secretKeyRef
- name: DB_NAME
  value: infisical
```

**The Mismatch:**
- Infisical expects: Direct PostgreSQL connection (session-scoped locks work)
- We provide: PgBouncer in transaction mode (session-scoped locks fail)

## Why This Wasn't Caught Earlier

1. **Previous deployment used direct connection:** Before pooler implementation, `DB_HOST=platform-db-rw` pointed directly to PostgreSQL
2. **Database wasn't empty:** Migrations had already run successfully, so startup didn't trigger migration logic
3. **Reset job triggered the issue:** `reset-infisical-db` dropped and recreated the database, forcing migrations to run on next startup
4. **PgBouncer was just added:** Pooler was introduced to fix connection exhaustion (504 errors), but transaction mode breaks migrations

## Impact Assessment

**Severity:** Critical  
**Affected Component:** Infisical (Identity & Secrets Management)  
**User Impact:** Complete service outage, no authentication possible  
**Duration:** Since pooler deployment + database reset (~30 minutes)

## Solution Options

### Option 1: Use Session Pooling Mode (RECOMMENDED)

**Change:**
```yaml
# manifests/hub-core-services/platform-database/platform-db-pooler.yaml
spec:
  pgbouncer:
    poolMode: session  # Changed from transaction
```

**Pros:**
- Preserves advisory locks (session-scoped)
- Preserves prepared statements
- No application code changes required
- Standard pattern for applications using advisory locks

**Cons:**
- Slightly less efficient connection reuse (connections held for entire session)
- Still provides connection pooling benefits (max_client_conn limits)

**Recommendation:** Use this option. Session mode is the correct choice for applications using advisory locks or prepared statements.

### Option 2: Bypass Pooler for Migrations Only

**Change:**
```yaml
# manifests/hub-core-services/platform-infisical/values.yaml
extraEnv:
- name: DB_HOST
  value: platform-db-rw.zero-ops-system.svc  # Direct connection for migrations
```

**Pros:**
- Migrations work immediately
- No pooler configuration changes

**Cons:**
- Defeats the purpose of pooler (connection exhaustion returns)
- Doesn't solve the 504 error root cause
- Not a production-grade solution

**Recommendation:** Do NOT use this option. It's a workaround, not a fix.

### Option 3: Run Migrations as Init Container

**Change:**
```yaml
# manifests/hub-core-services/platform-infisical/values.yaml
initContainers:
- name: run-migrations
  image: infisical/infisical:v0.158.0
  command: ["node", "dist/main.js", "--migrate-only"]
  env:
  - name: DB_HOST
    value: platform-db-rw.zero-ops-system.svc  # Direct connection
```

**Pros:**
- Separates migration concerns from application runtime
- Init container can use direct connection, app uses pooler

**Cons:**
- Requires Infisical to support `--migrate-only` flag (it doesn't)
- Would need custom migration script
- Adds complexity to deployment

**Recommendation:** Do NOT use this option. Infisical doesn't support this pattern.

## Recommended Fix

**Use Session Pooling Mode:**

1. Update `manifests/hub-core-services/platform-database/platform-db-pooler.yaml`:
```yaml
spec:
  pgbouncer:
    poolMode: session
```

2. Commit and push to trigger ArgoCD sync

3. Wait for pooler pods to restart with new configuration

4. Infisical pods will automatically restart and run migrations successfully

5. Verify: `curl -k https://infisical.nutgraf.in/api/status` returns 200

## Prevention Measures

1. **Document PgBouncer pooling mode requirements** in `docs/infisical/database-connection-pooling.md`
2. **Add validation check** in bootstrap script to verify pooler mode matches application requirements
3. **Test migrations in dev environment** before deploying pooler changes to production
4. **Monitor migration logs** during deployments to catch failures early

## References

- Infisical Source: `archived/references/identity-auth/infisical/backend/src/auto-start-migrations.ts`
- Infisical Source: `archived/references/identity-auth/infisical/backend/src/main.ts`
- Infisical Source: `archived/references/identity-auth/infisical/backend/src/db/instance.ts`
- PgBouncer Docs: https://www.pgbouncer.org/config.html#pool_mode
- PostgreSQL Advisory Locks: https://www.postgresql.org/docs/current/explicit-locking.html#ADVISORY-LOCKS
- CNPG Pooler: https://cloudnative-pg.io/documentation/current/connection_pooling/

## Timeline

- **16:30 UTC:** Pooler deployed with transaction mode
- **16:35 UTC:** Database reset job completed
- **16:36 UTC:** Infisical pods start crashing with migration errors
- **16:45 UTC:** RCA completed, identified transaction mode as root cause
- **16:50 UTC:** Fix committed to Git (session mode in YAML)
- **16:55 UTC:** Attempted fix by deleting Infisical pod - FAILED
- **20:30 UTC:** Discovered pooler pods never restarted after config change
- **20:40 UTC:** Manually restarted pooler deployment with `kubectl rollout restart`
- **20:42 UTC:** New pooler pods running with session mode
- **20:43 UTC:** Deleted Infisical pod to trigger migrations
- **20:45 UTC:** Migrations completed successfully
- **20:46 UTC:** Service restored - API returns HTTP 200

## Lessons Learned

1. **PgBouncer transaction mode is incompatible with advisory locks** - Always use session mode for applications that use PostgreSQL advisory locks or prepared statements
2. **CNPG Pooler doesn't auto-restart on spec changes** - After changing Pooler CRD configuration, manually trigger `kubectl rollout restart deployment <pooler-name>` to apply changes
3. **Verify running configuration, not just YAML** - Check actual pod configuration with `kubectl exec` to confirm changes were applied
4. **Test infrastructure changes with empty databases** - Migration logic only runs when database is empty or has pending migrations
5. **Read application source code** - Don't assume connection pooling is transparent; check for session-scoped features
6. **Document pooling mode requirements** - Add to application deployment documentation

## Critical Fix Procedure

When changing CNPG Pooler configuration:

1. Update Pooler YAML in Git and commit
2. Wait for ArgoCD to sync (or force sync)
3. **Manually restart pooler deployment**: `kubectl rollout restart deployment -n <namespace> <pooler-name>`
4. Verify new pods are running: `kubectl get pods -l cnpg.io/poolerName=<pooler-name>`
5. Verify configuration applied: `kubectl exec <pooler-pod> -- cat /etc/pgbouncer/pgbouncer.ini | grep pool_mode`
6. Restart application pods if needed to pick up new pooler configuration
