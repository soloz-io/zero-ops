# OpenMeter E2E Tests

This directory contains end-to-end tests for the OpenMeter provider implementation, covering all validation criteria from the kube-sbt-metering spec.

## Test Coverage

### CHECKPOINT 1: OpenMeter Provider Foundation
- ✅ Subject registration with namespace isolation
- ✅ Subject retrieval
- ✅ Subject listing
- ✅ Catalog endpoints (meters, features, plans)
- ✅ Entitlement checking with fail-open behavior
- ✅ Subject deletion

### CHECKPOINT 2: Billing Provider Complete
- ✅ Subscription creation with namespace isolation
- ✅ Subscription listing
- ✅ Subscription updates with proration
- ✅ Subscription cancellation preserving data
- ✅ Invoice operations (preview, list)

### CHECKPOINT 3: REST API Functional (Provider-level)
- ✅ Tenant-scoped usage queries
- ✅ User-scoped usage queries
- ✅ Namespace isolation enforcement

## Prerequisites

1. **OpenMeter deployed in Hub cluster** at `openmeter-api.hub-platform-billing.svc.cluster.local`
2. **KUBECONFIG** set to Hub cluster: `export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig`
3. **Port-forward** to OpenMeter API (if running locally):
   ```bash
   kubectl port-forward -n hub-platform-billing svc/openmeter-api 8080:8080
   export OPENMETER_URL=http://localhost:8080
   ```

## Running Tests

### Run all tests
```bash
go test -v ./test/e2e/openmeter/
```

### Run specific test
```bash
go test -v ./test/e2e/openmeter/ -run TestSubjectRegistration
```

### Run with custom OpenMeter URL
```bash
OPENMETER_URL=http://localhost:8080 go test -v ./test/e2e/openmeter/
```

### Run from within cluster (recommended)
```bash
# Create test pod with kubeconfig
kubectl run kube-sbt-test \
  --image=golang:1.23 \
  --rm -it \
  --restart=Never \
  --namespace=hub-platform-ops \
  -- bash

# Inside pod:
git clone <repo-url>
cd zero-ops
go test -v ./test/e2e/openmeter/
```

## Test Data

Tests use the following test namespace and subject:
- **Namespace**: `test-tenant-e2e`
- **Subject ID**: `test-tenant-e2e#user-001`

Tests automatically clean up created resources in `TestMain`.

## Expected Behavior

### Success Criteria
- All subject operations succeed with correct namespace isolation
- Catalog endpoints return read-only data
- Entitlement checking returns correct values or fails open
- Subscription lifecycle operations work correctly
- Invoice operations return structured data
- Usage queries return aggregated metrics

### Acceptable Failures
- Catalog endpoints may return empty arrays if no meters/features/plans configured
- Invoice preview may fail if no pending lines exist
- Subscription tests may skip if no plans configured

## Troubleshooting

### Connection Refused
```
Error: dial tcp: connect: connection refused
```
**Solution**: Ensure port-forward is active or OPENMETER_URL points to correct endpoint

### 404 Not Found
```
Error: openmeter: query usage failed: status=404
```
**Solution**: Namespace or meter may not exist. Check OpenMeter configuration.

### 401 Unauthorized
```
Error: openmeter: register subject failed: status=401
```
**Solution**: Check OpenMeter authentication configuration (Svix JWT)

## Integration with CI/CD

These tests are designed to run in the Hub cluster as part of the deployment validation pipeline:

```yaml
# .github/workflows/kube-sbt-api.yml
- name: Run E2E Tests
  run: |
    kubectl run kube-sbt-e2e-test \
      --image=golang:1.23 \
      --rm \
      --restart=Never \
      --namespace=hub-platform-ops \
      -- bash -c "
        git clone ${{ github.repository }} /workspace &&
        cd /workspace &&
        go test -v ./test/e2e/openmeter/
      "
```

## Manual Validation Checklist

After running automated tests, manually verify:

- [ ] Subject registration creates subjects in OpenMeter UI
- [ ] Namespace isolation prevents cross-tenant access
- [ ] Usage queries match expected aggregation logic
- [ ] Entitlement checking returns correct quota values
- [ ] Subscription creation appears in OpenMeter billing dashboard
- [ ] Invoice operations return correct line items and totals
