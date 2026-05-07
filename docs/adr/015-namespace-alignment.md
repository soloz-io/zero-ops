# Namespace Alignment

**Status**: Implemented  
**Date**: 2026-05-07  
**Implementation**: Automated sed script + manual verification

## Platform Namespaces (Unified across Hub and Spoke)

| Namespace | Components |
|-----------|------------|
| `platform-capi` | Cluster API resources, cluster-api-operator |
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