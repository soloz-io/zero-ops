# Infisical Deployment Issues - Debug Log

## Issue Timeline

### 1. ✅ RESOLVED: Worker Node Capacity
**Problem:** Worker nodes at 87-88% capacity, Infisical pods couldn't schedule (1000Mi memory + 350m CPU required)

**Solution:** Reduced VictoriaMetrics memory requests:
- vmselect: 512Mi → 256Mi
- vminsert: 512Mi → 256Mi  
- vmstorage: 1Gi → 512Mi
- **Total freed:** 768Mi

**Result:** Worker node 1 now at 60% capacity (2240Mi/3809Mi used), 1569Mi available

---

### 2. ✅ RESOLVED: Secret Zero Pattern
**Problem:** ArgoCD selfHeal was overwriting hub-managed secrets with "changeme" from Git

**Solution:**
- Added `argocd.argoproj.io/sync-options: Replace=false` to all credential secrets
- Removed `stringData` from Git manifests
- Hub CLI owns secret data, Git owns metadata only

**Result:** Secrets no longer overwritten by ArgoCD

---

### 3. ✅ RESOLVED: Redis Deployment
**Problem:** Infisical needed standalone Redis with password authentication

**Solution:**
- Created `manifests/platform-infisical/redis.yaml` (StatefulSet)
- Hub CLI generates Redis password and injects REDIS_URL into `infisical-secrets`
- Helm chart uses `envFrom.secretRef.name=infisical-secrets` pattern

**Result:** Redis running successfully at `redis-master.zero-ops-system.svc:6379`

---

### 4. ❌ CURRENT ISSUE: Database Password Synchronization

**Problem:** Password authentication failed for user "infisical"

**Root Cause:** Lifecycle disconnect between imperative secret rotation (hub CLI) and declarative job execution (ArgoCD)

**The Flow:**
1. `hub init-secrets` generates NEW passwords → updates K8s secrets via client-go
2. Git manifests unchanged → ArgoCD sees no diff → doesn't re-run `setup-platform-roles` job
3. PostgreSQL database still has OLD passwords
4. Infisical tries to connect with NEW password → authentication fails

**Solution Implemented:**
- Updated `internal/hub/components/installer.go` to auto-delete `setup-platform-roles` job after password rotation
- Forces ArgoCD/Kubernetes to recreate job with fresh passwords
- Job uses `ALTER ROLE` to sync PostgreSQL passwords with updated secrets

**Current Status:** Job recreated but stuck in "Waiting for database..." loop

---

### 5. ❌ BLOCKER A: PostgreSQL Superuser Password Authentication Failure

**Symptoms:** `setup-platform-roles` job stuck in "Waiting for database..." loop

**Root Cause:** CNPG superuser secret out of sync
- Password from `platform-db-superuser` works ONLY inside database pods (local peer auth)
- External connections fail: "password authentication failed for user postgres"
- CNPG superuserSecretVersion: 12406960 (password rotated, secret not synced)

**Fix:** Use `platform-db-app` user instead of postgres superuser in job

---

### 6. ❌ BLOCKER B: Infisical SSL Configuration Mismatch

**Source References:**
- `archived/references/identity-auth/infisical/backend/src/db/knexfile.ts:18-29` (Knex config)
- `archived/references/identity-auth/infisical/helm-charts/infisical-standalone-postgres/templates/infisical.yaml:66-73` (Helm template)
- `archived/references/identity-auth/infisical/helm-charts/infisical-standalone-postgres/values.yaml:73` (kubeSecretRef)

**Root Cause:** Contradictory SSL configuration between connection string and environment variable

**Current Configuration Analysis:**

1. **Helm Chart Behavior** (from `infisical.yaml:66-73`):
```yaml
env:
- name: DB_CONNECTION_URI
  valueFrom:
    secretKeyRef:
      name: infisical-postgres-connection
      key: connection-string
envFrom:
- secretRef:
    name: infisical-secrets  # Contains DB_ROOT_CERT
```

2. **Infisical's Knex Priority** (from `knexfile.ts:18-29`):
```typescript
connection: {
  connectionString: process.env.DB_CONNECTION_URI,  // Takes precedence
  host: process.env.DB_HOST,
  // ...
  ssl: process.env.DB_ROOT_CERT
    ? { rejectUnauthorized: true, ca: Buffer.from(process.env.DB_ROOT_CERT, "base64").toString("ascii") }
    : false
}
```

**The Conflict:**
- `DB_CONNECTION_URI=postgresql://infisical:...@platform-db-rw.zero-ops-system.svc:5432/infisical?sslmode=disable`
- `DB_ROOT_CERT=<base64-cert>` (from `infisical-secrets`)
- **Problem:** Connection string explicitly disables SSL, but `DB_ROOT_CERT` is provided for strict SSL
- **Result:** Infisical uses connection string (no SSL) → CNPG rejects (requires SSL from external clients)

**Current Secrets in Cluster:**
- `infisical-secrets`: Contains `ENCRYPTION_KEY`, `AUTH_SECRET`, `REDIS_URL`, `DB_ROOT_CERT`
- `infisical-postgres-connection`: Contains `connection-string` (with `sslmode=disable`), `ca-cert`

**Why This Happens:**
1. `internal/hub/components/installer.go:721` creates connection string with `sslmode=disable`
2. `internal/hub/components/installer.go:619` provides `DB_ROOT_CERT` in `infisical-secrets`
3. Helm chart injects BOTH as env vars
4. Knex prioritizes `DB_CONNECTION_URI`, ignores `DB_ROOT_CERT` SSL config
5. Connection fails because CNPG requires SSL from external clients

**Recommended Fix:**
Change connection string to use `sslmode=require` instead of `sslmode=disable`:
```go
// internal/hub/components/installer.go:721
connectionString := fmt.Sprintf(
  "postgresql://infisical:%s@platform-db-rw.zero-ops-system.svc:5432/infisical?sslmode=require",
  url.QueryEscape(password),
)
```

This allows PostgreSQL client to use SSL, and Knex will use `DB_ROOT_CERT` for certificate validation.

---

## Fix Implementation (2026-03-30T09:15:00Z)

### Changes Made

**Blocker A: PostgreSQL Superuser mTLS Authentication**

1. **File:** `manifests/platform-database/setup-platform-roles-job.yaml`
   - Changed authentication from password to mTLS using CNPG client certificates
   - Added environment variables:
     - `PGSSLMODE=verify-ca` (was `require`)
     - `PGSSLCERT=/etc/postgresql/client/tls.crt` (new)
     - `PGSSLKEY=/etc/postgresql/client/tls.key` (new)
     - `PGSSLROOTCERT=/etc/postgresql/ca/ca.crt` (path changed)
   - Removed `PGUSER` and `PGPASSWORD` from secret references
   - Added `PGUSER=postgres` as static value
   - Added volume mount for client certificates:
     - `client-cert` volume from `platform-db-server` secret (CORRECTED: was platform-db-superuser)
     - Mounted at `/etc/postgresql/client` with mode `0600`
   - Updated `ca-cert` mount path from `/etc/postgresql` to `/etc/postgresql/ca`
   - Updated annotation timestamp to `2026-03-30T09:00:00Z`

**Issue Found During Deployment:**
- Initial implementation used `platform-db-superuser` secret for client certificates
- CNPG stores server certificates in `platform-db-server` secret (type: kubernetes.io/tls)
- `platform-db-superuser` only contains basic-auth credentials (username, password, uri, etc.)
- Fixed by changing volume source from `platform-db-superuser` to `platform-db-server`

**Blocker B: Infisical SSL Configuration**

2. **File:** `internal/hub/components/installer.go`
   - Function: `InstallPostgresConnectionSecret()`
   - Removed connection string generation (line ~721)
   - Changed secret data from:
     - `connection-string` (with `sslmode=disable`)
     - `ca-cert`
   - To individual parameters:
     - `DB_HOST=platform-db-rw.zero-ops-system.svc.cluster.local`
     - `DB_PORT=5432`
     - `DB_USER=infisical`
     - `DB_PASSWORD=<from infisical-db-credentials>`
     - `DB_NAME=infisical`
     - `DB_ROOT_CERT=<base64-encoded CA cert>`
   - Updated success message to indicate "individual DB params"
   - Removed unused `net/url` import

3. **File:** `manifests/platform-infisical/values.yaml`
   - Disabled `postgresql.useExistingPostgresSecret.enabled` (was `true`)
   - Added `envFrom` section to inject both secrets:
     - `infisical-secrets` (ENCRYPTION_KEY, AUTH_SECRET, REDIS_URL, DB_ROOT_CERT)
     - `infisical-postgres-connection` (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME, DB_ROOT_CERT)
   - Updated pod annotation timestamp to `2026-03-30T09:00:00Z`
   - Updated comments to reflect new architecture

### Expected Behavior After Fix

**Blocker A:**
- Job connects to PostgreSQL using X.509 client certificates
- Bypasses password authentication entirely
- No dependency on `platform-db-superuser` password sync
- Uses CNPG-managed certificates for authentication

**Blocker B:**
- Infisical receives individual DB parameters via environment variables
- Knex detects `DB_ROOT_CERT` and enables SSL with `rejectUnauthorized: true`
- No connection string to override SSL configuration
- CNPG accepts connection with proper SSL/TLS

### Testing Steps

1. Commit changes to Git
2. Run `hub init-secrets` to regenerate `infisical-postgres-connection` with new format
3. Force ArgoCD sync: `kubectl patch application platform-database -n argocd --type merge -p '{"operation":{"sync":{"syncStrategy":{"apply":{"force":true}}}}}'`
4. Verify job completes: `kubectl logs -n zero-ops-system -l job-name=setup-platform-roles --tail=50`
5. Force Infisical sync: `kubectl patch application platform-infisical -n argocd --type merge -p '{"operation":{"sync":{"syncStrategy":{"apply":{"force":true}}}}}'`
6. Verify Infisical pods start: `kubectl get pods -n zero-ops-system -l app.kubernetes.io/name=infisical`
7. Check Infisical logs: `kubectl logs -n zero-ops-system -l app.kubernetes.io/name=infisical --tail=100`

### Deployment Status (2026-03-30T09:30:00Z)

**Git Commit:** c4f6288 (mTLS + individual DB params)

**ArgoCD Sync Status:**
- Forced sync initiated via kubectl patch
- ArgoCD detected revision c4f6288
- Operation phase: "Running"
- Waiting for `setup-platform-roles` job to become healthy

**Issue Found (2026-03-30T09:45:00Z):**
- Job pod failed to start: `MountVolume.SetUp failed for volume "client-cert" : references non-existent secret key: tls.crt`
- Root cause: Volume spec used `items` array with `defaultMode` which caused key reference issues
- The secret `platform-db-server` exists and has `tls.crt` and `tls.key`, but the volume mount configuration was incorrect

**Why This Was Missed:**
- Changes were committed and pushed without waiting for ArgoCD sync to complete
- Did not check pod events immediately after job creation
- ArgoCD reported "job.batch/setup-platform-roles created" but pod never started
- Violated GitOps workflow: should have validated sync completion before proceeding

**Fix Applied:**
- Removed `items` array from `client-cert` volume spec
- Kept `defaultMode: 0600` for security (psql requires restrictive permissions on private keys)
- Secret will mount all keys (tls.crt, tls.key) at `/etc/postgresql/client/`

**Issue Found (2026-03-30T10:20:00Z) - mTLS Authentication Failure:**
- Manual kubectl apply succeeded, job created and running
- Job stuck in "Waiting for database..." loop
- Error: `SSL error: ssl/tls alert unsupported certificate`
- Root cause: `platform-db-server` secret contains SERVER certificates, not CLIENT certificates
- mTLS requires client certificates for authentication, but CNPG doesn't provide separate client certs
- The `platform-db-server` secret is for server-side TLS, not client authentication

**Solution Applied (2026-03-30T10:25:00Z):**
- Switched from mTLS (postgres superuser) to password authentication (app user)
- Changed `PGUSER` from `postgres` to use `platform-db-app` secret username
- Changed `PGPASSWORD` to use `platform-db-app` secret password
- Changed `PGSSLMODE` from `verify-ca` to `require` (SSL without client cert verification)
- Removed all mTLS-related environment variables (PGSSLCERT, PGSSLKEY, PGSSLROOTCERT)
- Removed volume mounts for certificates (ca-cert, client-cert)
- Removed volumes section entirely

**Why mTLS Failed:**
- CNPG `platform-db-server` secret contains server certificates for TLS encryption
- These are NOT client certificates for mutual TLS authentication
- PostgreSQL rejected connection: "unsupported certificate"
- The app user (`platform-db-app`) uses standard password authentication with SSL encryption

**Next Actions:**
1. Delete manually created job: `kubectl delete job setup-platform-roles -n zero-ops-system`
2. Commit and push password authentication fix
3. Wait for ArgoCD sync to complete
4. Verify job completes successfully
5. Check database roles created
6. Proceed to Infisical deployment

---

## Files Modified (Historical)

### GitOps Manifests
- `manifests/argocd/apps/platform-victoriametrics.yaml` - Reduced memory requests
- `manifests/platform-infisical/values.yaml` - Updated pod restart annotation, changed to envFrom pattern
- `manifests/platform-infisical/redis.yaml` - Standalone Redis deployment
- `manifests/platform-database/migrations/infisical-migrations.yaml` - Database wipe logic
- `manifests/platform-database/setup-platform-roles-job.yaml` - Changed to mTLS authentication

### Hub CLI
- `internal/hub/components/installer.go` - Auto-delete job after password rotation, changed to individual DB params

### Secrets (Hub CLI Managed)
- `infisical-secrets` - ENCRYPTION_KEY, AUTH_SECRET, REDIS_URL, DB_ROOT_CERT
- `infisical-redis-credentials` - Redis password
- `infisical-postgres-connection` - Individual DB parameters (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME, DB_ROOT_CERT)
- `control-plane-db-credentials` - mcp_server password
- `hub-db-credentials` - spoke_controller password
- `infisical-db-credentials` - infisical user password

---

## Lessons Learned

1. **GitOps + Imperative CLI = Lifecycle Gap**
   - When CLI updates secrets imperatively, GitOps doesn't detect changes
   - Jobs with `Replace=true` only recreate when Git manifest changes
   - Solution: CLI must delete jobs to force recreation

2. **SSL Configuration Matters**
   - CNPG generates self-signed certificates
   - Jobs need CA certificate mounted or use `sslmode=disable`
   - Production should use `sslmode=verify-ca` with proper cert injection

3. **Secret Zero Pattern Works**
   - Hub CLI owns secret data (never in Git)
   - Git owns secret metadata (namespace, name, labels, annotations)
   - ArgoCD `Replace=false` prevents overwrites

4. **Capacity Planning Critical**
   - cx23 nodes (2 vCPU, 3.8GB RAM) too small for full platform stack
   - VictoriaMetrics default requests too high for dev clusters
   - Production needs cx33+ (4 vCPU, 8GB RAM) worker nodes


---

## CRITICAL FINDING: Two Separate Blockers

**Date:** 2026-03-30T08:15:00Z

### Blocker A: PostgreSQL Superuser Password Mismatch

**Evidence:**
- ✅ Password works from inside database pod
- ❌ Same password fails from external clients
- CNPG superuserSecretVersion: 12406960 (rotated)

**Solution:** Use `platform-db-app` user in `setup-platform-roles` job

---

### Blocker B: Infisical SSL Configuration Contradiction

**Source:** `archived/references/identity-auth/infisical/backend/src/db/knexfile.ts`

**Evidence:**
```typescript
// Infisical's Knex config (lines 18-29)
connection: {
  connectionString: process.env.DB_CONNECTION_URI,  // Takes precedence
  host: process.env.DB_HOST,
  // ...
  ssl: process.env.DB_ROOT_CERT
    ? { rejectUnauthorized: true, ca: Buffer.from(process.env.DB_ROOT_CERT, "base64").toString("ascii") }
    : false
}
```

**Our Config:**
- `DB_CONNECTION_URI=postgresql://...?sslmode=disable` (no SSL)
- `DB_ROOT_CERT=<base64-cert>` (strict SSL)
- **Conflict:** Connection string disables SSL, but we provide cert for SSL

**Solution:** Remove `DB_CONNECTION_URI`, use individual params (`DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_ROOT_CERT`)
