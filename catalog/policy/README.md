# Policy Catalog

This directory contains Kyverno policies for the Zero-Ops platform.

## CAPI Cluster to ArgoCD Secret

**File**: `kyverno-argocd-discovery.yaml`

Automatically generates ArgoCD cluster Secret when a CAPI Cluster with label `spoke-type: pool` reaches Provisioned state.

### Trigger Conditions

- **Resource**: Cluster (cluster.x-k8s.io/v1beta1)
- **Label**: `spoke-type: pool`
- **Status**: `phase=Provisioned`
- **Precondition**: `spec.controlPlaneEndpoint.host` is not empty

### Generated Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: <cluster-name>-argocd-cluster
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: cluster
    spoke-type: pool
    cell-id: <cluster-name>
type: Opaque
stringData:
  name: <cluster-name>
  server: https://<endpoint>:<port>
  config: <kubeconfig-json>
```

### Discovery Time

ArgoCD discovers the cluster within 30 seconds of Secret creation (NFR-1.4).

### Requirements

- FR-1.3: Cluster Registration
- NFR-1.4: Discovery time < 30 seconds
- AC-2: Kyverno Cluster Discovery

## Related Files

- ClusterResourceSet: `xrds/compositions/spokepool-clusterresourceset.yaml`
- Composition: `xrds/compositions/spokepool-hetzner.yaml`
