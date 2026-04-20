# Design: Spoke Pool Provisioner (Cell-Based Scaling)

**Feature Name**: `spoke-pool-provisioner`  
**Phase**: Phase 1 - Foundation  
**Status**: Design  
**Created**: 2026-04-09

---

## 1. Overview

### 1.1 Purpose

Automate the provisioning of Spoke Pool clusters (cells) that host multiple Starter tier tenants with database-level isolation. Each cell is a complete Kubernetes cluster containing shared infrastructure (CNPG cluster, NATS leaf node, ArgoCD agent) that serves all tenants within that cell. Each tenant gets a dedicated logical database, connection pooler, and PostgREST instance provisioned via Crossplane AINativeSaaS XR.

### 1.2 Architecture Principles

- **GitOps-First**: All infrastructure changes via Git commits, ArgoCD reconciles
- **Declarative Provisioning**: AINativeSaaS XR expands into tenant database + pooler + PostgREST
- **Event-Driven**: CAPI cluster Ready → Kyverno generates ArgoCD Secret → ApplicationSet deploys edge catalog
- **Hub-Spoke Model**: Hub manages control plane, Spokes operate autonomously with eventual consistency
- **Database-Level Isolation**: 100 tenant databases per cell with deterministic naming (`tenant_<id>_db`)
- **Composite Migrations**: Atlas merges shared baseline + tenant-specific migrations declaratively

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
│ Spoke Catalog:   │  │              │  │              │
│ - ArgoCD Agent   │  │              │  │              │
│ - CNPG (shared)  │  │              │  │              │
│ - Atlas Operator │  │              │  │              │
│ - NATS Leaf Node │  │              │  │              │
│ - Grafana Alloy  │  │              │  │              │
│                  │  │              │  │              │
│ Per-Tenant:      │  │              │  │              │
│ - Database       │  │              │  │              │
│ - Pooler         │  │              │  │              │
│ - PostgREST      │  │              │  │              │
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
ArgoCD ApplicationSet deploys spoke catalog (sync waves 0-4)
         ↓
Cell Ready for tenant onboarding
```

---

## 3. Component Architecture


### 3.1 Crossplane SpokePool XRD

**Purpose**: Define declarative API for Spoke Pool cell provisioning

**Schema**:
```yaml
apiVersion: nutgraf.in/v1alpha1
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

### 3.3 ClusterResourceSet (Cluster BIOS)

**Purpose**: Inject CNI, CCM, and ArgoCD Agent at cluster bootstrap

**Resources** (FR-1.2, AC-3):
1. Cilium CNI (Secret) - Network ready
2. Hetzner CCM (Secret) - Cloud integration
3. Hetzner credentials (Secret, per-cluster)
4. ArgoCD namespace (ConfigMap)
5. ArgoCD Agent Deployment (ConfigMap)
6. ArgoCD Agent Config (ConfigMap, per-cluster)
7. ArgoCD Agent RBAC (ConfigMap)
8. ArgoCD Agent mTLS cert (Secret)
9. ArgoCD Agent CA (Secret)

**Template Source**: `manifests/platform-ops/cluster-bios/` (Git)
- Hub bootstrap CLI reads from this directory
- ArgoCD syncs to hub cluster for spoke pool provisioning
- Single source of truth for both hub and spoke clusters

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

**Purpose**: Deploy spoke catalog to all Spoke Pool clusters

**Generator**: Cluster Generator with selector `spoke-type: pool`

**Pattern**: App-of-Apps (umbrella Application creates children)

**Sync Waves**:
- Wave 0: Database extensions (if needed)
- Wave 1: CNPG Cluster + PgBouncer
- Wave 2: Atlas Operator + AtlasMigration CRs
- Wave 3: PostgREST
- Wave 4: NATS Leaf Node, Grafana Alloy

**Health Checks**: CNPG Ready before Wave 2, AtlasMigration Ready before Wave 3

---

## 4. Data Models

### 4.1 Shared CNPG Cluster

**Configuration**:
- 3 PostgreSQL replicas (HA)
- pgvector extension enabled
- 100Gi storage per instance
- Backup to Hetzner S3 (30-day retention)
- Hosts 100 logical databases (one per tenant)

**Per-Tenant Pooler Configuration** (provisioned via AINativeSaaS XR):
- Each tenant gets dedicated CNPG Pooler CR
- Pooler connects only to tenant's database using tenant-specific credentials
- Pooler credentials sourced from tenant-specific Secret: `<tenantId>-db-credentials`
- Secret contains: `username: tenant_<id>_user`, `password: <random-32-char>`, `database: tenant_<id>_db`, `host: shared-cnpg-rw`, `port: 5432`
- Transaction pooling mode (CRITICAL: not session pooling)
- 5 concurrent connections per tenant pooler
- Pooler deployed in tenant namespace
- Shared cluster-wide `app` user is NOT used for tenant workloads

### 4.2 Tenant Database Model

**Naming Convention**: `tenant_<id>_db` (deterministic, idempotent)

**Database Structure**:
```sql
CREATE DATABASE tenant_acme_db;
-- All tables in public schema
```

**Per-Tenant Resources** (provisioned via AINativeSaaS XR):
- CNPG Database CR: `tenant_<id>_db` (logical database in shared cluster)
- CNPG Pooler CR: Dedicated pooler connecting to tenant's database
- PostgREST Deployment: Dedicated instance connecting via tenant's pooler
- PostgREST Service: ClusterIP service in tenant namespace

**Per-Tenant Database User**:
- User naming: `tenant_<id>_user` (e.g., `tenant_acme_user`)
- Password generation: 32-character random alphanumeric (via Crossplane function or provider-sql)
- User creation SQL:
  ```sql
  CREATE USER tenant_acme_user WITH PASSWORD '<random-password>';
  GRANT CONNECT ON DATABASE tenant_acme_db TO tenant_acme_user;
  GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO tenant_acme_user;
  GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO tenant_acme_user;
  ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO tenant_acme_user;
  ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO tenant_acme_user;
  ```
- Revoke access to other databases (explicit REVOKE not needed - user has no default access)
- Credentials stored in Secret: `<tenantId>-db-credentials` in tenant namespace
- Secret fields: `username`, `password`, `database`, `host`, `port`

**Baseline Tables** (adapted from Supabase, in `public` schema):
- `public.users` (auth)
- `public.sessions` (auth)
- `public.identities` (OAuth/SSO)
- `public.buckets` (storage)
- `public.objects` (storage)
- Application-specific tables (e.g., `posts`)

**RLS Policies**: End-user isolation within tenant database using JWT `user_id` claim

### 4.3 Migration Files

**Shared Baseline Location**: `migrations/tenant-baseline/`

**Tenant-Specific Location**: `fleet-registry/tenants/tenant-<id>/migrations/`

**Naming**: `YYYYMMDDHHMMSS_description.sql` (Atlas format)

**Requirements** (NFR-6.1, NFR-6.2):
- All migrations idempotent (`IF NOT EXISTS`, `CREATE OR REPLACE`)
- All migrations forward-only (no destructive operations)
- Immutable once merged to main branch

**Tracking**: `atlas_schema_revisions` table per database

**Composite Sources**: Atlas merges tenant-baseline + tenant-specific migrations declaratively

---

## 5. Component Interactions

### 5.1 Cell Provisioning Sequence

```
1. Platform Admin: kubectl apply -f spokepool-01.yaml
2. Crossplane: Selects Composition, generates CAPI resources
3. cert-manager: Issues mTLS certificates (ArgoCD Agent, NATS Leaf Node)
4. CAPI: Provisions Hetzner VMs, injects ClusterResourceSet (CNI + CCM + ArgoCD Agent)
5. Cilium CNI: Applied, nodes become Ready
6. Hetzner CCM: Applied, cloud integration enabled
7. ArgoCD Agent: Starts, connects to Hub using mTLS
8. Kyverno: Generates ArgoCD cluster Secret
9. ArgoCD: Discovers cluster, ApplicationSet creates spoke catalog Application
10. ArgoCD Agent: Pulls spoke catalog, applies with sync waves
11. CNPG: Cluster reaches Ready (Wave 1)
12. Atlas Operator: Deployed (Wave 2)
13. PostgREST: Deployed (Wave 3)
14. NATS Leaf Node + Grafana Alloy: Deployed (Wave 4)
15. Cell: Ready for tenant onboarding
```

### 5.2 Tenant Database Provisioning Sequence

```
1. MCP API: Receives tenant_create(tenant_id="acme", tier="starter", cell_id="spokepool-01")
2. MCP API: Commits values to Git: fleet-registry/tenants/tenant-acme/values.yaml
3. ArgoCD ApplicationSet: Detects new tenant directory (Git Generator)
4. ApplicationSet: Creates Helm Application pointing to Universal Tenant Chart
5. Helm: Renders templates with tenant values:
   - AINativeSaaS XR (provisions Database, Pooler, PostgREST)
   - AtlasMigration CR (database migrations with composite sources)
   - Namespace (tenant-acme)
   - RBAC, ResourceQuota
6. ArgoCD: Deploys generated CRs to Spoke Pool cluster (destination: cell_id)
7. Crossplane: Reconciles AINativeSaaS XR:
   a. Creates CNPG Database CR: tenant_acme_db
   b. Generates random password (32 chars, alphanumeric)
   c. Creates Kubernetes Secret: acme-db-credentials in tenant-acme namespace
   d. Executes SQL via provider-sql: CREATE USER tenant_acme_user WITH PASSWORD '...'
   e. Executes SQL: GRANT CONNECT ON DATABASE tenant_acme_db TO tenant_acme_user
   f. Executes SQL: GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO tenant_acme_user
   g. Executes SQL: GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO tenant_acme_user
   h. Executes SQL: ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO tenant_acme_user
   i. Executes SQL: ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO tenant_acme_user
   j. Creates CNPG Pooler CR: connects to tenant_acme_db using Secret acme-db-credentials (transaction mode)
   k. Creates PostgREST Deployment: connects via tenant's pooler using Secret acme-db-credentials
   l. Creates PostgREST Service: ClusterIP in tenant-acme namespace
8. Atlas Operator: Reconciles AtlasMigration CR:
   a. Reads migrations from Git (tenant-baseline + tenant-specific via composite sources)
   b. Connects to tenant_acme_db via tenant's pooler using tenant_acme_user credentials
   c. Checks atlas_schema_revisions for tenant_acme_db
   d. Detects missing migrations
   e. Validates migrations in ephemeral dev database
   f. Applies migrations: CREATE TABLE IF NOT EXISTS public.users ...
   g. Applies baseline tables with RLS policies in public schema
   h. Updates atlas_schema_revisions
   i. Updates CR status: Ready=True
9. ArgoCD: Health check passes (Ready=True)
10. Tenant: Ready for requests via Hub AgentGateway → Tenant PostgREST → Tenant Pooler → Tenant Database
```

### 5.3 Authentication Flow

```
Developer/Agent (IDE Client)
    ↓ [JWT from Hub Ory Hydra]
Hub AgentGateway
    ↓ [Validates JWT using Hub Ory JWKS]
    ↓ [Extracts tenant_id from JWT claims]
    ↓ [Routes to appropriate Spoke Pool]
    ↓ [Forwards: JWT to tenant's PostgREST]
Tenant PostgREST (Spoke Pool, Dedicated Instance)
    ↓ [Connects to tenant's database via tenant's pooler]
    ↓ [Executes query in public schema]
Tenant Pooler (Spoke Pool, Dedicated)
    ↓ [Transaction pooling to tenant's database]
Tenant Database (Spoke Pool, Logical Database: tenant_<id>_db)
```

### 5.4 Drift Detection and Recovery

```
Every 30-60 seconds (Atlas Operator reconciliation loop):
1. Atlas Operator: Reads AtlasMigration CR for tenant_acme
2. Atlas Operator: Fetches migration directory from Git (tenant-baseline + tenant-specific)
3. Atlas Operator: Connects to tenant_acme_db via tenant's pooler
4. Atlas Operator: Queries atlas_schema_revisions for tenant_acme_db
5. Atlas Operator: Compares Git migrations vs applied migrations
6. If drift detected:
   a. Validates missing migrations in ephemeral dev database
   b. Applies missing migrations to production database
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

### 6.2 ArgoCD Principal Connectivity (LoadBalancer Pattern)

**Architecture Decision**: Use Kubernetes LoadBalancer service (not Ingress) for ArgoCD Principal exposure

**Rationale**:
- **Official Pattern**: Matches argocd-agent reference implementation
- **Direct mTLS**: No TLS termination at proxy layer, end-to-end encryption
- **No Ingress Complexity**: Avoids ssl-passthrough configuration and double mTLS verification
- **Multi-Environment Portability**: DNS-based addressing works across dev/staging/prod
- **Automatic Provisioning**: Hetzner CCM auto-creates Load Balancer from service annotations

**Implementation**:
```yaml
apiVersion: v1
kind: Service
metadata:
  name: argocd-agent-principal
  namespace: hub-platform-ops
  annotations:
    load-balancer.hetzner.cloud/location: fsn1
    load-balancer.hetzner.cloud/name: argocd-principal
    external-dns.alpha.kubernetes.io/hostname: argocd-principal.nutgraf.in
spec:
  type: LoadBalancer
  ports:
  - name: https
    port: 443
    targetPort: 8443
```

**DNS Configuration**:
- **Production**: `argocd-principal.nutgraf.in` → LoadBalancer IP
- **Staging**: `argocd-principal-stg.nutgraf.in` → Staging LB IP
- **Dev**: `argocd-principal-dev.nutgraf.in` → Dev LB IP
- **Automation**: external-dns creates A records from LoadBalancer service annotations

**Agent Configuration** (per-cluster):
```yaml
agent.server.address: "argocd-principal.nutgraf.in"
agent.server.port: "443"
agent.tls.root-ca-secret-name: "argocd-agent-ca"
agent.tls.secret-name: "argocd-agent-client-cert"
```

**Certificate SAN Requirements**:
- Principal certificate must include DNS name in Subject Alternative Names
- Example: `argocd-principal.nutgraf.in`
- IP addresses NOT required (DNS-based addressing)

**Connection Flow**:
1. Agent resolves DNS name to LoadBalancer IP
2. Agent initiates TLS handshake with client certificate
3. LoadBalancer forwards to Principal pod (no TLS termination)
4. Principal validates client certificate against CA
5. Principal extracts agent ID from certificate CN
6. mTLS connection established

**Benefits**:
- Environment-agnostic Agent configuration (same config across all environments)
- No hardcoded IPs in certificates or configurations
- Automatic failover if Principal pod restarts (LoadBalancer maintains connection)
- Standard HTTPS port (443) for firewall compatibility

### 6.3 Tenant Isolation

**Platform-Level Isolation** (Database-Level):
- Deterministic database naming: `tenant_<id>_db`
- Each tenant has dedicated logical database in shared CNPG cluster
- Each tenant has dedicated database user: `tenant_<id>_user` with unique password
- User credentials stored in Secret: `<tenantId>-db-credentials` in tenant namespace
- Each tenant user has GRANT permissions ONLY on their database
- PostgreSQL prevents cross-tenant access (separate databases + separate users)
- Each tenant has dedicated pooler connecting only to their database using tenant-specific credentials
- Each tenant has dedicated PostgREST instance connecting via their pooler using tenant-specific credentials
- Hub AgentGateway validates JWT, extracts `tenant_id`, routes to correct tenant's PostgREST
- PgBouncer transaction pooling resets session state between transactions
- No cross-tenant access possible (separate databases + separate users + separate poolers)
- Shared cluster-wide `app` user is NOT used for tenant workloads

### 6.4 End-User Isolation
- PostgreSQL RLS policies within tenant schema
- JWT `user_id` claim enforces access control
- Example: `user_id = current_setting('request.jwt.claims')::json->>'user_id'`

### 6.5 JWT Validation

**Primary Validation** (Hub AgentGateway):
- Fetches JWKS from Hub Ory on startup (cache for 1 hour)
- Validates JWT signature using cached public key (RS256)
- Validates issuer, audience, expiration
- Extracts `tenant_id` claim
- Routes request to appropriate Spoke Pool based on tenant-to-cell mapping
- Forwards to PostgREST with `X-Tenant-ID` header

**Secondary Caching** (PostgREST):
- In-memory JWT cache (10000 entries)
- Cache TTL: Token expiration time (max 1 hour)
- Cache eviction: LRU (Least Recently Used)

**Performance**:
- Cached token lookup: < 1ms (NFR-1.5)
- Uncached token validation: < 50ms (NFR-1.6)

---

## 6.6 Per-Tenant Database User Management

### User Creation Approach

**Option 1: Crossplane provider-sql** (RECOMMENDED):
- Composition includes `ProviderConfig` pointing to shared CNPG cluster
- Composition includes `User` resource with `name: tenant_<id>_user`, `passwordSecretRef: <tenantId>-db-credentials`
- Composition includes `Grant` resource with `privileges: [CONNECT]`, `database: tenant_<id>_db`, `role: tenant_<id>_user`
- Composition includes `Grant` resource with `privileges: [ALL]`, `schema: public`, `role: tenant_<id>_user`
- Composition includes `Grant` resource with `privileges: [ALL]`, `objectType: sequences`, `schema: public`, `role: tenant_<id>_user`
- Composition includes `Grant` resource for default privileges: `ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO tenant_<id>_user`

**Option 2: CNPG native user management**:
- CNPG Cluster spec includes `managed.roles` with per-tenant users
- Requires dynamic Cluster CR updates (not idiomatic for Crossplane)
- NOT RECOMMENDED (violates immutable infrastructure principle)

### Password Generation
- Crossplane function-patch-and-transform with `type: string`, `fmt: "random-32"`
- 32-character random alphanumeric string
- Stored in Kubernetes Secret: `<tenantId>-db-credentials`

### Secret Creation
- Crossplane Object resource wrapping Kubernetes Secret
- Secret contains: `username: tenant_<id>_user`, `password: <random>`, `database: tenant_<id>_db`, `host: shared-cnpg-rw`, `port: 5432`
- Secret created in tenant namespace
- Referenced by Pooler and PostgREST via `secretRef`

### Credential Rotation
- Phase 2: External Secrets Operator + Vault integration
- Rotation with overlap period (old + new credentials valid for 24 hours)
- Automated rotation every 90 days

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
cnpg_pg_stat_activity_count{user="tenant_<id>_user"}  # Per-tenant user connection tracking

# NATS
nats_leafnode_connected{cell_id="spokepool-01"}
nats_leafnode_in_msgs{cell_id="spokepool-01"}

# PostgREST (per-tenant instances)
postgrest_requests_total{cell_id="spokepool-01", tenant_id="acme", namespace="tenant-acme"}
postgrest_jwt_cache_hits_total{cell_id="spokepool-01", tenant_id="acme"}

# Atlas
atlas_drift_detected_total{cell_id="spokepool-01"}
atlas_migrations_applied_total{cell_id="spokepool-01"}
```

### 7.2 Logging

**Structured Logs** (JSON format):
- ArgoCD Agent: Connection status, Application sync events
- Atlas Operator: Migration apply, drift detection
- PostgREST: Query execution, JWT validation

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
4. Verify spoke catalog synced: `argocd app list | grep spokepool-01`
5. Verify CNPG Ready: `kubectl --context spokepool-01 get cluster shared-cnpg -o jsonpath='{.status.phase}'`
6. Verify Atlas Operator Ready: `kubectl --context spokepool-01 get deployment atlas-operator`
7. Verify PostgREST Ready: `kubectl --context spokepool-01 get deployment postgrest`

**Expected Result**: All components reach Healthy status within 15 minutes

### 8.2 Tenant Database Provisioning Test

**Test Case**: Create tenant database via GitOps flow

**Steps**:
1. Call MCP API: `tenant_create(tenant_id="acme", tier="starter", cell_id="spokepool-01")`
2. Verify MCP API commits values to Git: `fleet-registry/tenants/tenant-acme/values.yaml`
3. Verify ArgoCD detects new tenant directory and creates Helm Application
4. Verify AINativeSaaS XR deployed: `kubectl --context spokepool-01 get ainativesaas tenant-acme`
5. Verify CNPG Database CR created: `kubectl --context spokepool-01 get database tenant-acme-db`
6. Verify CNPG Pooler CR created: `kubectl --context spokepool-01 get pooler tenant-acme-pooler`
7. Verify PostgREST Deployment created: `kubectl --context spokepool-01 get deployment -n tenant-acme postgrest`
8. Verify database created: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U postgres -c "\l tenant_acme_db"`
9. Verify baseline tables: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U postgres -d tenant_acme_db -c "\dt public.*"`
10. Verify AtlasMigration CR status: `kubectl --context spokepool-01 get atlasmigration tenant-acme -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'`

**Expected Result**: Database provisioning completes in < 5 seconds

### 8.3 Authentication Flow Test

**Test Case**: Tenant can access their database via Hub AgentGateway → Tenant PostgREST

**Steps**:
1. Obtain JWT from Hub Ory: `curl -X POST https://auth.nutgraf.in/oauth2/token ...`
2. Make authenticated request: `curl -H "Authorization: Bearer $JWT" https://api.nutgraf.in/documents`
3. Verify Hub AgentGateway logs show JWT validation and routing to tenant PostgREST
4. Verify tenant PostgREST logs show connection to tenant database
5. Verify response contains only tenant's documents

**Expected Result**: Request succeeds, tenant isolation enforced

### 8.4 Drift Detection Test

**Test Case**: Atlas Operator detects and repairs manual database change

**Steps**:
1. Manually alter database: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U postgres -d tenant_acme_db -c "ALTER TABLE public.users ADD COLUMN test VARCHAR(20)"`
2. Wait 60 seconds (Atlas Operator reconciliation loop)
3. Verify Atlas Operator logs show drift detection
4. Create new migration file: `migrations/shared/20240101000007_add_test_column.sql`
5. Commit to Git, ArgoCD syncs
6. Verify Atlas Operator applies migration to tenant_acme_db
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

### 8.6 Per-Tenant User Isolation Test

**Test Case**: Verify per-tenant database user isolation and credential security

**Steps**:
1. Create two tenants: tenant-acme, tenant-xyz
2. Verify Secret exists: `kubectl get secret acme-db-credentials -n tenant-acme`
3. Extract credentials: `kubectl get secret acme-db-credentials -n tenant-acme -o jsonpath='{.data.username}' | base64 -d`
4. Verify username format: `tenant_acme_user`
5. Verify password length: `kubectl get secret acme-db-credentials -n tenant-acme -o jsonpath='{.data.password}' | base64 -d | wc -c` (should be 32)
6. Attempt cross-tenant access: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U tenant_acme_user -d tenant_xyz_db` (should fail with permission denied)
7. Verify tenant can access own database: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U tenant_acme_user -d tenant_acme_db -c "SELECT 1"` (should succeed)
8. Verify Pooler uses tenant credentials: `kubectl --context spokepool-01 logs -n tenant-acme pooler-<pod> | grep "tenant_acme_user"`
9. Verify PostgREST uses tenant credentials: `kubectl --context spokepool-01 logs -n tenant-acme postgrest-<pod> | grep "tenant_acme_user"`
10. Verify shared `app` user is NOT used: `kubectl --context spokepool-01 logs -n tenant-acme pooler-<pod> | grep "app"` (should return no results)

**Expected Result**: Cross-tenant access denied, own database access succeeds, tenant-specific credentials used throughout

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

### Phase 2: Spoke Catalog (Week 3-4)

**Deliverables**:
- ArgoCD ApplicationSet with Cluster Generator
- CNPG Cluster + PgBouncer configuration
- Atlas Operator deployment
- PostgREST deployment
- NATS Leaf Node deployment
- Grafana Alloy deployment

**Validation**: Verify all spoke catalog components reach Healthy status with correct sync waves

### Phase 3: Tenant Database Provisioning (Week 5-6)

**Deliverables**:
- AINativeSaaS XRD and Composition
- Universal Tenant Helm Chart (generates AINativeSaaS XR + AtlasMigration CR)
- Tenant baseline migration files (Supabase-adapted) in `migrations/tenant-baseline/`
- AtlasMigration CR template with composite sources
- ArgoCD ApplicationSet (Git Generator for tenants) with cellId destination
- Per-tenant database user creation via provider-sql
- Credential Secret generation and storage
- Pooler and PostgREST credential configuration

**Validation**: 
- Create tenant via MCP API, verify database + user + pooler + PostgREST provisioned in < 5 seconds
- Verify user isolation (cross-tenant access denied)
- Verify credentials stored in Secret and used by Pooler/PostgREST

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
- Crossplane provider-sql (v0.9+) for SQL user creation and GRANT management
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

### 11.6 Risk: Credential Rotation Failure

**Impact**: Tenant cannot access database after rotation

**Probability**: Low

**Mitigation**:
- Implement rotation with overlap period (old + new credentials valid for 24 hours)
- Phase 2 automated rotation with External Secrets Operator
- Monitor credential expiration via Prometheus alerts
- Rollback mechanism to restore previous credentials

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
