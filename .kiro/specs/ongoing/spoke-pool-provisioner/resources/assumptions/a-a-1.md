## Phase 1 Discovery: Spoke Pool Provisioner (Cell-Based Scaling)

I'll validate requirements through targeted questions. Each question includes the technical assumption being tested.

### 1. Cell Definition & Scope

**Assumption**: A Spoke Pool "cell" is a complete Kubernetes cluster (not a namespace or VPC) that hosts multiple Starter tier tenants with namespace-level isolation.

**Q1**: Is the Spoke Pool cell a physical Kubernetes cluster provisioned via CAPI/CAPH, where each cell can host up to N Starter tenants (e.g., 100 tenants per cell)?

---

**Assumption**: The cell contains shared infrastructure (CNPG cluster, NATS leaf node, ArgoCD agent) that serves all tenants within that cell.

**Q2**: Does each Spoke Pool cell contain exactly one shared CNPG PostgreSQL cluster that provides logical databases for all Starter tenants in that cell?

---

**Assumption**: Cell capacity is determined by a configurable threshold (tenant count, resource utilization, or policy-based limit).

**Q3**: Is the cell saturation policy defined by a maximum tenant count (e.g., 100 tenants), or does it also consider resource metrics (CPU/memory utilization, database connection count)?

---

### 2. Cell Provisioning Mechanism

**Assumption**: The SpokePool XR (Crossplane Composite Resource) is the declarative API for creating a new cell, similar to how AINativeSaaS XR provisions tenant infrastructure.

**Q4**: Will the `SpokePool` XRD define the cell's infrastructure blueprint (cluster size, node count, region, shared services), and will Crossplane Compositions expand this into CAPI Cluster + ClusterResourceSet?

---

**Assumption**: The edge-catalog (argocd-agent, nats-leaf-node, spire-agent, shared CNPG) is injected at cluster bootstrap via ClusterResourceSet, not via ArgoCD ApplicationSet.

**Q5**: Is the edge-catalog injection a one-time bootstrap operation via ClusterResourceSet that runs during CAPI cluster provisioning, before any tenant workloads are deployed?

---

**Assumption**: The Hub's ArgoCD manages the SpokePool XR lifecycle (create/update/delete), and the Spoke's ArgoCD Agent pulls tenant workloads after the cell is ready.

**Q6**: Does the Hub's ArgoCD watch the GitOps repo for SpokePool XR manifests and apply them to the Hub cluster, triggering Crossplane to provision the Spoke Pool cluster?

---

### 3. Cell Registration & Discovery

**Assumption**: After a Spoke Pool cell is provisioned, it must register itself with the Hub so the Hub knows which cells are available for tenant placement.

**Q7**: Does the Spoke Pool cell self-register with the Hub by writing its metadata (cell-id, capacity, region, status) to the Hub Centralised DB via PostgREST after bootstrap completes?

---

**Assumption**: The Hub's tenant onboarding logic queries available cells from the Hub Centralised DB to determine placement for new Starter tenants.

**Q8**: When a new Starter tenant is onboarded via MCP (`tenant_create`), does the Hub query the Hub Centralised DB for available cells (status=ready, tenant_count < max_capacity) before committing the tenant manifest to GitOps?

---

### 4. Tenant Placement Strategy

**Assumption**: The Hub implements a cell placement algorithm (e.g., round-robin, least-loaded, region-aware) to distribute tenants across available cells.

**Q9**: Is the initial placement strategy "fill cell sequentially until capacity is reached, then provision next cell" (pre-provisioned model), or "provision new cell on-demand when all existing cells are full" (dynamic model)?

---

**Assumption**: Tenant-to-cell mapping is stored in the Hub Centralised DB and used for routing decisions (API Gateway, AgentGateway).

**Q10**: Does the Hub Centralised DB have a `tenant_cell_mapping` table that stores `(tenant_id, cell_id, spoke_cluster_endpoint)` for routing tenant requests to the correct Spoke Pool?

---

### 5. Shared CNPG Cluster Architecture

**Assumption**: The shared CNPG cluster in each Spoke Pool cell uses logical databases (one per tenant) with PostgreSQL RLS for tenant isolation, not separate CNPG clusters per tenant.

**Q11**: Does the Crossplane Composition for Starter tenants create a logical database + role inside the shared CNPG cluster (via `cnpg.io/v1.Database` CR), rather than provisioning a new CNPG Cluster CR per tenant?

---

**Assumption**: The shared CNPG cluster is sized based on the cell's maximum tenant capacity (e.g., 100 tenants → 500 max connections via PgBouncer).

**Q12**: Is the shared CNPG cluster configuration (instance size, replica count, PgBouncer pool size) defined in the SpokePool Composition and scaled based on expected tenant count per cell?

---

### 6. Cell Lifecycle & Scaling

**Assumption**: New cells are provisioned automatically when all existing cells reach capacity, triggered by the Hub's tenant onboarding logic.

**Q13**: When the Hub detects that all available Spoke Pool cells are at capacity (tenant_count >= max_capacity), does it automatically commit a new SpokePool XR manifest to the GitOps repo to provision an additional cell?

---

**Assumption**: Cells can be decommissioned when tenant count drops below a threshold (e.g., cell has <10 tenants due to churn), and remaining tenants are migrated to other cells.

**Q14**: Is cell rebalancing/decommissioning in scope for Phase 1, or is it deferred to a later phase (Phase 2+)?

---

### 7. Edge Catalog Components

**Assumption**: The edge-catalog includes only the minimal components required for cell operation: ArgoCD Agent (tenant workload sync), NATS Leaf Node (billing events), Spire Agent (mTLS identity), and shared CNPG Cluster.

**Q15**: Does the edge-catalog ClusterResourceSet include cert-manager, external-dns, and nginx-ingress (as listed in phases.md), or are those provisioned separately via ArgoCD after the cell is ready?

---

**Assumption**: The ArgoCD Agent in each Spoke Pool cell connects to the Hub's ArgoCD server and pulls ApplicationSets filtered by cell-id label.

**Q16**: Does the ArgoCD Agent use a cell-specific kubeconfig with RBAC limited to namespaces within that cell, and does it filter Applications using a label selector (e.g., `spoke-cell-id=pool-01`)?

---

### 8. Hub-Spoke Communication Patterns

**Assumption**: The Spoke Pool cell uses HubStore (direct PostgreSQL connection to Control Plane Shared DB) for tenant configuration, not LocalStore + NATS sync.

**Q17**: Do Spoke Pool cells use the HubStore pattern (synchronous writes to Hub's Control Plane Shared DB) because they are managed infrastructure, unlike Spoke Silo cells which use LocalStore + NATS for autonomy?

---

**Assumption**: Billing events from Spoke Pool cells flow through NATS Leaf Node → Hub JetStream → Hub Event Router → Hub Centralised DB.

**Q18**: Does the NATS Leaf Node in each Spoke Pool cell publish billing events (e.g., `spoke.{cell-id}.billing.usage`) to the Hub's NATS JetStream, which are then consumed by the Hub Event Router?

---

### 9. Crossplane Composition Structure

**Assumption**: The SpokePool Composition generates a CAPI Cluster CR (Ubuntu + kubeadm + CAPH provider) + ClusterResourceSet (edge-catalog) + ArgoCD Application CR (for tenant workload sync).

**Q19**: Does the SpokePool Composition output include: (1) CAPI Cluster CR, (2) ClusterResourceSet CR with edge-catalog manifests, (3) ArgoCD Application CR pointing to the tenant GitOps repo filtered by cell-id?

---

**Assumption**: The SpokePool XRD schema includes fields for: region (Hetzner datacenter), node_count, node_type, max_tenant_capacity, and cell_id.

**Q20**: Does the SpokePool XRD spec include `spec.region`, `spec.nodePool.count`, `spec.nodePool.instanceType`, `spec.maxTenantCapacity`, and `metadata.labels["cell-id"]`?

---

### 10. Status Sync & Observability

**Assumption**: The Spoke Controller in each Spoke Pool cell watches the shared CNPG Cluster status and writes cell health metrics to the Hub Centralised DB.

**Q21**: Does the Spoke Controller in each cell monitor the CNPG Cluster's `status.conditions` (Ready=True/False) and write cell-level status updates (available_capacity, health_status) to the Hub Centralised DB via PostgREST?

---

**Assumption**: The Hub's Platform Console displays a "Cells" dashboard showing all Spoke Pool cells, their tenant count, capacity, and health status.

**Q22**: Is a cell observability dashboard (showing cell-id, tenant_count, capacity_percentage, cnpg_status, node_health) a required deliverable for Phase 1, or is it deferred to Phase 2 (HeadLamp Integration)?

---

I'll pause here and await your responses to these 22 questions before proceeding with spec generation. Each answer will clarify the technical boundaries and ensure 100% alignment before implementation.

# ANSWERS:

Use this design for reference Hub-Spoke architecture.

A Spoke Pool "cell" is a complete Kubernetes cluster (not a namespace or VPC) that hosts multiple Starter tier tenants with namespace-level isolation.

The cell contains shared infrastructure (CNPG cluster, NATS leaf node, ArgoCD agent) that serves all tenants within that cell.

Cell capacity is determined by a configurable threshold (tenant count, resource utilization, or policy-based limit).

The SpokePool XR (Crossplane Composite Resource) is the declarative API for creating a new cell, similar to how AINativeSaaS XR provisions tenant infrastructure.

CAPI ClusterResourceSet is exclusively used to inject the argocd-agent (Secret Zero). Once the agent connects to the Hub, an ArgoCD Cluster-Generator ApplicationSet deploys the remainder of the edge-catalog.

The Hub's ArgoCD manages the SpokePool XR lifecycle (create/update/delete), and the Spoke's ArgoCD Agent pulls tenant workloads after the cell is ready.

Cell Registration & Discovery:

The hub centralized DB will not be implemented in this pahase. So assume that we only have K8 native API.

**Industry Standard:** Kyverno policy watches CAPI `Cluster` resources and auto-generates ArgoCD cluster secrets.

**How It Works:**

1. CAPI provisions Spoke cluster → creates kubeconfig Secret

2. Kyverno watches CAPI `Cluster` CR

3. Kyverno extracts kubeconfig from CAPI Secret

4. Kyverno generates ArgoCD cluster Secret in `argocd` namespace

5. ArgoCD/Headlamp auto-discovers via Secret label `argocd.argoproj.io/secret-type: cluster`

There should be no custom logic written for cell placement algorithm. Adop Industry Standard tools. 

The shared CNPG cluster in each Spoke Pool cell uses logical databases (one per tenant) with PostgreSQL RLS for tenant isolation, not separate CNPG clusters per tenant.

The shared CNPG cluster is sized based on the cell's maximum tenant capacity (e.g., 100 tenants → 500 max connections via PgBouncer).

New cells are provisioned automatically when all existing cells reach capacity, triggered by the Hub's tenant onboarding logic.

rebalancing/decommissioning is it deferred to a later phase.

The edge-catalog includes only the minimal components required for cell operation: ArgoCD Agent (tenant workload sync) in phase 1.

Hub-Spoke Communication Patterns:

The spoke ArgoCD agent uses mTLS (Mutual TLS) to securely connect to the Hub.

A shared CA is established that both Principal and Agents trust.

Agent (Spoke)                    Principal (Hub)

     │                                │

     │──── gRPC/HTTP2 + mTLS ────────►│

     │    (Client Certificate)        │

     │                                │

     │◄─── Verify & Establish ────────│

     │    (Server Certificate)        │

     │                                │

     │◄──── Bidirectional Stream ────►│

Dont include any features that needs Hub Centralised DB.

Crossplane Composition:

sbt-patterns/docs/crossplane-compositions.md

Observability metrics are always sent to HUB from all spokes. Spoke will never have observibiity tools.