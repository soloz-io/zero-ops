# Namespace Alignment

## Hub Platform Namespaces

| Namespace | Components |
|-----------|------------|
| `hub-platform-capi` | Cluster API resources, cluster-api-operator |
| `hub-platform-messaging` | NATS subscriber |
| `hub-platform-identity` | Kratos, Keto, Hydra, kratos-ui |
| `hub-platform-data` | PostgreSQL clusters, PgBouncer, Redis, CloudNativePG Operator |
| `hub-platform-ops` | ArgoCD, Argo-repo-Server, capi2argo, ESO, Crossplane, KEDA, cnpg2monitor, External-DNS |
| `hub-platform-security` | Infisical, SPIFFE/SPIRE, spire-k8s-registrar |
| `hub-platform-edge` | AgentGateway, auth-proxy, Ingress NGINX |
| `hub-platform-network` | Cilium |
| `hub-platform-observability` | VictoriaMetrics (Operator & Cluster), Grafana Alloy, VictoriaMetrics Alerts/Rules, Prometheus Operator |
| `hub-platform-apps` | MCP Server, AgentRegistry |
| `hub-cloud-system` | Hetzner CCM (Cloud Controller Manager), Hetzner CSI (Container Storage Interface) |

## Upstream Namespaces

| Namespace | Components | Notes |
|-----------|------------|-------|
| `cert-manager` | Cert-Manager | Upstream default namespace |
| `cnpg-system` | CloudNativePG Operator | Upstream default namespace |

## Spoke Pool Namespaces

| Namespace | Components | Notes |
|-----------|------------|-------|
| `kube-system` | Hetzner CCM, Hetzner CSI, Cilium CNI, CoreDNS, kube-proxy | Standard Kubernetes system namespace for spoke clusters |


| Namespace | Components | Notes |
|-----------|------------|-------|
| `spoke-platform-ops` | ArgoCD Agent, Crossplane (local), provider-sql, provider-kubernetes | Platform operators for the cell |
| `spoke-platform-data` | Shared CNPG Cluster, CNPG Operator, per-tenant Poolers, crossplane-admin-credentials Secret, bootstrap Job | Database layer for the cell. Includes Crossplane DB credentials (exception: colocated with workloads due to K8s secretKeyRef namespace boundary) |
| `spoke-platform-messaging` | NATS Leaf Node | Event messaging for the cell |
| `spoke-platform-observability` | Grafana Alloy, metrics collection | Observability for the cell |
| `tenant-<id>` | AINativeSaaS XR, PostgREST, AtlasMigration, tenant credentials Secret | One namespace per tenant; e.g. `tenant-app-creator` |

## Notes

Namespace: hub-platform-capi
├── Cluster
├── ClusterResourceSet