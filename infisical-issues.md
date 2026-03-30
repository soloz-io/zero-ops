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

### 5. ❌ CURRENT BLOCKER: setup-platform-roles Job Cannot Connect to PostgreSQL

**Symptoms:**
```
Waiting for PostgreSQL to be ready...
Waiting for database...
Waiting for database...
(infinite loop)
```

**Investigation:**
- Init container (`wait-for-db`) succeeds: `pg_isready` confirms database accepting connections
- Main container (`setup-roles`) fails: `psql -c "SELECT 1"` cannot connect
- Database pods are healthy (platform-db-1, platform-db-2, platform-db-3 all Running)
- Direct connection from database pod works: `psql -U postgres -c "SELECT 1"` succeeds

**Root Cause:** SSL connection issue
- Job uses `PGSSLMODE=require` but doesn't mount CA certificate
- PostgreSQL cluster uses self-signed certificates (CNPG generates `platform-db-ca` secret)
- Job cannot verify SSL certificate → connection fails

**Fix Applied:**
Changed `PGSSLMODE` from `require` to `disable` in `manifests/platform-database/setup-platform-roles-job.yaml`

**Next Steps:**
1. Commit SSL fix to Git
2. Delete current job pod
3. Force ArgoCD sync to recreate job with new SSL setting
4. Verify job completes and updates database role passwords
5. Delete crashing Infisical pods to restart with correct passwords
6. Verify Infisical starts successfully

---

## Files Modified

### GitOps Manifests
- `manifests/argocd/apps/platform-victoriametrics.yaml` - Reduced memory requests
- `manifests/platform-infisical/values.yaml` - Updated pod restart annotation
- `manifests/platform-infisical/redis.yaml` - Standalone Redis deployment
- `manifests/platform-database/migrations/infisical-migrations.yaml` - Database wipe logic
- `manifests/platform-database/setup-platform-roles-job.yaml` - SSL mode fix (pending commit)

### Hub CLI
- `internal/hub/components/installer.go` - Auto-delete job after password rotation

### Secrets (Hub CLI Managed)
- `infisical-secrets` - ENCRYPTION_KEY, AUTH_SECRET, REDIS_URL, DB_ROOT_CERT
- `infisical-redis-credentials` - Redis password
- `infisical-postgres-connection` - PostgreSQL connection string
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
