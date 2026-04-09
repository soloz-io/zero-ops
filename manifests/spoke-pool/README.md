# Spoke Pool Manifests

This directory contains SpokePool XR manifests for provisioning Spoke Pool clusters (cells).

## Prerequisites

Before applying SpokePool manifests, ensure:

1. **Crossplane installed** (v1.14+) in Hub cluster
2. **Crossplane Function** `function-patch-and-transform` installed
3. **CAPI + CAPH** installed and configured
4. **cert-manager** installed (v1.13+)
5. **Kyverno** installed (v1.11+)
6. **ArgoCD** installed and configured

## Test Manifest

**File**: `spokepool-test-01.yaml`

Test SpokePool XR for Phase 1 validation.

### Apply via GitOps

```bash
# Commit and push
git add manifests/spoke-pool/spokepool-test-01.yaml
git commit -m "test: Add SpokePool test-01 for Phase 1 validation"
git push

# Force ArgoCD sync if needed
kubectl patch application platform-core -n hub-platform-ops \
  --type merge \
  -p '{"operation":{"initiatedBy":{"username":"admin"},"sync":{"syncStrategy":{"hook":{},"apply":{"force":true}}}}}'
```

### Monitor Provisioning

```bash
# Watch SpokePool status
kubectl get spokepool spokepool-test-01 -o yaml

# Watch CAPI Cluster
kubectl get cluster spokepool-test-01 -n default -w

# Wait for Ready (timeout 20 minutes)
kubectl wait --for=condition=Ready cluster/spokepool-test-01 --timeout=20m
```

### Validation Steps

See tasks 1.5.2-1.5.4 in `.kiro/specs/spoke-pool-provisioner/tasks.md`

### Requirements

- FR-1.1: Declarative Cell Creation
- NFR-1.1: Provisioning time < 15 minutes
- AC-1, AC-6: Acceptance criteria
