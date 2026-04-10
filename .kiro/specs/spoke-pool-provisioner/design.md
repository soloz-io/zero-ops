# Design: Spoke Pool Provisioner (Cell-Based Scaling)

**Feature Name**: `spoke-pool-provisioner`  
**Phase**: Phase 1 - Foundation  
**Status**: Design  
**Created**: 2026-04-09

---

## 1. Overview

### 1.1 Purpose

Automate the provisioning of Spoke Pool clusters (cells) that host multiple Starter tier tenants with schema-level isolation. Each cell is a complete Kubernetes cluster containing shared infrastructure (CNPG database, NATS leaf node, ArgoCD agent) that serves all tenants within that cell.

### 1.2 Architecture Principles

- **GitOps-First**: All infrastructure changes via Git commits, ArgoCD reconciles
- **Declarative Provisioning**: Single SpokePool XR expands into full cluster + edge catalog
- **Event-Driven**: CAPI cluster Ready → Kyverno generates ArgoCD Secret → ApplicationSet deploys edge catalog
- **Hub-Spoke Model**: Hub manages control plane, Spokes operate autonomously with eventual consistency
- **Schema-Level Isolation**: 100 tenant schemas per cell with deterministic naming (`tenant_<id>`)

---

## 2. High-Level Architecture

### 2.1 Hub-Spoke Topology

```
┌─────────────────────────────────────────────────────────────────┐
│ Hub Cluster (mothership)                                         │
│                                                                  │
│  ┌────────────────────────────────────────────────────────┐    │
│  │ Control Plane                                           │    │
│  │ - Crossplane (SpokePool XR orchestration)              │    │
│  │ - CAPI + CAPH (Hetzner VM provisioning)                │    │
│  │ - Kyverno (Cluster discovery)                          │    │
│  │ - ArgoCD Principal (ApplicationSet management)         │    │
│  │ - cert-manager (mTLS certificate generation)           │    │
│  │ - Hub Ory (Kratos + Hydra - centralized identity)      │    │
│  │ - VictoriaMetrics (metrics aggregation)                │    │
│  │ - NATS JetStream (event aggregation)                   │    │
│  └────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
                           │
         ┌─────────────────┼─────────────────┐
         │                 │                 │
┌────────▼─────────┐  ┌───▼──────────┐  ┌──▼───────────┐
│ Spoke Pool 01    │  │ Spoke Pool 02│  │ Spoke Pool N │
│ (100 tenants)    │  │ (100 tenants)│  │ (100 tenants)│
│                  │  │              │  │              │
│ Edge Catalog:    │  │              │  │              │
│ - ArgoCD Agent   │  │              │  │              │
│ - CNPG (shared)  │  │              │  │              │
│ - Atlas Operator │  │              │  │              │
│ - PostgREST      │  │              │  │              │
│ - AgentGateway   │  │              │  │              │
│ - NATS Leaf Node │  │              │  │              │
│ - Grafana Alloy  │  │              │  │              │
└──────────────────┘  └──────────────┘  └──────────────┘
```

### 2.2 Provisioning Flow

```
Platform Admin applies SpokePool XR
         ↓
Crossplane generates CAPI resources
         ↓
CAPI provisions Hetzner VMs (< 15 min)
         ↓
ClusterResourceSet injects ArgoCD Agent + mTLS certs
         ↓
Kyverno generates ArgoCD cluster Secret
         ↓
ArgoCD ApplicationSet deploys edge catalog (sync waves 0-4)
         ↓
Cell Ready for tenant onboarding
```

---

## 3. Component Architecture


### 3.1 Crossplane SpokePool XRD

**Purpose**: Define declarative API for Spoke Pool cell provisioning

**Schema**:
```yaml
apiVersion: zero-ops.io/v1alpha1
kind: SpokePool
metadata:
  name: spokepool-01
spec:
  region: fsn1                    # Hetzner datacenter
  nodePool:
    count: 3                      # Worker nodes
    instanceType: cx33            # Hetzner server type
  maxTenantCapacity: 100          # Max tenant schemas per cell
```

**Composition Generates**:
- CAPI Cluster CR (Ubuntu + kubeadm)
- HetznerCluster CR (load balancer, network)
- KubeadmControlPlane CR (1 control plane node)
- MachineDeployment CR (worker nodes)
- ClusterResourceSet CR (ArgoCD Agent bootstrap)
- Certificate CRs (mTLS for ArgoCD Agent, NATS Leaf Node)

### 3.2 CAPI Cluster Provisioning

**Provider**: Cluster API Provider Hetzner (CAPH)  
**OS**: Ubuntu 24.04 (not Talos)  
**Kubernetes**: v1.31.6  
**Network**: Cilium CNI, Hetzner CCM

**Provisioning Time**: < 15 minutes (NFR-1.1)

### 3.3 ClusterResourceSet (Secret Zero)

**Purpose**: Inject ArgoCD Agent at cluster bootstrap

**5 Resources** (FR-1.2, AC-3):
1. ArgoCD Agent Deployment (ConfigMap)
2. Agent ConfigMap (Hub URL, cluster name, mode=managed)
3. mTLS Client Certificate (Secret)
4. CA Certificate (Secret)
5. RBAC (ServiceAccount, ClusterRole, ClusterRoleBinding)

**Injection Timing**: CAPI injects when Cluster.status.phase=Provisioned

### 3.4 Kyverno Cluster Discovery

**Purpose**: Auto-generate ArgoCD cluster Secret when CAPI cluster becomes Ready

**Policy Type**: Generate (creates new resource)

**Trigger Conditions**:
- Resource: `Cluster` (cluster.x-k8s.io/v1beta1)
- Label: `spoke-type: pool`
- Status: `phase=Provisioned`

**Generated Secret**:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: spokepool-01-argocd-cluster
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: cluster
    spoke-type: pool
    cell-id: spokepool-01
type: Opaque
stringData:
  name: spokepool-01
  server: https://10.0.0.1:6443
  config: <kubeconfig-from-capi-secret>
```

**Discovery Time**: < 30 seconds (NFR-1.4)

### 3.5 ArgoCD ApplicationSet

**Purpose**: Deploy edge catalog to all Spoke Pool clusters

**Generator**: Cluster Generator with selector `spoke-type: pool`

**Pattern**: App-of-Apps (umbrella Application creates children)

**Sync Waves**:
- Wave 0: Database extensions (if needed)
- Wave 1: CNPG Cluster + PgBouncer
- Wave 2: Atlas Operator + AtlasMigration CRs
- Wave 3: PostgREST + AgentGateway
- Wave 4: NATS Leaf Node, Grafana Alloy

**Health Checks**: CNPG Ready before Wave 2, AtlasMigration Ready before Wave 3

---

## 4. Data Models

### 4.1 Shared CNPG Cluster

**Configuration**:
- 3 PostgreSQL replicas (HA)
- pgvector extension enabled
- PgBouncer transaction pooling (500 max connections)
- 100Gi storage per instance
- Backup to Hetzner S3 (30-day retention)

**Connection Pooling** (NFR-2.4):
```ini
pool_mode = transaction  # CRITICAL: Must be transaction mode
max_client_conn = 500    # 100 tenants * 5 connections
default_pool_size = 20
max_db_connections = 100
```

### 4.2 Tenant Schema Model

**Naming Convention**: `tenant_<id>` (deterministic, idempotent)

**Schema Structure**:
```sql
CREATE SCHEMA IF NOT EXISTS tenant_acme;
CREATE ROLE tenant_acme_role;
GRANT ALL ON SCHEMA tenant_acme TO tenant_acme_role;
```

**Baseline Tables** (adapted from Supabase):
- `tenant_<id>.users` (auth)
- `tenant_<id>.sessions` (auth)
- `tenant_<id>.identities` (OAuth/SSO)
- `tenant_<id>.buckets` (storage)
- `tenant_<id>.objects` (storage)
- Application-specific tables (e.g., `posts`)

**RLS Policies**: End-user isolation within tenant schema using JWT `user_id` claim

### 4.3 Migration Files

**Location**: `migrations/tenant-baseline/`

**Naming**: `YYYYMMDDHHMMSS_description.sql` (Atlas format)

**Requirements** (NFR-6.1, NFR-6.2):
- All migrations idempotent (`IF NOT EXISTS`, `CREATE OR REPLACE`)
- All migrations forward-only (no destructive operations)
- Immutable once merged to main branch

**Tracking**: `atlas_schema_revisions` table per schema

---

## 5. Component Interactions

### 5.1 Cell Provisioning Sequence

```
1. Platform Admin: kubectl apply -f spokepool-01.yaml
2. Crossplane: Selects Composition, generates CAPI resources
3. cert-manager: Issues mTLS certificates (ArgoCD Agent, NATS Leaf Node)
4. CAPI: Provisions Hetzner VMs, injects ClusterResourceSet
5. ArgoCD Agent: Starts, connects to Hub using mTLS
6. Kyverno: Generates ArgoCD cluster Secret
7. ArgoCD: Discovers cluster, ApplicationSet creates edge catalog Application
8. ArgoCD Agent: Pulls edge catalog, applies with sync waves
9. CNPG: Cluster reaches Ready (Wave 1)
10. Atlas Operator: Deployed (Wave 2)
11. PostgREST + AgentGateway: Deployed (Wave 3)
12. NATS Leaf Node + Grafana Alloy: Deployed (Wave 4)
13. Cell: Ready for tenant onboarding
```

### 5.2 Tenant Schema Provisioning Sequence

```
1. MCP API: Receives tenant_create(tenant_id="acme", tier="starter")
2. MCP API: Commits values to Git: fleet-registry/tenants/tenant-acme/values.yaml
3. ArgoCD ApplicationSet: Detects new tenant directory (Git Generator)
4. ApplicationSet: Creates Helm Application pointing to Universal Tenant Chart
5. Helm: Renders templates with tenant values:
   - AINativeSaaS XR (namespace, RBAC, ResourceQuota)
   - AtlasMigration CR (schema provisioning)
6. ArgoCD: Deploys generated CRs to Spoke Pool cluster (sync wave 2)
7. Atlas Operator: Reconciles AtlasMigration CR:
   a. Reads migrations from ConfigMap
   b. Connects to CNPG via PgBouncer
   c. Checks atlas_schema_revisions for tenant_acme schema
   d. Detects missing migrations
   e. Validates migrations in ephemeral dev database
   f. Applies migrations: CREATE SCHEMA IF NOT EXISTS tenant_acme
   g. Creates schema owner role: tenant_acme_role
   h. Applies baseline tables with RLS policies
   i. Updates atlas_schema_revisions
   j. Updates CR status: Ready=True
8. ArgoCD: Health check passes (Ready=True)
9. ArgoCD: Proceeds to sync wave 3 (PostgREST deployment)
10. PostgREST: Deployed with db-schemas including tenant_acme
11. Tenant: Ready for requests via AgentGateway → PostgREST
```

### 5.3 Authentication Flow

```
Developer/Agent (IDE Client)
    ↓ [JWT from Hub Ory Hydra]
AgentGateway (Spoke Pool)
    ↓ [Validates JWT using Hub Ory JWKS]
    ↓ [Extracts tenant_id from JWT claims]
    ↓ [Forwards: JWT + X-Tenant-ID header]
PostgREST (Spoke Pool, Internal Service)
    ↓ [Caches validated JWT (10000 entries)]
    ↓ [Sets search_path=tenant_<id>]
    ↓ [Executes query via PgBouncer]
CNPG Cluster (Spoke Pool, Shared Database)
```

### 5.4 Drift Detection and Recovery

```
Every 30-60 seconds (Atlas Operator reconciliation loop):
1. Atlas Operator: Reads AtlasMigration CR for tenant_acme
2. Atlas Operator: Fetches migration directory from ConfigMap
3. Atlas Operator: Connects to CNPG via PgBouncer
4. Atlas Operator: Queries atlas_schema_revisions for tenant_acme schema
5. Atlas Operator: Compares Git migrations vs applied migrations
6. If drift detected:
   a. Validates missing migrations in ephemeral dev database
   b. Applies missing migrations to production
   c. Updates atlas_schema_revisions
   d. Updates CR status: Ready=True
   e. Emits metric: atlas_drift_detected_total++
7. If no drift: CR status remains Ready=True
```

---

## 6. Security Architecture

### 6.1 mTLS Certificate Management

**Certificate Authority**: Self-signed CAs for ArgoCD and NATS (separate CAs for defense in depth)

**Certificate Lifecycle**:
1. cert-manager generates certificates in Hub cluster
2. Certificates valid for 90 days, auto-renew at 83 days (NFR-4.2)
3. Crossplane patches certificates into ClusterResourceSet
4. CAPI injects certificates into Spoke Pool at bootstrap
5. ArgoCD Agent and NATS Leaf Node mount certificates from Secrets

**Certificate Rotation** (Phase 1): Manual rotation (quarterly), Phase 2: Automated via External Secrets Operator

### 6.2 Tenant Isolation

**Platform-Level Isolation** (Schema-Level):
- Deterministic schema naming: `tenant_<id>`
- AgentGateway validates JWT, extracts `tenant_id`
- PostgREST sets `search_path=tenant_<id>` per request
- PgBouncer transaction pooling resets session state between transactions
- No cross-tenant access possible (no shared search_path)

**End-User Isolation** (Row-Level Security):
- PostgreSQL RLS policies within tenant schema
- JWT `user_id` claim enforces access control
- Example: `user_id = current_setting('request.jwt.claims')::json->>'user_id'`

### 6.3 JWT Validation

**Primary Validation** (AgentGateway):
- Fetches JWKS from Hub Ory on startup (cache for 1 hour)
- Validates JWT signature using cached public key (RS256)
- Validates issuer, audience, expiration
- Extracts `tenant_id` claim
- Forwards to PostgREST with `X-Tenant-ID` header

**Secondary Caching** (PostgREST):
- In-memory JWT cache (10000 entries)
- Cache TTL: Token expiration time (max 1 hour)
- Cache eviction: LRU (Least Recently Used)

**Performance**:
- Cached token lookup: < 1ms (NFR-1.5)
- Uncached token validation: < 50ms (NFR-1.6)

---

## 7. Observability

### 7.1 Metrics Collection

**Grafana Alloy** (deployed in each Spoke Pool):
- Scrapes KSM (Kubernetes State Metrics)
- Scrapes CNPG metrics (connection count, replication lag, disk usage)
- Scrapes NATS metrics (leaf node connection status, message counts)
- Scrapes PostgREST metrics (request latency, cache hits)
- Injects `cell_id` label into all metrics
- Remote writes to Hub VictoriaMetrics

**Key Metrics**:
```
# CNPG
cnpg_pg_stat_database_numbackends{cell_id="spokepool-01"}
cnpg_pg_replication_lag_seconds{cell_id="spokepool-01"}
cnpg_pg_database_size_bytes{cell_id="spokepool-01"}

# NATS
nats_leafnode_connected{cell_id="spokepool-01"}
nats_leafnode_in_msgs{cell_id="spokepool-01"}

# PostgREST
postgrest_requests_total{cell_id="spokepool-01", schema="tenant_acme"}
postgrest_jwt_cache_hits_total{cell_id="spokepool-01"}

# Atlas
atlas_drift_detected_total{cell_id="spokepool-01"}
atlas_migrations_applied_total{cell_id="spokepool-01"}
```

### 7.2 Logging

**Structured Logs** (JSON format):
- ArgoCD Agent: Connection status, Application sync events
- Atlas Operator: Migration apply, drift detection
- PostgREST: Query execution, JWT validation
- AgentGateway: Request routing, JWT validation

**Log Aggregation**: Grafana Alloy → Loki (Hub)

### 7.3 Alerts

**Critical Alerts**:
- CNPG connection pool exhaustion (> 80% capacity)
- NATS Leaf Node disconnected (> 5 minutes)
- Atlas Operator drift detected (manual schema change)
- PostgREST JWT validation failures (> 1% error rate)
- Certificate expiring soon (< 14 days)

---

## 8. Manual Testing Strategy

**User Requirement**: NO unit tests, NO property-based tests, NO integration tests. ONLY manual testing and validation.

### 8.1 Cell Provisioning Test

**Test Case**: Provision first Spoke Pool cell

**Steps**:
1. Apply SpokePool XR: `kubectl apply -f spokepool-01.yaml`
2. Wait for CAPI cluster Ready: `kubectl wait --for=condition=Ready cluster/spokepool-01 --timeout=20m`
3. Verify ArgoCD cluster Secret: `kubectl get secret -n argocd -l cell-id=spokepool-01`
4. Verify edge catalog synced: `argocd app list | grep spokepool-01`
5. Verify CNPG Ready: `kubectl --context spokepool-01 get cluster shared-cnpg -o jsonpath='{.status.phase}'`
6. Verify Atlas Operator Ready: `kubectl --context spokepool-01 get deployment atlas-operator`
7. Verify PostgREST Ready: `kubectl --context spokepool-01 get deployment postgrest`

**Expected Result**: All components reach Healthy status within 15 minutes

### 8.2 Tenant Schema Provisioning Test

**Test Case**: Create tenant schema via GitOps flow

**Steps**:
1. Call MCP API: `tenant_create(tenant_id="acme", tier="starter")`
2. Verify MCP API commits values to Git: `fleet-registry/tenants/tenant-acme/values.yaml`
3. Verify ArgoCD detects new tenant directory and creates Helm Application
4. Verify AtlasMigration CR deployed: `kubectl --context spokepool-01 get atlasmigration tenant-acme`
5. Verify schema created: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U postgres -c "\dn tenant_acme"`
6. Verify schema owner role: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U postgres -c "\du tenant_acme_role"`
7. Verify baseline tables: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U postgres -c "\dt tenant_acme.*"`
8. Verify AtlasMigration CR status: `kubectl --context spokepool-01 get atlasmigration tenant-acme -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'`

**Expected Result**: Schema provisioning completes in < 5 seconds

### 8.3 Authentication Flow Test

**Test Case**: Tenant can access their schema via AgentGateway → PostgREST

**Steps**:
1. Obtain JWT from Hub Ory: `curl -X POST https://auth.nutgraf.in/oauth2/token ...`
2. Make authenticated request: `curl -H "Authorization: Bearer $JWT" https://api.nutgraf.in/documents`
3. Verify AgentGateway logs show JWT validation
4. Verify PostgREST logs show `search_path=tenant_acme`
5. Verify response contains only tenant's documents

**Expected Result**: Request succeeds, tenant isolation enforced

### 8.4 Drift Detection Test

**Test Case**: Atlas Operator detects and repairs manual schema change

**Steps**:
1. Manually alter schema: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U postgres -c "ALTER TABLE tenant_acme.users ADD COLUMN test VARCHAR(20)"`
2. Wait 60 seconds (Atlas Operator reconciliation loop)
3. Verify Atlas Operator logs show drift detection
4. Create new migration file: `20240101000007_add_test_column.sql`
5. Commit to Git, ArgoCD syncs ConfigMap
6. Verify Atlas Operator applies migration
7. Verify AtlasMigration CR status: `Ready=True`

**Expected Result**: Drift detected and repaired within 5 minutes

### 8.5 NATS Buffering Test

**Test Case**: NATS Leaf Node buffers events during Hub unavailability

**Steps**:
1. Publish test event: `kubectl --context spokepool-01 exec -n spoke-pool-system nats-0 -- nats pub spoke.cell-01.billing.usage '{"test": "event"}'`
2. Verify event received in Hub: `kubectl --context hub exec -n hub-platform-messaging nats-0 -- nats stream info spoke.cell-01.billing.usage`
3. Scale Hub NATS to 0: `kubectl --context hub scale statefulset nats -n hub-platform-messaging --replicas=0`
4. Publish 10 events during outage
5. Verify Spoke JetStream buffer: `kubectl --context spokepool-01 exec -n spoke-pool-system nats-0 -- nats stream ls`
6. Restore Hub NATS: `kubectl --context hub scale statefulset nats -n hub-platform-messaging --replicas=3`
7. Wait 30 seconds for reconnection
8. Verify all events delivered to Hub

**Expected Result**: No data loss, events delivered after reconnection

---

## 9. Implementation Phases

### Phase 1: Core Infrastructure (Week 1-2)

**Deliverables**:
- SpokePool XRD and Composition
- CAPI + CAPH integration
- Kyverno cluster discovery policy
- cert-manager certificate generation
- ClusterResourceSet with ArgoCD Agent

**Validation**: Apply SpokePool XR, verify cluster provisioned and ArgoCD Agent connects

### Phase 2: Edge Catalog (Week 3-4)

**Deliverables**:
- ArgoCD ApplicationSet with Cluster Generator
- CNPG Cluster + PgBouncer configuration
- Atlas Operator deployment
- PostgREST + AgentGateway deployment
- NATS Leaf Node deployment
- Grafana Alloy deployment

**Validation**: Verify all edge catalog components reach Healthy status with correct sync waves

### Phase 3: Tenant Schema Provisioning (Week 5-6)

**Deliverables**:
- Universal Tenant Helm Chart
- Baseline migration files (Supabase-adapted)
- AtlasMigration CR template
- ArgoCD ApplicationSet (Git Generator for tenants)
- PostgREST schema discovery

**Validation**: Create tenant via MCP API, verify schema provisioned in < 5 seconds

### Phase 4: Observability and Monitoring (Week 7)

**Deliverables**:
- Grafana dashboards (cell health, tenant count, capacity utilization)
- Prometheus alerts (connection pool exhaustion, drift detection, certificate expiration)
- Logging configuration (structured JSON logs)

**Validation**: Verify metrics forwarded to Hub, alerts trigger correctly

### Phase 5: End-to-End Testing (Week 8)

**Deliverables**:
- Manual test scripts for all acceptance criteria
- Documentation (runbooks, troubleshooting guides)
- Capacity planning analysis (100 tenants per cell)

**Validation**: Execute all manual tests, verify acceptance criteria met

---

## 10. Dependencies

### 10.1 External Dependencies

- CAPI + CAPH (v1.6+)
- Crossplane (v1.14+)
- ArgoCD (v2.10+)
- Kyverno (v1.11+)
- cert-manager (v1.13+)
- CloudNativePG (v1.22+)
- NATS (v2.10+)
- Grafana Alloy (v1.0+)
- Atlas Kubernetes Operator (v0.3+)
- PostgREST (v12.0+)
- Ory Kratos (v1.0+)
- Ory Hydra (v2.2+)

### 10.2 Internal Dependencies

- Hub cluster must be provisioned and operational
- Hub Ory Kratos/Hydra must be deployed and configured
- Hub ArgoCD Principal must be deployed
- Hub VictoriaMetrics must be deployed
- Hub NATS JetStream must be deployed
- Hetzner Cloud API token configured in Crossplane ProviderConfig

---

## 11. Risks and Mitigations

### 11.1 Risk: CAPI Cluster Provisioning Failure

**Impact**: Cell provisioning fails, tenants cannot be onboarded

**Mitigation**:
- Crossplane continuous reconciliation (retries with exponential backoff)
- Idempotent operations (re-applying SpokePool XR has no side effects)
- Manual intervention: Platform Admin can delete and re-apply SpokePool XR

### 11.2 Risk: Certificate Expiration

**Impact**: ArgoCD Agent or NATS Leaf Node cannot connect to Hub

**Mitigation**:
- Certificates auto-renew 7 days before expiration (cert-manager)
- Prometheus alerts for certificate expiration (< 14 days)
- Phase 2: Automated certificate rotation via External Secrets Operator

### 11.3 Risk: Connection Pool Exhaustion

**Impact**: PostgREST returns 503 Service Unavailable

**Mitigation**:
- PgBouncer transaction pooling (500 max connections)
- Prometheus alerts for connection pool usage (> 80%)
- Scale PgBouncer replicas or increase pool size

### 11.4 Risk: Schema Drift

**Impact**: Manual schema changes break tenant applications

**Mitigation**:
- Atlas Operator continuous drift detection (30-60s loop)
- Automatic drift recovery (apply missing migrations)
- Prometheus alerts for drift detection
- Immutable migration files (cannot modify once merged)

### 11.5 Risk: Hub Unavailability

**Impact**: Spoke Pool clusters cannot sync status or forward events

**Mitigation**:
- Spoke Pool clusters operate autonomously (local ArgoCD Agent, local CNPG)
- NATS Leaf Node buffers events locally (JetStream persistence)
- Automatic reconnection with exponential backoff
- Eventual consistency guarantees (no data loss)

---

## 12. Future Enhancements (Phase 2+)

### 12.1 Automated Cell Provisioning

**Description**: Automatically provision new cells when capacity reaches 80%

**Implementation**: Hub Event Router monitors tenant count per cell, triggers SpokePool XR creation

### 12.2 Cell Rebalancing

**Description**: Move tenants between cells to balance load

**Implementation**: Tenant migration workflow (backup → restore → DNS update)

### 12.3 Cell Decommissioning

**Description**: Drain and delete cells when tenant count drops below threshold

**Implementation**: Tenant migration + CAPI cluster deletion

### 12.4 Multi-Region Support

**Description**: Provision cells in multiple Hetzner datacenters

**Implementation**: SpokePool XR with `region` parameter (fsn1, nbg1, hel1)

### 12.5 Automated Certificate Rotation

**Description**: Automatically rotate certificates without manual intervention

**Implementation**: External Secrets Operator syncs certificates from Hub to Spokes

---

**Document Version**: 1.0  
**Last Updated**: 2026-04-09  
**Status**: Ready for Implementation
