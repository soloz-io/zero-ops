# Requirements: Spoke Pool Provisioner (Cell-Based Scaling)

**Feature Name**: `spoke-pool-provisioner`  
**Phase**: Phase 1 - Foundation  
**Status**: Draft  
**Created**: 2026-04-07

---

## 1. Executive Summary

### 1.1 Purpose
Automate the provisioning of Spoke Pool clusters (cells) that host multiple Starter tier tenants with schema-level isolation. Each cell is a complete Kubernetes cluster containing shared infrastructure (CNPG database, NATS leaf node, ArgoCD agent) that serves all tenants within that cell.

**Note**: This specification covers Starter tier (Composition A) using schema-per-tenant in shared CNPG clusters. Enterprise tier (Composition B) uses dedicated clusters with separate databases per tenant and is documented in the main PRD v9.

### 1.2 Business Value
- **Horizontal Scalability**: Support unlimited Starter tier tenants by provisioning additional cells on demand
- **Blast Radius Containment**: Isolate tenant groups into separate clusters to limit impact of failures
- **Cost Efficiency**: Share infrastructure (database, networking, observability) across multiple tenants per cell
- **Operational Simplicity**: Declarative cell provisioning via single Crossplane XR eliminates manual cluster setup

### 1.3 Success Criteria
- Platform Admin can provision a new Spoke Pool cell by applying a single YAML manifest
- Cell provisioning completes within 15 minutes (CAPI cluster + edge catalog deployment)
- Cells automatically register with Hub ArgoCD for tenant workload deployment
- Shared CNPG cluster supports 100 tenant schemas per cell with proper isolation
- Tenant schema provisioning completes within 5 seconds (schema creation + baseline migrations)
- AgentGateway validates JWTs from Hub Ory and forwards authenticated requests to PostgREST

---

## 2. Stakeholders

| Role | Responsibility | Success Metric |
|------|---------------|----------------|
| Platform Admin | Provision and monitor Spoke Pool cells | Cell provisioning time < 15 min |
| SRE Team | Ensure cell health | Zero manual cluster configuration |
| Development Team | Implement Crossplane compositions and ArgoCD ApplicationSets | All acceptance tests pass |

---

## 3. Functional Requirements

### 3.1 Cell Provisioning

**FR-1.1: Declarative Cell Creation**
- **Description**: Platform Admin can create a new Spoke Pool cell by applying a `SpokePool` Custom Resource
- **Acceptance Criteria**:
  - SpokePool XRD defines schema: `region`, `nodePool.count`, `nodePool.instanceType`, `maxTenantCapacity`
  - Applying SpokePool XR triggers Crossplane to provision CAPI Cluster + HetznerCluster + MachineDeployment
  - ClusterResourceSet is automatically created and bound to the new cluster
  - Cell-id is derived from `metadata.name` of the SpokePool XR

**FR-1.2: Automated Cluster Bootstrap**
- **Description**: CAPI provisions Hetzner VMs and injects ArgoCD Agent via ClusterResourceSet
- **Acceptance Criteria**:
  - ClusterResourceSet contains exactly 5 resources: Agent Deployment, ConfigMap, mTLS cert Secret, CA Secret, RBAC
  - ArgoCD Agent starts within 2 minutes of cluster Ready state
  - Agent successfully connects to Hub ArgoCD using pre-generated mTLS certificate

**FR-1.3: Cluster Registration**
- **Description**: Kyverno policy auto-generates ArgoCD cluster Secret when CAPI cluster becomes Ready
- **Acceptance Criteria**:
  - Kyverno watches `Cluster.status.phase=Provisioned`
  - Extracts kubeconfig from CAPI-generated Secret
  - Creates ArgoCD cluster Secret in `argocd` namespace with labels: `argocd.argoproj.io/secret-type: cluster`, `spoke-type: pool`, `cell-id: <cluster-name>`
  - ArgoCD discovers cluster within 30 seconds

### 3.2 Edge Catalog Deployment

**FR-2.1: Automatic Edge Catalog Provisioning**
- **Description**: ArgoCD ApplicationSet deploys edge catalog components to all Spoke Pool clusters with dependency ordering
- **Acceptance Criteria**:
  - ApplicationSet uses Cluster Generator with selector: `spoke-type: pool`
  - Deploys App-of-Apps umbrella Application to each discovered cluster
  - Individual Applications created for: Shared CNPG, PostgREST, NATS Leaf Node, Grafana Alloy, Atlas Operator
  - ArgoCD sync waves enforce dependency ordering:
    * Wave 0: Database extensions (if needed)
    * Wave 1: CNPG Cluster + PgBouncer
    * Wave 2: Atlas Operator + AtlasMigration CRs
    * Wave 3: PostgREST (requires schemas to exist)
    * Wave 4: Tenant workloads
  - Health checks gate progression: CNPG `status.phase=Ready` before Wave 2
  - All edge catalog components reach Healthy status within 10 minutes

**FR-2.2: Shared CNPG Cluster**
- **Description**: Each cell contains one shared PostgreSQL cluster for all tenant logical databases
- **Acceptance Criteria**:
  - CNPG Cluster CR deployed with HA configuration (3 replicas)
  - PgBouncer pooler enabled with connection limit: `maxTenantCapacity * 5`
  - pgvector extension enabled
  - Cluster reaches Ready state within 5 minutes

**FR-2.3: NATS Leaf Node**
- **Description**: NATS Leaf Node connects to Hub JetStream for billing event forwarding
- **Acceptance Criteria**:
  - Leaf Node connects to Hub NATS using mTLS authentication
  - Tenant workloads can publish to local NATS: `nats://nats.spoke-pool.svc:4222`
  - Events are forwarded to Hub with subject: `spoke.{cell-id}.billing.usage`

**FR-2.4: Grafana Alloy**
- **Description**: Alloy scrapes metrics and forwards to Hub VictoriaMetrics
- **Acceptance Criteria**:
  - Scrapes KSM (Kubernetes State Metrics) and CNPG metrics
  - Remote writes to Hub VictoriaMetrics using static bearer token authentication
  - Injects `cell_id` label into all metrics

**FR-2.6: PostgREST API Gateway (Behind AgentGateway)**
- **Description**: PostgREST provides auto-generated REST API for tenant databases, exposed only through AgentGateway
- **Acceptance Criteria**:
  - PostgREST deployed as internal service in each Spoke Pool cluster (ArgoCD sync wave 3)
  - PostgREST is NOT directly exposed - all traffic flows through AgentGateway
  - AgentGateway validates JWTs using Hub Ory JWKS (RS256 signature verification)
  - AgentGateway extracts `tenant_id` from validated JWT claims
  - AgentGateway forwards authenticated requests to PostgREST with JWT + `X-Tenant-ID` header
  - PostgREST maintains in-memory JWT cache (10000 entries) for performance
  - PostgREST sets `search_path=tenant_<id>` based on `X-Tenant-ID` header
  - PostgREST configured with `db-schemas` listing all tenant schemas (dynamically updated)
  - Connection pooling via PgBouncer (transaction mode)
  - PostgREST reaches Ready state within 2 minutes

### 3.5 Control Plane Responsibilities

**FR-5.2: GitOps-Driven Schema Provisioning (Universal Tenant Helm Chart Pattern)**
- **Description**: MCP API commits tenant intent (values.yaml), Helm generates CRs, ArgoCD syncs, Atlas Operator applies migrations
- **Acceptance Criteria**:
  - MCP API receives `tenant_create` call with `tier=starter`
  - MCP API commits tenant values to `fleet-registry/tenants/tenant-<id>/values.yaml`
  - Values file contains tenant intent: `tenantId`, `tier`, `region`, `database.schemaName: tenant_<id>`, `database.migrations.gitRepo`
  - ArgoCD ApplicationSet (Git Generator) detects new tenant directory
  - ApplicationSet creates Helm-based Application pointing to Universal Tenant Chart
  - Helm chart generates CRs from values: `AINativeSaaS` XR, `AtlasMigration` CR, namespace, RBAC
  - Chart enforces platform policy (sync waves, resource limits, security contexts)
  - Values provide tenant-specific input (schema name, capacity, features)
  - Application deploys generated CRs to Spoke Pool cluster
  - Atlas Operator reconciles: reads migrations from Git → applies to CNPG
  - Schema name is deterministic: `tenant_<id>` (enables idempotent replay)
  - Schema owner role created: `tenant_<id>_role`
  - Migration history tracked in `atlas_schema_revisions` table per schema
  - Atlas Operator validates migrations in dev database before production apply
  - Failed migrations are rolled back automatically
  - All operations logged for audit trail

**FR-5.3: Reconciliation Loop**
- **Description**: Atlas Operator periodically verifies tenant schema state (handled by FR-4.4)
- **Acceptance Criteria**:
  - Verifies schema exists in CNPG cluster
  - Verifies roles exist with correct permissions
  - Verifies migrations are applied (via `atlas_schema_revisions` table per schema)
  - Automatically repairs drift (missing schemas, roles, or migrations)
  - Reconciliation runs every 30-60 seconds (Atlas Operator default)

**FR-5.4: Retry & Failure Handling**
- **Description**: Control plane handles provisioning failures gracefully
- **Acceptance Criteria**:
  - Failed provisioning is retried with exponential backoff (1s, 2s, 4s, 8s, 16s)
  - Partial failures are reconciled automatically
  - Provisioning status is tracked: pending, provisioning, ready, failed
  - Failed tenants are flagged for manual intervention after 5 retries

### 3.4 Tenant Schema Provisioning

**FR-4.1: Tenant Schema Creation**
- **Description**: Each tenant gets a dedicated PostgreSQL schema within the shared CNPG cluster database via GitOps flow
- **Acceptance Criteria**:
  - Schema migrations are stored in Git repository: `migrations/tenant-baseline/YYYYMMDDHHMMSS_*.sql`
  - Migration files follow Atlas naming convention: `YYYYMMDDHHMMSS_description.sql`
  - Schema name is deterministic: `tenant_<tenant-id>` (e.g., `tenant_acme`, `tenant_xyz`)
  - Schema owner role created: `tenant_<tenant-id>_role`
  - Deterministic naming enables safe migration replay (idempotent CREATE SCHEMA IF NOT EXISTS)
  - Baseline migrations applied via Atlas Operator (creates tenant-specific tables)
  - Schema provisioning completes within 5 seconds (CREATE SCHEMA + baseline migrations)
  - All migrations are idempotent and replayable
  - Migration history tracked in `atlas_schema_revisions` table per schema
  - PostgREST `db-schemas` config updated to include new tenant schema
  - PostgREST health check verifies schema exists before accepting requests

**FR-4.2: Tenant Isolation and RLS Support**
- **Description**: Tenant isolation is enforced by dedicated PostgreSQL schema; within each schema, RLS isolates end-users
- **Acceptance Criteria**:
  - Tenant isolation enforced by deterministic schema naming: `tenant_<id>`
  - AgentGateway validates JWT and extracts `tenant_id` claim
  - AgentGateway forwards request to PostgREST with `X-Tenant-ID: <id>` header
  - PostgREST sets `search_path=tenant_<id>` per request (from `X-Tenant-ID` header)
  - PgBouncer transaction pooling ensures connection reuse without session state leakage
  - PostgreSQL schema isolation prevents cross-tenant access (no shared search_path)
  - Within tenant schema, Row-Level Security (RLS) enabled for end-user isolation
  - RLS policies use JWT claims (e.g., `user_id` from JWT) for end-user access control
  - Baseline migrations create tenant-specific tables within `tenant_<id>` schema with RLS enabled
  - All migrations must be idempotent (`IF NOT EXISTS`, `CREATE OR REPLACE`)
  - Migrations are forward-only (no destructive operations without approval)

**FR-4.3: Schema Migration Replay Capability**
- **Description**: Schema migrations can be replayed for disaster recovery or schema updates
- **Acceptance Criteria**:
  - Migrations can be replayed for cluster recovery
  - Migrations can be replayed for drift correction
  - Migrations can be replayed for onboarding retries
  - Replay is safe and idempotent
  - Atlas supports rollback via declarative schema definitions

**FR-4.4: GitOps-Driven Drift Detection**
- **Description**: Atlas Kubernetes Operator continuously reconciles Git (desired state) vs Schema (actual state)
- **Acceptance Criteria**:
  - Atlas Operator deployed to each Spoke Pool cluster (ArgoCD sync wave 2)
  - Operator watches `AtlasMigration` Custom Resources (one per tenant schema)
  - Reconciliation loop runs every 30-60 seconds (Atlas default)
  - Compares Git migrations vs `atlas_schema_revisions` table for `tenant_<id>` schema
  - Deterministic schema naming ensures safe drift detection (no ambiguity)
  - Detects drift: missing migrations, schema changes, manual alterations
  - Auto-applies missing migrations when drift detected
  - Validates migrations in temporary dev database first (safety check)
  - Updates CR status: `status.conditions[Ready=True/False]`
  - ArgoCD health check uses CR status (blocks sync wave 3 if not Ready)
  - Drift events logged for audit trail
  - Metrics exposed: `atlas_drift_detected_total`, `atlas_migrations_applied_total`

**FR-4.5: Centralized Identity Management (via AgentGateway)**
- **Description**: Hub Ory Kratos/Hydra manages authentication for all tenants across all cells; AgentGateway validates tokens
- **Acceptance Criteria**:
  - Ory Kratos deployed in Hub cluster (single instance for all tenants)
  - Ory Hydra deployed in Hub cluster for OAuth2/OIDC token issuance
  - JWT tokens include claims: `user_id`, `tenant_id`, `role`, `tenant_tier`
  - JWKS endpoint exposed: `https://kratos.hub.example.com/.well-known/jwks.json`
  - AgentGateway validates JWTs:
    * Fetches JWKS from Hub on startup
    * Validates JWT signatures using cached public keys
    * Extracts `tenant_id` claim and forwards as `X-Tenant-ID` header
    * Forwards JWT + headers to PostgREST
  - PostgREST caches validated JWTs in-memory (10000 entries) for performance
  - PostgREST receives pre-authenticated requests from AgentGateway only
  - PostgREST is NOT directly exposed to external traffic
  - User login/signup happens via Hub Ory (cells are stateless)
  - Tenant users stored in Hub Ory database (not per-cell)

---

## 4. Non-Functional Requirements

### 4.1 Performance
- **NFR-1.1**: Cell provisioning completes within 15 minutes (CAPI cluster + edge catalog)
- **NFR-1.3**: Tenant schema provisioning completes within 5 seconds (GitOps commit → ArgoCD sync → Atlas apply)
- **NFR-1.4**: ArgoCD cluster discovery completes within 30 seconds of CAPI cluster Ready
- **NFR-1.5**: JWT validation completes within 1ms (P95) for cached tokens (in-memory lookup in PostgREST)
- **NFR-1.6**: JWT validation for uncached tokens completes within 50ms (P95) (AgentGateway validates, PostgREST caches)
- **NFR-1.7**: PostgREST request latency < 50ms (P95) excluding database query time

### 4.2 Scalability
- **NFR-2.1**: Support up to 100 tenant schemas per Spoke Pool cell
- **NFR-2.2**: Support up to 50 Spoke Pool cells per Hub cluster
- **NFR-2.3**: Shared CNPG cluster handles 500 concurrent connections via PgBouncer transaction pooling
- **NFR-2.4**: PgBouncer MUST use transaction pooling mode (not session pooling)
- **NFR-2.5**: Direct connections to PostgreSQL are prohibited for tenant workloads
- **NFR-2.6**: PostgREST JWT cache handles 10000 cached tokens per cell
- **NFR-2.7**: Hub Ory Kratos scales horizontally (stateless service)
- **NFR-2.8**: PostgREST is NOT directly exposed - all traffic flows through AgentGateway

### 4.3 Reliability
- **NFR-3.1**: Cell provisioning is idempotent (re-applying SpokePool XR has no side effects)
- **NFR-3.2**: CNPG cluster has 99.9% uptime (HA with 3 replicas)
- **NFR-3.3**: NATS Leaf Node buffers events during Hub unavailability (no data loss)
- **NFR-3.4**: ArgoCD Agent reconnects automatically after network disruption
- **NFR-3.5**: Schema provisioning is idempotent and replayable
- **NFR-3.6**: Drift recovery completes within 5 minutes of detection (P95)
- **NFR-3.7**: PostgREST automatically reconnects to CNPG after connection loss
- **NFR-3.8**: Hub Ory Kratos deployed with HA (multiple replicas)

### 4.4 Security
- **NFR-4.1**: All Hub-Spoke communication uses mTLS authentication
- **NFR-4.2**: mTLS certificates auto-rotate 7 days before expiration
- **NFR-4.3**: Tenants are isolated via dedicated PostgreSQL schemas within the shared cluster
- **NFR-4.4**: ArgoCD Agent has RBAC limited to its own cluster (no cross-cluster access)
- **NFR-4.5**: JWT tokens validated in AgentGateway using Hub Ory JWKS (RS256 signature)
- **NFR-4.6**: PostgREST JWT cache prevents token replay attacks (cache invalidation on token expiry)
- **NFR-4.7**: PostgREST enforces schema isolation via `search_path` (no cross-tenant access)
- **NFR-4.8**: PostgREST is NOT directly exposed - only accessible via AgentGateway
- **NFR-4.9**: Hub Ory identity database isolated from tenant data

### 4.5 Observability
- **NFR-5.1**: All Spoke Pool metrics are forwarded to Hub VictoriaMetrics
- **NFR-5.2**: Cell health status is visible in ArgoCD UI (Application health)
- **NFR-5.3**: CNPG cluster metrics include: connection count, replication lag, disk usage
- **NFR-5.4**: Kyverno policy execution is logged and auditable
- **NFR-5.5**: All database migrations are logged for audit and replay

### 4.6 Migration Safety
- **NFR-6.1**: All SQL migrations must be idempotent (`IF NOT EXISTS`, `CREATE OR REPLACE`)
- **NFR-6.2**: All SQL migrations must be forward-only (no destructive operations)
- **NFR-6.3**: Destructive operations require explicit approval
- **NFR-6.4**: Migrations are version-controlled in Git (single source of truth)
- **NFR-6.5**: Migration engine (Atlas) validates migrations in dev database before applying to production
- **NFR-6.6**: Migration files follow Atlas timestamp format: `YYYYMMDDHHMMSS_description.sql`
- **NFR-6.7**: Each migration file is immutable once merged to main branch
- **NFR-6.8**: Atlas Operator performs automatic drift detection and recovery

---

## 5. User Stories

### 5.1 Platform Admin Stories

**US-1.1: Provision First Spoke Pool Cell**
```
As a Platform Admin
I want to provision the first Spoke Pool cell
So that the platform can onboard Starter tier tenants

Acceptance Criteria:
- I apply a SpokePool XR manifest with region=fsn1, nodePool.count=3, maxTenantCapacity=100
- CAPI provisions a 3-node Hetzner cluster in Falkenstein datacenter
- ArgoCD Agent connects to Hub within 2 minutes
- Edge catalog (CNPG, NATS, Alloy) deploys within 10 minutes
- Shared CNPG cluster reaches Ready state
- Cell appears in ArgoCD cluster list with label spoke-type=pool
```

**US-1.2: Provision Additional Cell for Capacity**
```
As a Platform Admin
I want to provision a second Spoke Pool cell when the first is near capacity
So that new tenants can continue onboarding without delays

Acceptance Criteria:
- I monitor cell capacity via ArgoCD UI or kubectl
- When cell-01 reaches 90 tenants (90% capacity), I apply spokepool-02.yaml
- Cell-02 provisions independently without affecting cell-01
- New tenants are automatically placed into cell-02 after it becomes Ready
```

**US-1.3: Verify Cell Health**
```
As a Platform Admin
I want to verify that a Spoke Pool cell is healthy
So that I can troubleshoot issues before they impact tenants

Acceptance Criteria:
- I run: kubectl get cluster spokepool-01 -o jsonpath='{.status.phase}'
- I check ArgoCD Applications for edge-catalog components
- I verify CNPG cluster status: kubectl --context spokepool-01 get cluster shared-cnpg
- All components show Healthy/Ready status
```

### 5.2 Tenant Onboarding System Stories

**US-2.1: Place Tenant in Available Cell**
```
As the Tenant Onboarding System
I want to automatically place a new tenant into the least-loaded cell
So that tenants are distributed evenly across cells

Acceptance Criteria:
- I call tenant provisioning API with tier=starter
- System queries control plane database for cell capacity
- System selects cell with lowest tenant count
- MCP API commits tenant values to `fleet-registry/tenants/tenant-<id>/values.yaml`
- ArgoCD ApplicationSet detects new tenant directory and creates Helm Application
- Helm chart generates CRs: `AINativeSaaS` XR, `AtlasMigration` CR, namespace, RBAC
- Atlas Operator creates deterministic schema: `tenant_<id>` (< 5 seconds)
- Baseline migrations applied from Git via Atlas Operator
- PostgREST db-schemas config updated to include new tenant schema
- Tenant's AINativeSaaS XR is created with label cell-id=<selected-cell>
- Tenant workload is deployed to the correct Spoke Pool cluster
```

**US-2.2: Handle Capacity Exhaustion**
```
As the Tenant Onboarding System
I want to receive a clear error when all cells are at capacity
So that I can notify the Platform Admin to provision more cells

Acceptance Criteria:
- I call tenant provisioning API when all cells have 100 tenants
- System returns error: "Capacity Exhausted - Contact Platform Admin"
- Error includes cell capacity details: cell-01: 100/100, cell-02: 100/100
- No tenant XR is created
- No schema is created
```

### 5.3 SRE Team Stories

**US-3.1: Monitor Cell Capacity**
```
As an SRE
I want to monitor tenant count per cell
So that I can proactively provision new cells before capacity is reached

Acceptance Criteria:
- I query control plane database for tenant count per cell
- I see tenant count per cell in Grafana dashboard
- I receive alert when cell reaches 80% capacity
```

**US-3.2: Verify Edge Catalog Deployment**
```
As an SRE
I want to verify that edge catalog components are healthy
So that I can ensure tenant workloads have required infrastructure

Acceptance Criteria:
- I check ArgoCD Applications for cell: argocd app list | grep spokepool-01
- I see Applications: shared-cnpg, nats-leaf-node, grafana-alloy
- All Applications show Healthy and Synced status
- I can drill down into each Application to see resource details
```

---

## 6. Acceptance Criteria

### 6.1 Phase 1 Completion Criteria

**AC-1: SpokePool XRD and Composition**
- [ ] SpokePool XRD is defined with schema: region, nodePool, maxTenantCapacity
- [ ] SpokePool Composition generates: CAPI Cluster, HetznerCluster, MachineDeployment, ClusterResourceSet
- [ ] Composition patches pre-generated mTLS certificate into ClusterResourceSet
- [ ] Applying SpokePool XR provisions a functional Kubernetes cluster

**AC-2: Kyverno Cluster Discovery**
- [ ] Kyverno ClusterPolicy watches CAPI Cluster resources
- [ ] Policy extracts kubeconfig from CAPI Secret when Cluster.status.phase=Provisioned
- [ ] Policy generates ArgoCD cluster Secret with correct labels
- [ ] ArgoCD discovers cluster within 30 seconds

**AC-3: ArgoCD Agent Bootstrap**
- [ ] ClusterResourceSet contains 5 resources: Deployment, ConfigMap, mTLS cert, CA, RBAC
- [ ] ArgoCD Agent starts within 2 minutes of cluster Ready
- [ ] Agent connects to Hub ArgoCD using mTLS authentication
- [ ] Agent can pull Applications from Hub

**AC-4: Edge Catalog Deployment**
- [ ] ApplicationSet with Cluster Generator deploys edge catalog to all pool clusters
- [ ] App-of-Apps pattern creates individual Applications for each component
- [ ] Shared CNPG cluster reaches Ready state within 5 minutes
- [ ] PostgREST deployed as internal service (NOT directly exposed)
- [ ] AgentGateway deployed and configured with Hub Ory JWKS endpoint
- [ ] AgentGateway routes to PostgREST after JWT validation
- [ ] NATS Leaf Node connects to Hub JetStream
- [ ] Grafana Alloy forwards metrics to Hub VictoriaMetrics
- [ ] Atlas Operator deployed and ready

**AC-5: Tenant Schema Provisioning (Universal Tenant Helm Chart Pattern)**
- [ ] Schema migrations are stored in Git repository: `migrations/tenant-baseline/YYYYMMDDHHMMSS_*.sql`
- [ ] Migration files follow Atlas naming convention: `YYYYMMDDHHMMSS_description.sql`
- [ ] Universal Tenant Helm Chart exists in `charts/universal-tenant/`
- [ ] Chart templates generate: `AINativeSaaS` XR, `AtlasMigration` CR, namespace, RBAC, ResourceQuota
- [ ] Chart enforces platform policy via templates (sync waves, security contexts, resource limits)
- [ ] MCP API commits tenant values to `fleet-registry/tenants/tenant-<id>/values.yaml`
- [ ] Values file contains: `tenantId: acme`, `tier: starter`, `database.schemaName: tenant_acme`, `database.migrations.gitRepo`
- [ ] ArgoCD ApplicationSet (Git Generator) detects new tenant directory
- [ ] ApplicationSet creates Helm Application: `source.chart: charts/universal-tenant`, `source.helm.valueFiles: [fleet-registry/tenants/tenant-<id>/values.yaml]`
- [ ] Helm renders templates with tenant values and generates CRs
- [ ] Application deploys generated CRs to Spoke Pool cluster with sync waves
- [ ] Atlas Operator creates deterministic schema: `tenant_<tenant-id>` (e.g., `tenant_acme`)
- [ ] Atlas Operator applies baseline migrations from Git
- [ ] Schema owner role created: `tenant_<tenant-id>_role`
- [ ] RLS policies are enabled for end-user isolation (via JWT `user_id`)
- [ ] All migrations are idempotent and replayable (deterministic schema names enable safe replay)
- [ ] Migration history tracked in `atlas_schema_revisions` table per schema
- [ ] AtlasMigration CR status updates to `Ready=True`
- [ ] ArgoCD health check verifies CR status before progressing to wave 3
- [ ] PostgREST deployed in wave 3 with `db-schemas` including `tenant_<id>`
- [ ] PostgREST health check verifies schema exists before accepting requests
- [ ] Schema provisioning completes in < 5 seconds (end-to-end GitOps flow)
- [ ] Tenant applications can access their schema via AgentGateway → PostgREST
- [ ] AgentGateway validates JWT and extracts `tenant_id` claim
- [ ] AgentGateway forwards request with `X-Tenant-ID` header to PostgREST
- [ ] PostgREST sets `search_path=tenant_<id>` based on `X-Tenant-ID` header
- [ ] Atlas Operator reconciles drift automatically (30-60s loop)

**AC-6: End-to-End Integration Test (GitOps Flow)**
- [ ] Apply SpokePool XR: `kubectl apply -f spokepool-01.yaml`
- [ ] Wait for cluster Ready: `kubectl wait --for=condition=Ready cluster/spokepool-01 --timeout=20m`
- [ ] Verify ArgoCD cluster Secret: `kubectl get secret -n argocd -l cell-id=spokepool-01`
- [ ] Verify edge catalog synced with correct sync waves: `argocd app list | grep spokepool-01`
- [ ] Verify CNPG Ready (wave 1): `kubectl --context spokepool-01 get cluster shared-cnpg -o jsonpath='{.status.phase}'`
- [ ] Verify Atlas Operator Ready (wave 2): `kubectl --context spokepool-01 get deployment atlas-operator`
- [ ] Verify PostgREST Ready (wave 3): `kubectl --context spokepool-01 get deployment postgrest -o jsonpath='{.status.readyReplicas}'`
- [ ] Call MCP API: `tenant_create(tenant_id="acme", tier="starter")`
- [ ] Verify MCP API commits values to Git: `fleet-registry/tenants/tenant-acme/values.yaml`
- [ ] Verify values file contains: `tenantId: acme`, `database.schemaName: tenant_acme`
- [ ] Verify ArgoCD detects new tenant directory and creates Helm Application
- [ ] Verify Application uses Universal Tenant Chart: `argocd app get tenant-acme -o json | jq .spec.source.chart`
- [ ] Verify Helm renders CRs: `helm template charts/universal-tenant -f fleet-registry/tenants/tenant-acme/values.yaml`
- [ ] Verify AtlasMigration CR deployed: `kubectl --context spokepool-01 get atlasmigration tenant-acme`
- [ ] Verify deterministic schema created: `\dn tenant_acme` in CNPG cluster
- [ ] Verify schema owner role: `\du tenant_acme_role`
- [ ] Verify tenant metadata in control plane database
- [ ] Verify baseline tables exist in `tenant_acme` schema
- [ ] Verify AtlasMigration CR status: `Ready=True`
- [ ] Verify PostgREST `db-schemas` includes `tenant_acme`
- [ ] Verify tenant can access their schema via AgentGateway → PostgREST with JWT from Hub Ory
- [ ] Verify AgentGateway validates JWT and forwards with `X-Tenant-ID` header
- [ ] Verify PostgREST sets `search_path=tenant_acme` based on `X-Tenant-ID` header
- [ ] Verify PgBouncer transaction pooling (no session state leakage between requests)
- [ ] Verify Atlas Operator drift detection: manually alter schema, wait 60s, verify auto-repair

---

## 7. Out of Scope (Phase 1)

The following features are explicitly deferred to later phases:

**Deferred to Phase 2+**:
- Hub Centralised DB for cell metadata storage
- Spoke Controller for status sync (Spoke → Hub)
- Cell rebalancing (moving tenants between cells)
- Cell decommissioning (draining and deleting cells)
- HeadLamp UI for fleet management
- Tenant workload deployment (AINativeSaaS Composition for tenant namespaces)
- Automatic cell provisioning on capacity exhaustion
- Multi-region cell provisioning
- Tenant metadata management in Control Plane DB
- Cell capacity tracking and tenant placement logic
- Cell health monitoring dashboard
- Cell health monitoring dashboard
- MCP server implementation (tenant provisioning API exists, but MCP interface deferred)

**Explicitly Not Included**:
- Spoke Silo clusters (Enterprise tier, dedicated clusters)
- Tenant-specific CNPG clusters (only shared cluster in Phase 1)
- Custom cell sizing policies (all cells use same node pool configuration)
- Cell-to-cell communication (cells are isolated)

---

## 8. Dependencies

### 8.1 External Dependencies
- **CAPI + CAPH**: Cluster API with Hetzner provider for VM provisioning
- **Crossplane**: Infrastructure provisioning engine (v1.14+)
- **Crossplane provider-sql**: Logical database provisioning (v0.9+)
- **ArgoCD**: GitOps deployment engine (v2.10+)
- **Kyverno**: Policy engine for CAPI-to-ArgoCD bridge (v1.11+)
- **cert-manager**: mTLS certificate generation (v1.13+)
- **CloudNativePG**: PostgreSQL operator (v1.22+)
- **NATS**: Messaging system with JetStream (v2.10+)

- **Grafana Alloy**: Metrics collection and forwarding (v1.0+)
- **Atlas Kubernetes Operator**: GitOps-driven database migration engine with drift detection (v0.3+)
- **Atlas Cloud**: Optional SaaS for migration visibility and schema visualization
- **PostgREST**: Auto-generated REST API for PostgreSQL, exposed only through AgentGateway (v12.0+)
- **AgentGateway**: Authentication gateway that validates JWTs and routes to PostgREST
- **Ory Kratos**: Identity and user management system (v1.0+)
- **Ory Hydra**: OAuth2 and OpenID Connect server (v2.2+)

### 8.2 Internal Dependencies
- Hub cluster must be provisioned and operational
- Hub Ory Kratos/Hydra must be deployed and configured
- AgentGateway must be deployed to each Spoke Pool cluster
- AgentGateway must be configured with Hub Ory JWKS endpoint
- Hub ArgoCD must be configured with ApplicationSets
- Hub NATS JetStream must be running

- Hub VictoriaMetrics must be running
- GitOps repository structure must be defined
- Control plane database must be provisioned for tenant metadata
- Atlas Operator must be deployed to each Spoke Pool cluster
- PostgREST must be deployed to each Spoke Pool cluster
- SQL migration repository must be initialized in Git
- Migration repository structure: `migrations/tenant-baseline/YYYYMMDDHHMMSS_*.sql`
- Fleet registry repository: `fleet-registry/tenants/<tenant-id>/values.yaml` for tenant intent
- Universal Tenant Helm Chart: `charts/universal-tenant/` with templates for all tenant resources
- Atlas Cloud token (optional, for enhanced visibility)

### 8.3 Prerequisite Configuration
- Hetzner Cloud API token configured in Crossplane
- ArgoCD mTLS CA certificate generated via cert-manager
- NATS mTLS CA certificate generated

- VictoriaMetrics remote_write endpoint configured
- Control plane database schema initialized
- Tenant baseline migrations prepared in Git (`migrations/tenant-baseline/`)
- Fleet registry repository initialized (`fleet-registry/tenants/`)
- Atlas Operator installed via Helm: `helm install atlas-operator oci://ghcr.io/ariga/charts/atlas-operator`
- AtlasMigration CRD registered in Spoke Pool clusters
- Migration engine connection credentials configured
- SQL migration repository structure defined in Git
- Migration repository access configured for control plane
- Base tenant database migrations committed to Git (YYYYMMDDHHMMSS_*.sql format)

---

## 9. Risks and Mitigations

| Risk | Impact | Probability | Mitigation |
|------|--------|-------------|------------|
| CAPI cluster provisioning fails | High | Medium | Implement retry logic in Crossplane Composition; monitor CAPI controller logs |
| Kyverno policy fails to generate ArgoCD Secret | High | Low | Add Kyverno policy validation tests; implement fallback manual registration |
| ArgoCD Agent fails to connect to Hub | High | Medium | Pre-validate mTLS certificates; implement connection retry with exponential backoff |
| Shared CNPG cluster reaches connection limit | High | Medium | Monitor connection count; alert at 80% capacity; enforce PgBouncer transaction pooling |
| Edge catalog deployment times out | Medium | Medium | Increase ArgoCD sync timeout; implement health checks for each component |
| Schema migration fails | High | Low | Add SQL migration validation; implement rollback mechanism for failed migrations; use idempotent migrations |
| Control plane database unavailable | High | Low | Implement retry logic with exponential backoff; cache cell capacity data; implement circuit breaker |

---

## 10. Success Metrics

### 10.1 Operational Metrics
- **Cell Provisioning Time**: < 15 minutes (P95)
- **Cell Provisioning Success Rate**: > 99%
- **Tenant Schema Provisioning Time**: < 5 seconds (P95) - GitOps commit → ArgoCD sync → Atlas apply
- **ArgoCD Cluster Discovery Time**: < 30 seconds (P95)
- **Migration Execution Time**: < 3 seconds (P95)
- **JWT Validation Time (Cached)**: < 1ms (P95) - in-memory cache lookup in PostgREST
- **JWT Validation Time (Uncached)**: < 50ms (P95) - AgentGateway validates, PostgREST caches
- **PostgREST Request Latency**: < 50ms (P95) - excluding database query time (receives pre-authenticated requests)

### 10.2 Reliability Metrics
- **Cell Uptime**: > 99.9%
- **CNPG Cluster Uptime**: > 99.9%
- **ArgoCD Agent Connection Uptime**: > 99.5%
- **NATS Leaf Node Connection Uptime**: > 99.5%
- **Schema Provisioning Success Rate**: > 99.9%
- **Migration Replay Success Rate**: 100%
- **Hub Ory Kratos Uptime**: > 99.9%
- **PostgREST Uptime**: > 99.9%

### 10.3 Capacity Metrics
- **Tenant Schemas per Cell**: 100 (max)
- **Cells per Hub**: 50 (max)
- **Total Starter Tenants Supported**: 5,000 (100 tenants * 50 cells)
- **CNPG Connections per Cell**: 500 (via PgBouncer transaction pooling)
- **PostgREST JWT Cache Size**: 10,000 tokens per cell

---

## 11. Glossary

| Term | Definition |
|------|------------|
| **Cell** | A complete Kubernetes cluster that hosts multiple Starter tier tenants with namespace-level isolation |
| **Spoke Pool** | A cell that uses shared infrastructure (pooled model) for cost efficiency |
| **Edge Catalog** | Set of infrastructure components deployed to every Spoke Pool: CNPG, PostgREST, NATS, Alloy, Atlas Operator |
| **Secret Zero** | The minimal bootstrap secret (mTLS certificate) injected via ClusterResourceSet |
| **ClusterResourceSet** | CAPI mechanism for injecting manifests into newly provisioned clusters |
| **Kyverno** | Kubernetes policy engine used to bridge CAPI and ArgoCD |

| **Tenant Schema** | A dedicated PostgreSQL schema within the shared CNPG cluster database, isolating a single tenant's data |
| **RLS** | Row-Level Security - PostgreSQL feature used within tenant schemas to isolate end-users (not for platform-level tenant isolation) |
| **App-of-Apps** | ArgoCD pattern where one Application generates multiple child Applications |
| **Atlas** | SQL migration engine with Kubernetes Operator for GitOps-driven database management and drift detection |
| **AtlasMigration** | Custom Resource (CR) that defines database migrations in a declarative, GitOps-friendly manner |
| **Control Plane Database** | PostgreSQL database storing tenant metadata, cell assignments, and provisioning state |
| **Transaction Pooling** | PgBouncer mode that reuses connections between transactions (required for high tenant density) |
| **Tenant Baseline** | Standard tenant table structure applied within each `tenant_<id>` schema via Atlas migrations |
| **Sync Waves** | ArgoCD annotation-based dependency ordering (wave 0-4) ensuring components deploy in correct sequence |
| **Fleet Registry** | Git repository (`fleet-registry/tenants/<tenant-id>/values.yaml`) storing tenant intent as Helm values |
| **Universal Tenant Chart** | Helm chart that generates all tenant CRs from values.yaml (platform policy in templates, tenant input in values) |
| **Drift Detection** | Continuous reconciliation by Atlas Operator comparing Git (desired) vs Schema (actual) state |
| **PostgREST** | Auto-generated REST API for PostgreSQL with in-memory JWT cache, exposed only through AgentGateway |
| **AgentGateway** | Authentication gateway that validates JWTs using Hub Ory JWKS and routes to PostgREST |
| **Ory Kratos** | Identity and user management system deployed in Hub for centralized authentication |
| **Ory Hydra** | OAuth2/OIDC server deployed in Hub for token issuance |
| **JWKS** | JSON Web Key Set - public keys used by AgentGateway to validate JWT signatures |
| **Schema Routing** | AgentGateway extracts `tenant_id` from JWT, forwards as `X-Tenant-ID` header to PostgREST |

---

**Document Version**: 1.0  
**Last Updated**: 2026-04-07  
**Next Review**: After Phase 1 implementation completion
