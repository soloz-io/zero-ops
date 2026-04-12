# Next Steps: capi2argo Integration

## What Was Changed

1. **Crossplane Composition** - Added take-along labels to CAPI Cluster
2. **capi2argo RBAC** - Created ServiceAccount, ClusterRole, Role, and Bindings
3. **capi2argo Deployment** - Updated with ServiceAccount and environment variables
4. **Kyverno Policy** - Removed (replaced by capi2argo)
5. **ArgoCD Application** - Created for capi2argo RBAC

## Manual Cleanup Required

Run these commands on the Hub cluster:

```bash
# 1. Delete old capi2argo-system namespace
kubectl delete namespace capi2argo-system

# 2. Delete Kyverno-generated ArgoCD cluster Secret (will be recreated by capi2argo)
kubectl delete secret spoke-pool-eu-prod-01-argocd-cluster -n hub-platform-ops

# 3. Delete Kyverno policy
kubectl delete clusterpolicy capi-kubeconfig-to-argocd-secret
```

## Verification Steps

After ArgoCD syncs the changes:

### 1. Check capi2argo RBAC is created
```bash
kubectl get sa capi2argo-controller-manager -n hub-platform-ops
kubectl get clusterrole capi2argo-controller-role
kubectl get role capi2argo-secret-writer -n hub-platform-ops
```

### 2. Check capi2argo deployment is updated
```bash
kubectl get deploy capi2argo-controller-manager -n hub-platform-ops -o yaml | grep serviceAccountName
```

### 3. Check capi2argo logs (should no longer show RBAC errors)
```bash
kubectl logs -n hub-platform-ops -l control-plane=controller-manager --tail=50
```

### 4. Check ArgoCD cluster Secret is created with correct format
```bash
# Wait a few seconds for capi2argo to reconcile
kubectl get secret spoke-pool-eu-prod-01-argocd-cluster -n hub-platform-ops -o yaml

# Verify labels
kubectl get secret spoke-pool-eu-prod-01-argocd-cluster -n hub-platform-ops -o jsonpath='{.metadata.labels}' | jq .

# Should show:
# {
#   "argocd.argoproj.io/secret-type": "cluster",
#   "capi-to-argocd/owned": "true",
#   "spoke-type": "pool",
#   "cell-id": "spoke-pool-eu-prod-01"
# }

# Verify config is JSON (not YAML)
kubectl get secret spoke-pool-eu-prod-01-argocd-cluster -n hub-platform-ops -o jsonpath='{.data.config}' | base64 -d | jq .

# Should show JSON with tlsClientConfig structure
```

### 5. Check ApplicationSet discovers the cluster
```bash
kubectl get appset edge-catalog -n hub-platform-ops -o jsonpath='{.status.conditions}' | jq .

# Should no longer show "invalid character 'a'" error
```

### 6. Check edge catalog Application is created
```bash
kubectl get app spoke-pool-eu-prod-01-edge-catalog -n hub-platform-ops

# Should exist and be syncing
```

### 7. Verify edge catalog deployed to spoke cluster
```bash
export KUBECONFIG=k8-secrets/kubeconfig/spoke-pool-eu-prod-01.kubeconfig
kubectl get pods -n spoke-pool-system

# Should show CNPG, Atlas, PostgREST, NATS, Alloy pods
```

## Expected Outcome

- capi2argo running without RBAC errors
- ArgoCD cluster Secret created with correct JSON format and labels
- ApplicationSet discovers spoke cluster
- Edge catalog deployed to spoke cluster
- Phase 2 validation can proceed

## Troubleshooting

If capi2argo still shows errors:
```bash
# Check if ServiceAccount is mounted
kubectl get pod -n hub-platform-ops -l control-plane=controller-manager -o yaml | grep serviceAccountName

# Check RBAC permissions
kubectl auth can-i list secrets --as=system:serviceaccount:hub-platform-ops:capi2argo-controller-manager
kubectl auth can-i list clusters.cluster.x-k8s.io --as=system:serviceaccount:hub-platform-ops:capi2argo-controller-manager
```

If ApplicationSet still shows errors:
```bash
# Check if Secret has correct labels
kubectl get secret spoke-pool-eu-prod-01-argocd-cluster -n hub-platform-ops --show-labels

# Restart ApplicationSet controller to clear cache
kubectl delete pod -n hub-platform-ops -l app.kubernetes.io/name=argocd-applicationset-controller
```
