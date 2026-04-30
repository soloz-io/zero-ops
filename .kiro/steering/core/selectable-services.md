---
inclusion: always
---

Here are the updated, v9.0-compliant architectural documents. They reflect the shift to **Hub-Spoke Architecture**, **Distributed Identity**, **PostgREST + NATS State Sync**, and the **Agentic Control Plane**.

---

# 1. Selectable Services & Hub-Spoke GitOps

**File:** `docs/architecture/selectable-services.md`
**Status:** APPROVED (Aligned with PRD v9.0)

### 1. The High-Level Concept: Hub-Spoke GitOps & OCI

In the v9.0 hub-spoke architecture, the platform scales to 10,000+ clusters through distributed GitOps delivery. The Hub manages the catalog and tenant configurations, while Spokes pull and apply autonomously.

**The Three-Tier Delivery Model:**

1. **The Catalog:** Services (Cilium, CNPG, Prometheus, etc.) are stored in the monorepo's `catalog/` and `manifests/spoke-catalog/` directories.

2. **The Delivery:** CI/CD packages catalogs into versioned **OCI Artifacts** (e.g., `ghcr.io/zero-ops/catalog:v1.5.0`) and publishes to registry.

3. **The Spoke GitOps Agents:**
   - **Spoke Pool (Starter Tier):** ArgoCD Agent connects to Hub, pulls authorized services for multiple tenants
   - **Spoke Silo (Enterprise Autopilot):** ArgoCD Agent connects to Hub, pulls services for single tenant
   - **Spoke Silo (Enterprise Self-Managed):** Full ArgoCD instance, tenant manages their own GitOps control plane

4. **The Sync:** Spoke GitOps agents connect to the OCI registry, pulling the specific services the Hub API authorized for them. Cluster API (via `ClusterResourceSet`) injects the GitOps agent during cluster bootstrap.

### 2. The Catalog Directory Structure

The monorepo acts as the single source of truth for all available services.

```text
zero-ops/
├── manifests/spoke-catalog/     # Injected at cluster bootstrap via ClusterResourceSet
│   ├── argocd-agent.yaml        # ArgoCD Agent (Starter + Enterprise Autopilot)
│   ├── grafana-alloy.yaml       # Metrics collection
│   ├── kube-events-exporter.yaml
│   ├── k8sgpt-operator.yaml
│   ├── cnpg2monitor.yaml       # Fleet-wide CNPG monitoring
│   ├── cert-manager.yaml
│   ├── external-dns.yaml
│   ├── nginx-ingress.yaml
│   └── spoke-controller.yaml   # NEW v9.0 — Status sync to Hub
│
├── catalog/                    # Packaged as OCI artifact, pulled by ArgoCD
│   ├── cni/
│   │   └── cilium/
│   │       ├── service.yaml    # Metadata (name, tier, dependencies)
│   │       └── install.yaml    # HelmRelease or raw manifests
│   ├── databases/
│   │   └── cloudnative-pg/
│   │       ├── service.yaml
│   │       └── install.yaml
│   └── observability/
│       └── prometheus/
│           ├── service.yaml
│           └── install.yaml
```

**`service.yaml` Example:**
```yaml
name: cloudnative-pg
version: 1.22.0
category: databases
description: Production-grade PostgreSQL operator
tier: recommended
dependencies: ["cilium", "hcloud-csi"]
spoke_types: ["pool", "silo"]  # NEW v9.0 — Which spoke types can use this
```

### 3. How a Service gets to a Spoke Cluster

Let's trace what happens when a tenant requests a database via the API or MCP client (`environment_create` with database spec):

1. **Intent:** The MCP request hits AgentGateway → mcp-server (Hub).

2. **Validation:** The mcp-server checks the tenant's quota and parses the requested service against the known catalog metadata.

3. **Tenant Config Update:** The mcp-server commits an `AINativeSaaS` CR to the tenant's control plane repository with database specifications.

4. **Crossplane Reconciliation:** Crossplane (Hub) detects the CR, expands Composition B (Enterprise), provisions:
   - CAPI Cluster CR (Ubuntu kubeadm, CAPH)
   - CNPG Cluster CR (HA, pgvector, PgBouncer)
   - ArgoCD Application CR pointing to OCI catalog

5. **Spoke Bootstrap:** CAPI provisions the cluster, injects manifests/spoke-catalog via ClusterResourceSet. ArgoCD Agent starts, connects to Hub.

6. **Edge Pull:** The ArgoCD Agent running in the Spoke Silo detects the Application CR. It pulls the `cloudnative-pg` manifests from the OCI Catalog Artifact and applies them locally.

7. **Status Sync:** Spoke Controller watches the CNPG Cluster CR conditions. On Ready=True, writes status directly to Hub Centralised DB via Hub-side PostgREST (`POST /resource-status`, Bearer JWT).

8. **Observation:**
   - Hub Event Router processes NATS events for billing (spoke.{tenant}.billing.usage → Hub Centralised DB)
   - Grafana Alloy remote_write → VictoriaMetrics (metrics) and Loki (logs)
   - kube-events-exporter writes the ArgoCD sync event to OpenSearch `argocd-syncs` index
     (OpenSearch handles structured event indexes only — not general log aggregation)

### 4. Implementation Guide: Adding a new Service

To add a new service (e.g., **Redis**) to the platform, no Go code needs to be modified:

1. **Create Files:** Add `catalog/databases/redis/service.yaml` and `catalog/databases/redis/install.yaml`.

2. **Specify Spoke Types:** In `service.yaml`, set `spoke_types: ["pool", "silo"]` to indicate which spoke types can use this service.

3. **Merge PR:** The Platform Admin merges the Pull Request into the `main` branch.

4. **CI/CD Pipeline:** GitHub Actions builds a new OCI image: `ghcr.io/zero-ops/catalog:v1.6.0`.

5. **API Update:** The mcp-server dynamically discovers the new service by reading the OCI image metadata.

6. **Availability:** Tenants can immediately request Redis via MCP or API.

### 5. Hub-Spoke GitOps Modes

The platform supports three GitOps deployment patterns based on tenant tier and preference:

**Mode 1: Spoke Pool (Starter Tier)**
- **ArgoCD Agent** (shared, multi-tenant)
- Connects to Hub, pulls catalog for multiple tenants
- Namespace-level isolation
- Zero-Ops manages all GitOps operations

**Mode 2: Spoke Silo Autopilot (Enterprise Default)**
- **ArgoCD Agent** (dedicated, single-tenant)
- Connects to Hub, pulls catalog for single tenant
- Physical cluster isolation
- Zero-Ops manages GitOps, tenant approves changes via PR

**Mode 3: Spoke Silo Self-Managed (Enterprise Option)**
- **Full ArgoCD Instance** (dedicated, single-tenant)
- Tenant manages their own GitOps control plane
- Complete autonomy, Zero-Ops provides catalog only
- Tenant handles ArgoCD updates, policies, troubleshooting

Controlled by `spec.gitops.mode: autopilot|self-managed` in AINativeSaaS XRD.

### 6. Dual Delivery Mechanisms: OCI Artifacts vs Alloy Config Server

The platform uses **two distinct delivery mechanisms** for different types of configuration:

**OCI Artifact Delivery (Application Workloads):**
- **What:** Cilium, CloudNativePG, Prometheus, Redis, etc.
- **How:** Packaged in `catalog/` → OCI registry → ArgoCD pulls → Applied to spoke cluster
- **Latency:** Minutes (requires ArgoCD sync cycle)
- **Use Case:** Application deployment and updates

**Alloy Config Server Delivery (Observability Configuration):**
- **What:** Grafana Alloy scrape configs, forwarding rules, relabeling rules
- **How:** Central config server → Alloy instances pull directly → Hot reload
- **Latency:** Seconds (immediate propagation to 10,000+ clusters)
- **Use Case:** Observability configuration changes

**Critical Distinction:** A change to PostgreSQL version uses OCI artifacts. A change to PostgreSQL metrics scraping interval uses the Alloy config server. These are separate update paths with different guarantees.

### 7. Hub-Spoke State Synchronization Patterns

**Synchronous Status Updates (Spoke Controller → Hub):**
- Spoke Controller watches Crossplane claim conditions
- Derives platform status (provisioning/ready/failed/deleting)
- Writes directly to Hub Centralised DB via Hub-side PostgREST
- Uses per-spoke JWT (Hydra client_credentials grant)
- Controller-runtime native retry with exponential backoff

**User Management (Spoke Pool):**
- kube-sbt User_Manager connects to per-tenant PostgREST in Spoke Pool
- PostgREST exposes tenant's app plane logical DB with RLS
- JWT claims enforce user_id isolation (request.jwt.claims->>'user_id')
- Accessed via AgentGateway for mTLS and routing

**Asynchronous Event Flow (NATS):**
- Billing events: `spoke.{tenant-id}.billing.usage` → Hub Event Router → Hub Centralised DB
- Lifecycle events: `spoke.{tenant-id}.lifecycle.>` → Hub workflows
- Notifications: `spoke.{tenant-id}.notifications.>` → notification service
- Spoke Silo state sync: Local Tenant Control Plane DB → NATS Leaf Node → Hub Event Router → Control Plane Shared DB

**Usage Metering (OTLP):**
- AgentGateway emits OTLP traces to OpenMeter (Hub) for every authenticated request
- Meters: api_calls, storage_operations, custom_api_calls, developer_api_calls
- Span attributes: tenant_id, user_id, meter_id
- Batched emission (max 100 events), async non-blocking
- Local buffering (1-hour retention) when OpenMeter unreachable

**Observability Push (Alloy):**
- All spokes run Grafana Alloy
- Metrics pushed to VictoriaMetrics (Hub) via remote_write
- Logs pushed to Loki (Hub)
- No NATS involvement in metrics/logs pipeline

### 8. Why this is the "Idiomatic Way" for Hub-Spoke Fleet Scale

**No Central Bottleneck:** mcp-server is completely decoupled from the actual application of YAMLs. If the Hub goes down:
- Spoke Pool clusters continue running (no new provisioning)
- Spoke Silo clusters operate fully independently (local Ory stack, local DB, NATS buffers events)
- ArgoCD in spokes keeps reconciling against the OCI registry

**Version Control:** Upgrading a service fleet-wide (e.g., patching a CVE in Cilium) is achieved by the UpgradeAgent updating tenant ArgoCD Applications to point to `catalog:v1.6.1` instead of `v1.6.0`.

**Physical Isolation:** Enterprise Spoke Silos run in tenant's own Hetzner account (BYOC). Complete network, compute, and data isolation from other tenants.

**Eventual Consistency:** Spoke Silo state syncs to Hub via NATS with eventual consistency guarantees. If Hub is unreachable, Silo operates independently and syncs when Hub reconnects.

---
