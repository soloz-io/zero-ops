# ADR-002: Hub CNPG Database Migration Pattern

**Version:** 2.0
**Date:** 2026-05-08
**Status:** SUPERSEDED by ADR-023
**Supersedes:** v1.0 (2026-03-25)

> **Note:** The imperative database management logic described in this ADR caused split-brain scenarios with declarative controllers. It has been completely replaced by the unified declarative approach detailed in ADR-020.

## Context

During Hub cluster bootstrap, database migrations were failing with dirty state errors, causing cascading failures in the operator reconciliation loop. Root causes identified:

1. Non-idempotent SQL (`CREATE POLICY`, `CREATE TRIGGER`) failing on re-runs
2. `migrate force <version>` hardcoded in GitOps jobs — dangerous in production
3. Duplicate RLS ownership across migration files and standalone ConfigMaps
4. Wrong table references (`agentregistry.agents` vs `agentregistry.agent_definitions`)
5. Hub-operator treating all dirty states as permanent errors requiring manual intervention

This ADR documents the corrected patterns and principles adopted.

---

## Core Principle

**CNPG manages infrastructure. The Hub Operator manages schema lifecycle. GitOps jobs are rerunnability-safe.**

```
CNPG Cluster (infra)
  ├── Database creation (postInitSQL)
  ├── Role creation (postInitSQL)
  └── TLS CA injection (CLI Secret Zero)
           ↓
Hub Operator (schema lifecycle)
  ├── Dirty state auto-recovery (version=0 only)
  ├── Versioned migrations via golang-migrate
  └── Idempotent SQL — safe to re-run
           ↓
ArgoCD Migration Jobs (supplementary role setup)
  ├── migrate up only — no force
  ├── Idempotent SQL
  └── Retryable via backoffLimit
           ↓
Application (runtime)
  └── Connects to ready databases via ESO-synced credentials
```

---

## The Three Layers

### Layer 1: CNPG Cluster — Infrastructure Provisioning

**Single cluster, multiple logical databases:**

```yaml
spec:
  bootstrap:
    initdb:
      database: postgres
      owner: app
      secret:
        name: platform-db-app        # CLI-injected Secret Zero
      postInitSQL:
        - CREATE DATABASE control_plane;
        - CREATE DATABASE hub;
        - CREATE DATABASE infisical;
        - CREATE DATABASE openmeter;
  certificates:
    serverCASecret: platform-db-ca   # CLI-injected ECDSA CA
    clientCASecret: platform-db-ca
```

**What belongs in postInitSQL:**
- ✅ `CREATE DATABASE`
- ✅ `CREATE ROLE` / `CREATE USER`
- ✅ `GRANT` database-level privileges
- ❌ Schema creation → use migrations
- ❌ Table creation → use migrations
- ❌ RLS policies → use migrations

**TLS Day-0 Pattern:**

The CA secret (`platform-db-ca`) is injected by the CLI **before** ArgoCD deploys CNPG. This eliminates restart loops.

```
hub init-secrets
  → generates ECDSA P-256 CA (not RSA — CNPG requires EC PRIVATE KEY)
  → injects platform-db-ca with keys: ca.crt, ca.key, tls.crt, tls.key
  → CNPG reads ca.key to sign server certificates on first boot
```

**Required CA secret keys:**

| Key | Purpose |
|-----|---------|
| `ca.crt` | CA certificate for TLS verification |
| `ca.key` | **Required by CNPG** to sign server certificates (ECDSA P-256) |
| `tls.crt` | Required by Kubernetes `kubernetes.io/tls` secret type |
| `tls.key` | Required by Kubernetes `kubernetes.io/tls` secret type |

---

### Layer 2: Hub Operator — Schema Lifecycle

The Hub Operator (`HubEnvironmentReconciler`) owns migration execution via `golang-migrate`.

**Dirty State Recovery Policy:**

```go
if dirty {
    if version == 0 {
        // First migration failed — safe to auto-recover
        // All migrations are idempotent, so re-running is safe
        migrator.Force(0)
        // then re-run migrate up
    } else {
        // Partial schema change — require manual intervention
        return &DirtyDatabaseError{...}
    }
}
```

**Rationale:** `version=0, dirty=true` means the very first migration failed before writing any schema. Since all SQL is idempotent, auto-recovery is safe. For `version>0`, manual intervention is required to prevent data loss.

**Manual Recovery Runbook (version > 0):**

```bash
# 1. Identify the dirty version
kubectl get hubenvironment hub-production -o jsonpath='{.status.conditions}'

# 2. Connect to database
kubectl exec -n platform-data platform-db-1 -- psql -U app -d hub

# 3. Inspect migration state
SELECT * FROM schema_migrations;

# 4. Force to last known good version (manual only, never in GitOps)
migrate -path=/migrations -database="${DATABASE_URL}" force <version>

# 5. Trigger operator re-reconciliation
kubectl annotate hubenvironment hub-production ops.nutgraf.in/reconcile-trigger="$(date -u +%Y-%m-%dT%H:%M:%SZ)" --overwrite
```

---

### Layer 3: ArgoCD Migration Jobs — Supplementary Role Setup

Jobs run via ArgoCD sync waves for role/permission setup that cannot be done in `postInitSQL` (requires connecting to specific logical databases).

**Job Pattern — migrate up only:**

```yaml
containers:
- name: migrate
  image: migrate/migrate:latest
  command:
    - sh
    - -c
    - migrate -path=/migrations -database="${DATABASE_URL}" up
```

**Never use `migrate force` in GitOps jobs.** It resets migration bookkeeping globally and is dangerous in automated pipelines. Force is a manual recovery tool only.

---

## Idempotent SQL Patterns

All migration SQL must be safe to re-run. These are the required patterns:

### Tables and Indexes

```sql
-- ✅ Always use IF NOT EXISTS
CREATE TABLE IF NOT EXISTS agent_infra_status (...);
CREATE INDEX IF NOT EXISTS idx_agent_infra_status_tenant ON agent_infra_status(tenant_id);
```

### Functions

```sql
-- ✅ Always use CREATE OR REPLACE
CREATE OR REPLACE FUNCTION notify_agent_infra_status_change()
RETURNS TRIGGER AS $$ ... $$ LANGUAGE plpgsql;
```

### Triggers

```sql
-- ✅ Drop before create (idempotent)
DROP TRIGGER IF EXISTS agent_infra_status_change ON agent_infra_status;
CREATE TRIGGER agent_infra_status_change
AFTER INSERT OR UPDATE ON agent_infra_status
FOR EACH ROW EXECUTE FUNCTION notify_agent_infra_status_change();
```

### RLS Policies

```sql
-- ✅ Wrap in DO block with existence check
ALTER TABLE agent_infra_status ENABLE ROW LEVEL SECURITY;  -- idempotent natively

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE schemaname = 'public'
      AND tablename  = 'agent_infra_status'
      AND policyname = 'tenant_isolation'
  ) THEN
    CREATE POLICY tenant_isolation ON agent_infra_status
      USING (tenant_id = current_setting('app.tenant_id', true)::UUID);
  END IF;
END $$;
```

### Roles and Grants

```sql
-- ✅ Check before create
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'spire_server') THEN
    CREATE ROLE spire_server WITH LOGIN PASSWORD '${SPIRE_SERVER_PASSWORD}';
  ELSE
    ALTER ROLE spire_server WITH PASSWORD '${SPIRE_SERVER_PASSWORD}';
  END IF;
END $$;

-- GRANT is idempotent natively
GRANT CONNECT ON DATABASE hub TO spire_server;
```

### Schemas

```sql
-- ✅ Always use IF NOT EXISTS
CREATE SCHEMA IF NOT EXISTS agentregistry;
```

---

## RLS Ownership Rule

**Migrations own schema AND RLS. No standalone RLS ConfigMaps.**

| Responsibility | Owner |
|---------------|-------|
| Table creation | Migration SQL |
| Index creation | Migration SQL |
| RLS enablement | Migration SQL |
| RLS policies | Migration SQL |
| Role creation | Migration Job (spire-server-role.yaml) or postInitSQL |
| Grants | Migration SQL or postInitSQL |

**Anti-pattern (do not do):**

```
migrations/control-plane-migrations.yaml  → creates policies
migrations/agentregistry-rls.yaml         → also creates policies on same tables
```

This causes drift, ordering issues, and repeated sync failures. Consolidate all RLS into the primary migration file for each database.

---

## Transactional Migrations

Wrap multi-statement migrations in transactions to prevent partial dirty states:

```sql
BEGIN;

CREATE TABLE IF NOT EXISTS agent_infra_status (...);

CREATE INDEX IF NOT EXISTS idx_agent_infra_status_tenant
  ON agent_infra_status(tenant_id);

ALTER TABLE agent_infra_status ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE schemaname = 'public'
      AND tablename  = 'agent_infra_status'
      AND policyname = 'tenant_isolation'
  ) THEN
    CREATE POLICY tenant_isolation ON agent_infra_status
      USING (tenant_id = current_setting('app.tenant_id', true)::UUID);
  END IF;
END $$;

COMMIT;
```

Note: DDL in PostgreSQL is transactional. `CREATE TABLE`, `CREATE INDEX`, `ALTER TABLE` all participate in transactions.

---

## Sync Wave Ordering

```
Wave -2: AppProject (platform-infrastructure)
Wave  1: CNPG Operator, Redis, platform-infisical-prerequisites
Wave  2: CNPG Cluster (platform-db), platform-database application
Wave  3: Infisical (requires DB ready)
Wave  4: Migration Jobs (requires DB + credentials from ESO)
Wave  5: Applications (requires migrations complete)
```

**Wave 4 prerequisite:** ESO must have synced `hub-db-credentials`, `control-plane-db-credentials`, `spire-server-db-credentials` from Infisical before migration jobs run.

---

## Node Scheduling

CNPG and migration jobs must schedule on worker nodes. CAPI/kubeadm sets the worker role label as a **key-only label** (empty value):

```yaml
# ✅ Correct — matches CAPI node label
nodeSelector:
  node-role.kubernetes.io/worker: ""

# ❌ Wrong — never matches
nodeSelector:
  node-role.kubernetes.io/worker: "true"
```

---

## Directory Structure

```
manifests/hub-core-services/database/
  platform-db.yaml                        ← CNPG Cluster
  platform-db-pooler.yaml                 ← PgBouncer
  migrations/
    hub-job.yaml                          ← migrate up only
    hub-migrations.yaml                   ← ConfigMap with idempotent SQL
    control-plane-job.yaml                ← migrate up only
    control-plane-migrations.yaml         ← ConfigMap with idempotent SQL
    agentregistry-rls.yaml                ← ConfigMap (consolidated RLS)
    spire-server-role.yaml                ← Role setup job (idempotent)
  hub-db-credentials-es.yaml              ← ExternalSecret (ESO → Infisical)
  control-plane-db-credentials-es.yaml
  spire-server-db-credentials-es.yaml
  platform-db-app-credentials-es.yaml
```

---

## Implementation Checklist

- [ ] CA secret uses ECDSA P-256 with all four keys (`ca.crt`, `ca.key`, `tls.crt`, `tls.key`)
- [ ] All `CREATE TABLE` use `IF NOT EXISTS`
- [ ] All `CREATE INDEX` use `IF NOT EXISTS`
- [ ] All `CREATE FUNCTION` use `CREATE OR REPLACE`
- [ ] All `CREATE TRIGGER` preceded by `DROP TRIGGER IF EXISTS`
- [ ] All `CREATE POLICY` wrapped in `DO $$ IF NOT EXISTS` block
- [ ] `ALTER TABLE ... ENABLE ROW LEVEL SECURITY` used directly (natively idempotent)
- [ ] No `migrate force` in any GitOps job
- [ ] RLS policies owned exclusively by migration SQL (no duplicate standalone ConfigMaps)
- [ ] Table references verified against actual schema (e.g. `agent_definitions` not `agents`)
- [ ] Migration jobs use `migrate up` only
- [ ] `nodeSelector: node-role.kubernetes.io/worker: ""` (empty string, not "true")
- [ ] Multi-statement migrations wrapped in `BEGIN; ... COMMIT;`
