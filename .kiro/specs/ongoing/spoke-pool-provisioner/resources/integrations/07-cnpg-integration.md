# CloudNativePG (CNPG) Integration Analysis

**Spec**: spoke-pool-provisioner  
**Component**: CloudNativePG (PostgreSQL Operator)  
**Purpose**: Provide shared HA PostgreSQL cluster for 100 tenant schemas per cell  
**Status**: CNPG operator exists, Spoke Pool shared cluster configuration not implemented

---

## 1. Position in Spoke Pool Provisioning Flow

```
Cell Provisioning (FR-1.x)
    ↓
Edge Catalog Deployment (FR-2.x)
    ↓
    Wave 0: Database extensions (if needed)
    Wave 1: CNPG Cluster + PgBouncer (FR-2.2) ← THIS INTEGRATION
    Wave 2: Atlas Operator + AtlasMigration CRs
    ↓
Tenant Schema Provisioning (FR-4.1)
    ↓
    Atlas Operator creates schema: tenant_<id>
    ↓
    PostgREST accesses via PgBouncer (Wave 3)
```

**Critical Constraint**: CNPG Cluster MUST be Ready before Wave 2 (Atlas Operator) can provision tenant schemas.

---

## 2. Architecture Overview

### 2.1 Shared CNPG Cluster Pattern

```
Spoke Pool Cluster
    ↓
    CNPG Operator (deployed via ArgoCD Wave 1)
    ↓
    Watches: Cluster CR (shared-cnpg)
    ↓
    Provisions: 3 PostgreSQL pods (primary + 2 replicas)
    ↓
    Provisions: PgBouncer Pooler (transaction mode)
    ↓
    Tenant Schemas (100 max per cell)
        ├── tenant_acme (schema)
        ├── tenant_xyz (schema)
        └── tenant_foo (schema)
```

**Key Principle**: One shared CNPG cluster per Spoke Pool cell. All tenants share the same PostgreSQL cluster but have isolated schemas.


### 2.2 CNPG Cluster CR (Spec-Specific Configuration)

**Purpose**: Define shared PostgreSQL cluster for all tenants in a Spoke Pool cell

**FR-2.2 Requirements**:
- HA configuration (3 replicas)
- PgBouncer pooler enabled with connection limit: `maxTenantCapacity * 5`
- pgvector extension enabled
- Cluster reaches Ready state within 5 minutes

**Cluster CR**:
```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: shared-cnpg
  namespace: spoke-pool-data
  labels:
    spoke-type: pool
    cell-id: spokepool-01
spec:
  instances: 3  # Primary + 2 replicas (HA)
  
  imageName: ghcr.io/cloudnative-pg/postgresql:17.2
  
  postgresql:
    parameters:
      max_connections: "500"  # NFR-2.3: 500 concurrent connections
      shared_buffers: "2GB"
      effective_cache_size: "6GB"
      maintenance_work_mem: "512MB"
      checkpoint_completion_target: "0.9"
      wal_buffers: "16MB"
      default_statistics_target: "100"
      random_page_cost: "1.1"
      effective_io_concurrency: "200"
      work_mem: "10485kB"
      min_wal_size: "1GB"
      max_wal_size: "4GB"
    
    shared_preload_libraries:
      - "pg_stat_statements"
      - "vector"  # pgvector extension (FR-2.2)
  
  bootstrap:
    initdb:
      database: shared_cnpg
      owner: postgres
      postInitSQL:
        - CREATE EXTENSION IF NOT EXISTS vector;
        - CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
  
  storage:
    size: 100Gi
    storageClass: hcloud-volumes
  
  resources:
    requests:
      memory: "4Gi"
      cpu: "2"
    limits:
      memory: "8Gi"
      cpu: "4"
  
  monitoring:
    enabled: true
    podMonitorEnabled: true
  
  backup:
    barmanObjectStore:
      destinationPath: s3://spoke-pool-backups/spokepool-01/
      s3Credentials:
        accessKeyId:
          name: hetzner-s3-credentials
          key: ACCESS_KEY_ID
        secretAccessKey:
          name: hetzner-s3-credentials
          key: SECRET_ACCESS_KEY
      wal:
        compression: gzip
        maxParallel: 2
    retentionPolicy: "30d"
```


### 2.3 PgBouncer Pooler CR (Transaction Mode)

**Purpose**: Connection pooling for high tenant density (NFR-2.3, NFR-2.4)

**Critical Requirement**: MUST use transaction pooling mode (not session pooling) to prevent session state leakage between tenants

**Pooler CR**:
```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Pooler
metadata:
  name: shared-cnpg-pooler
  namespace: spoke-pool-data
spec:
  cluster:
    name: shared-cnpg
  
  instances: 3  # HA pooler
  
  type: rw  # Read-write pooler
  
  pgbouncer:
    poolMode: transaction  # NFR-2.4: MUST be transaction mode
    parameters:
      max_client_conn: "500"  # NFR-2.3: 500 concurrent connections
      default_pool_size: "20"  # Connections per tenant schema
      reserve_pool_size: "5"   # Emergency connections
      reserve_pool_timeout: "3"
      max_db_connections: "100"  # Total connections to PostgreSQL
      pool_mode: "transaction"   # CRITICAL: Transaction pooling
      server_reset_query: "DISCARD ALL"  # Reset session state
      server_check_delay: "30"
      server_check_query: "SELECT 1"
      server_lifetime: "3600"
      server_idle_timeout: "600"
      query_timeout: "0"
      query_wait_timeout: "120"
      client_idle_timeout: "0"
      idle_transaction_timeout: "0"
      log_connections: "1"
      log_disconnections: "1"
      log_pooler_errors: "1"
  
  template:
    metadata:
      labels:
        app: pgbouncer
        cell-id: spokepool-01
    spec:
      containers:
      - name: pgbouncer
        resources:
          requests:
            memory: "256Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
```

**Key Configuration**:
- `pool_mode: transaction`: Resets session state after each transaction (prevents `search_path` leakage)
- `max_client_conn: 500`: Supports 100 tenants * 5 connections (FR-2.2)
- `default_pool_size: 20`: Connections per database/schema
- `max_db_connections: 100`: Total connections to PostgreSQL (connection multiplexing)


---

## 3. Component Responsibilities

### 3.1 CNPG Operator (Spoke Pool)

**Responsibilities**:
- Watch for Cluster CR create/update/delete events
- Provision PostgreSQL pods (primary + replicas)
- Manage streaming replication between instances
- Handle failover (elect new primary if primary fails)
- Provision PgBouncer Pooler pods
- Manage backups to Hetzner S3
- Update Cluster CR status (phase, conditions)

**Deployment** (via ArgoCD Wave 1):
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: cnpg-operator
  namespace: argocd
  annotations:
    argocd.argoproj.io/sync-wave: "1"
spec:
  project: default
  source:
    repoURL: https://cloudnative-pg.github.io/charts
    chart: cloudnative-pg
    targetRevision: 0.22.1
    helm:
      values: |
        monitoring:
          podMonitorEnabled: true
  destination:
    server: https://kubernetes.default.svc
    namespace: cnpg-system
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

### 3.2 CNPG Cluster Lifecycle

**Creation Flow** (FR-2.2):
```
1. ArgoCD syncs Cluster CR to Spoke Pool (Wave 1)
2. CNPG Operator detects Cluster CR
3. CNPG Operator provisions:
   - PersistentVolumeClaims (3x 100Gi)
   - StatefulSet (3 pods: primary + 2 replicas)
   - Services (rw, ro, r)
   - Secrets (postgres superuser, app user)
4. Primary pod starts, initializes database
5. Replica pods start, begin streaming replication
6. CNPG Operator updates Cluster.status.phase: Cluster setup completed → Ready
7. ArgoCD health check passes (Cluster Ready)
8. Wave 2 (Atlas Operator) can proceed
```

**Failover Flow**:
```
1. Primary pod fails (node failure, OOM, crash)
2. CNPG Operator detects primary unavailable
3. CNPG Operator elects new primary (replica with least lag)
4. CNPG Operator promotes replica to primary
5. CNPG Operator updates Service endpoints (rw → new primary)
6. CNPG Operator updates Cluster.status.currentPrimary
7. Remaining replica begins replicating from new primary
8. Failed pod is recreated and joins as replica
```


---

## 4. Tenant Schema Isolation

### 4.1 Schema-Level Isolation (Platform-Level)

**Mechanism**: Deterministic schema naming + PgBouncer transaction pooling

**Schema Creation** (via Atlas Operator, Wave 2):
```sql
-- Each tenant gets a dedicated schema
CREATE SCHEMA IF NOT EXISTS tenant_acme;
CREATE ROLE tenant_acme_role;
GRANT ALL ON SCHEMA tenant_acme TO tenant_acme_role;
GRANT USAGE ON SCHEMA tenant_acme TO tenant_acme_role;
GRANT CREATE ON SCHEMA tenant_acme TO tenant_acme_role;
```

**Isolation Guarantees**:
- Each tenant schema is isolated (no cross-schema access without explicit grants)
- PgBouncer transaction pooling resets `search_path` after each transaction
- PostgREST sets `search_path=tenant_<id>` per request (from `X-Tenant-ID` header)
- No shared search_path between tenants

### 4.2 Connection Flow

```
PostgREST (Wave 3)
    ↓ [Receives: JWT + X-Tenant-ID: acme]
    ↓ [Acquires connection from PgBouncer pool]
    ↓ [Executes: SET search_path=tenant_acme]
    ↓ [Executes: SET request.jwt.claims='<claims>']
    ↓ [Executes tenant query]
    ↓ [Commits transaction]
    ↓ [Returns connection to pool]
    ↓ [PgBouncer executes: DISCARD ALL]
    ↓ [search_path reset, no session state leakage]
PgBouncer Pooler (transaction mode)
    ↓ [Connection multiplexing: 500 client → 100 DB connections]
CNPG Cluster (shared-cnpg)
    ↓ [Primary handles writes, replicas handle reads]
```


---

## 5. High Availability and Disaster Recovery

### 5.1 HA Configuration (NFR-3.2)

**Replication**:
- Streaming replication (synchronous or asynchronous)
- Primary → Replica 1 (sync)
- Primary → Replica 2 (async)
- Replication lag monitored via `pg_stat_replication`

**Failover**:
- Automatic failover on primary failure
- Promotion time: < 30 seconds (P95)
- Zero data loss for synchronous replica
- Minimal data loss for asynchronous replica (< 1 second of WAL)

**Service Endpoints**:
```yaml
# Read-write service (points to primary)
shared-cnpg-rw.spoke-pool-data.svc:5432

# Read-only service (points to replicas)
shared-cnpg-ro.spoke-pool-data.svc:5432

# Any instance service (primary + replicas)
shared-cnpg-r.spoke-pool-data.svc:5432

# PgBouncer pooler service (recommended)
shared-cnpg-pooler-rw.spoke-pool-data.svc:5432
```

### 5.2 Backup and Recovery

**Backup Strategy**:
- Continuous WAL archiving to Hetzner S3
- Daily base backups (full backup)
- Retention: 30 days
- Compression: gzip

**Recovery**:
- Point-in-time recovery (PITR) supported
- Recovery from S3 backup + WAL replay
- Recovery time objective (RTO): < 15 minutes
- Recovery point objective (RPO): < 5 minutes

**ScheduledBackup CR**:
```yaml
apiVersion: postgresql.cnpg.io/v1
kind:ScheduledBackup
metadata:
  name: shared-cnpg-daily-backup
  namespace: spoke-pool-data
spec:
  schedule: "0 2 * * *"  # Daily at 2 AM
  backupOwnerReference: self
  cluster:
    name: shared-cnpg
  method: barmanObjectStore
  immediate: true
```


---

## 6. Observability and Monitoring

### 6.1 Metrics

**CNPG Cluster Metrics** (NFR-5.3):
```
cnpg_pg_stat_database_xact_commit{datname="shared_cnpg"}
cnpg_pg_stat_database_xact_rollback{datname="shared_cnpg"}
cnpg_pg_stat_database_numbackends{datname="shared_cnpg"}  # Connection count
cnpg_pg_stat_replication_replay_lag_seconds  # Replication lag
cnpg_pg_database_size_bytes{datname="shared_cnpg"}  # Disk usage
cnpg_pg_stat_bgwriter_buffers_alloc
cnpg_pg_stat_bgwriter_buffers_backend
cnpg_pg_locks_count
```

**PgBouncer Metrics**:
```
pgbouncer_pools_cl_active  # Active client connections
pgbouncer_pools_cl_waiting  # Waiting client connections
pgbouncer_pools_sv_active  # Active server connections
pgbouncer_pools_sv_idle  # Idle server connections
pgbouncer_pools_sv_used  # Used server connections
pgbouncer_pools_maxwait  # Max wait time
```

**Grafana Alloy Scrape Config** (Wave 1):
```yaml
prometheus.scrape "cnpg" {
  targets = [
    {"__address__" = "shared-cnpg-rw.spoke-pool-data.svc:9187"},
  ]
  forward_to = [prometheus.remote_write.hub.receiver]
  scrape_interval = "30s"
  metrics_path = "/metrics"
}

prometheus.scrape "pgbouncer" {
  targets = [
    {"__address__" = "shared-cnpg-pooler-rw.spoke-pool-data.svc:9127"},
  ]
  forward_to = [prometheus.remote_write.hub.receiver]
  scrape_interval = "30s"
  metrics_path = "/metrics"
}
```

### 6.2 Logging

**PostgreSQL Logs**:
```json
{
  "timestamp": "2026-04-08T10:30:00Z",
  "level": "info",
  "msg": "connection received",
  "user": "postgres",
  "database": "shared_cnpg",
  "application_name": "pgbouncer",
  "remote_host": "10.244.1.5"
}
```

**CNPG Operator Logs**:
```json
{
  "timestamp": "2026-04-08T10:30:00Z",
  "level": "info",
  "msg": "Cluster is ready",
  "cluster": "shared-cnpg",
  "namespace": "spoke-pool-data",
  "instances": 3,
  "currentPrimary": "shared-cnpg-1"
}
```


---

## 7. Spec Alignment

### 7.1 Functional Requirements

| Requirement | Implementation | Validation |
|-------------|----------------|------------|
| FR-2.2 | CNPG Cluster CR with 3 replicas | kubectl get cluster -n spoke-pool-data |
| FR-2.2 | PgBouncer pooler with maxTenantCapacity * 5 connections | kubectl get pooler -n spoke-pool-data |
| FR-2.2 | pgvector extension enabled | psql -c "SELECT * FROM pg_extension WHERE extname='vector'" |
| FR-2.2 | Cluster reaches Ready within 5 minutes | Time from CR apply to status.phase=Ready |
| FR-4.1 | Shared CNPG cluster supports 100 tenant schemas | Count schemas: SELECT count(*) FROM information_schema.schemata WHERE schema_name LIKE 'tenant_%' |

### 7.2 Non-Functional Requirements

| Requirement | Implementation | Target | Validation |
|-------------|----------------|--------|------------|
| NFR-2.1 | Schema-per-tenant isolation | 100 schemas | Count tenant schemas |
| NFR-2.3 | PgBouncer connection pooling | 500 concurrent | PgBouncer max_client_conn |
| NFR-2.4 | Transaction pooling mode | Required | PgBouncer pool_mode=transaction |
| NFR-2.5 | No direct PostgreSQL connections | Prohibited | NetworkPolicy enforcement |
| NFR-3.2 | CNPG cluster uptime | > 99.9% | Prometheus uptime metric |
| NFR-4.3 | Schema-level tenant isolation | Enforced | PostgreSQL schema permissions |

### 7.3 Acceptance Criteria

| Criteria | Implementation | Validation |
|----------|----------------|------------|
| AC-4 | Shared CNPG cluster reaches Ready within 5 minutes | kubectl wait --for=condition=Ready cluster/shared-cnpg --timeout=5m |
| AC-6 | CNPG Ready (wave 1) | kubectl --context spokepool-01 get cluster shared-cnpg -o jsonpath='{.status.phase}' |
| AC-6 | PgBouncer transaction pooling | kubectl exec -it shared-cnpg-pooler-0 -- pgbouncer -V |


---

## 8. Implementation Checklist

### 8.1 Spoke Pool Prerequisites

- [ ] CNPG Operator installed via ArgoCD (Wave 1)
- [ ] Hetzner CSI driver installed (for PersistentVolumes)
- [ ] Hetzner S3 credentials configured (for backups)
- [ ] spoke-pool-data namespace created
- [ ] Monitoring enabled (Prometheus PodMonitor)

### 8.2 CNPG Cluster Configuration

- [ ] Cluster CR created: `shared-cnpg`
- [ ] 3 instances configured (primary + 2 replicas)
- [ ] pgvector extension enabled
- [ ] PostgreSQL parameters tuned for 500 connections
- [ ] Storage: 100Gi per instance
- [ ] Resources: 4Gi memory, 2 CPU per instance
- [ ] Backup configured (Hetzner S3, 30-day retention)

### 8.3 PgBouncer Pooler Configuration

- [ ] Pooler CR created: `shared-cnpg-pooler`
- [ ] Transaction pooling mode configured
- [ ] max_client_conn: 500
- [ ] default_pool_size: 20
- [ ] max_db_connections: 100
- [ ] server_reset_query: DISCARD ALL
- [ ] 3 pooler instances (HA)

### 8.4 ArgoCD Integration

- [ ] CNPG Operator Application (Wave 1)
- [ ] Cluster CR Application (Wave 1)
- [ ] Pooler CR Application (Wave 1)
- [ ] Health checks: Cluster.status.phase=Ready
- [ ] Health checks: Pooler.status.phase=Ready
- [ ] Sync waves configured (Wave 1 before Wave 2)

### 8.5 Observability

- [ ] Prometheus PodMonitor enabled
- [ ] Grafana Alloy scrapes CNPG metrics
- [ ] Grafana Alloy scrapes PgBouncer metrics
- [ ] Metrics forwarded to Hub VictoriaMetrics
- [ ] Grafana dashboard: CNPG cluster health
- [ ] Grafana dashboard: PgBouncer connection pool usage
- [ ] Alerts: Connection pool exhaustion (80% capacity)
- [ ] Alerts: Replication lag > 10 seconds


---

## 9. Design Patterns (from sbt-patterns)

### 9.1 Shared Infrastructure Pattern

**Pattern**: Single shared database cluster for multiple tenants with schema-level isolation

**Application**: One CNPG cluster per Spoke Pool cell, 100 tenant schemas per cluster

**Reference**: `docs/crossplane-compositions.md` - Composition A (Shared Spoke Pool)

### 9.2 Connection Pooling Pattern

**Pattern**: PgBouncer transaction pooling for high tenant density

**Application**: 500 client connections → 100 database connections (5:1 multiplexing)

**Reference**: `docs/sbt-design-principles.md` - Section 8 (Multi-Tenant Security)

### 9.3 GitOps-First Approach

**Pattern**: All infrastructure changes via Git commits, ArgoCD reconciles

**Application**: CNPG Cluster CR stored in Git, ArgoCD syncs to Spoke Pool

**Reference**: `docs/sbt-design-principles.md` - Section 6 (GitOps-First Approach)

---

## 10. Troubleshooting Guide

### 10.1 Cluster Stuck in Provisioning

**Symptom**: Cluster.status.phase remains "Cluster setup in progress" for > 10 minutes

**Diagnosis**:
```bash
# Check CNPG Operator logs
kubectl logs -n cnpg-system deployment/cnpg-controller-manager

# Check Cluster status
kubectl get cluster -n spoke-pool-data shared-cnpg -o yaml

# Check pod status
kubectl get pods -n spoke-pool-data -l cnpg.io/cluster=shared-cnpg

# Check PVC status
kubectl get pvc -n spoke-pool-data
```

**Resolution**:
- Verify Hetzner CSI driver is running
- Verify PVCs are bound
- Verify pods are scheduled (check node resources)
- Check pod logs for initialization errors


### 10.2 Connection Pool Exhaustion

**Symptom**: PostgREST returns 503 Service Unavailable, PgBouncer logs show "no more connections allowed"

**Diagnosis**:
```bash
# Check PgBouncer stats
kubectl exec -n spoke-pool-data shared-cnpg-pooler-0 -- \
  psql -p 6432 -U postgres pgbouncer -c "SHOW POOLS"

# Check active connections
kubectl exec -n spoke-pool-data shared-cnpg-pooler-0 -- \
  psql -p 6432 -U postgres pgbouncer -c "SHOW CLIENTS"

# Check PostgreSQL connection count
kubectl exec -n spoke-pool-data shared-cnpg-1 -- \
  psql -U postgres -c "SELECT count(*) FROM pg_stat_activity"
```

**Resolution**:
- Increase PgBouncer `max_client_conn` (if < 500)
- Increase PgBouncer `default_pool_size` (if connection wait time high)
- Increase PostgreSQL `max_connections` (if < 500)
- Scale PgBouncer pooler replicas (if CPU/memory saturated)
- Investigate slow queries (blocking connections)

### 10.3 Replication Lag

**Symptom**: Replica lag > 10 seconds, read queries return stale data

**Diagnosis**:
```bash
# Check replication status
kubectl exec -n spoke-pool-data shared-cnpg-1 -- \
  psql -U postgres -c "SELECT * FROM pg_stat_replication"

# Check replica lag
kubectl exec -n spoke-pool-data shared-cnpg-1 -- \
  psql -U postgres -c "SELECT client_addr, state, sync_state, replay_lag FROM pg_stat_replication"

# Check WAL sender/receiver status
kubectl logs -n spoke-pool-data shared-cnpg-2 | grep "wal receiver"
```

**Resolution**:
- Verify network connectivity between primary and replicas
- Check replica disk I/O (slow disk can cause lag)
- Check replica CPU/memory (resource contention)
- Consider switching to synchronous replication (if zero data loss required)
- Increase `wal_sender_timeout` and `wal_receiver_timeout`

---

**Document Version**: 1.0  
**Last Updated**: 2026-04-08  
**Next Review**: After implementation completion
