# ADR: Crossplane + CAPI Ownership Pattern

**Status**: Proposed  
**Date**: 2026-04-10  
**Context**: Spoke Pool Provisioner (Phase 1 - Blocked)  
**Deciders**: Platform Team

---

## Context and Problem Statement

The SpokePool provisioner uses Crossplane Compositions to create CAPI clusters on Hetzner. We've encountered an ownership conflict that blocks cluster provisioning:

**Current Implementation**: Crossplane Composition creates all CAPI resources directly (Cluster, HetznerCluster, KubeadmControlPlane, MachineDeployment, etc.)

**Problem**: Crossplane sets `ownerReferences` with `controller: true` on all managed resources to the SpokePool XR. CAPI's Cluster controller expects to set ownerReferences on infrastructure resources (HetznerCluster), but cannot because Crossplane already owns them. This creates a deadlock where HetznerCluster controller waits indefinitely for the Cluster controller to set OwnerRef.

**Impact**: Phase 1 validation blocked - clusters cannot provision.

**Key Question**: How should Crossplane and CAPI share responsibility for cluster lifecycle management?

---

## Decision Drivers

- **Ownership Clarity**: Clear boundaries between Crossplane and CAPI responsibilities
- **Industry Standards**: Align with proven patterns from production platforms
- **Unblock Phase 1**: Resolve the deadlock quickly to continue validation
- **Maintainability**: Simple, understandable pattern for the team
- **CAPI Compatibility**: Work with CAPI's ownership model, not against it

---

## Considered Options

### Option 1: Let CAPI Own Lifecycle (RECOMMENDED)
Crossplane creates ONLY the root Cluster CR. CAPI controllers create and manage all other resources (HetznerCluster, KubeadmControlPlane, MachineDeployment, etc.)

### Option 2: Wrap with provider-kubernetes
Use provider-kubernetes to create CAPI resources as Kubernetes Objects, separating ownership between Crossplane and CAPI.

### Option 3: Use ArgoCD ApplicationSets Directly
Skip Crossplane for CAPI resources entirely, use ArgoCD to template and deploy cluster manifests.

---

## Decision Outcome

**Chosen Option**: Option 1 - Let CAPI Own Lifecycle

Crossplane will create ONLY the CAPI Cluster CR (root object) with proper configuration. CAPI controllers will create and manage all dependent resources following their native ownership model.

---

## Rationale

### 1. Clear Ownership Boundaries

| Layer | Responsibility |
|-------|---------------|
| **Crossplane** | Platform API abstraction (SpokePool XRD) |
| **CAPI** | Cluster lifecycle orchestration |
| **Infrastructure Provider (CAPH)** | Hetzner infrastructure provisioning |

This separation aligns with each system's design intent.

### 2. Industry Standard Pattern

Research shows mature platform teams follow this pattern:
- **Crossplane**: Provisions cloud infrastructure (VPC, IAM, etc.) and exposes platform APIs
- **CAPI**: Fully owns cluster lifecycle from creation to deletion
- **GitOps (ArgoCD/Flux)**: Applies cluster manifests and installs workloads

Reference: Production implementations (TKG on vSphere, EKS clusters) use this approach.

### 3. CAPI's Native Ownership Model

CAPI is designed with a specific ownership graph:
```
Cluster (root)
├── owns → HetznerCluster
├── owns → KubeadmControlPlane
│   └── owns → HCloudMachineTemplate (control plane)
└── owns → MachineDeployment
    ├── owns → MachineSet
    ├── owns → KubeadmConfigTemplate
    └── owns → HCloudMachineTemplate (workers)
```

When Crossplane creates all resources directly, it breaks this model by claiming ownership before CAPI can establish it.

### 4. Simpler Composition

**Before** (9 resources in Composition):
- Cluster
- HetznerCluster
- KubeadmControlPlane
- HCloudMachineTemplate (control plane)
- MachineDeployment
- KubeadmConfigTemplate
- HCloudMachineTemplate (workers)
- ClusterResourceSet
- ConfigMap (per-cluster)

**After** (2-3 resources in Composition):
- Cluster (with proper spec)
- ClusterResourceSet (optional)
- ConfigMap (per-cluster, optional)

CAPI handles the complexity of creating and managing all other resources based on the Cluster spec.

### 5. Faster Resolution

- **Effort**: 2-4 hours (simplify Composition, test)
- **Risk**: Low (following proven pattern)
- **Unblocks**: Phase 1 validation immediately

---

## Implementation Approach

### Composition Changes

**Crossplane creates**:
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: spoke-pool-eu-prod-01
  namespace: hub-platform-capi
  labels:
    spoke-type: pool
    cell-id: spoke-pool-eu-prod-01
spec:
  clusterNetwork:
    pods:
      cidrBlocks: ["10.244.0.0/16"]
    services:
      cidrBlocks: ["10.96.0.0/12"]
  controlPlaneRef:
    apiVersion: controlplane.cluster.x-k8s.io/v1beta1
    kind: KubeadmControlPlane
    name: spoke-pool-eu-prod-01
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
    kind: HetznerCluster
    name: spoke-pool-eu-prod-01
```

**CAPI creates** (automatically):
- HetznerCluster (based on infrastructureRef)
- KubeadmControlPlane (based on controlPlaneRef)
- All machine templates and deployments

### Configuration Strategy

Two approaches for configuring what CAPI creates:

**A. Inline Specs** (simpler, for Phase 1):
Include full specs for HetznerCluster and KubeadmControlPlane in the Cluster CR. CAPI creates them based on these specs.

**B. ClusterClass** (advanced, for future):
Define a ClusterClass template that parameterizes cluster configuration. Reference it from Cluster CR with variables.

**Recommendation**: Start with inline specs (A) for Phase 1, migrate to ClusterClass (B) if we need to support multiple cluster configurations.

---

## Consequences

### Positive

✅ **Unblocks Phase 1**: Resolves ownership conflict immediately  
✅ **Industry Alignment**: Follows proven production patterns  
✅ **Clear Boundaries**: Each system manages its domain  
✅ **Simpler Composition**: Less YAML, easier to maintain  
✅ **CAPI Native**: Works with CAPI's design, not against it  
✅ **Proper Namespace**: Resources created in `hub-platform-capi` by CAPI

### Negative

⚠️ **Less Direct Control**: Cannot explicitly manage each CAPI resource in Composition  
⚠️ **Learning Curve**: Team needs to understand CAPI's resource creation logic  
⚠️ **Debugging**: Need to check CAPI controller logs for resource creation issues

### Neutral

🔄 **Configuration Method**: Need to decide between inline specs vs ClusterClass  
🔄 **Composition Refactor**: Existing Composition needs simplification  
🔄 **Testing**: Validate CAPI creates resources as expected

---

## Alternatives Considered

### Why Not provider-kubernetes (Option 2)?

**Rejected Reasons**:
- Adds provider-kubernetes dependency
- More verbose Composition (wrapping each resource in Object)
- Still managing CAPI resources from Crossplane (ownership separation, but not lifecycle delegation)
- Doesn't align with industry standard pattern

**When to Use**: If we need to create non-CAPI Kubernetes resources alongside clusters (e.g., custom CRDs, ConfigMaps in other namespaces).

### Why Not ArgoCD ApplicationSets (Option 3)?

**Rejected Reasons**:
- Loses Crossplane abstraction benefits (SpokePool XRD as unified API)
- Harder to compose clusters with other resources (secrets, IAM, etc.)
- More effort to implement (~8-12 hours vs 2-4 hours)
- Doesn't leverage Crossplane's strengths

**When to Use**: If we decide Crossplane adds no value for cluster provisioning and want pure GitOps templating.

---

## Migration Path

1. **Simplify Composition** (2 hours)
   - Remove HetznerCluster, KubeadmControlPlane, MachineDeployment, machine templates from Composition
   - Keep only Cluster CR with full spec
   - Keep ClusterResourceSet and per-cluster ConfigMap

2. **Configure Cluster Spec** (1 hour)
   - Add inline specs for HetznerCluster and KubeadmControlPlane
   - Ensure all parameters (region, instance type, node count) are patched from SpokePool XR

3. **Test Provisioning** (1 hour)
   - Apply updated Composition
   - Create test SpokePool XR
   - Verify CAPI creates all resources
   - Verify cluster reaches Ready state

4. **Validate Phase 1** (ongoing)
   - Complete tasks 1.5.1-1.5.4
   - Proceed to Phase 2

---

## Related Decisions

- **ADR: Dual-Repository GitOps Pattern** - Defines where cluster manifests are stored
- **ADR: Cluster Management** - Defines bootstrap vs GitOps phases
- **Spec: Spoke Pool Provisioner** - Implements this pattern for cell-based scaling

---

## References

- CAPI Ownership Model: https://cluster-api.sigs.k8s.io/reference/api/owner-references
- Crossplane + CAPI Pattern: https://www.mestredelpino.com/abstract-your-cluster-provisioning-away-with-crossplane/
- Crossplane Ownership Discussion: https://github.com/crossplane/crossplane/discussions/3116

---

**Document Version**: 1.0  
**Last Updated**: 2026-04-10  
**Next Review**: After Phase 1 completion
