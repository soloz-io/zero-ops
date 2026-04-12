# capi2argo - CAPI to ArgoCD Cluster Operator

## Overview

capi2argo automatically converts Cluster API (CAPI) kubeconfig Secrets into ArgoCD cluster Secrets with the correct JSON format. This enables ArgoCD ApplicationSets to discover and deploy workloads to CAPI-provisioned clusters.

## Architecture

**Problem Solved:**
- CAPI creates kubeconfig Secrets in YAML format
- ArgoCD expects cluster Secrets with JSON config containing `tlsClientConfig`
- Manual conversion is error-prone and not GitOps-compliant

**Solution:**
- capi2argo watches CAPI kubeconfig Secrets
- Extracts certificates and credentials
- Creates ArgoCD cluster Secrets with proper JSON format
- Copies labels from CAPI Cluster using "take-along" mechanism

## Label Propagation

To make labels available to ArgoCD ApplicationSets, add them to the CAPI Cluster resource with the `take-along-label` prefix:

```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: spoke-pool-eu-prod-01
  labels:
    spoke-type: pool
    cell-id: spoke-pool-eu-prod-01
    take-along-label.capi-to-argocd.spoke-type: ""
    take-along-label.capi-to-argocd.cell-id: ""
```

capi2argo will copy `spoke-type` and `cell-id` to the ArgoCD cluster Secret, making them available for ApplicationSet cluster generators.

## Configuration

Environment variables (set in deployment.yaml):
- `ARGOCD_NAMESPACE`: Namespace where ArgoCD cluster Secrets are created (default: `hub-platform-ops`)
- `ENABLE_GARBAGE_COLLECTION`: Delete ArgoCD Secrets when CAPI Secret is deleted (default: `true`)

## RBAC

capi2argo requires:
- ClusterRole: Read CAPI Clusters and Secrets cluster-wide
- Role: Write Secrets in the ArgoCD namespace

See `rbac.yaml` for full permissions.

## Deployment

Managed by ArgoCD Application: `manifests/argocd/apps/platform-capi2argo.yaml`

**Sync Wave:** 2 (after basic infrastructure)

## Verification

Check if capi2argo is working:

```bash
# Check pod status
kubectl get pods -n hub-platform-ops -l control-plane=controller-manager

# Check logs
kubectl logs -n hub-platform-ops -l control-plane=controller-manager --tail=50

# Verify ArgoCD cluster Secret format
kubectl get secret cluster-<cluster-name> -n hub-platform-ops -o jsonpath='{.data.config}' | base64 -d | jq .
```

Expected output: JSON with `tlsClientConfig` structure containing `caData`, `certData`, `keyData`.

## Troubleshooting

**RBAC Errors:**
```
secrets is forbidden: User "system:serviceaccount:hub-platform-ops:capi2argo-controller-manager" cannot list resource "secrets"
```
Solution: Verify ClusterRole and ClusterRoleBinding are applied.

**No ArgoCD Secrets Created:**
- Check if CAPI Cluster has kubeconfig Secret
- Verify Secret has label `cluster.x-k8s.io/cluster-name`
- Check capi2argo logs for errors

**Labels Not Copied:**
- Verify CAPI Cluster has `take-along-label.capi-to-argocd.<label-key>: ""` markers
- Check that the actual label exists on the Cluster resource

## References

- [capi2argo GitHub](https://github.com/dntosas/capi2argo-cluster-operator)
- [ArgoCD Cluster Secrets](https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/#clusters)
- [CAPI Documentation](https://cluster-api.sigs.k8s.io/)
