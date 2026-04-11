# Edge Catalog

This directory contains manifests that are injected into Spoke Pool clusters at bootstrap time via ClusterResourceSet (Secret Zero pattern).

## ArgoCD Agent Bootstrap

The ArgoCD Agent is the first component deployed to each Spoke Pool cluster, enabling GitOps-driven management from the Hub.

### Components

1. **argocd-agent-deployment.yaml** - The actual ConfigMap argocd-agent-config is dynamically generated per-cluster by the Crossplane Composition in spokepool-hetzner.yaml

### Bootstrap Flow

1. CAPI provisions Hetzner VMs
2. ClusterResourceSet injects 5 resources when cluster reaches Provisioned state:
   - ArgoCD Agent Deployment (ConfigMap)
   - ArgoCD Agent Config (ConfigMap)
   - ArgoCD Agent RBAC (ConfigMap)
   - mTLS Client Certificate (Secret)
   - CA Certificate (Secret)
3. ArgoCD Agent starts within 2 minutes
4. Agent connects to Hub using mTLS authentication
5. Agent pulls Applications from Hub

### Security

- mTLS authentication using cert-manager generated certificates
- ServiceAccount with ClusterRole limited to agent's own cluster
- No cross-cluster access (NFR-4.4)

### Requirements

- FR-1.2: Automated Cluster Bootstrap
- AC-3: ArgoCD Agent Bootstrap
- NFR-4.1: mTLS authentication
- NFR-4.4: RBAC limited to own cluster

## Related Files

- Certificates: `catalog/security/argocd-agent-cert.yaml`
- ClusterResourceSet: `xrds/compositions/spokepool-clusterresourceset.yaml`
- Composition: `xrds/compositions/spokepool-hetzner.yaml`
