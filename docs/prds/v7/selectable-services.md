Here are the updated, v7.0-compliant architectural documents. They reflect the shift to **Edge GitOps**, **OCI Artifacts**, and the **Agentic Control Plane**.

---

# 1. Selectable Services & Edge GitOps

**File:** `docs/architecture/selectable-services.md`
**Status:** APPROVED (Aligned with PRD v7.0)

### 1. The High-Level Concept: Edge GitOps & OCI

In previous iterations, the CLI acted as an installer, pushing YAMLs into a central management cluster. In the v7.0 Agentic architecture, this is replaced by the **Edge GitOps** and **OCI Artifact** pattern.

The platform must scale to 10,000+ clusters. A central ArgoCD cannot manage this. Instead:
1. **The Catalog:** "Services on Top" (Prometheus, Postgres, etc.) are stored in the monorepo's `catalog/` directory.
2. **The Delivery:** CI/CD packages this `catalog/` into a versioned **OCI Artifact** (e.g., `ghcr.io/zero-ops/catalog:v1.5.0`) and publishes it to a registry.
3. **The Edge Agent:** When a tenant cluster is provisioned, Cluster API (via `ClusterResourceSet`) injects a lightweight ArgoCD instance directly into the tenant cluster.
4. **The Sync:** The tenant's local ArgoCD connects to the OCI registry, pulling the specific services the SaaS API authorized for them.

### 2. The Catalog Directory Structure

The monorepo acts as the single source of truth for all available services.

```text
zero-ops/
├── catalog/                    # Packaged as an OCI artifact in CI/CD
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
```

### 3. How a Service gets to a Tenant Cluster

Let's trace what happens when a tenant requests a database via the API or CLI (`zero-ops cluster update my-cluster --database=postgres`):

1. **Intent:** The REST request hits the `zero-ops-api` (SaaS Control Plane).
2. **Validation:** The API checks the tenant's quota and parses the requested service against the known catalog metadata.
3. **Tenant Config Update:** The `zero-ops-api` generates a specific ArgoCD `Application` with ORAS-based overlay composition on top of the base catalog OCI artifact for tenant-specific customization.
4. **Edge Pull:** The ArgoCD instance running *inside* the tenant cluster detects the configuration change. It pulls the base `cloudnative-pg` manifests from the main OCI Catalog Artifact and applies them locally.
5. **Observation:** OpenSearch logs the ArgoCD sync event. VictoriaMetrics registers the new workloads.

### 4. Implementation Guide: Adding a new Service

To add a new service (e.g., **Redis**) to the platform, no Go code needs to be modified:

1. **Create Files:** Add `catalog/databases/redis/service.yaml` and `catalog/databases/redis/install.yaml`.
2. **Merge PR:** The Platform Admin merges the Pull Request into the `main` branch.
3. **CI/CD Pipeline:** GitHub Actions builds a new OCI image: `ghcr.io/zero-ops/catalog:v1.6.0`.
4. **API Update:** The `zero-ops-api` dynamically discovers the new service by reading the OCI image metadata (or the updated local disk if running in the control plane).
5. **Availability:** Tenants can immediately request `--database=redis`.

### 6. Dual Delivery Mechanisms: OCI Artifacts vs Alloy Config Server

The platform uses **two distinct delivery mechanisms** for different types of configuration:

**OCI Artifact Delivery (Application Workloads):**
- **What:** Cilium, CloudNativePG, Prometheus, Redis, etc.
- **How:** Packaged in `catalog/` → OCI registry → ArgoCD pulls → Applied to tenant cluster
- **Latency:** Minutes (requires ArgoCD sync cycle)
- **Use Case:** Application deployment and updates

**Alloy Config Server Delivery (Observability Configuration):**
- **What:** Grafana Alloy scrape configs, forwarding rules, relabeling rules
- **How:** Central config server → Alloy instances pull directly → Hot reload
- **Latency:** Seconds (immediate propagation to 10,000+ clusters)
- **Use Case:** Observability configuration changes

**Critical Distinction:** A change to PostgreSQL version uses OCI artifacts. A change to PostgreSQL metrics scraping interval uses the Alloy config server. These are separate update paths with different guarantees.

### 7. Why this is the "Idiomatic Way" for Fleet Scale
* **No Central Bottleneck:** `zero-ops-api` is completely decoupled from the actual application of YAMLs. If the SaaS API goes down, ArgoCD in the tenant cluster keeps functioning and reconciling against the OCI registry.
* **Version Control:** Upgrading a service fleet-wide (e.g., patching a CVE in Cilium) is achieved by the `UpgradeAgent` telling tenant ArgoCD instances to point to `catalog:v1.6.1` instead of `v1.6.0`.

---
