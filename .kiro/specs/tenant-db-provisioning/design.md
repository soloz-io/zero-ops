# Design: Tenant Database Provisioning (Per-Tenant User Isolation)

**Feature Name**: `tenant-db-provisioning`
**Status**: Approved
**Created**: 2026-04-22
**Design Reference**: `.kiro/specs/spoke-pool-provisioner/resources/per-tenant-user-design.md`

---

## 1. Architecture

### 1.1 Pattern: Remote Provider + TenantDatabase Abstraction

Hub manages **intent** (`TenantDatabase` XR). Spoke manages **implementation** (DB, Role, Grants).

```
Hub Cluster
├── AINativeSaaS Composition
│   └── provider-kubernetes Object → TenantDatabase CR (pushed to Spoke API server)
│       └── readinessChecks: MatchCondition Ready=True
│
└── provider-kubernetes (existing)

Spoke Cluster
├── crossplane-system: Crossplane operator (local — reconciles TenantDatabase XRs)
├── spoke-platform-ops: provider-sql v0.9.0 (local, connects to shared-cnpg-rw via ClusterIP)
├── TenantDatabase XRD + Composition (new)
│   ├── CNPG Database CR          → tenant-<id>-db
│   ├── provider-sql Role CR      → tenant-<id>-user
│   ├── provider-sql Grant CR     → CONNECT on tenant-<id>-db
│   ├── provider-sql Grant CR     → ALL on public tables
│   ├── provider-sql Grant CR     → ALL on public sequences
│   ├── Kubernetes Secret         → tenant-<id>-db-credentials (Crossplane-generated)
│   ├── ESO PushSecret            → mirrors Secret to Infisical (mandatory backup)
│   └── ESO ExternalSecret        → restores Secret from Infisical on cluster rebuild
└── shared-cnpg-rw (ClusterIP — local, no cross-cluster network needed)
```

### 1.2 Why TenantDatabase abstraction (not raw Role/Grant from Hub)

Pushing `Role` + `Grant` individually from the Hub couples the Hub to PostgreSQL internals. `TenantDatabase` is a platform concept — the Spoke owns the implementation. Swapping CNPG for another DB engine requires no Hub changes.

---

## 2. Component Design

### 2.1 TenantDatabase XRD (Spoke)

**File**: `xrds/definitions/tenantdatabase-v1.yaml`

```yaml
apiVersion: apiextensions.crossplane.io/v1
kind: CompositeResourceDefinition
metadata:
  name: tenantdatabases.nutgraf.in
spec:
  group: nutgraf.in
  names:
    kind: TenantDatabase
    plural: tenantdatabases
  versions:
  - name: v1alpha1
    served: true
    referenceable: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            required: [tenantId, databaseName]
            properties:
              tenantId:
                type: string
                pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'
              databaseName:
                type: string
                description: "e.g. tenant-acme-db"
              cellId:
                type: string
                description: "Spoke Pool cluster name — for Infisical path"
```

### 2.2 TenantDatabase Composition (Spoke)

**File**: `xrds/compositions/tenantdatabase-spoke.yaml`

Resources in order (sync wave enforced via Crossplane readiness dependencies):

| # | Resource | Kind | Purpose |
|---|---|---|---|
| 1 | `db-credentials-restore` | ESO ExternalSecret | Restore Secret from Infisical on rebuild (`creationPolicy: Merge`) |
| 2 | `database` | CNPG Database (via provider-kubernetes Object) | Create `tenant-<id>-db` in shared CNPG |
| 3 | `db-user` | provider-sql Role | Create `tenant-<id>-user` with password from Secret |
| 4 | `grant-connect` | provider-sql Grant | CONNECT on `tenant-<id>-db` |
| 5 | `default-privileges-tables` | provider-sql DefaultPrivileges | Future table privileges (crossplane_admin → tenant user) |
| 6 | `default-privileges-sequences` | provider-sql DefaultPrivileges | Future sequence privileges (crossplane_admin → tenant user) |
| 7 | `db-credentials-backup` | ESO PushSecret | Mirror Secret to Infisical |

**ProviderConfig reference**: `default` (points to `shared-cnpg-rw` via `crossplane-admin-credentials`)

**CRITICAL FINDING**: provider-sql Grant resource only supports database-level privileges (CONNECT, CREATE, TEMPORARY). For table/sequence privileges, must use DefaultPrivileges resource which handles `ALTER DEFAULT PRIVILEGES` statements for future objects created by `crossplane_admin`.

### 2.2.1 provider-sql Limitations and Solutions

**Issue**: The original design assumed provider-sql Grant could handle schema-level privileges like `GRANT ALL ON ALL TABLES IN SCHEMA public TO tenant_user`. However, analysis of provider-sql v0.9.0 API reveals:

1. **Grant resource limitations**:
   - Only supports database-level privileges: CONNECT, CREATE, TEMPORARY
   - No `schema` or `objectType` fields in the API
   - Cannot grant privileges on existing tables/sequences

2. **Solution: DefaultPrivileges resource**:
   - Handles `ALTER DEFAULT PRIVILEGES FOR ROLE crossplane_admin IN SCHEMA public GRANT ... TO tenant_user`
   - Only affects **future objects** created by `crossplane_admin`
   - Does NOT grant privileges on existing tables/sequences

3. **Implications for existing objects**:
   - Baseline migration tables created by Atlas Operator will NOT have tenant user privileges
   - Atlas Operator runs as `crossplane_admin`, so DefaultPrivileges will apply to new tables
   - **Workaround**: Atlas migrations must include explicit GRANT statements for existing tables

**Recommended approach**:
- Use DefaultPrivileges for future objects (automated)
- Include GRANT statements in baseline migrations for existing tables (manual)
- Document this limitation for operational teams

### 2.3 provider-sql ProviderConfig (Spoke)

**File**: `manifests/spoke-catalog/infra/crossplane-providerconfig-sql.yaml`

```yaml
apiVersion: postgresql.sql.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: default
  namespace: spoke-platform-security
spec:
  credentials:
    source: PostgreSQLConnectionString
    connectionSecretRef:
      namespace: spoke-platform-security
      name: crossplane-admin-credentials
```

Connection string in `crossplane-admin-credentials`:
```
postgresql://crossplane_admin:<password>@shared-cnpg-rw.spoke-platform-data.svc.cluster.local:5432/postgres
```

### 2.4 crossplane_admin bootstrap

**File**: `manifests/spoke-catalog/infra/crossplane-admin-bootstrap.yaml`

Created once per Spoke Pool during cell bootstrap (sync wave -1, before CNPG operator):

```sql
CREATE ROLE crossplane_admin WITH LOGIN CREATEDB CREATEROLE PASSWORD '<from-infisical>';
```

- `CREATEDB` — creates `tenant-<id>-db` databases dynamically
- `CREATEROLE` — creates `tenant-<id>-user` roles and executes GRANTs
- No SELECT/INSERT/UPDATE/DELETE on any tenant table

**Password Generation Flow (CR Reconciliation Only)**:
1. SpokePool CR created on Hub (e.g., `spoke-pool-eu-prod-01`)
2. Hub Operator (SpokePool controller) reconciles the CR
3. Controller extracts SpokePool CR name (e.g., `spoke-pool-eu-prod-01`)
4. **Controller derives Infisical key:** `<spokepool-cr-name>-crossplane-admin-password`
   - Example: `spoke-pool-eu-prod-01-crossplane-admin-password`
   - This ensures each SpokePool has a unique, identifiable password
5. Controller checks status condition: is this first-time creation?
6. Controller queries Infisical API for existing password using the derived key (idempotency via API query)
7. **If password exists in Infisical** → skip generation (idempotent)
8. **If password missing AND first-time creation** → generate 32-char hex password using `secrets.GenerateSecurePassword()` and upload to Infisical at `/spoke-pool/<spokepool-cr-name>-crossplane-admin-password`
9. **If password missing AND NOT first-time** → FAIL reconciliation with error "Password missing from Infisical for already-provisioned SpokePool - manual recovery required"
10. Spoke ESO ExternalSecret syncs from Infisical to `crossplane-admin-credentials` Secret in `crossplane-system` namespace
11. Bootstrap Job reads Secret and creates `crossplane_admin` role

**No Hub K8s Secret**: Hub Operator queries Infisical directly for idempotency, does not create K8s secret on Hub.

**Operator Role**: Operator generates password ONLY during first-time CR reconciliation. Operator does NOT manage secret lifecycle post-creation. ESO handles all subsequent secret syncing.

**Manual Intervention Required**: If password is accidentally deleted from Infisical after initial provisioning, the controller will fail and require manual password restoration to prevent breaking Spoke's CNPG connection.

---

## 3. Secret Lifecycle

### 3.1 Initial provisioning

```
Crossplane generates 32-char random password (function-patch-and-transform)
    ↓
Writes K8s Secret: tenant-<id>-db-credentials in tenant-<id> namespace
    ↓
provider-sql Role CR uses Secret.password as passwordSecretRef → CREATE ROLE
    ↓
ESO PushSecret mirrors Secret to Infisical ← durable backup
```

### 3.2 Disaster recovery (cluster rebuild)

```
Spoke cluster rebuilt — etcd wiped, all Secrets lost
    ↓
ESO ExternalSecret (creationPolicy: Merge) runs first
    ↓
Pulls existing password from Infisical → restores K8s Secret
    ↓
Crossplane reconciles TenantDatabase XR
    ↓
Sees Secret already exists → skips password regeneration
    ↓
provider-sql reconciles Role → DB password still matches → tenants recover
```

**Critical**: Without Infisical backup, Crossplane would generate a new password that doesn't match the DB → all tenant connections broken.

### 3.3 Secret structure

```yaml
Secret name:  tenant-<id>-db-credentials
Namespace:    tenant-<id>
Fields:
  username:   tenant-<id>-user
  password:   <32-char random alphanumeric>
  database:   tenant-<id>-db
  host:       shared-cnpg-rw.spoke-platform-data.svc.cluster.local
  port:       5432
```

### 3.4 Infisical paths

```
/spoke-pool/<cell-id>/tenants/<tenant-id>/db-credentials   ← tenant credentials
/spoke-pool/<cell-id>/crossplane-admin-credentials          ← crossplane_admin credentials
```

---

## 4. Hub Composition Changes

### 4.1 AINativeSaaS Composition update

**File**: `xrds/compositions/ainativesaas-starter-hetzner.yaml`

Replace resources `db-credentials-secret`, `db-user`, `grant-connect`, `grant-tables`, `grant-sequences` with a single `provider-kubernetes Object`:

```yaml
- name: tenant-database-remote
  base:
    apiVersion: kubernetes.crossplane.io/v1alpha2
    kind: Object
    spec:
      managementPolicies: ["*"]
      forProvider:
        manifest:
          apiVersion: nutgraf.in/v1alpha1
          kind: TenantDatabase
          metadata:
            name: ""      # Patched: tenantId
          spec:
            tenantId: ""  # Patched
            databaseName: ""  # Patched: database.name
            cellId: ""    # Patched: cellId
      readinessChecks:
      - type: MatchCondition
        matchCondition:
          type: Ready
          status: "True"
      providerConfigRef:
        name: ""  # Patched: cellId (provider-kubernetes ProviderConfig for the Spoke)
  patches:
  - type: FromCompositeFieldPath
    fromFieldPath: spec.cellId
    toFieldPath: spec.providerConfigRef.name
  - type: FromCompositeFieldPath
    fromFieldPath: spec.tenantId
    toFieldPath: spec.forProvider.manifest.metadata.name
  - type: FromCompositeFieldPath
    fromFieldPath: spec.tenantId
    toFieldPath: spec.forProvider.manifest.spec.tenantId
  - type: FromCompositeFieldPath
    fromFieldPath: spec.database.name
    toFieldPath: spec.forProvider.manifest.spec.databaseName
  - type: FromCompositeFieldPath
    fromFieldPath: spec.cellId
    toFieldPath: spec.forProvider.manifest.spec.cellId
```

### 4.2 Pooler update

CNPG Pooler `authQueryUser.secretRef` references `tenant-<id>-db-credentials` (not shared `app` user).

### 4.3 PostgREST update

- `PGRST_DB_URI` sourced from `tenant-<id>-db-credentials` Secret
- `PGRST_DB_ANON_ROLE` set to `tenant-<id>-user`

---

## 5. Sync Wave Ordering

### 5.1 Universal Tenant Helm Chart (unchanged)

- Wave 1: `AINativeSaaS` XR — only Ready after `TenantDatabase` Ready=True (via readinessChecks)
- Wave 2: `AtlasMigration` CR — only starts after Wave 1 health check passes

### 5.2 TenantDatabase Composition internal ordering

Crossplane resolves dependencies via readiness checks on composed resources:

```
ESO ExternalSecret (restore from Infisical)
    ↓ Secret exists
CNPG Database CR (create tenant-<id>-db)
    ↓ Database Ready
provider-sql Role CR (create tenant-<id>-user)
    ↓ Role Ready
provider-sql Grant CRs (CONNECT + tables + sequences)
    ↓ Grants Ready
ESO PushSecret (mirror to Infisical)
    ↓
TenantDatabase XR → Ready=True
```

---

## 6. Deployment Changes

### 6.1 Edge catalog additions (every Spoke Pool)

| File | Sync Wave | Purpose |
|---|---|---|
| `manifests/spoke-catalog/infra/crossplane-provider-sql.yaml` | -1 | Install provider-sql v0.9.0 on Spoke |
| `manifests/spoke-catalog/infra/crossplane-admin-bootstrap.yaml` | -1 | Create `crossplane_admin` role (one-time) |
| `manifests/spoke-catalog/infra/crossplane-admin-eso.yaml` | -1 | ESO ExternalSecret for `crossplane-admin-credentials` |
| `manifests/spoke-catalog/infra/crossplane-providerconfig-sql.yaml` | 0 | ProviderConfig → `shared-cnpg-rw` via `crossplane-admin-credentials` |

### 6.2 New XRD + Composition

| File | Purpose |
|---|---|
| `xrds/definitions/tenantdatabase-v1.yaml` | TenantDatabase XRD |
| `xrds/compositions/tenantdatabase-spoke.yaml` | Spoke-side Composition |

### 6.3 Hub changes

| File | Change |
|---|---|
| `xrds/compositions/ainativesaas-starter-hetzner.yaml` | Replace db-user/grant-* with TenantDatabase Object + readinessChecks |
| `manifests/hub-core-services/crossplane/providers/kustomization.yaml` | Remove provider-sql entry |
| `manifests/hub-core-services/crossplane/providers/provider-sql/` | Delete directory |

---

## 7. Credential Rotation (Day 2)

Rotation is out of scope for this implementation. Two patterns are documented for operational runbooks:

**Pattern B (Phase 1 — acceptable blip)**:
`ALTER ROLE` → ~30s outage → update Infisical → ESO syncs → pods restart

**Pattern A (future — zero-downtime)**:
Create `v2` role → switch apps → confirm 0 `v1` connections → drop `v1`

See design doc Section 9 for full details.

---

## 8. Known Limitations and Operational Considerations

### 8.1 Existing Table Privileges

**Issue**: DefaultPrivileges only affects future objects created by `crossplane_admin`. Existing tables created by Atlas migrations will NOT automatically have tenant user privileges.

**Impact**: 
- Baseline migration tables (users, sessions, identities, buckets, objects) will be inaccessible to tenant user
- Tenant applications will fail with permission denied errors

**Solutions**:

**Option A (Recommended): Include GRANT statements in baseline migrations**
```sql
-- Add to end of each baseline migration file
GRANT SELECT, INSERT, UPDATE, DELETE ON public.users TO tenant_${tenant_id}_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.sessions TO tenant_${tenant_id}_user;
-- etc. for all tables
```

**Option B: Post-migration GRANT via Atlas Operator**
- Atlas Operator could run additional SQL after migrations complete
- Requires custom Atlas Operator configuration
- More complex but automated

**Option C: Manual GRANT via provider-sql (Not Recommended)**
- Create Grant resources for each existing table
- Requires knowing table names in advance
- Does not scale with dynamic table creation

### 8.2 Operational Runbook

**When adding new baseline migrations**:
1. Add table/sequence creation SQL
2. Add explicit GRANT statements for tenant user template
3. Test with actual tenant user credentials
4. Document any new privileges required

**When troubleshooting tenant permission issues**:
1. Check DefaultPrivileges are applied: `\ddp` in psql
2. Check existing table privileges: `\dp table_name`
3. Verify tenant user exists: `\du tenant_*_user`
4. Test connection with tenant credentials
