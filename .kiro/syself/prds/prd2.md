# Product Requirements Document: Zero-Ops Infrastructure Platform

**Version:** 2.0 (Final Architecture)
**Status:** DRAFT
**Project Name:** zero-ops
**Repository Model:** Monorepo
**Author:** Product Architect / Platform Engineering Lead

---

## 1. Executive Summary
**Zero-Ops** is an opinionated, multi-tenant Kubernetes SaaS platform designed to democratize access to production-grade infrastructure via a "Bring Your Own Cloud" (BYOC) model. It replaces bespoke IOC scripts and manual operations with a centralized **Management Cluster** ("The Mothership") driven by **Cluster API (CAPI)**.

Targeting **Platform Engineers** (SaaS Admins) and **Tenant Developers** (Customers), Zero-Ops delivers a **Thin Client CLI** that abstracts complex infrastructure into simple, topology-aware intents. By leveraging a Monorepo architecture with embedded `ClusterClass` blueprints and a service catalog, the platform ensures Zero-Touch provisioning, automated Day-2 operations, and strict tenant isolation on Hetzner (MVP) and future cloud providers.

---

## 2. Problem Statement

### 2.1 Current State
Provisioning Kubernetes currently relies on "Fat Client" workflows: developers running imperative tools (`clusterctl`, `terraform`, `ssh`) on local machines. State is fragmented across `kubeconfig` files and local directories. There is no centralized control plane to enforce policy, manage upgrades, or offer "Services on Top" (e.g., Managed Postgres, Observability) uniformly.

### 2.2 Gap & Motivation
To scale as a SaaS, infrastructure logic must move off the developer's laptop and into a managed control plane.
*   **Missing:** A **Unified Monorepo** ensuring the CLI, API, and Infrastructure Templates are always version-synced.
*   **Missing:** A **Management Cluster** acting as the single source of truth for all tenant clusters.
*   **Missing:** **ClusterClass topologies** to standardize "Production" vs "Development" cluster shapes.
*   **Missing:** A **Service Catalog** ("Embed & Overlay") to allow tenants to easily add managed services via CLI flags.

### 2.3 Constraints & Non-Goals
*   **Thin Client Principle:** The CLI must be stateless. It acts purely as a manifesto generator and API client. It uses `go:embed` to ship templates, removing dependencies on external git cloning during runtime.
*   **BYOC:** Tenants pay for compute. We manage the Control Plane; they provide the Cloud API Token.
*   **Immutable Infrastructure:** All provisioning is declarative via Kubernetes CRDs (`Cluster`, `ClusterClass`).

---

## 3. User Personas & User Journeys

### 3.1 Personas
*   **Platform Admin:** Owns the `zero-ops` Monorepo and the Management Cluster. Responsible for defining `ClusterClasses` (Blueprints) and vetting the Service Catalog.
*   **Tenant Developer:** Consumes the platform. Provides cloud credentials and requests clusters/services based on the catalog.

### 3.2 End-to-End User Journeys

#### Journey A: Platform Bootstrap (Admin)
*   **Trigger:** Initial SaaS setup.
*   **Action:** Admin runs `zero-ops mgmt bootstrap` locally.
*   **Response:** The CLI spins up a local Kind cluster, installs CAPI/CAPH, provisions a persistent Management Cluster on Hetzner, moves the state to the cloud, and installs the `ClusterClass` definitions.
*   **Outcome:** A self-hosted "Mothership" is ready to accept API requests.

#### Journey B: Tenant Onboarding (Admin/API)
*   **Trigger:** New customer signup.
*   **Action:** Admin runs `zero-ops tenant onboard --name=acme`.
*   **Response:** Platform creates a dedicated Kubernetes Namespace (`tenant-acme`) with RBAC constraints.
*   **Outcome:** Secure isolation boundary established.

#### Journey C: Cluster Provisioning (Tenant)
*   **Trigger:** Tenant needs a production cluster on Hetzner.
*   **Action:** Tenant runs `zero-ops cluster create --name=prod-1 --class=hetzner-prod-v1 --token=...`.
*   **Response:** CLI encrypts the token into a Secret, generates a topology-aware `Cluster` manifest, and applies it to the Management Cluster.
*   **Outcome:** CAPI reconciles the infrastructure; CAPH creates servers; a kubeconfig is generated.

#### Journey D: Service Injection (Day 0/2)
*   **Trigger:** Tenant needs observability and a database.
*   **Action:** Tenant runs `zero-ops cluster update --monitoring=prometheus --db=postgres`.
*   **Response:** CLI looks up the adapters in the internal Registry, generates the required ArgoCD Application/Flux manifests from the embedded Catalog, and applies them.
*   **Outcome:** Services are deployed automatically to the tenant cluster.

---

## 4. Proposed Architecture

### 4.1 High-Level Flow
```mermaid
graph TD
    CLI[zero-ops CLI (Thin)] -->|1. Generate from Embed| Manifests[K8s Manifests]
    CLI -->|2. Apply| MgmtAPI[Management Cluster API]
    
    subgraph "SaaS Control Plane (Management Cluster)"
        MgmtAPI -->|3. Persist| Etcd
        CAPI[CAPI Controller] -->|4. Watch| ClusterCR[Cluster Resource]
        CAPI -->|5. Hydrate| Class[ClusterClass CRD]
        CAPI -->|6. Delegate| CAPH[Hetzner Provider]
        Argo[ArgoCD] -->|8. Watch| Secret[Cluster Kubeconfig]
    end
    
    subgraph "Tenant Infrastructure (Hetzner)"
        CAPH -->|7. Provision| VMs[Virtual Machines]
        LB[Load Balancer]
    end
    
    Argo -->|9. Sync Catalog Apps| VMs
```

### 4.2 Monorepo Structure
This structure enforces the "Code + Config" unity required for a reliable SaaS.

```text
zero-ops/
├── cmd/
│   └── zero-ops/               # The Thin Client Entrypoint
├── pkg/
│   ├── capability/             # Interfaces (ClusterProvisioner, Installer)
│   ├── registry/               # Adapter Pattern (Selection Logic)
│   └── adapter/                # Implementations (Hetzner, AWS, ArgoCD)
├── manifests/                  # "SYSELF AUTOPILOT" (Infrastructure)
│   ├── core/                   # CAPI/CAPH System Manifests
│   └── classes/                # ClusterClass Definitions (The "Product")
├── catalog/                    # "SERVICES ON TOP" (Add-ons)
│   ├── cni/                    # Cilium/Calico Helm values
│   ├── observability/          # Prometheus/Grafana
│   └── databases/              # Postgres Operator
├── internal/assets/            # go:embed logic
└── go.mod
```

### 4.3 Integration & Control Plane
*   **Provisioning:** Powered by **Cluster API**. We do not script VM creation; we declare `MachineDeployments`.
*   **Delivery:** Powered by **ArgoCD** (or Flux). The Management Cluster runs ArgoCD. When CAPI creates a tenant cluster, `capi2argo` (or a custom operator) registers it as a remote destination.
*   **Networking:** The Management Cluster API is exposed publicly (secured via MTLS/OIDC). The CLI talks to this API.

---

## 5. Technical Specifications

### 5.1 Platform Changes
*   **Bootstrapper:** The CLI must include a `bootstrap` command that orchestrates the "Kind to Cloud" pivot.
*   **ClusterClass Strategy:** We will maintain versioned `ClusterClass` files in `manifests/classes/`.
    *   *Example:* `hetzner-prod-v1.yaml` (HA Control Plane, Private Network, PVC support).

### 5.2 CLI Technical Design
*   **Language:** Go (Cobra).
*   **Packaging:** **`go:embed`** is used to compile `manifests/` and `catalog/` into the binary.
*   **Registry Pattern:**
    *   Interface: `Installer.GenerateManifest(config)`
    *   Implementations: `pkg/adapter/gitops/argocd`, `pkg/adapter/infra/hetzner`.
    *   Runtime: The CLI flags (`--provider`, `--gitops`) select the correct implementation from the Registry.

### 5.3 Defaulting & Automation
*   **Secret Management:** CLI accepts tokens via ENV or Flag, creates a Sealed/Opaque Secret in the Management Cluster, and references it in the `Cluster` CRD. The CLI **never** stores the token locally.
*   **Namespaces:** The CLI creates resources *only* in the namespace matching the tenant context.

### 5.4 Security, Compliance & Reliability
*   **Multi-Tenancy:** Achieved via Kubernetes Namespaces. Tenant A cannot read Tenant B's Secrets.
*   **Network:** Tenant Clusters are air-gapped from the Management Cluster. They connect *out* to the Management Cluster (or ArgoCD connects *in* via Kube API).
*   **Versioning:** The CLI version determines the Infrastructure version. (e.g., CLI v1.2 deploys CAPI Templates v1.2).

---

## 6. User Journey Deep Dives (Scenario-Based)

### Scenario: The "Zero-Ops" Cluster Creation
**Scenario:** A Tenant wants a standard production cluster with monitoring enabled.
**Actors:** Tenant, CLI, Management Cluster.

**Step-by-Step Flow:**
1.  **Input:** User runs: `zero-ops cluster create --name=api-prod --class=hetzner-prod-v1 --monitoring=true`.
2.  **CLI (Internal):**
    *   Registry selects `hetzner` infrastructure adapter.
    *   Registry selects `prometheus` observability adapter (due to `--monitoring`).
3.  **CLI (Asset Loading):**
    *   Loads `manifests/classes/hetzner-prod-v1.yaml` from embedded FS.
    *   Loads `catalog/observability/prometheus/values.yaml` from embedded FS.
4.  **CLI (Generation):**
    *   Generates a CAPI `Cluster` object referencing the class.
    *   Generates a `ClusterResourceSet` (or ArgoCD `Application`) containing the Prometheus config.
5.  **CLI (Execution):**
    *   Connects to Management Cluster.
    *   Applies generated YAMLs to `tenant-namespace`.
6.  **Platform (Async):**
    *   CAPI sees `Cluster`, provisions Hetzner resources.
    *   Once Ready, CAPI writes `kubeconfig` Secret.
    *   ArgoCD detects `kubeconfig`, connects to cluster, installs Prometheus.
7.  **Feedback:** CLI polls status and returns "Cluster Provisioned & Monitoring Active".

---

## 7. Success Criteria & Acceptance Tests

### 7.1 Functional
- [ ] **Single Binary:** The `zero-ops` binary must work on a fresh machine without cloning the repo (verified via `go:embed`).
- [ ] **Bootstrap:** `zero-ops mgmt bootstrap` successfully pivots from Kind to a permanent Hetzner cluster.
- [ ] **Topology:** Changes to `manifests/classes/*.yaml` in the repo are reflected in new clusters after a CLI rebuild/update.
- [ ] **Service Catalog:** Using `--monitoring` successfully deploys the catalogued Prometheus stack to the target cluster via GitOps.

### 7.2 Architecture Verification
- [ ] **Thin Client:** The CLI code contains **no** Terraform calls, Ansible playbooks, or SSH logic. It only generates and applies Kubernetes YAMLs.
- [ ] **Monorepo:** A single PR can update a `ClusterClass` definition and the CLI validation logic simultaneously.

### 7.3 Multi-Tenancy
- [ ] **Isolation:** Resources for Tenant A created via CLI are invisible to Tenant B (verified via RBAC simulation).