# Namespace Alignment

**Status**: Accepted (Updated)  
**Date**: 2026-05-11  
**Implementation**: Automated sed script + manual verification

## Context

During the implementation of Spoke cluster provisioning, we identified a hard security boundary enforced by Cluster API (CAPI) `ClusterResourceSet` controller. CAPI strictly prohibits cross-namespace references for bootstrap templates (`Secrets` and `ConfigMaps`) to prevent privilege escalation and unauthorized manifest injection across tenant boundaries.

## Platform Namespaces (Unified across Hub and Spoke)

| Namespace | Components |
|-----------|------------|
| `platform-capi` | Cluster API resources, cluster-api-operator, Crossplane composite resources (SpokePool), AND all associated bootstrap templates (ClusterResourceSet dependencies, CNI/CCM payloads, and bootstrap CA secrets) |
| `platform-messaging` | NATS subscriber (Hub), NATS Leaf Node (Spoke) |
| `platform-identity` | Kratos, Keto, Hydra, kratos-ui |
| `platform-data` | PostgreSQL clusters, PgBouncer, Redis, CloudNativePG Operator |
| `platform-ops` | ArgoCD/ArgoCD Agent, Argo-repo-Server, capi2argo, ESO, Crossplane, KEDA, cnpg2monitor, External-DNS, kube-sbt API |
| `platform-security` | Infisical, SPIFFE/SPIRE, spire-k8s-registrar |
| `platform-edge` | AgentGateway, auth-proxy, Ingress NGINX |
| `platform-network` | Cilium |
| `platform-observability` | VictoriaMetrics (Operator & Cluster), Grafana Alloy, VictoriaMetrics Alerts/Rules, Prometheus Operator |
| `platform-controlplane` | MCP Server, AgentRegistry |
| `platform-billing` | OpenMeter (metering & billing engine) |
| `kube-system` | Hetzner CCM (Cloud Controller Manager), Hetzner CSI (Container Storage Interface), Cilium CNI, CoreDNS, kube-proxy |

## Decision -> Namespace Definitions

Update of definition of `platform-capi` to explicitly include bootstrap assets:
*   **`platform-capi`**: Owns all Cluster API (CAPI) resources (`Cluster`, `MachinePool`, `ClusterClass`), Crossplane composite resources (`SpokePool`), **AND all associated bootstrap templates (`ClusterResourceSet` dependencies, CNI/CCM payloads, and bootstrap CA secrets)**.

## Decision -> Security Boundaries

*   **CAPI Federation Boundary:** Any asset that needs to be injected into a Spoke cluster at bootstrap time (Phase 1) via `ClusterResourceSet` **must** reside in the `platform-capi` namespace. This includes static Helm charts converted to YAML, ArgoCD Agent configurations, and Kyverno-transformed TLS certificates. `platform-ops` remains as boundary for Day-2 continuous reconciliation tools, but Day-0 bootstrap payloads belong exclusively to `platform-capi`.

## Upstream Namespaces

| Namespace | Components | Notes |
|-----------|------------|-------|
| `cert-manager` | Cert-Manager | Upstream default namespace |
| `cnpg-system` | CloudNativePG Operator | Upstream default namespace |

## Tenant Namespaces (Spoke Only)

| Namespace | Components | Notes |
|-----------|------------|-------|
| `tenant-<id>` | AINativeSaaS XR, PostgREST, AtlasMigration, tenant credentials Secret | One namespace per tenant; e.g. `tenant-app-creator` |

## Topology Labels

All platform namespaces include topology labels for multi-cluster routing:

```yaml
metadata:
  labels:
    topology.platform.io/role: hub|spoke
    topology.platform.io/cell-id: <spoke-pool-id>  # Spoke only
```

## Notes

- **Unified Naming**: Hub and Spoke use identical namespace names (`platform-*`) for simplified multi-cluster operations
- **kube-system Consolidation**: CCM, CSI, and CNI use standard `kube-system` namespace per Kubernetes conventions
- **Topology-Aware**: Labels enable Cilium ClusterMesh and SPIFFE federation across clusters
- **ArgoCD App Naming**: Application files use role prefix (`hub-*`, `spoke-*`, `tenant-*`) while namespaces use `platform-*`