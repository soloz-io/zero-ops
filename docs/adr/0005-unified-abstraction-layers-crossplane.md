# ADR 005: Hub-Spoke Crossplane Composition Pattern

**Date:** 2026-04-26  
**Status:** Accepted  
**Authors:** Platform Engineering Team  
**Crossplane Version:** v2.0+

## Context

Zero-Ops operates a hub-spoke architecture where a central Hub cluster provisions and manages infrastructure for multiple Spoke clusters (both Pool and Silo configurations). Crossplane is the infrastructure provisioning engine running on the Hub.

The platform must compose heterogeneous resources in a single Composition:
- **Infrastructure Resources:** CAPI Clusters, Hetzner Cloud resources, networking
- **Application Plane Resources:** CNPG databases, ESO secrets, Kyverno policies
- **Remote Delivery:** Resources must be deployed from Hub to remote Spoke clusters

Crossplane v2.0+ (released 2025) provides native capabilities for composing any Kubernetes resource type without schema resolution constraints that existed in v1.x.

## Decision

We adopt the industry-standard hub-spoke Crossplane composition pattern aligned with Crossplane v2.0+ capabilities and AWS/CNCF reference architectures.

### Pattern 1: Hub Cluster Compositions

**Use Native Managed Resources (MRs) for infrastructure provisioning:**

- CAPI Clusters (`cluster.x-k8s.io/v1beta1/Cluster`)
- CAPI Machine Deployments
- Hetzner Cloud resources (via CAPH provider)
- AWS resources (if using AWS provider)
- Crossplane Provider configurations

**Rationale:** Native MRs provide strongly-typed APIs, full status observability, and direct reconciliation by Crossplane providers.

### Pattern 2: Remote Spoke Delivery

**Use `provider-kubernetes` Object MRs ONLY for deploying resources to remote Spoke clusters:**

```yaml
apiVersion: kubernetes.crossplane.io/v1alpha2
kind: Object
spec:
  providerConfigRef:
    name: spoke-cluster-kubeconfig  # Remote cluster connection
  forProvider:
    manifest:
      apiVersion: postgresql.cnpg.io/v1
      kind: Cluster
      # ... resource deployed TO the spoke
```

**Rationale:** Object MRs enable cross-cluster resource deployment. The Hub Crossplane controller applies manifests to remote Spoke clusters via kubeconfig-based authentication.

### Pattern 3: Spoke Cluster Reconciliation

**Local operators on Spoke clusters reconcile deployed resources:**

- CNPG Operator reconciles `postgresql.cnpg.io/Cluster` CRs
- External Secrets Operator reconciles `external-secrets.io/ExternalSecret` CRs
- Kyverno reconciles `kyverno.io/Policy` CRs
- ArgoCD Agent reconciles `argoproj.io/Application` CRs

**Rationale:** Separation of concerns - Hub handles delivery, Spoke operators handle lifecycle management and state reconciliation.

### Pattern 4: Mixed Resource Composition

**Compositions MAY freely mix resource types:**

A single Composition can include:
- Native Crossplane MRs (CAPI, AWS, GCP)
- Kubernetes native resources (Deployments, Services, ConfigMaps)
- Third-party CRs (CNPG, ESO, Kyverno)
- Object MRs for remote delivery

**Rationale:** Crossplane v2.0+ natively supports composing any Kubernetes resource without schema resolution race conditions. No artificial separation is required.

## Consequences

### Positive

**Clear Semantic Separation:**
- Native MRs = Infrastructure provisioning on Hub
- Object MRs = Remote delivery to Spokes
- Spoke Operators = Local reconciliation

**Full Status Observability:**
- Native MRs provide strongly-typed status fields
- Crossplane v2.0+ propagates status from composed resources to XRs
- No loss of observability compared to v1.x workarounds

**Composition Flexibility:**
- Single Composition can provision infrastructure AND deploy application resources
- No artificial "Pure Object" vs "Pure Native" constraints
- Aligns with Crossplane v2.0+ design philosophy

**Industry Alignment:**
- Matches AWS Multi-Cluster GitOps reference architecture
- Follows CNCF Crossplane community best practices
- Compatible with Upbound managed Crossplane offerings

### Negative

**Remote Cluster Authentication Complexity:**
- Requires managing kubeconfig Secrets for each Spoke cluster
- ProviderConfig per Spoke cluster must be maintained
- Credential rotation requires updating ProviderConfigs

**Status Propagation Latency:**
- Object MR status reflects "manifest applied" not "resource ready"
- Spoke resource readiness requires polling or event-driven status sync
- Addressed by Spoke Controller writing status to Hub Centralised DB (see ADR 004)

**Crossplane Version Dependency:**
- Pattern requires Crossplane v2.0+ (released 2025)
- v1.x deployments require upgrade before adopting this pattern
- Breaking changes from v1.x to v2.0+ must be managed

## Implementation Constraints

### Hub Cluster Requirements

- Crossplane v2.0+ installed
- `provider-kubernetes` installed and configured
- ProviderConfig per Spoke cluster with kubeconfig Secret
- RBAC: Crossplane ServiceAccount must have cluster-admin on Spokes (or scoped RBAC)

### Spoke Cluster Requirements

- Target operators installed (CNPG, ESO, Kyverno, etc.)
- CRDs installed before Hub attempts to deploy CRs
- Network connectivity from Hub to Spoke API server
- Valid kubeconfig with sufficient permissions

### Composition Design Rules

1. **Use Native MRs for Hub-local resources** (CAPI, cloud provider resources)
2. **Use Object MRs ONLY when `providerConfigRef` points to a remote Spoke cluster**
3. **Do NOT wrap Hub-local resources in Object MRs** (unnecessary indirection)
4. **Ensure Spoke operators are installed via ClusterResourceSet or ArgoCD bootstrap** before Composition deploys dependent CRs

## Related ADRs

- **ADR 004 (Declarative Operator State):** Mandates use of declarative CRs over imperative Jobs. This ADR defines HOW those CRs are delivered from Hub to Spoke.
- **Namespace Alignment ADR:** Defines namespace conventions (`hub-platform-ops`, `spoke-platform-data`) that Compositions must respect.
- **ClusterResourceSet Addon Management ADR:** Defines Day-1 bootstrap (CNI, CCM, ArgoCD Agent) that must complete before Crossplane Compositions deploy Day-2 resources.

## References

- [Crossplane v2.0 Official Documentation](https://docs.crossplane.io/latest/whats-new/)
- [AWS Multi-Cluster GitOps with Crossplane](https://aws.amazon.com/blogs/containers/part-2-multi-cluster-gitops-cluster-fleet-provisioning-and-bootstrapping/)
- [Crossplane Composite Resources Guide](https://docs.crossplane.io/latest/composition/composite-resources/)
- [provider-kubernetes Documentation](https://github.com/crossplane-contrib/provider-kubernetes)
