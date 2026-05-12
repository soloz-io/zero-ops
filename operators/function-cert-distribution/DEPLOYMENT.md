# Function Cert Distribution - GitOps Deployment Guide

## Architecture Overview

This Crossplane Composition Function implements enterprise-grade certificate distribution with state-aware orchestration, eliminating reconciliation deadlocks while providing SRE observability.

## Deployment Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    ArgoCD (GitOps)                     │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ Crossplane Core Engine                         │    │
│  │  ┌─────────────────────────────────────────┐  │    │
│  │  │ Composition Pipeline                   │  │    │
│  │  │  ┌─────────────────────────────┐    │  │    │
│  │  │  │ Step 1: Base Resources   │    │  │    │
│  │  │  │ (CAPI, cert-manager)   │    │    │
│  │  │  └─────────────────────────────┘    │  │    │
│  │  │  ┌─────────────────────────────┐    │  │    │
│  │  │  │ Step 2: Stateful       │    │  │    │
│  │  │  │ Orchestration           │    │  │    │
│  │  │  │ (function-cert-         │    │  │    │
│  │  │  │ distribution)           │    │  │    │
│  │  │  └─────────────────────────────┘    │  │    │
│  │  └─────────────────────────────────────────┘  │    │
│  └─────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────┘
```

## Data Flow

1. **Crossplane Core Engine** reads generated `argocd-agent-client-cert` Secret from `platform-capi` namespace
2. **Crossplane Core Engine** packages Secret data into gRPC payload and sends to `function-cert-distribution`
3. **Function executes** stateful logic:
   - Validates certificate readiness (`CertificatesMinted` condition)
   - Waits for secret data projection (`CertificatesDistributed` condition)
   - Generates idempotent `provider-kubernetes` Object
4. **Crossplane Core Engine** receives desired Object and applies to target Spoke cluster
5. **Provider-Kubernetes** handles retries, backoff, and eventual consistency

## Security Isolation

- ✅ **No Direct K8s Access**: Function cannot read secrets directly from cluster
- ✅ **Stateless Operation**: No persistent state or side effects
- ✅ **Scoped RBAC**: Function runs with minimal Crossplane service permissions
- ✅ **Secret Payload Protection**: Explicit prohibition on debug logging of secret material

## GitOps Integration

### Phase 1: CI/CD (Automated)

**GitHub Actions** builds and publishes OCI image:
```bash
# Triggered by changes to operators/function-cert-distribution/**
ghcr.io/soloz-io/zero-ops/function-cert-distribution:${{ github.sha }}
```

### Phase 2: Function Registration (GitOps)

**Crossplane Function manifest** declares function to cluster:
```yaml
# manifests/hub-core-services/crossplane/providers/function/function-cert-distribution.yaml
apiVersion: pkg.crossplane.io/v1beta1
kind: Function
metadata:
  name: function-cert-distribution
  annotations:
    argocd.argoproj.io/sync-wave: "1"
spec:
  package: ghcr.io/soloz-io/zero-ops/function-cert-distribution:latest
  packagePullPolicy: Always
```

### Phase 3: Composition Integration (GitOps)

**SpokePool composition** wires function into pipeline:
```yaml
# manifests/hub-core-services/crossplane/tenant-platform/compositions/spokepool-hetzner.yaml
pipeline:
  # Step 1: Base resources (CAPI cluster, cert-manager requests)
  - step: patch-and-transform
    functionRef:
      name: function-patch-and-transform
  
  # Step 2: Stateful orchestration (NEW)
  - step: distribute-certificates
    functionRef:
      name: function-cert-distribution
```

## Operational Characteristics

### SRE Observability
- **Semantic Conditions**: `CertificatesMinted`, `CertificatesDistributed`
- **Deterministic Timeouts**: 15-minute threshold with wall-clock comparison
- **Complete Condition Emission**: Prevents state flapping with full condition sets
- **Enterprise Logging**: Structured logs without secret payload exposure

### Reliability Features
- **Short-Circuit Rendering**: Omits downstream resources when dependencies not ready
- **Idempotent Operations**: Safe for repeated reconciliation cycles
- **Graceful Degradation**: Timeout states with clear error messages
- **Eventual Consistency**: Provider-Kubernetes handles spoke outages and reconnection

### Deployment Safety
- **Zero-Downtime Updates**: Function updates propagate through Crossplane reconciliation
- **Rollback Capability**: Previous function versions remain available
- **Isolation Boundary**: Clear separation between Hub and Spoke concerns
- **GitOps Native**: All changes tracked through version control

## Troubleshooting

### Function Not Responding
```bash
# Check function pod status
kubectl logs -n crossplane-system deployment/function-cert-distribution-xxxxx

# Verify function registration
kubectl get function function-cert-distribution -n crossplane-system
```

### Certificate Distribution Stuck
```bash
# Check XR conditions
kubectl get spokepool <spool-name> -o yaml

# Verify target cluster connectivity
kubectl get providerconfig <spoke-name> -o yaml
```

### Secret Data Issues
```bash
# Check source secret exists
kubectl get secret argocd-agent-client-cert -n platform-capi

# Verify cert-manager status
kubectl get certificate argocd-agent-client-cert -n platform-capi
```

## Upgrade Path

1. **Update function code** in `operators/function-cert-distribution/`
2. **CI/CD automatically** builds new OCI image
3. **ArgoCD syncs** updated Function manifest
4. **Crossplane Package Manager** rolling restarts with new version
5. **Zero downtime** - new function handles existing resources gracefully

## Monitoring Integration

### Prometheus Metrics
- Function execution duration
- Condition transition counts
- Error rates by reason type
- Resource generation success/failure rates

### Alerting Rules
- **Critical**: Function pod crashes or restarts
- **Warning**: Certificate distribution timeout (>15 minutes)
- **Info**: Normal condition transitions

This architecture provides enterprise-grade reliability while maintaining GitOps best practices and security isolation.