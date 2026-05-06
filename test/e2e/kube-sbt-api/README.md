# kube-sbt-api E2E Tests

E2E tests for OpenMeter provider using [kubernetes-sigs/e2e-framework](https://github.com/kubernetes-sigs/e2e-framework).

## Prerequisites

- Kubernetes cluster with OpenMeter deployed
- `kubectl` configured with cluster access
- Port-forward to OpenMeter (local testing only)

## Local Testing

### 1. Start Port-Forward

```bash
kubectl port-forward -n hub-platform-billing svc/openmeter-api 8080:80
```

### 2. Run Tests

```bash
# From workspace root
export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig
export OPENMETER_URL=http://localhost:8080
cd test/e2e
go test -v ./kube-sbt-api

# Run specific checkpoint
go test -v ./kube-sbt-api -run=TestOpenMeterProviderFoundation  # Checkpoint 1
go test -v ./kube-sbt-api -run=TestBillingProviderComplete      # Checkpoint 2
go test -v ./kube-sbt-api -run=TestRESTAPIFunctional            # Checkpoint 3
```

## CI/CD Testing

No port-forward needed. Tests access OpenMeter directly via cluster DNS.

```bash
export KUBECONFIG=/path/to/kubeconfig
cd test/e2e
go test -v ./kube-sbt-api
```

## Test Organization

- **Checkpoint 1**: Subject Management, Catalog Endpoints, Entitlement Checking
- **Checkpoint 2**: Subscription Management, Invoice Operations
- **Checkpoint 3**: Usage Queries, Namespace Isolation

## Troubleshooting

**DNS errors locally**: Ensure port-forward is running and `OPENMETER_URL=http://localhost:8080`

**Connection refused**: Verify OpenMeter is running:
```bash
kubectl get pods -n hub-platform-billing -l app.kubernetes.io/name=openmeter
```

**Test failures**: Check OpenMeter logs:
```bash
kubectl logs -n hub-platform-billing -l app.kubernetes.io/name=openmeter --tail=50
```
