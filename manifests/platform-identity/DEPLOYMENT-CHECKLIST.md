# Task 1 Deployment Checklist

## Pre-Deployment Verification

- [ ] Kubernetes cluster is accessible (`kubectl cluster-info`)
- [ ] CNPG operator is installed (`kubectl get crd clusters.postgresql.cnpg.io`)
- [ ] ArgoCD is installed (`kubectl get namespace argocd`)
- [ ] Helm is installed (for Ory charts via ArgoCD)

## Deployment Steps

### Phase 1: Secrets and Namespaces
- [ ] Run `./manifests/platform-identity/bootstrap-secrets.sh`
- [ ] Verify secret created: `kubectl get secret identity-postgres-passwords -n ory-system`

### Phase 2: Database Infrastructure
- [ ] Apply CNPG cluster: `kubectl apply -f manifests/platform-identity/cnpg-cluster.yaml`
- [ ] Wait for cluster ready: `kubectl wait --for=condition=Ready cluster/identity-postgres -n ory-system --timeout=300s`
- [ ] Verify cluster status: `kubectl get cluster -n ory-system`
- [ ] Apply database CRDs: `kubectl apply -f manifests/platform-identity/databases/`
- [ ] Verify databases: `kubectl get database -n ory-system`

### Phase 3: Kratos Configuration
- [ ] Apply identity schema ConfigMap: `kubectl apply -f manifests/platform-identity/ory-kratos/identity-schema-configmap.yaml`
- [ ] Verify ConfigMap: `kubectl get configmap kratos-identity-schema -n ory-system`

### Phase 4: ArgoCD Applications
- [ ] Apply all ArgoCD apps: `kubectl apply -f manifests/platform-identity/argocd/`
- [ ] Verify apps created: `kubectl get application -n argocd | grep -E "(identity-postgres|ory-hydra|ory-kratos|ory-keto|kratos-ui|demo-echo)"`
- [ ] Wait for apps to sync (may take 5-10 minutes)

### Phase 5: Verification
- [ ] Run verification script: `./manifests/platform-identity/verify-deployment.sh`
- [ ] Check all pods running: `kubectl get pods -n ory-system`
- [ ] Check demo echo: `kubectl get pods -n api-gateway`

## Health Checks

### Hydra
```bash
kubectl exec -n ory-system deploy/ory-hydra -- hydra health --endpoint http://localhost:4444
```

### Kratos
```bash
kubectl exec -n ory-system deploy/ory-kratos -- kratos health --endpoint http://localhost:4433
```

### Keto
```bash
kubectl exec -n ory-system deploy/ory-keto -- keto health --endpoint http://localhost:4466
```

### Database Connectivity
```bash
kubectl exec -n ory-system identity-postgres-1 -- psql -U postgres -c "\l"
```

## Expected Results

### Namespaces
- `ory-system` - Contains CNPG cluster, Ory services, Kratos UI
- `api-gateway` - Contains demo echo service

### Pods in ory-system
- `identity-postgres-1` (Running)
- `identity-postgres-2` (Running)
- `identity-postgres-3` (Running)
- `ory-hydra-*` (Running)
- `ory-kratos-*` (Running)
- `ory-keto-*` (Running)
- `kratos-selfservice-ui-*` (Running)

### Pods in api-gateway
- `demo-echo-*` (Running)

### Services
All internal service endpoints should be resolvable:
- `ory-hydra-public.ory-system.svc.cluster.local:4444`
- `ory-hydra-admin.ory-system.svc.cluster.local:4445`
- `ory-kratos-public.ory-system.svc.cluster.local:4433`
- `ory-kratos-admin.ory-system.svc.cluster.local:4434`
- `ory-keto-read.ory-system.svc.cluster.local:4466`
- `ory-keto-write.ory-system.svc.cluster.local:4467`
- `kratos-selfservice-ui.ory-system.svc.cluster.local:3000`
- `demo-echo.api-gateway.svc.cluster.local:8080`

## Troubleshooting

### Pods in CrashLoopBackOff
- **Expected during initial startup** - Ory services retry until CNPG is ready
- Check initContainer logs: `kubectl logs -n ory-system <pod-name> -c wait-for-postgres`
- Verify CNPG cluster is ready: `kubectl get cluster -n ory-system`

### Database Connection Errors
- Check CNPG cluster status: `kubectl describe cluster identity-postgres -n ory-system`
- Verify database CRDs: `kubectl get database -n ory-system`
- Check database passwords secret: `kubectl get secret identity-postgres-passwords -n ory-system`

### ArgoCD Sync Issues
- Check application status: `kubectl describe application <app-name> -n argocd`
- View sync logs in ArgoCD UI
- Manually sync: `argocd app sync <app-name>`

## Success Criteria

✅ All pods in `Running` state
✅ All Ory health checks return healthy
✅ Database connections successful
✅ ArgoCD applications synced
✅ Services resolvable via cluster DNS

## Next Steps

Once Task 1 is complete and verified:
1. Proceed to Task 2: Implement auth-proxy service
2. Configure OAuth client pre-registration
3. Implement login/consent handlers
4. Set up JWT validation and extAuthz endpoint
