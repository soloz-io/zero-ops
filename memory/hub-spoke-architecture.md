---
purpose: Hub-Spoke architecture patterns, fleet management, and tenant provisioning
scope: Hub cluster infrastructure, spoke provisioning, fleet registry, ArgoCD agents, observability
topics: [hub-spoke-pattern, fleet-registry, argocd-agents, spoke-provisioning, tenant-onboarding]
update_criteria: Hub-spoke architecture changes, fleet management patterns, spoke provisioning decisions
---

# Hub-Spoke Architecture

## Hub Cluster (Management Cluster)

### Current Infrastructure
- **Cluster Name:** `mothership`
- **Nodes:** 3 control plane + 2 workers
- **Provider:** Hetzner Cloud
- **OS:** Ubuntu 24.04 LTS
- **Kubernetes:** v1.31.6
- **Container Runtime:** containerd 1.7.26

### Installed Components
- **CAPI Stack** (namespace: `capi-operator-system`):
  - capi-controller-manager (Cluster API core)
  - caph-controller-manager (Hetzner provider)
  - capi-kubeadm-bootstrap-controller-manager
  - capi-kubeadm-control-plane-controller-manager
  - capi-operator-controller-manager
- **ArgoCD** (namespace: `argocd`):
  - argocd-application-controller
  - argocd-applicationset-controller
  - argocd-repo-server
  - argocd-server
  - argocd-dex-server
  - argocd-redis
  - argocd-notifications-controller
- **CloudNativePG** (namespace: `cnpg-system`):
  - cnpg-cloudnative-pg operator
- **capi2argo** (namespace: `capi2argo-system`):
  - capi2argo-controller-manager (CAPI to ArgoCD integration)

### ClusterClass Templates
- **Existing:** `hetzner-mgmt-ubuntu-v1` (in zero-ops-system namespace)
- **Needed for MVP:**
  - `hetzner-prod-ubuntu-v1` (3 CP + auto-scaling workers, production spoke)
  - `hetzner-staging-ubuntu-v1` (3 CP + min 1 worker, staging spoke)

### Hub Bootstrap Implementation
- **Location:** `zero-ops/internal/hub/`
- **Orchestrator:** `bootstrap/orchestrator.go`
  - Phases: Bootstrap → CAPI Init → Cluster Provision → Pivot → ClusterClass Deploy → PostBoot
  - Recovery/resume capability via state manager
  - Idempotent operations throughout
- **Component Installer:** `components/installer.go`
  - Installs ArgoCD via Helm (version 5.51.6)
  - Installs capi2argo for CAPI→ArgoCD integration
  - Installs CloudNativePG operator
  - Installs Hetzner CSI driver
- **ArgoCD Integration:** ArgoCD IS part of hub orchestration (installed in PostBoot phase)

### Missing Components (Phase 1 MVP)
- **VictoriaMetrics:** Metrics storage and querying (not installed)
- **Grafana:** Dashboards and visualization (not installed)
- **Fleet Registry:** Git repository structure for tenant descriptors
- **Tenant Onboarding MCP Server:** API for tenant lifecycle management

## Spoke Cluster Architecture

### Provisioning Pattern
- **Declarative:** CAPI Cluster resources committed to Git
- **ClusterClass-based:** Use templates for consistent topology
- **HA Control Plane:** 3 master nodes across 3 availability zones
- **Auto-scaling Workers:** Worker pools with min/max replicas
- **Network Isolation:** Private network per tenant (dedicated spoke)

### Spoke Components (to be deployed)
- **ArgoCD Agent:** Lightweight agent connecting to hub ArgoCD
- **Grafana Alloy:** Metrics collection and forwarding to hub
- **Tenant Workloads:** Control plane + application plane

## Fleet Registry Pattern

### Proposed Structure (Monorepo)
```
zero-ops/
├── fleet-registry/
│   ├── tenants/
│   │   ├── tenant-acme.yaml
│   │   ├── tenant-globex.yaml
│   │   └── ...
│   ├── applicationsets/
│   │   ├── control-plane-appset.yaml
│   │   └── app-plane-appset.yaml
│   └── README.md
```

### Tenant Descriptor Schema
```yaml
apiVersion: fleet.zero-ops.io/v1alpha1
kind: TenantDescriptor
metadata:
  name: tenant-acme
spec:
  tenantId: acme
  orgName: Acme Corporation
  plan: dedicated  # free, shared, dedicated
  features:
    - ai-runtime
    - vector-search
  controlPlaneRepo:
    url: https://github.com/acme/control-plane
    path: manifests
    targetRevision: main
  appPlaneRepo:
    url: https://github.com/acme/app-plane
    path: manifests
    targetRevision: main
  clusterRef: spoke-acme
  environment: production  # production, staging
```

### ApplicationSet Pattern
- **Git Generator:** Watches `fleet-registry/tenants/*.yaml`
- **Auto-Discovery:** New tenant descriptors trigger Application creation
- **Control Plane ApplicationSet:** Deploys tenant control plane services
- **App Plane ApplicationSet:** Deploys tenant application workloads

## GitOps Flow (Declarative)

### Imperative Layer (Thin)
1. Create tenant record in PostgreSQL
2. Create/register tenant Git repositories
3. Commit tenant descriptor to `fleet-registry/tenants/`

### Declarative Layer (Controllers)
1. ApplicationSet detects new tenant descriptor
2. Generates ArgoCD Application resources
3. CAPI provisions spoke cluster (if dedicated)
4. ArgoCD agent deployed to spoke cluster
5. Agent syncs control plane + app plane manifests
6. Tenant transitions to READY state

## Observability Architecture

### Hub Metrics (VictoriaMetrics)
- Tenant count by plan (free/shared/dedicated)
- Spoke cluster health (Ready/Degraded/Failed)
- ArgoCD sync status (Synced/OutOfSync/Unknown)
- Resource utilization (CPU/memory/storage)

### Spoke Metrics Collection
- **Grafana Alloy:** Lightweight agent on each spoke
- **Push Pattern:** Spokes push metrics to hub VictoriaMetrics
- **Buffering:** Alloy buffers metrics when hub unreachable
- **mTLS:** Secure spoke-to-hub communication

### Dashboards (Grafana)
- Platform health overview
- Tenant status by plan
- Cluster health by region
- ArgoCD sync status
- Resource utilization trends

## Tenant Onboarding Flow

### Phase 1 MVP (Manual Repo Creation)
1. Platform admin calls MCP tool: `create_tenant`
2. MCP server validates tenant metadata
3. MCP server creates tenant record in PostgreSQL
4. Platform admin manually creates GitHub repos (control + app plane)
5. MCP server commits tenant descriptor to fleet-registry
6. ApplicationSets auto-discover and provision

### Phase 2 (Automated Repo Creation)
1. MCP server creates GitHub repos from templates
2. MCP server initializes repos with starter manifests
3. Rest of flow same as Phase 1

## Security Patterns

### mTLS for ArgoCD Agents
- **Certificate Authority:** cert-manager with self-signed CA
- **Agent Certificates:** Unique cert per spoke cluster
- **Certificate Rotation:** Automated via cert-manager
- **Hub Verification:** Hub ArgoCD verifies agent certificates

### Network Isolation
- **Dedicated Spokes:** Private network per tenant
- **Shared Spokes:** Namespace isolation + NetworkPolicies
- **Hub-to-Spoke:** No direct connections (agents pull from hub)
- **Spoke-to-Hub:** mTLS authenticated connections only

## Architectural Principles

### GitOps-First
- All provisioning via Git commits
- No imperative kubectl commands
- ArgoCD reconciles desired state
- Audit trail via Git history

### Declarative Provisioning
- CAPI Cluster resources define infrastructure
- ApplicationSets define application deployment
- Controllers converge to desired state
- No orchestration scripts

### Hub-Spoke Communication
- **Pull Model:** Agents pull from hub (no hub-to-spoke push)
- **Async Pattern:** Hub publishes events, spokes consume
- **Resilient:** Spokes work independently when hub unavailable
- **Secure:** mTLS everywhere, zero-trust

### Tenant Isolation
- **Dedicated Spoke:** 1 tenant = 1 cluster (Enterprise tier)
- **Shared Spoke:** Multi-tenant control plane, namespace isolation (Starter tier)
- **Network Policies:** Enforce traffic isolation
- **RBAC:** Tenant-specific access controls
