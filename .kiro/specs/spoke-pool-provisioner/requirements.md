# Requirements: Spoke Pool Provisioner (Cell-Based Scaling)

**Feature Name**: `spoke-pool-provisioner`  
**Phase**: Phase 1 - Foundation  
**Status**: Draft  
**Created**: 2026-04-07

---

## 1. Executive Summary

### 1.1 Purpose
Automate the provisioning of Spoke Pool clusters (cells) that host multiple Starter tier tenants with namespace-level isolation. Each cell is a complete Kubernetes cluster containing shared infrastructure (CNPG database, NATS leaf node, ArgoCD agent) that serves all tenants within that cell.

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
- Tenant schema provisioning completes within 5 seconds (schema creation + Supabase baseline migrations)
- PostgREST validates JWTs from Hub Ory Kratos and routes requests to correct tenant schema

---

## 2. Stakeholders

| Role | Responsibility | Success Metric |
|------|---------------|----------------|
| Platform Admin | Provision and monitor Spoke Pool cells | Cell provisioning time < 15 min |
| SRE Team | Ensure cell health and capacity planning | Zero manual cluster configuration |
| Tenant Onboarding System | Place new tenants into available cells | Tenant placement latency < 1 sec |
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
- **Description**: ArgoCD ApplicationSet deploys edge catalog components to all Spoke Pool clusters
- **Acceptance Criteria**:
  - ApplicationSet uses Cluster Generator with selector: `spoke-type: pool`
  - Deploys App-of-Apps umbrella Application to each discovered cluster
  - Individual Applications created for: Shared CNPG, PostgREST, NATS Leaf Node, Spire Agent, Grafana Alloy, Atlas Operator
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

**FR-2.4: Spire Agent**
- **Description**: Spire Agent provides workload identity (SVIDs) for mTLS between services
- **Acceptance Criteria**:
  - Agent connects to Hub Spire Server using PSAT attestation (no join tokens)
  - Agent can issue SVIDs to workloads in the cluster
  - mTLS certificates auto-rotate before expiration

**FR-2.5: Grafana Alloy**
- **Description**: Alloy scrapes metrics and forwards to Hub VictoriaMetrics
- **Acceptance Criteria**:
  - Scrapes KSM (Kubernetes State Metrics) and CNPG metrics
  - Remote writes to Hub VictoriaMetrics using SPIRE SVID authentication (or static token for Phase 1)
  - Injects `cell_id` label into all metrics

**FR-2.6: PostgREST API Gateway**
- **Description**: PostgREST provides auto-generated REST API for tenant databases with JWT authentication and schema routing
- **Acceptance Criteria**:
  - PostgREST deployed as shared service in each Spoke Pool cluster
  - Configured with `db-schemas` listing all tenant schemas (dynamically updated)
  - JWT validation using Hub Ory Kratos JWKS endpoint
  - In-memory JWT cache enabled (`jwt-cache-max-entries: 10000`)
  - Schema routing via `Accept-Profile` header (extracted from JWT `tenant_id` claim)
  - Connection pooling via PgBouncer (transaction mode)
  - PostgREST reaches Ready state within 2 minutes

### 3.5 Control Plane Responsibilities

**FR-5.1: Tenant Metadata Management**
- **Description**: Control plane maintains tenant registry with cell assignments
- **Acceptance Criteria**:
  - Tenant registry stores: tenant-id, assigned cell-id, schema name, provisioning status
  - Registry is stored in control plane database (not Kubernetes API)
  - Registry is queryable for capacity planning and tenant placement

**FR-5.2: Schema Provisioning Execution**
- **Description**: Control plane executes SQL migrations from Git against CNPG cluster
- **Acceptance Criteria**:
  - Uses migration engine (Atlas or Flyway)
  - Reads migration files from Git repository
  - Connects to shared CNPG cluster with admin credentials
  - Executes migrations in transaction
  - Ensures idempotent execution
  - Logs all migration executions for audit
  - Migration engine validates SQL syntax before execution
  - Failed migrations are rolled back automatically

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

### 3.6 Cell Capacity Management

**FR-6.1: Capacity Tracking (Control Plane Driven)**
- **Description**: Control plane maintains tenant-to-cell mapping for capacity tracking
- **Acceptance Criteria**:
  - Control plane database stores tenant-to-cell mapping
  - Capacity is calculated using control plane data, not Kubernetes API
  - Kubernetes labels (cell-id on AINativeSaaS XR) are treated as derived state only
  - Capacity query completes in < 100ms (database query, not K8s API call)

**FR-6.2: Tenant Placement**
- **Description**: New tenants are placed into the least-loaded available cell
- **Acceptance Criteria**:
  - MCP `tenant_create` queries all cells and selects cell with lowest tenant count
  - Tenant's AINativeSaaS XR includes label: `cell-id: <selected-cell>`
  - Placement decision completes in < 1 second

**FR-3.3: Capacity Exhaustion Handling**
- **Description**: System returns error when all cells are at capacity
- **Acceptance Criteria**:
  - If all cells have `tenant_count >= maxTenantCapacity`, return error: "Capacity Exhausted"
  - Error message includes guidance: "Contact Platform Admin to provision additional cells"
  - No automatic cell provisioning in Phase 1 (deferred to Platform Admin)

### 3.4 Tenant Schema Provisioning

**FR-4.1: Tenant Schema Creation**
- **Description**: Each tenant gets a dedicated PostgreSQL schema within the shared CNPG cluster database
- **Acceptance Criteria**:
  - Schema migrations are stored in Git repository (e.g., `migrations/supabase-baseline/`)
  - Migration files follow Atlas naming convention: `YYYYMMDDHHMMSS_description.sql`
  - Schema name format: `tenant_<tenant-id>`
  - Schema owner role created: `tenant_<tenant-id>_role`
  - Supabase baseline migrations applied via Atlas (creates `auth`, `storage`, `public` tables within tenant schema)
  - Schema provisioning completes within 5 seconds (CREATE SCHEMA + baseline migrations)
  - All migrations are idempotent and replayable
  - Migration history tracked in `atlas_schema_revisions` table per schema
  - PostgREST `db-schemas` config updated to include new tenant schema

**FR-4.2: Tenant Isolation and RLS Support**
- **Description**: Tenant isolation is enforced by dedicated PostgreSQL schema; within each schema, RLS isolates end-users
- **Acceptance Criteria**:
  - Tenant isolation enforced by dedicated schema (PostgreSQL `search_path` prevents cross-schema access)
  - Within tenant schema, Row-Level Security (RLS) enabled for end-user isolation
  - RLS policies use Ory Kratos JWT claims (e.g., `user_id` from JWT) for end-user access control
  - Supabase baseline creates `auth`, `storage`, `public` tables within tenant schema with RLS enabled
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
  - Atlas Operator deployed to each Spoke Pool cluster
  - Operator watches `AtlasMigration` Custom Resources (one per tenant schema)
  - Reconciliation loop runs every 30-60 seconds (Atlas default)
  - Compares Git migrations vs `atlas_schema_revisions` table per schema
  - Detects drift: missing migrations, schema changes, manual alterations
  - Auto-applies missing migrations when drift detected
  - Validates migrations in temporary dev database first (safety check)
  - Updates CR status: `status.conditions[Ready=True/False]`
  - Drift events logged for audit trail
  - Metrics exposed: `atlas_drift_detected_total`, `atlas_migrations_applied_total`

**FR-4.5: Centralized Identity Management**
- **Description**: Hub Ory Kratos/Hydra manages authentication for all tenants across all cells
- **Acceptance Criteria**:
  - Ory Kratos deployed in Hub cluster (single instance for all tenants)
  - Ory Hydra deployed in Hub cluster for OAuth2/OIDC token issuance
  - JWT tokens include claims: `user_id`, `tenant_id`, `role`, `tenant_tier`
  - JWKS endpoint exposed: `https://kratos.hub.example.com/.well-known/jwks.json`
  - PostgREST in cells validates JWTs using Hub JWKS endpoint
  - JWT cache in PostgREST reduces validation overhead (10000 entries)
  - User login/signup happens via Hub Ory (cells are stateless)
  - Tenant users stored in Hub Ory database (not per-cell)

---

## 4. Non-Functional Requirements

### 4.1 Performance
- **NFR-1.1**: Cell provisioning completes within 15 minutes (CAPI cluster + edge catalog)
- **NFR-1.2**: Tenant placement decision completes within 1 second
- **NFR-1.3**: Tenant schema provisioning completes within 5 seconds (CREATE SCHEMA + Supabase baseline migrations)
- **NFR-1.4**: ArgoCD cluster discovery completes within 30 seconds of CAPI cluster Ready
- **NFR-1.5**: JWT validation completes within 10ms (P95) for cached tokens
- **NFR-1.6**: PostgREST request latency < 50ms (P95) excluding database query time

### 4.2 Scalability
- **NFR-2.1**: Support up to 100 tenant schemas per Spoke Pool cell
- **NFR-2.2**: Support up to 50 Spoke Pool cells per Hub cluster
- **NFR-2.3**: Shared CNPG cluster handles 500 concurrent connections via PgBouncer transaction pooling
- **NFR-2.4**: PgBouncer MUST use transaction pooling mode (not session pooling)
- **NFR-2.5**: Direct connections to PostgreSQL are prohibited for tenant workloads
- **NFR-2.6**: PostgREST JWT cache handles 10000 cached tokens per cell
- **NFR-2.7**: Hub Ory Kratos scales horizontally (stateless service)

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
- **NFR-4.5**: JWT tokens validated using Hub Ory JWKS (RS256 signature)
- **NFR-4.6**: PostgREST enforces schema isolation via `search_path` (no cross-tenant access)
- **NFR-4.7**: Hub Ory identity database isolated from tenant data

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
- Edge catalog (CNPG, NATS, Spire, Alloy) deploys within 10 minutes
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
- Tenant schema created in selected cell's CNPG cluster (CREATE SCHEMA tenant_<id>)
- Supabase baseline migrations applied via Atlas Operator (< 5 seconds)
- AtlasMigration CR created for tenant schema
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
- I see Applications: shared-cnpg, nats-leaf-node, spire-agent, grafana-alloy
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
- [ ] PostgREST deployed and configured with Hub Ory JWKS endpoint
- [ ] NATS Leaf Node connects to Hub JetStream
- [ ] Spire Agent connects to Hub Spire Server using PSAT attestation
- [ ] Grafana Alloy forwards metrics to Hub VictoriaMetrics
- [ ] Atlas Operator deployed and ready

**AC-5: Tenant Placement Logic**
- [ ] Control plane queries tenant count per cell from control plane database
- [ ] Placement algorithm selects least-loaded cell
- [ ] Tenant metadata is written to control plane database with cell assignment
- [ ] Tenant XR includes cell-id label
- [ ] Placement decision completes in < 1 second

**AC-6: Tenant Schema Provisioning**
- [ ] Schema migrations are stored in Git repository: `migrations/supabase-baseline/`
- [ ] Migration files follow Atlas naming convention: `YYYYMMDDHHMMSS_description.sql`
- [ ] Control plane creates schema: `CREATE SCHEMA tenant_<tenant-id>`
- [ ] Atlas Operator applies Supabase baseline migrations (creates `auth`, `storage`, `public` tables in schema)
- [ ] AtlasMigration CR created per tenant schema
- [ ] Schema name format: `tenant_<tenant-id>`
- [ ] Schema owner role is created
- [ ] RLS policies are enabled for end-user isolation (via JWT `user_id`)
- [ ] All migrations are idempotent and replayable
- [ ] Migration history tracked in `atlas_schema_revisions` table per schema
- [ ] Schema provisioning completes in < 5 seconds
- [ ] PostgREST db-schemas config updated with new tenant schema
- [ ] Tenant applications can access their schema via PostgREST
- [ ] Atlas Operator reconciles drift automatically

**AC-7: End-to-End Integration Test**
- [ ] Apply SpokePool XR: `kubectl apply -f spokepool-01.yaml`
- [ ] Wait for cluster Ready: `kubectl wait --for=condition=Ready cluster/spokepool-01 --timeout=20m`
- [ ] Verify ArgoCD cluster Secret: `kubectl get secret -n argocd -l cell-id=spokepool-01`
- [ ] Verify edge catalog synced: `argocd app list | grep spokepool-01`
- [ ] Verify CNPG Ready: `kubectl --context spokepool-01 get cluster shared-cnpg -o jsonpath='{.status.phase}'`
- [ ] Verify PostgREST Ready: `kubectl --context spokepool-01 get deployment postgrest -o jsonpath='{.status.readyReplicas}'`
- [ ] Create test tenant via provisioning API
- [ ] Verify tenant schema exists in CNPG cluster: `\dn tenant_*`
- [ ] Verify tenant metadata in control plane database
- [ ] Verify Supabase baseline tables (`auth.*`, `storage.*`, `public.*`) exist in tenant schema
- [ ] Verify tenant can access their schema via PostgREST with JWT from Hub Ory
- [ ] Verify PostgREST routes to correct schema based on JWT `tenant_id` claim

---

## 7. Out of Scope (Phase 1)

The following features are explicitly deferred to later phases:

**Deferred to Phase 2+**:
- Hub Centralised DB for cell metadata storage (using control plane DB in Phase 1)
- Spoke Controller for status sync (Spoke → Hub)
- Cell rebalancing (moving tenants between cells)
- Cell decommissioning (draining and deleting cells)
- HeadLamp UI for fleet management
- Tenant workload deployment (AINativeSaaS Composition for tenant namespaces)
- Automatic cell provisioning on capacity exhaustion
- Multi-region cell provisioning
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
- **Spire**: Workload identity and mTLS (v1.8+)
- **Grafana Alloy**: Metrics collection and forwarding (v1.0+)
- **Atlas Kubernetes Operator**: GitOps-driven database migration engine with drift detection (v0.3+)
- **Atlas Cloud**: Optional SaaS for migration visibility and schema visualization
- **PostgREST**: Auto-generated REST API for PostgreSQL with JWT authentication (v12.0+)
- **Ory Kratos**: Identity and user management system (v1.0+)
- **Ory Hydra**: OAuth2 and OpenID Connect server (v2.2+)

### 8.2 Internal Dependencies
- Hub cluster must be provisioned and operational
- Hub Ory Kratos/Hydra must be deployed and configured
- Hub ArgoCD must be configured with ApplicationSets
- Hub NATS JetStream must be running
- Hub Spire Server must be running
- Hub VictoriaMetrics must be running
- GitOps repository structure must be defined
- Control plane database must be provisioned for tenant metadata
- Atlas Operator must be deployed to each Spoke Pool cluster
- PostgREST must be deployed to each Spoke Pool cluster
- SQL migration repository must be initialized in Git
- Migration repository structure: `migrations/supabase-baseline/YYYYMMDDHHMMSS_*.sql`
- Atlas Cloud token (optional, for enhanced visibility)

### 8.3 Prerequisite Configuration
- Hetzner Cloud API token configured in Crossplane
- ArgoCD mTLS CA certificate generated via cert-manager
- NATS mTLS CA certificate generated
- Spire Server trust domain configured: `zero-ops.io`
- VictoriaMetrics remote_write endpoint configured
- Control plane database schema initialized
- Supabase baseline migrations prepared in Git
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
| Tenant placement selects wrong cell | Medium | Low | Add validation logic to verify cell capacity before placement; implement placement audit log |
| Edge catalog deployment times out | Medium | Medium | Increase ArgoCD sync timeout; implement health checks for each component |
| Schema migration fails | High | Low | Add SQL migration validation; implement rollback mechanism for failed migrations; use idempotent migrations |
| Control plane database unavailable | High | Low | Implement retry logic with exponential backoff; cache cell capacity data; implement circuit breaker |

---

## 10. Success Metrics

### 10.1 Operational Metrics
- **Cell Provisioning Time**: < 15 minutes (P95)
- **Cell Provisioning Success Rate**: > 99%
- **Tenant Placement Latency**: < 1 second (P99)
- **Tenant Schema Provisioning Time**: < 5 seconds (P95) - CREATE SCHEMA + Supabase baseline migrations
- **ArgoCD Cluster Discovery Time**: < 30 seconds (P95)
- **Migration Execution Time**: < 3 seconds (P95)
- **JWT Validation Time**: < 10ms (P95) - cached tokens
- **PostgREST Request Latency**: < 50ms (P95) - excluding database query time

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
| **Edge Catalog** | Set of infrastructure components deployed to every Spoke Pool: CNPG, PostgREST, NATS, Spire, Alloy, Atlas Operator |
| **Secret Zero** | The minimal bootstrap secret (mTLS certificate) injected via ClusterResourceSet |
| **ClusterResourceSet** | CAPI mechanism for injecting manifests into newly provisioned clusters |
| **Kyverno** | Kubernetes policy engine used to bridge CAPI and ArgoCD |
| **PSAT Attestation** | Projected Service Account Token attestation for Spire Agent authentication |
| **Tenant Schema** | A dedicated PostgreSQL schema within the shared CNPG cluster database, isolating a single tenant's data |
| **RLS** | Row-Level Security - PostgreSQL feature used within tenant schemas to isolate end-users (not for platform-level tenant isolation) |
| **App-of-Apps** | ArgoCD pattern where one Application generates multiple child Applications |
| **Atlas** | SQL migration engine with Kubernetes Operator for GitOps-driven database management and drift detection |
| **AtlasMigration** | Custom Resource (CR) that defines database migrations in a declarative, GitOps-friendly manner |
| **Control Plane Database** | PostgreSQL database storing tenant metadata, cell assignments, and provisioning state |
| **Transaction Pooling** | PgBouncer mode that reuses connections between transactions (required for high tenant density) |
| **Supabase Baseline** | Standard Supabase table structure (`auth.*`, `storage.*`, `public.*`) applied within each tenant schema |
| **Drift Detection** | Continuous reconciliation by Atlas Operator comparing Git (desired) vs Schema (actual) state |
| **PostgREST** | Auto-generated REST API for PostgreSQL with JWT authentication and schema routing |
| **Ory Kratos** | Identity and user management system deployed in Hub for centralized authentication |
| **Ory Hydra** | OAuth2/OIDC server deployed in Hub for token issuance |
| **JWKS** | JSON Web Key Set - public keys used to validate JWT signatures |
| **Schema Routing** | PostgREST mechanism to route requests to correct tenant schema based on JWT claims |

---

**Document Version**: 1.0  
**Last Updated**: 2026-04-07  
**Next Review**: After Phase 1 implementation completion
