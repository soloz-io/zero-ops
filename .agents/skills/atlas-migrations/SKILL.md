---
name: atlas-migrations
description: >
  Guide for authoring, applying, and maintaining Atlas database migrations in
  the zero-ops platform. Use when adding tenant schema migrations, creating
  AtlasMigration CRs, regenerating atlas.sum checksums, or debugging Atlas
  Operator failures. Covers the full lifecycle: SQL authoring → checksum →
  ConfigMap → AtlasMigration CR → ArgoCD sync.
---

# Atlas Migrations Skill

## When to use this skill

Use when you are:

- Adding new SQL migrations for a tenant or platform database
- Creating or updating an `AtlasMigration` CR
- Regenerating `atlas.sum` after changing migration files
- Debugging Atlas Operator sync failures or checksum mismatches
- Onboarding a new tenant that needs tenant-specific schema extensions
- Understanding the two-layer migration model (baseline + tenant-specific)

---

## Platform Migration Architecture

Migrations in this platform follow a **two-layer model** managed by the
[Atlas Operator](https://atlasgo.io/integrations/kubernetes/operator):

```
Layer 1 — Baseline (zero-ops/migrations/tenant-baseline/)
  Applied to every tenant database.
  Shared tables: users, sessions, identities, buckets, objects + RLS.
  Source of truth: zero-ops repo, gitPath: migrations/tenant-baseline

Layer 2 — Tenant-specific (fleet-registry/tenants/{tenantId}/migrations/)
  Applied after baseline, per-tenant.
  Contains schema extensions specific to that tenant's service.
  Source of truth: fleet-registry repo, gitPath: tenants/{tenantId}/migrations
```

Both layers are applied by the Atlas Operator reading from a `ConfigMap` or
directly from Git. Atlas tracks applied migrations in the
`atlas_schema_revisions` table and only runs new files on each sync.

---

## Directory Layout

```
fleet-registry/tenants/{tenantId}/
  values.yaml                  ← tenant descriptor (references migration paths)
  migrations/
    YYYYMMDDHHMMSS_description.sql   ← migration files (UTC timestamp)
    atlas.sum                        ← checksum file (MUST be regenerated after any change)
```

```
zero-ops/manifests/hub-core-services/database-migrations/
  {name}-atlasmigration.yaml         ← AtlasMigration CR
  {name}-migrations-configmap.yaml   ← ConfigMap containing SQL + atlas.sum
```

---

## Migration File Rules

### Naming convention
Files MUST follow UTC timestamp format:
```
YYYYMMDDHHMMSS_descriptive_name.sql
```
Example: `20260527000001_waypoint_control_plane_schema.sql`

Atlas applies files in lexicographic order. The timestamp prefix guarantees
correct ordering across contributors and branches.

### SQL authoring rules

1. **Always idempotent** — every statement must be safe to re-run:
   - Tables: `CREATE TABLE IF NOT EXISTS`
   - Indexes: `CREATE INDEX IF NOT EXISTS`
   - Functions: `CREATE OR REPLACE FUNCTION`
   - Triggers: `DROP TRIGGER IF EXISTS` before `CREATE TRIGGER`
   - Policies: wrap in `DO $$ BEGIN IF NOT EXISTS (...) THEN CREATE POLICY ... END IF; END $$`
   - Columns: `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`

2. **Schema prefix** — always qualify table names with the schema:
   ```sql
   -- ✅ correct
   CREATE TABLE IF NOT EXISTS public.my_table (...);
   -- ❌ wrong — relies on search_path
   CREATE TABLE IF NOT EXISTS my_table (...);
   ```

3. **One concern per file** — do not mix unrelated tables in one migration.
   Split by logical group (e.g. core tables, indexes, RLS policies).

4. **Never edit a committed migration** — Atlas checksums are immutable once
   committed. To fix a mistake, add a new migration file. Editing an existing
   file will cause a checksum mismatch and the Atlas Operator will refuse to apply.

5. **Header comment** — every file must start with:
   ```sql
   -- Migration: <Short Title>
   -- Description: <What this migration does>
   -- Idempotent: Yes (explain how)
   ```

---

## atlas.sum — Checksum File

`atlas.sum` is a SHA-256 integrity file that Atlas Operator verifies before
applying any migration. It must be regenerated every time migration files are
added, removed, or renamed.

### Regenerating atlas.sum (Atlas CLI)

```bash
# Install Atlas CLI (if not already installed)
curl -sSf https://atlasgo.sh | sh

# Regenerate checksums for a tenant's migrations
atlas migrate hash --dir file://fleet-registry/tenants/{tenantId}/migrations

# Regenerate for platform-level migrations
atlas migrate hash --dir file://zero-ops/manifests/hub-core-services/database-migrations/control-plane
atlas migrate hash --dir file://zero-ops/manifests/hub-core-services/database-migrations/hub
```

### atlas.sum format

```
h1:<directory-hash>=
<filename> h1:<file-hash>=
<filename> h1:<file-hash>=
```

- Line 1: SHA-256 hash of all file-hash lines concatenated
- Subsequent lines: one entry per migration file, in filename order
- Hashes are base64-encoded SHA-256 digests prefixed with `h1:`

### Critical rules

- ✅ Always commit `atlas.sum` in the same commit as the SQL file changes
- ✅ Regenerate after every add, remove, or rename of a migration file
- ❌ Never hand-edit `atlas.sum` — always regenerate with the CLI
- ❌ Never commit SQL changes without updating `atlas.sum` — the operator will reject the sync

---

## AtlasMigration CR

The `AtlasMigration` CR tells the Atlas Operator which database to target and
where to find the migration files.

### Minimal CR template

```yaml
apiVersion: db.atlasgo.io/v1alpha1
kind: AtlasMigration
metadata:
  name: {name}-migrations
  namespace: {namespace}
  annotations:
    argocd.argoproj.io/sync-wave: "2"   # after DB provisioning, before workloads
  labels:
    app.kubernetes.io/name: {name}-migrations
    app.kubernetes.io/component: database-migrations
spec:
  # Database connection — always from a K8s Secret, never hardcoded
  urlFrom:
    secretKeyRef:
      name: {db-credentials-secret}
      key: uri

  # Migration files — from ConfigMap in same namespace
  dir:
    configMapRef:
      name: {name}-migrations

  # Tracks applied migrations in this schema (prevents cross-database collisions)
  revisionsSchema: "{schema-name}"

  # First migration filename (without .sql) — Atlas starts from here on fresh DB
  baseline: "{YYYYMMDDHHMMSS}"

  # Apply in strict chronological order
  execOrder: linear

  # Retry on transient failures (DB not ready yet)
  backoffLimit: 20
```

### Key fields explained

| Field | Purpose |
|-------|---------|
| `urlFrom.secretKeyRef` | Database connection string from K8s Secret. Never inline credentials. |
| `dir.configMapRef` | ConfigMap containing SQL files and `atlas.sum`. Must be in the same namespace. |
| `revisionsSchema` | PostgreSQL schema where `atlas_schema_revisions` is stored. Use a unique value per database to avoid collisions when multiple AtlasMigration CRs target the same cluster. |
| `baseline` | The timestamp of the first migration. Atlas skips files before this on a fresh database. Set to the timestamp of your first migration file. |
| `execOrder: linear` | Enforces strict sequential application. Never use `non-linear` in production. |
| `backoffLimit: 20` | Allows the operator to retry if the database is not yet ready (common during initial provisioning). |

---

## ConfigMap for Migrations

When migrations are managed via ConfigMap (platform-level databases), the
ConfigMap embeds SQL file contents and `atlas.sum` inline:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: {name}-migrations
  namespace: {namespace}
  annotations:
    argocd.argoproj.io/sync-wave: "2"
data:
  YYYYMMDDHHMMSS_description.sql: |
    -- Migration: ...
    CREATE TABLE IF NOT EXISTS ...;

  atlas.sum: |
    h1:<directory-hash>=
    YYYYMMDDHHMMSS_description.sql h1:<file-hash>=
```

For tenant-specific migrations in `fleet-registry`, the Atlas Operator reads
directly from Git via the `tenantSpecific.gitPath` reference in `values.yaml`.
No ConfigMap is needed — the operator fetches from the Git source defined in
the `AINativeSaaS` XR.

---

## Sync Wave Ordering

Migrations must run after the database is provisioned but before application
workloads start. Use ArgoCD sync-wave annotations:

```
Wave 0 — Namespace
Wave 1 — Database provisioning (CNPG / AINativeSaaS XR)
Wave 2 — Migrations (AtlasMigration CR + ConfigMap)  ← migrations go here
Wave 3 — Application workloads (Deployments, Services)
```

---

## Tenant-Specific Migration Workflow

When adding schema extensions for a new or existing tenant:

1. **Create the migration file** in `fleet-registry/tenants/{tenantId}/migrations/`
   following the naming convention and SQL authoring rules above.

2. **Regenerate atlas.sum**:
   ```bash
   atlas migrate hash --dir file://fleet-registry/tenants/{tenantId}/migrations
   ```

3. **Commit both files together** — the SQL file and the updated `atlas.sum`
   must be in the same Git commit.

4. **ArgoCD syncs automatically** — the `AINativeSaaS` XR watches the
   `tenantSpecific.gitPath` and the Atlas Operator applies new migrations on
   the next sync cycle.

5. **Verify** — check Atlas Operator logs and the `atlas_schema_revisions`
   table in the tenant database to confirm the migration was applied:
   ```bash
   kubectl logs -n {tenant-namespace} -l app=atlas-operator --tail=50
   ```

---

## Debugging Atlas Operator Failures

### Checksum mismatch
```
Error: checksum mismatch for file "20260527000001_foo.sql"
```
**Cause:** `atlas.sum` is out of date.
**Fix:** Regenerate with `atlas migrate hash` and commit.

### Migration already applied with different content
```
Error: migration "20260527000001_foo.sql" was modified after it was applied
```
**Cause:** An already-applied migration file was edited.
**Fix:** Never edit committed migrations. Create a new migration file to correct the schema.

### Database not ready
```
Error: connection refused / dial tcp: connect: connection refused
```
**Cause:** AtlasMigration CR applied before the database pod is ready.
**Fix:** Ensure the AtlasMigration CR is in sync-wave 2 (after CNPG/XR in wave 1). Increase `backoffLimit` if needed.

### revisionsSchema collision
```
Error: atlas_schema_revisions already exists with different content
```
**Cause:** Two AtlasMigration CRs targeting the same database use the same `revisionsSchema`.
**Fix:** Use a unique `revisionsSchema` per AtlasMigration CR (e.g. `waypoint_cp`, `waypoint_rt`).

---

## Anti-Patterns

- ❌ Never edit a migration file after it has been committed and applied
- ❌ Never hand-edit `atlas.sum` — always use `atlas migrate hash`
- ❌ Never commit SQL changes without updating `atlas.sum` in the same commit
- ❌ Never use unqualified table names — always prefix with the schema (`public.`)
- ❌ Never apply migrations via `kubectl exec` or `psql` directly — always via GitOps
- ❌ Never inline database credentials in the AtlasMigration CR — always use `urlFrom.secretKeyRef`
- ❌ Never use `execOrder: non-linear` in production
- ❌ Never mix unrelated schema changes in a single migration file

## References

- [Atlas Operator docs](https://atlasgo.io/integrations/kubernetes/operator)
- [Atlas migrate hash CLI](https://atlasgo.io/versioned/hash)
- Platform examples:
  - `zero-ops/manifests/hub-core-services/database-migrations/` — platform-level AtlasMigration CRs and ConfigMaps
  - `fleet-registry/tenants/waypoint/migrations/` — tenant-specific migration example
  - `zero-ops/manifests/tenants/charts/universal-tenant/files/migrations/` — baseline migrations applied to all tenants
