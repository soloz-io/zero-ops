# Namespace Alignment

## Hub Platform Namespaces

| Namespace | Components |
|-----------|------------|
| `hub-platform-capi` | Cluster API resources, cluster-api-operator |
| `hub-platform-messaging` | NATS subscriber |
| `hub-platform-identity` | Kratos, Keto, Hydra, kratos-ui |
| `hub-platform-data` | PostgreSQL clusters, PgBouncer, Redis, CloudNativePG Operator |
| `hub-platform-ops` | ArgoCD, Argo-repo-Server, ESO, Crossplane, KEDA, cnpg2monitor, External-DNS |
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
