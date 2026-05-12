# ADR-025: Webhook-Aware Phased Operator and CRD Lifecycle Management

**Date:** 2026-05-12
**Status:** Proposed
**Related ADRs:** ADR-001 (Bootstrap Ubuntu Management Cluster), ADR-004 (Dual-Repo GitOps Pattern)

## Context

During the bootstrap of the Hub (management) and Spoke clusters, several core infrastructure operators (e.g., Cluster API Operator, cert-manager, External Secrets Operator) must be installed. These operator manifests contain highly complex, cross-resource dependency graphs consisting of:

1. Namespaces and RBAC primitives
2. Certificates and Issuers (cert-manager custom resources)
3. CustomResourceDefinitions (CRDs) with Webhook conversion strategies
4. Mutating and Validating webhook configurations
5. Deployments and StatefulSets

Upstream templates for these operators emit CRD schemas with dummy/placeholder CA bundles (typically `"caBundle": "Cg=="` which decodes to a newline `\n`). The API server cannot transition these CRDs to `Established: True` until `cert-manager-cainjector` asynchronously mutates the CRD with a valid, generated CA certificate.

Historically, bootstrap tools have attempted to bypass these race conditions and size limits (the 262,144-byte annotation ceiling) in one of two non-enterprise ways:

- **The Monolithic Server-Side Apply (SSA) Hack:** Executing `kubectl apply --server-side --force-conflicts` on the entire manifest bundle
- **The Fire-and-Forget Blind Apply:** Disabling validation (`--validate=false`) and ignoring CRD establishment errors, letting them "resolve eventually"

These approaches are rejected for production environments due to severe operational risks:

1. **API Server Exhaustion:** Bulky SSA of multiple massive CRDs causes severe CPU spikes, memory amplification, and HTTP/2 transport disconnects during OpenAPI schema regeneration
2. **Brittle Field Ownership:** `--force-conflicts` blindly steals field ownership from other controllers (or from gitops engines like ArgoCD), leading to non-deterministic cluster state and broken upgrades
3. **Implicit Webhook Deadlocks:** Waiting for CRDs to become established *before* applying the Certificate/Issuer resources that define their CAs leads to terminal bootstrap deadlocks

## Decision

We will enforce a **Deterministic, Webhook-Aware, Phased Bootstrap Sequence** for all core operator installations. This sequence respects the asynchronous nature of Kubernetes controller loops and prevents API server resource starvation.

The installer will execute the following five-stage lifecycle:

```
  ┌──────────────────────────────────────────────────────────┐
  │ PHASE 1: Apply Prerequisites                             │
  │ (Namespaces, ServiceAccounts, RBAC, Issuers, Certs)      │
  └──────────────────────────┬───────────────────────────────Target cert-manager starts
                             │                               generating secrets
                             ▼
  ┌──────────────────────────────────────────────────────────┐
  │ PHASE 2: Apply Injectables                               │
  │ (CRDs, Webhook Configurations with Cg== Placeholders)   │
  └──────────────────────────┬───────────────────────────────
                             │                               cainjector picks up Certs
                             ▼                               and overwrites Cg==
  ┌──────────────────────────────────────────────────────────┐
  │ PHASE 3: Wait for CA Injection                           │
  │ (Poll jsonpath for caBundle != "" && caBundle != "Cg==") │
  └──────────────────────────┬───────────────────────────────
                             │                               API Server validates and
                             ▼                               establishes the schemas
  ┌──────────────────────────────────────────────────────────┐
  │ PHASE 4: Wait for CRD Establishment                      │
  │ (Wait for Established: True condition)                   │
  └──────────────────────────┬───────────────────────────────
                             │
                             ▼
  ┌──────────────────────────────────────────────────────────┐
  │ PHASE 5: Apply Deployments                               │
  │ (Deploy controller pods to run reconciled CRDs)          │
  └──────────────────────────────────────────────────────────┘
```

### Specific Technical Constraints:

1. **Standard Client-Side Apply by Default:** We will use standard client-side apply (`kubectl apply -f -`) to preserve structural API validation and exact field ownership
2. **Explicit Annotation-Length Fallback:** If and only if a resource payload fails with the standard annotation-length error (`Too long: must have at most 262144 bytes`), the installer will capture the error and dynamically fall back to Server-Side Apply (`--server-side`) *without* `--force-conflicts`
3. **State-Driven, Webhook-Aware Wait Loops:** 
   - We will parse CRD manifests programmatically
   - If a CRD contains the `cert-manager.io/inject-ca-from` annotation, the installer MUST block on a polling loop verifying that the `spec.conversion.webhook.clientConfig.caBundle` path is neither empty nor the `"Cg=="` placeholder
   - Only after CA injection succeeds will the installer execute the `kubectl wait --for=condition=Established` block
4. **No Error Suppression:** All installation steps must be completely deterministic and explicitly checked. We reject any strategy that ignores schema establishment errors

## Rationale

### Deterministic Bootstraps

**Before:** Non-deterministic timing issues during cold starts with race conditions between CRDs and certificates
**After:** Sequential, state-aware installation that guarantees proper ordering

The phased approach eliminates bootstrap failures caused by timing dependencies between cert-manager, CRDs, and webhook configurations.

### API Server Safety

**Before:** Massive monolithic SSA operations cause CPU spikes, memory leaks, and TCP drops during OpenAPI V3 discovery
**After:** Controlled, incremental resource application prevents API server exhaustion

By avoiding bulk SSA operations, we protect the API server from resource starvation during critical bootstrap phases.

### Perfect GitOps Harmony

**Before:** `--force-conflicts` steals field ownership, breaking ArgoCD reconciliation and upgrades
**After:** Preserved field manager mapping enables seamless GitOps adoption

Client-side apply maintains proper field ownership, allowing GitOps tools to manage resources without conflicts.

### Enhanced Observability

**Before:** Silent failures and ignored errors during bootstrap
**After:** Clear, sequential debug logs with explicit wait conditions

Each phase provides explicit feedback about installation progress and any issues encountered.

## Consequences

### Positive

- **Deterministic Bootstraps:** Eliminates non-deterministic timing issues during cold starts
- **API Server Safety:** Eliminates CPU spikes, memory leaks, and TCP drops caused by massive monolithic SSA operations during OpenAPI V3 discovery
- **Perfect GitOps Harmony:** Preserves the field manager mapping. ArgoCD or other agents can seamlessly adopt, upgrade, and reconcile the resources post-bootstrap without conflict
- **Observability:** Provides clear, sequential debug logs (e.g. `Applying CRD 1/7... Waiting for CA Injection... Established`)

### Negative

- **Code Complexity:** Requires the bootstrap CLI to programmatically split and unmarshal YAML manifests, categorize resources, and manage multiple stateful polling loops

### Neutral

- **Bootstrap Duration:** Slightly longer bootstrap time due to explicit waiting, but with guaranteed success
- **Resource Ordering:** Requires careful manifest organization but eliminates race conditions

## Implementation

### Phase 1: Prerequisites Application

```go
func (i *Installer) applyPrerequisites(ctx context.Context) error {
    // Apply Namespaces, ServiceAccounts, RBAC
    // Apply cert-manager Issuers and Certificates
    // Wait for cert-manager to generate CA secrets
}
```

### Phase 2: Injectables Application

```go
func (i *Installer) applyInjectables(ctx context.Context) error {
    // Apply CRDs with Cg== placeholders
    // Apply webhook configurations
    // Use client-side apply by default
}
```

### Phase 3: CA Injection Wait

```go
func (i *Installer) waitForCAInjection(ctx context.Context, crd string) error {
    for {
        bundle, err := i.getCRDCABundle(crd)
        if err != nil {
            return err
        }
        if bundle != "" && bundle != "Cg==" {
            return nil
        }
        time.Sleep(1 * time.Second)
    }
}
```

### Phase 4: CRD Establishment

```go
func (i *Installer) waitForCRDEstablished(ctx context.Context, crd string) error {
    return i.kubectl.Wait(ctx, "crd", crd, "--for=condition=Established")
}
```

### Phase 5: Deployments

```go
func (i *Installer) applyDeployments(ctx context.Context) error {
    // Apply controller deployments and statefulsets
    // Services and other runtime resources
}
```

## Security Considerations

### Field Ownership Protection

The phased approach prevents unauthorized field ownership changes that could be exploited by malicious actors attempting to hijack resource management.

### CA Bundle Validation

Explicit CA injection waiting prevents man-in-the-middle attacks during webhook configuration by ensuring only valid, cert-manager-generated CA bundles are used.

### Error Handling

No error suppression means all security-relevant failures are caught and handled explicitly rather than ignored.

## Testing Strategy

### Unit Tests

- CRD parsing and categorization logic
- CA bundle polling mechanisms
- Error handling for annotation-length fallbacks

### Integration Tests

- End-to-end bootstrap sequence with cert-manager
- Webhook configuration validation
- Field ownership preservation verification

### Chaos Tests

- cert-manager pod deletion during CA injection
- API server resource constraints during bootstrap
- Network partitions between components

## Future Considerations

### Enhanced Automation

- Automatic manifest categorization based on resource annotations
- Dynamic timeout adjustment based on cluster size
- Parallel phase execution where dependencies allow

### Multi-Cluster Support

- Coordinated bootstrap across multiple clusters
- Cross-cluster webhook configuration management
- Shared CA bundle distribution

### Monitoring Integration

- Prometheus metrics for phase durations
- Alerting for bootstrap failures
- Dashboard for bootstrap progress visualization

## References

- [Kubernetes CRD Establishment Documentation](https://kubernetes.io/docs/tasks/access-kubernetes-api/custom-resources/custom-resource-definitions/#establishment)
- [cert-manager CA Injection Documentation](https://cert-manager.io/docs/concepts/ca-injection/)
- [kubectl Apply Field Management](https://kubernetes.io/docs/reference/generated/kubectl/kubectl-commands#apply)
- [ADR-001: Bootstrap Ubuntu Management Cluster](001-bootstrap-ubuntu-mgnt-cluster.md)
- [ADR-004: Dual-Repo GitOps Pattern](004-dual-repo-gitops-pattern.md)