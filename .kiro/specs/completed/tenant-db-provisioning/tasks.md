# Implementation Tasks: Tenant Database Provisioning

## Overview

Implements per-tenant PostgreSQL user isolation using the Remote Provider Pattern.
All tasks follow GitOps-first principles — changes via Git commits, ArgoCD reconciles.

**Design**: `.kiro/specs/spoke-pool-provisioner/resources/per-tenant-user-design.md`
**Requirements**: `requirements.md`

---

## Phase 1: Spoke Infrastructure

### 1.0 Deploy Crossplane to Spoke Clusters

- [x] 1.0.1 Add Crossplane ApplicationSet to Spoke Catalog
  - Edit `manifests/argocd/apps/platform-spoke-catalog-appsets.yaml`
  - Add `ApplicationSet` for `spoke-crossplane` using `charts.crossplane.io/stable` v1.20.5
  - Destination namespace: `crossplane-system`
  - Sync wave: 1 (before spoke-infrastructure at wave 4)
  - `--enable-composition-functions` arg (required for function-patch-and-transform)
  - _Requirements: FR-1.2_

### 1.1 crossplane_admin Bootstrap

- [x] 1.1.1 Implement Hub Operator SpokePool controller
  - Create `operators/hub-operator/internal/controller/spokepool_controller.go`
  - Watches SpokePool XRs on Hub
  - **On CR reconciliation:**
    - Extracts SpokePool CR name (e.g., `spoke-pool-eu-prod-01`)
    - **Derives Infisical key:** `<spokepool-cr-name>-crossplane-admin-password`
      - Example: `spoke-pool-eu-prod-01-crossplane-admin-password`
      - Implementation: `infisicalKey := fmt.Sprintf("%s-crossplane-admin-password", spokeName)`
    - Checks status condition to determine if first-time creation
    - Queries Infisical API for existing password using derived key (idempotency via `SecretExists()` API call)
    - **If password exists** → skip generation (idempotent)
    - **If password missing AND first-time** → generate 32-char hex password using `secrets.GenerateSecurePassword()` and upload to Infisical at derived key path
    - **If password missing AND NOT first-time** → FAIL reconciliation with error (requires manual intervention to prevent breaking Spoke CNPG connection)
  - **Does NOT create Hub K8s secret**
  - **Does NOT manage secret lifecycle post-creation** (ESO handles syncing)
  - Register controller in `operators/hub-operator/cmd/main.go`
  - Add RBAC for spokepools in `operators/hub-operator/config/rbac/role.yaml`
  - Add `SecretExists()` public method to `operators/hub-operator/internal/client/infisical.go`
  - _Requirements: FR-2.3_

- [x] 1.1.2 Create ESO ExternalSecret for Spoke
  - Create file: `manifests/spoke-catalog/infra/crossplane-admin-eso.yaml`
  - **Infisical Key Pattern:** `<spokepool-cr-name>-crossplane-admin-password`
    - Example: `spoke-pool-eu-prod-01-crossplane-admin-password`
    - Key MUST match the SpokePool CR name to ensure correct password retrieval
  - Syncs from Infisical path `/spoke-pool/<spokepool-cr-name>-crossplane-admin-password`
  - Creates `crossplane-admin-credentials` Secret in `crossplane-system` namespace
  - _Requirements: FR-2.3_

- [x] 1.1.3 Create crossplane_admin bootstrap manifest
  - Create SQL init manifest for `crossplane_admin` role
  - `CREATE ROLE crossplane_admin WITH LOGIN CREATEDB CREATEROLE PASSWORD '<from-infisical>'`
  - Create file: `manifests/spoke-catalog/infra/crossplane-admin-bootstrap.yaml`
  - Sync wave: -1 (before CNPG operator)
  - _Requirements: FR-2.3_

- [x] 1.1.4 Commit bootstrap manifests to Git
  - Commit to feature branch
  - Verify ArgoCD syncs to Spoke
  - _Requirements: FR-2.3_

### 1.2 provider-sql on Spoke

- [x] 1.2.1 Create provider-sql manifest for Spoke edge catalog
  - Provider: `xpkg.upbound.io/crossplane-contrib/provider-sql:v0.9.0`
  - Create file: `manifests/spoke-catalog/infra/crossplane-provider-sql.yaml`
  - Sync wave: -1
  - _Requirements: FR-5.1_

- [x] 1.2.2 Create provider-sql ProviderConfig for Spoke
  - Points to `shared-cnpg-rw.platform-data.svc.cluster.local:5432`
  - Uses `crossplane-admin-credentials` Secret
  - ProviderConfig name: `default`
  - Create file: `manifests/spoke-catalog/infra/crossplane-providerconfig-sql.yaml`
  - _Requirements: FR-5.1_

- [x] 1.2.3 Remove provider-sql from Hub
  - Remove `provider-sql/provider.yaml` from `manifests/hub-core-services/crossplane/providers/kustomization.yaml`
  - Delete `manifests/hub-core-services/crossplane/providers/provider-sql/` directory
  - _Requirements: FR-5.2_

- [x] 1.2.4 Commit provider-sql edge catalog changes to Git
  - Commit to feature branch
  - Verify ArgoCD syncs provider-sql to Spoke
  - Verify: `kubectl --context spoke-pool-eu-prod-01 get providers.pkg.crossplane.io provider-sql`
  - _Requirements: FR-5.1, FR-5.2_

---

## Phase 2: TenantDatabase XRD and Composition (Spoke)

### 2.1 TenantDatabase XRD

- [x] 2.1.1 Create TenantDatabase XRD
  - Schema: `tenantId`, `databaseName`, `cellId`
  - Cluster-scoped
  - Create file: `manifests/tenants/xrds/tenantdatabase.yaml`
  - _Requirements: FR-1.1_

- [x] 2.1.2 Commit XRD to Git
  - Commit to feature branch
  - Verify ArgoCD syncs XRD to Spoke
  - Verify: `kubectl --context spoke-pool-eu-prod-01 get xrd tenantdatabases.nutgraf.in`
  - _Requirements: FR-1.1_

### 2.2 TenantDatabase Composition

- [x] 2.2.1 Create TenantDatabase Composition — ESO ExternalSecret (restore)
  - Resource 1: ESO ExternalSecret with `creationPolicy: Merge`
  - Pulls `password` from Infisical path `/spoke-pool/<cell-id>/tenants/<tenant-id>/db-credentials`
  - Restores `tenant-<id>-db-credentials` Secret on cluster rebuild
  - Create file: `xrds/compositions/tenantdatabase-spoke.yaml` (initial)
  - _Requirements: FR-3.3_

- [x] 2.2.2 Add CNPG Database resource to Composition
  - Resource 2: CNPG Database CR via provider-kubernetes Object
  - Database name: `tenant-<id>-db` in `platform-data` namespace
  - Owner: `crossplane_admin`
  - _Requirements: FR-1.2_

- [x] 2.2.3 Add provider-sql Role resource to Composition
  - Resource 3: provider-sql Role CR
  - Role name: `tenant-<id>-user`
  - `privileges.login: true`, `privileges.createDb: false`, `privileges.superUser: false`
  - `passwordSecretRef`: references `tenant-<id>-db-credentials` Secret
  - `providerConfigRef.name: default`
  - _Requirements: FR-2.1_

- [x] 2.2.4 Add Grant resources to Composition
  - Resource 4: Grant CONNECT on `tenant-<id>-db`
  - Resource 5: Grant ALL on public tables
  - Resource 6: Grant ALL on public sequences
  - All grants scoped to `tenant-<id>-user` on `tenant-<id>-db` only
  - _Requirements: FR-2.2_

- [x] 2.2.5 Add ESO PushSecret resource to Composition
  - Resource 7: ESO PushSecret
  - Mirrors `tenant-<id>-db-credentials` Secret to Infisical
  - Path: `/spoke-pool/<cell-id>/tenants/<tenant-id>/db-credentials`
  - `refreshInterval: 1h`
  - _Requirements: FR-3.2_

- [x] 2.2.6 Set deletionPolicy: Delete on all composed resources
  - Ensures clean cascade: Grants revoked → Role dropped → Database deleted → Secret removed
  - _Requirements: FR-1.2_

- [x] 2.2.7 Commit TenantDatabase Composition to Git
  - Commit to feature branch
  - Verify ArgoCD syncs Composition to Spoke
  - Verify: `kubectl --context spoke-pool-eu-prod-01 get composition tenantdatabase-spoke`
  - _Requirements: FR-1.2_

---

## Phase 3: Hub Composition Update

### 3.1 AINativeSaaS Composition

- [x] 3.1.1 Remove direct provider-sql resources from Hub Composition
  - Remove from `xrds/compositions/ainativesaas-starter-hetzner.yaml`:
    - `db-credentials-secret`
    - `db-user`
    - `grant-connect`
    - `grant-tables`
    - `grant-sequences`
  - _Requirements: FR-4.1_

- [x] 3.1.2 Add TenantDatabase provider-kubernetes Object to Hub Composition
  - Single `provider-kubernetes Object` wrapping `TenantDatabase` CR
  - Patches: `tenantId`, `databaseName` (from `spec.database.name`), `cellId`
  - `readinessChecks: MatchCondition type=Ready status=True`
  - `providerConfigRef.name` patched from `spec.cellId`
  - _Requirements: FR-4.1_

- [x] 3.1.3 Update Pooler to use tenant credentials
  - `spec.pgbouncer.authQueryUser.secretRef.name`: `tenant-<id>-db-credentials`
  - Remove shared `app` user reference
  - _Requirements: FR-4.2_

- [x] 3.1.4 Update PostgREST to use tenant credentials
  - `PGRST_DB_URI`: from `tenant-<id>-db-credentials` Secret (key: `url` via pooler-secret)
  - `PGRST_DB_ANON_ROLE`: `tenant-<id>-user`
  - Remove shared `app` user reference
  - _Requirements: FR-4.3_

- [x] 3.1.5 Commit Hub Composition changes to Git
  - Commit to feature branch
  - Verify ArgoCD syncs updated Composition to Hub
  - _Requirements: FR-4.1, FR-4.2, FR-4.3_

---

## Phase 4: Validation

### 4.1 Spoke Infrastructure Validation

- [x] 4.1.1 Verify provider-sql installed on Spoke
  - `kubectl --context spoke-pool-eu-prod-01 get providers.pkg.crossplane.io provider-sql`
  - Verify: `INSTALLED=True, HEALTHY=True`
  - _Requirements: FR-5.1_

- [x] 4.1.2 Verify crossplane_admin role exists in CNPG
  - `kubectl --context spoke-pool-eu-prod-01 exec -n platform-data shared-cnpg-1 -- psql -U postgres -c "\du crossplane_admin"`
  - Verify: role exists with CREATEDB + CREATEROLE attributes
  - _Requirements: FR-2.3_

- [x] 4.1.3 Verify ProviderConfig is healthy
  - `kubectl --context spoke-pool-eu-prod-01 get providerconfigs.postgresql.sql.crossplane.io default`
  - Verify: `READY=True`
  - _Requirements: FR-5.1_

### 4.2 TenantDatabase Provisioning Validation

- [x] 4.2.1 Apply test TenantDatabase XR
  - Create test manifest for `tenant-app-creator`
  - Commit to fleet registry
  - Verify `TenantDatabase` XR reaches `Ready=True`
  - _Requirements: FR-1.1, FR-1.2_

- [x] 4.2.2 Verify CNPG Database CR created
  - `kubectl --context spoke-pool-eu-prod-01 get database tenant-app-creator-db -n platform-data`
  - _Requirements: FR-1.2_

- [x] 4.2.3 Verify per-tenant user created
  - `kubectl --context spoke-pool-eu-prod-01 exec -n platform-data shared-cnpg-1 -- psql -U postgres -c "\du tenant-app-creator-user"`
  - Verify user exists with login privilege
  - _Requirements: FR-2.1, AC-3_

- [x] 4.2.4 Verify tenant credentials Secret exists
  - `kubectl --context spoke-pool-eu-prod-01 get secret tenant-app-creator-db-credentials -n tenant-app-creator`
  - Verify fields: `username`, `password`, `database`, `host`, `port`
  - Verify `username` = `tenant-app-creator-user`
  - Verify password length = 32 chars
  - _Requirements: FR-3.1, AC-5_

- [ ] 4.2.5 Verify Secret mirrored to Infisical
  - Check Infisical UI: `/spoke-pool/spoke-pool-eu-prod-01/tenants/app-creator/db-credentials`
  - Verify password matches K8s Secret
  - _Requirements: FR-3.2, AC-6_

- [x] 4.2.6 Verify tenant user can connect to own database
  - `kubectl --context spoke-pool-eu-prod-01 exec -n platform-data shared-cnpg-1 -- psql -U tenant-app-creator-user -d tenant-app-creator-db -c "SELECT 1"`
  - Verify: connection succeeds
  - _Requirements: FR-2.2, AC-3_

- [x] 4.2.7 Verify tenant user CANNOT connect to other databases
  - `kubectl --context spoke-pool-eu-prod-01 exec -n platform-data shared-cnpg-1 -- psql -U tenant-app-creator-user -d postgres`
  - Verify: connection fails with permission denied
  - _Requirements: FR-2.2, AC-4_

### 4.3 Hub Composition Validation

- [x] 4.3.1 Verify AINativeSaaS XR waits for TenantDatabase Ready
  - Apply `AINativeSaaS` XR for test tenant
  - Verify Hub XR stays `Ready=False` until Spoke's `TenantDatabase` is `Ready=True`
  - _Requirements: FR-4.1, NFR-4.1, AC-8_

- [x] 4.3.2 Verify AtlasMigration connects as tenant user
  - Check AtlasMigration logs: `kubectl --context spoke-pool-eu-prod-01 logs -n tenant-app-creator atlasmigration-<pod>`
  - Verify: connection uses `tenant-app-creator-user`
  - Verify: no `app` user in logs
  - _Requirements: FR-4.1, AC-9_

- [x] 4.3.3 Verify Pooler uses tenant credentials
  - `kubectl --context spoke-pool-eu-prod-01 logs -n platform-data app-creator-pooler-<pod>`
  - Verify: `tenant-app-creator-user` in connection logs
  - Verify: no `app` user references
  - _Requirements: FR-4.2, AC-10_

- [x] 4.3.4 Verify PostgREST uses tenant credentials
  - `kubectl --context spoke-pool-eu-prod-01 logs -n tenant-app-creator postgrest-app-creator-<pod>`
  - Verify: `tenant-app-creator-user` in connection logs
  - Verify: no `app` user references
  - _Requirements: FR-4.3, AC-11_

### 4.4 Disaster Recovery Validation

- [x] 4.4.1 Simulate cluster rebuild (Secret deletion)
  - Delete `tenant-app-creator-db-credentials` Secret manually
  - Wait for ESO ExternalSecret to restore it from Infisical
  - Verify: Secret restored with same password
  - Verify: Pooler and PostgREST reconnect successfully
  - _Requirements: FR-3.3, NFR-2.1, AC-7_

### 4.5 Lifecycle Validation

- [x] 4.5.1 Verify tenant deletion cleans up all resources
  - Delete `AINativeSaaS` XR for test tenant
  - Verify: `TenantDatabase` XR deleted from Spoke
  - Verify: Role dropped: `\du tenant-app-creator-user` returns nothing
  - Verify: Database deleted: `\l tenant-app-creator-db` returns nothing
  - Verify: Secret deleted from namespace
  - _Requirements: FR-1.2, AC-12_

---

## Phase 5: Review Checkpoint

- [ ] 5.1.1 **MANDATORY STOP — Phase Review**
  - Present completion summary
  - Show validation results from Phase 4
  - Demonstrate: TenantDatabase XR → DB + Role + Grants + Secret + Infisical backup
  - Demonstrate: Cluster rebuild recovery
  - Demonstrate: AtlasMigration connects as tenant user
  - **WAIT FOR USER APPROVAL**
  - _Requirements: All_

---

## Notes

- All tasks follow GitOps-first: changes via Git commits, ArgoCD reconciles
- No `kubectl apply` except for validation read-only commands
- provider-sql runs on Spoke only — Hub uses provider-kubernetes for cross-cluster management
- ESO PushSecret is mandatory — without it, cluster rebuild breaks all tenant DB connections
- `deletionPolicy: Delete` on all TenantDatabase composed resources ensures clean teardown
- Sync wave ordering: ESO restore → Secret → Role → Grants → PushSecret
