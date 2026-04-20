# Infisical Database Connection Pooling Analysis

**Date:** 2026-03-30  
**Status:** ✅ APPROVED FOR IMPLEMENTATION  
**Classification:** Production-Grade Recovery Procedure  
**Infisical Version:** v0.158.0

---

## Approval Summary

**✅ APPROVED** as production-grade solution with the following classification:

- **Approved as:** Production-grade recovery procedure, architectural improvement, long-term scaling solution
- **NOT approved as:** Steady-state operating model requiring manual intervention or workaround
- **Approval Date:** 2026-03-30
- **Next Review:** After 30 days of production operation

**Key Approval Points:**
1. PgBouncer is the standard Kubernetes pattern for connection pooling (not a workaround)
2. CNPG includes PgBouncer as first-class feature (production-ready)
3. Solves root cause (connection leak) at infrastructure layer
4. Enables horizontal scaling without application changes
5. GitOps compliant (all changes via manifests)

---

## Problem Statement

Infisical deployment experiencing database connection pool exhaustion, resulting in:
- 504 Gateway Timeout errors on all API endpoints
- Pod showing Ready (1/1) but service endpoints empty
- All requests timing out with `KnexTimeoutError: Knex: Timeout acquiring a connection. The pool is probably full.`

## Root Cause Analysis

### 1. Connection Pool Exhaustion

**Observed State:**
```bash
# Active connections to infisical database
kubectl exec -n zero-ops-system platform-db-2 -- psql -U postgres -d infisical \
  -c "SELECT count(*) as active_connections, state FROM pg_stat_activity WHERE datname='infisical' GROUP BY state;"

# Result:
# active_connections | state  
# -------------------+--------
#                  2 | active
#                 16 | idle    <-- PROBLEM: 16 idle connections stuck
```

**Expected State:**
- Infisical hardcoded pool: `min: 0, max: 10` (per replica)
- Running replicas: 1
- Expected max connections: 10
- Actual idle connections: 16 (exceeds pool limit)

**Conclusion:** Connection leak - connections not being released properly back to the pool.

### 2. Infisical Pool Configuration Limitations

**Source Code Analysis:**
```typescript
// File: backend/src/db/instance.ts
export const initDbConnection = ({
  dbConnectionUri,
  dbRootCert,
  readReplicas = []
}: {
  dbConnectionUri: string;
  dbRootCert?: string;
  readReplicas?: { dbConnectionUri: string; dbRootCert?: string; }[];
}) => {
  db = knex({
    client: "pg",
    connection: {
      connectionString: modifiedDbConnectionUri,
      host: process.env.DB_HOST,
      port: process.env.DB_PORT ? parseInt(process.env.DB_PORT, 10) : undefined,
      user: process.env.DB_USER,
      database: process.env.DB_NAME,
      password: process.env.DB_PASSWORD,
      ssl: sslConfig
    },
    pool: { min: 0, max: 10 },  // <-- HARDCODED, NO ENV VAR OVERRIDE
    migrations: {
      tableName: "infisical_migrations"
    }
  });
  // ...
};
```

**Official Documentation Check:**
```bash
# File: docs/self-hosting/configuration/envars.mdx
# Available database environment variables:
- DB_CONNECTION_URI (required)
- DB_ROOT_CERT (optional)
- DB_READ_REPLICAS (optional)

# NO VARIABLES FOR:
- DB_CONNECTION_POOL_MIN
- DB_CONNECTION_POOL_MAX
- DB_ACQUIRE_CONNECTION_TIMEOUT
```

**Conclusion:** Infisical does NOT support database pool configuration via environment variables. Pool settings are hardcoded in application code.

### 3. Scaling Limitations

**Helm Chart Defaults:**
```yaml
# File: helm-charts/infisical-standalone-postgres/values.yaml
infisical:
  replicaCount: 2  # Default is 2 replicas
```

**Our Configuration:**
```yaml
# File: manifests/platform-core-services/platform-infisical/values.yaml
infisical:
  replicaCount: 1  # Reduced to 1 to limit connections
```

**Analysis:**
- Default 2 replicas × 10 connections = 20 max connections
- Our 1 replica × 10 connections = 10 max connections
- Actual: 16 idle connections from 1 pod = **connection leak prevents scaling**

**Conclusion:** Even with 1 replica, connection leak exhausts the pool. Scaling to 2+ replicas would worsen the problem.

## Attempted Solutions (Failed)

### Attempt 1: Add Environment Variables (FAILED)
```yaml
# Added to manifests/platform-core-services/platform-infisical/values.yaml
extraEnv:
  - name: DB_CONNECTION_POOL_MIN
    value: "2"
  - name: DB_CONNECTION_POOL_MAX
    value: "10"
  - name: DB_ACQUIRE_CONNECTION_TIMEOUT
    value: "60000"
```

**Result:** No effect. These environment variables are not read by Infisical code.

### Attempt 2: Increase PostgreSQL max_connections (NOT RECOMMENDED)
```yaml
# manifests/platform-database/platform-db.yaml
spec:
  postgresql:
    parameters:
      max_connections: "200"
```

**Why This Fails:**
- Treats symptom, not cause
- PostgreSQL connections are expensive (memory per connection)
- Doesn't solve Infisical's connection leak
- Not sustainable at scale

### Attempt 3: Increase Readiness Probe Timeout (INSUFFICIENT)
```yaml
# manifests/platform-core-services/platform-infisical/values.yaml
readinessProbe:
  timeoutSeconds: 3  # Increased from 1s
```

**Why This Fails:**
- Probe timeout doesn't fix pool exhaustion
- Requests still fail with KnexTimeoutError
- Pod becomes Ready but can't serve traffic

## Recommended Solution: PgBouncer

### Why PgBouncer is the Idiomatic Fix

**PgBouncer** is a lightweight connection pooler for PostgreSQL that sits between the application and database, multiplexing client connections to a smaller pool of database connections.

**Why This is NOT a Workaround:**
1. **Standard Kubernetes Pattern:** CloudNativePG (CNPG) includes PgBouncer as a first-class feature
2. **Solves Connection Leaks:** Applications with connection management issues benefit from external pooling
3. **Production-Ready:** Used by major platforms (Heroku, AWS RDS Proxy equivalent)
4. **Enables Scaling:** Allows multiple Infisical replicas without exhausting database connections

**How PgBouncer Solves the Problem:**
```
Without PgBouncer:
  Infisical Pod 1 → 16 leaked connections → PostgreSQL (max 100)
  Infisical Pod 2 → 16 leaked connections → PostgreSQL
  Total: 32 connections leaked, pool exhausted

With PgBouncer:
  Infisical Pod 1 → 100 client connections → PgBouncer → 25 DB connections → PostgreSQL
  Infisical Pod 2 → 100 client connections → PgBouncer → (reuses same 25)
  Total: 25 connections to PostgreSQL, multiplexed across 200 clients
```

### Implementation

**Step 1: Enable CNPG Pooler**
```yaml
# File: manifests/platform-database/platform-db.yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: platform-db
  namespace: zero-ops-system
spec:
  instances: 3
  
  # Enable PgBouncer pooler
  pooler:
    enabled: true
    type: rw
    instances: 1
    pgbouncer:
      poolMode: transaction  # Most efficient for stateless apps
      parameters:
        max_client_conn: "100"      # Max clients per PgBouncer instance
        default_pool_size: "25"     # Max connections to PostgreSQL
        reserve_pool_size: "5"      # Reserved connections for emergencies
        reserve_pool_timeout: "5"   # Seconds to wait for reserved connection
```

**Step 2: Update Infisical Connection Secret**
```go
// File: internal/hub/components/installer.go
func (i *Installer) InstallPostgresConnectionSecret(ctx context.Context) (bool, error) {
    // Change from direct PostgreSQL connection to PgBouncer
    dbHost := "platform-db-pooler-rw.zero-ops-system.svc"  // PgBouncer service
    dbPort := "5432"
    
    // ... rest of secret creation
}
```

**Step 3: Restart Infisical Pod**
```bash
# After committing changes and ArgoCD sync
kubectl delete pod -n zero-ops-system -l app.kubernetes.io/name=infisical
```

### Expected Results

**Before PgBouncer:**
- 1 Infisical replica: 16 leaked connections, pool exhausted
- 2 Infisical replicas: 32 leaked connections, complete failure
- API: 504 Gateway Timeout

**After PgBouncer:**
- 1 Infisical replica: 100 client connections → 25 DB connections
- 2 Infisical replicas: 200 client connections → 25 DB connections (multiplexed)
- API: 200 OK, normal operation
- Scaling: Can scale to 10+ replicas without database connection issues

## Alternative Solutions (Not Recommended)

### Option 1: Fork Infisical and Add Pool Configuration
**Pros:** Full control over pool settings  
**Cons:**
- Maintenance burden (must track upstream changes)
- Delays security patches
- Not sustainable for production

### Option 2: Submit Upstream PR
**Pros:** Benefits entire Infisical community  
**Cons:**
- Takes weeks/months for review and merge
- No guarantee of acceptance
- Blocks our deployment timeline

### Option 3: Use Infisical Cloud
**Pros:** Managed service, no infrastructure concerns  
**Cons:**
- Violates BYOC requirement
- Secrets transit through third-party
- Not acceptable for zero-ops architecture

## Lessons Learned

1. **Always check application source code** for configuration options, not just documentation
2. **Connection pooling is infrastructure concern** - applications shouldn't manage this alone
3. **PgBouncer is standard practice** for Node.js/Knex applications in Kubernetes
4. **CNPG includes PgBouncer** - use platform features instead of application workarounds

## References

- Infisical Source: `archived/references/identity-auth/infisical/backend/src/db/instance.ts`
- Infisical Docs: `archived/references/identity-auth/infisical/docs/self-hosting/configuration/envars.mdx`
- CNPG Pooler Docs: https://cloudnative-pg.io/documentation/current/connection_pooling/
- PgBouncer Docs: https://www.pgbouncer.org/
- Knex Pool Config: https://knexjs.org/guide/#pool

## Approval Details

### Classification

**✅ APPROVED** with the following important qualification:

**Approved as:**
- ✅ Production-grade recovery procedure for connection pool exhaustion
- ✅ Architectural improvement (external connection pooling)
- ✅ Long-term solution for scaling Infisical
- ✅ Standard Kubernetes pattern (not a workaround)

**NOT approved as:**
- ❌ Steady-state operating model requiring manual intervention
- ❌ Temporary workaround for missing application features
- ❌ Emergency-only procedure

### Why This Solution is Approved

**✔ Correct Architecture:**
- PgBouncer is the **idiomatic Kubernetes pattern** for connection pooling
- CNPG includes PgBouncer as a **first-class feature** (not a hack)
- Solves **root cause** (connection leak) at infrastructure layer
- Enables **horizontal scaling** without application changes

**✔ Production-Ready:**
- Used by major platforms (Heroku Postgres, AWS RDS Proxy equivalent)
- Battle-tested in production environments
- Supported by CloudNativePG operator
- No custom code or forks required

**✔ GitOps Compliant:**
- All changes via manifest updates
- No imperative kubectl commands in steady state
- ArgoCD manages pooler lifecycle
- Declarative configuration

### Day 2 Operations Model

**This is NOT a workaround.** PgBouncer is the standard solution for:
1. Applications with connection management issues (like Infisical)
2. Scaling stateless applications against PostgreSQL
3. Connection multiplexing in Kubernetes environments

**Operational Characteristics:**
- PgBouncer runs as a managed CNPG resource
- **Zero manual intervention required** after initial deployment
- Scales automatically with database cluster
- Monitored via standard Kubernetes metrics
- Fully declarative (GitOps managed)

**When to Revisit:**
- If Infisical adds native pool configuration support (unlikely)
- If connection leak is fixed upstream (monitor release notes)
- If we migrate to a different secret management solution
- After 30 days of production operation (scheduled review)

## Implementation Plan

### Phase 1: Enable PgBouncer (GitOps)

**Step 1: Update Database Manifest**
```yaml
# File: manifests/platform-database/platform-db.yaml
# Add pooler configuration to existing Cluster spec
```

**Step 2: Update Connection Secret Generator**
```go
// File: internal/hub/components/installer.go
// Change dbHost to use pooler service endpoint
```

**Step 3: Commit and Sync**
```bash
git add manifests/platform-database/platform-db.yaml
git add internal/hub/components/installer.go
git commit -m "feat(infisical): enable PgBouncer for connection pooling"
git push origin <branch>

# ArgoCD will sync automatically
# Verify: kubectl get pooler -n zero-ops-system
```

**Step 4: Regenerate Secrets**
```bash
# Run CLI to update connection secret with new pooler endpoint
./hub init-secrets --kubeconfig ~/.kube/config
```

**Step 5: Restart Infisical**
```bash
# Delete pod to pick up new connection string
kubectl delete pod -n zero-ops-system -l app.kubernetes.io/name=infisical
```

### Phase 2: Validation

**Verify PgBouncer is Running:**
```bash
kubectl get pooler -n zero-ops-system
# Expected: platform-db-pooler-rw (1/1 Running)

kubectl get svc -n zero-ops-system | grep pooler
# Expected: platform-db-pooler-rw service exists
```

**Verify Infisical Connects via PgBouncer:**
```bash
kubectl logs -n zero-ops-system -l app.kubernetes.io/name=infisical | grep -i "database\|connection"
# Should show successful connection, no timeout errors
```

**Verify Connection Count:**
```bash
# Check PostgreSQL connections (should be ~25, not 16+ idle)
kubectl exec -n zero-ops-system platform-db-2 -- psql -U postgres -d infisical \
  -c "SELECT count(*) as active_connections, state FROM pg_stat_activity WHERE datname='infisical' GROUP BY state;"
```

**Verify API Accessibility:**
```bash
curl -k -s -o /dev/null -w "%{http_code}" https://infisical.nutgraf.in/api/status
# Expected: 200
```

### Phase 3: Scale Testing (Optional)

**Test Horizontal Scaling:**
```yaml
# File: manifests/platform-core-services/platform-infisical/values.yaml
infisical:
  replicaCount: 2  # Scale to 2 replicas
```

**Verify Connection Multiplexing:**
```bash
# With 2 replicas, PostgreSQL connections should still be ~25
# PgBouncer multiplexes 200 client connections → 25 DB connections
kubectl exec -n zero-ops-system platform-db-2 -- psql -U postgres -d infisical \
  -c "SELECT count(*) FROM pg_stat_activity WHERE datname='infisical';"
```

## Day 2 Operations Runbook

### Normal Operations

**Zero manual intervention required.** PgBouncer is managed by CNPG operator as a first-class resource:

- ✅ Automatic failover with database cluster
- ✅ Health checks via Kubernetes probes
- ✅ Metrics exposed for monitoring
- ✅ Logs available via kubectl logs
- ✅ Declarative configuration (GitOps managed)
- ✅ No imperative commands in steady state

**This is the production operating model, not an emergency procedure.**

### Monitoring

**Key Metrics to Monitor:**
```bash
# PgBouncer pod health
kubectl get pods -n zero-ops-system -l cnpg.io/poolerName=platform-db-pooler-rw

# Connection pool stats (via PgBouncer admin console)
kubectl exec -n zero-ops-system platform-db-pooler-rw-<pod> -- \
  psql -p 5432 -U postgres pgbouncer -c "SHOW POOLS;"

# PostgreSQL connection count
kubectl exec -n zero-ops-system platform-db-2 -- psql -U postgres \
  -c "SELECT count(*) FROM pg_stat_activity WHERE datname='infisical';"
```

**Alert Thresholds:**
- PostgreSQL connections > 80: Warning (approaching limit)
- PgBouncer pod not Ready: Critical (connection pooling down)
- Infisical 504 errors: Critical (pool exhaustion)

### Troubleshooting

**Issue: Infisical still getting 504 errors**

**Diagnosis:**
```bash
# Check if Infisical is using pooler endpoint
kubectl get secret -n zero-ops-system infisical-postgres-connection -o yaml | grep DB_HOST
# Should show: platform-db-pooler-rw.zero-ops-system.svc

# Check PgBouncer logs
kubectl logs -n zero-ops-system -l cnpg.io/poolerName=platform-db-pooler-rw
```

**Resolution:**
1. Verify connection secret has correct pooler endpoint
2. Restart Infisical pods to pick up new connection
3. Check PgBouncer configuration in Cluster spec

**Issue: PgBouncer pod not starting**

**Diagnosis:**
```bash
kubectl describe pooler -n zero-ops-system platform-db-pooler-rw
kubectl logs -n zero-ops-system -l cnpg.io/poolerName=platform-db-pooler-rw
```

**Resolution:**
1. Verify CNPG operator is running
2. Check Cluster spec pooler configuration syntax
3. Review CNPG operator logs for errors

### Rollback Procedure (Emergency Only)

**If PgBouncer causes issues, rollback to direct connection:**

```bash
# Step 1: Update connection secret to direct PostgreSQL
# (via hub init-secrets with modified dbHost)

# Step 2: Restart Infisical
kubectl delete pod -n zero-ops-system -l app.kubernetes.io/name=infisical

# Step 3: Disable pooler in Git
# Edit manifests/platform-database/platform-db.yaml
# Set pooler.enabled: false
# Commit and push

# Step 4: Investigate root cause before re-enabling
```

**⚠️ Note:** Rollback returns to connection leak state. Only use for critical issues.

## Success Criteria

**Deployment is successful when:**
- ✅ Infisical API returns 200 OK on `/api/status`
- ✅ PostgreSQL connections ≤ 25 (not 16+ idle)
- ✅ Service endpoints populated (not empty)
- ✅ No KnexTimeoutError in logs
- ✅ Can scale to 2+ replicas without connection issues

**Long-term success indicators:**
- ✅ No manual intervention required for 30+ days
- ✅ Infisical scales horizontally without database connection issues
- ✅ Connection pool metrics remain stable under load
- ✅ Zero 504 errors related to database connections

## References

- Infisical Source: `archived/references/identity-auth/infisical/backend/src/db/instance.ts`
- Infisical Docs: `archived/references/identity-auth/infisical/docs/self-hosting/configuration/envars.mdx`
- CNPG Pooler Docs: https://cloudnative-pg.io/documentation/current/connection_pooling/
- PgBouncer Docs: https://www.pgbouncer.org/
- Knex Pool Config: https://knexjs.org/guide/#pool

---

**Document Status:** APPROVED FOR IMPLEMENTATION  
**Next Review:** After 30 days of production operation  
**Owner:** Platform Team
