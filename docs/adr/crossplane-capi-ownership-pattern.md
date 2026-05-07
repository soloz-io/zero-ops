# ADR: Crossplane + CAPI Ownership Pattern

**Status**: Accepted  
**Date**: 2026-04-10  
**Updated**: 2026-04-11  
**Context**: Spoke Pool Provisioner (Phase 1 - Blocked)  
**Deciders**: Platform Team

---

## Context and Problem Statement

The SpokePool provisioner uses Crossplane Compositions to create CAPI clusters on Hetzner. We've encountered an ownership conflict that blocks cluster provisioning:

**Current Implementation**: Crossplane Composition creates all CAPI resources directly (Cluster, HetznerCluster, KubeadmControlPlane, MachineDeployment, etc.)

**Problem**: Crossplane sets `ownerReferences` with `controller: true` on all managed resources to the SpokePool XR. CAPI's Cluster controller expects to set ownerReferences on infrastructure resources (HetznerCluster), but cannot because Crossplane already owns them. This creates a deadlock where HetznerCluster controller waits indefinitely for the Cluster controller to set OwnerRef.

**Technical Constraint**: Crossplane Compositions can ONLY create resources that implement the `xpv1.Managed` interface. CAPI resources (Cluster, HetznerCluster, etc.) and standard Kubernetes resources (ConfigMap, Secret) do not implement this interface. Attempting to create them directly results in: "composed resource is not a managed resource".

**Impact**: Phase 1 validation blocked - clusters cannot provision.

**Key Question**: How should Crossplane and CAPI share responsibility for cluster lifecycle management while respecting Crossplane's technical constraints?

---

## Decision Drivers

- **Ownership Clarity**: Clear boundaries between Crossplane and CAPI responsibilities
- **Industry Standards**: Align with proven patterns from production platforms
- **Unblock Phase 1**: Resolve the deadlock quickly to continue validation
- **Maintainability**: Simple, understandable pattern for the team
- **CAPI Compatibility**: Work with CAPI's ownership model, not against it

---

## Considered Options

### Option 1: Wrap with provider-kubernetes (CHOSEN)
Use provider-kubernetes to wrap CAPI resources in Crossplane-managed `Object` resources. Crossplane creates the wrapper, provider-kubernetes applies the CAPI manifests, CAPI manages cluster lifecycle.

### Option 2: Use ArgoCD ApplicationSets Directly
Skip Crossplane for CAPI resources entirely, use ArgoCD to template and deploy cluster manifests.

### Option 3: Custom Provider for CAPI
Build a custom Crossplane provider that implements xpv1.Managed interface for CAPI resources.

---

## Decision Outcome

**Chosen Option**: Option 1 - Wrap with provider-kubernetes

Crossplane will use provider-kubernetes to create CAPI resources. Each CAPI resource (Cluster, ClusterResourceSet, ConfigMap, ExternalSecret) will be wrapped in a `kubernetes.crossplane.io/v1alpha2/Object` managed resource. provider-kubernetes applies these manifests to the cluster, and CAPI controllers manage the cluster lifecycle from there.

---

## Rationale

### 1. Technical Necessity

Crossplane cannot create arbitrary Kubernetes resources directly. It requires all composed resources to implement the `xpv1.Managed` interface, which includes:
- `spec.providerConfigRef` - Reference to provider configuration
- `spec.writeConnectionSecretToRef` - Connection secret management
- Status conditions following Crossplane conventions

CAPI resources and standard Kubernetes resources (ConfigMap, Secret) do not implement this interface. Without provider-kubernetes, Crossplane will reject the Composition with: "composed resource is not a managed resource".

provider-kubernetes provides the `Object` resource type that:
- Implements `xpv1.Managed` interface (satisfies Crossplane)
- Wraps arbitrary Kubernetes manifests in `spec.forProvider.manifest`
- Handles lifecycle management (create, update, delete)
- Provides status feedback to Crossplane

### 2. Clear Ownership Boundaries

| Layer | Responsibility |
|-------|---------------|
| **Crossplane** | Platform API abstraction (SpokePool XRD), composition logic |
| **provider-kubernetes** | Bridge between Crossplane and Kubernetes API |
| **CAPI** | Cluster lifecycle orchestration |
| **Infrastructure Provider (CAPH)** | Hetzner infrastructure provisioning |

This separation aligns with each system's design intent and technical constraints.

### 3. Industry Standard Pattern

Research shows mature platform teams using Crossplane with CAPI follow this pattern:
- **Crossplane**: Provides platform API abstraction and composition
- **provider-kubernetes**: Bridges Crossplane to Kubernetes API
- **CAPI**: Fully owns cluster lifecycle from creation to deletion
- **GitOps (ArgoCD/Flux)**: Applies cluster manifests and installs workloads

This is the proven production pattern for integrating Crossplane with CAPI.

### 4. CAPI's Native Ownership Model Preserved

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

By using provider-kubernetes to create only the root Cluster CR (with ClusterClass reference), CAPI maintains full control over its ownership model. CAPI creates and manages all dependent resources based on the ClusterClass template.

### 5. GitOps-First Compliance

Using provider-kubernetes allows Crossplane itself to be deployed via ArgoCD, eliminating imperative installation. The entire platform stack becomes declarative:
- ArgoCD deploys Crossplane + provider-kubernetes
- Crossplane deploys CAPI Cluster (via provider-kubernetes)
- CAPI provisions infrastructure
- ArgoCD deploys workloads to provisioned clusters

---

## Implementation Approach

### Composition Structure

**Crossplane Composition creates** (via provider-kubernetes Object wrappers):

1. **CAPI Cluster CR** (references ClusterClass)
```yaml
- name: capi-cluster
  base:
    apiVersion: kubernetes.crossplane.io/v1alpha2
    kind: Object
    spec:
      forProvider:
        manifest:
          apiVersion: cluster.x-k8s.io/v1beta1
          kind: Cluster
          metadata:
            labels:
              spoke-type: pool
          spec:
            clusterNetwork:
              pods:
                cidrBlocks: ["10.244.0.0/16"]
              services:
                cidrBlocks: ["10.96.0.0/12"]
            topology:
              class: hetzner-spoke-pool-v1
              version: v1.31.6
              controlPlane:
                replicas: 1
              workers:
                machineDeployments:
                  - class: default-worker
                    name: md-0
                    replicas: 2
              variables:
                - name: region
                  value: ""
                - name: workerMachineType
                  value: ""
  patches:
    - type: FromCompositeFieldPath
      fromFieldPath: metadata.name
      toFieldPath: spec.forProvider.manifest.metadata.name
    - type: FromCompositeFieldPath
      fromFieldPath: spec.nodePool.count
      toFieldPath: spec.forProvider.manifest.spec.topology.workers.machineDeployments[0].replicas
```

2. **ClusterResourceSet** (for bootstrap addons)
3. **ConfigMaps** (per-cluster configuration)
4. **ExternalSecret** (Hetzner credentials)

All wrapped in `kubernetes.crossplane.io/v1alpha2/Object` resources.

**CAPI creates** (automatically based on ClusterClass):
- HetznerCluster (infrastructure)
- KubeadmControlPlane (control plane nodes)
- MachineDeployment (worker nodes)
- All machine templates

### Required Components

1. **Crossplane Core** (v1.14+)
2. **provider-kubernetes** (v0.13+) - REQUIRED
3. **function-patch-and-transform** - For composition logic
4. **CAPI + CAPH** - Cluster provisioning
5. **ClusterClass** - Template for cluster configuration

### Deployment Order

1. ArgoCD deploys Crossplane + provider-kubernetes (sync wave 2)
2. ArgoCD deploys CAPI + CAPH (sync wave 3)
3. ArgoCD deploys ClusterClass (sync wave 4)
4. ArgoCD deploys SpokePool XRD + Composition (sync wave 5)
5. Platform Admin applies SpokePool XR
6. Crossplane creates Object resources
7. provider-kubernetes applies CAPI manifests
8. CAPI provisions cluster

---

## Consequences

### Positive

✅ **Technically Correct**: Uses provider-kubernetes to bridge Crossplane and CAPI  
✅ **Unblocks Phase 1**: Resolves ownership conflict and technical constraint  
✅ **Industry Alignment**: Follows proven production patterns  
✅ **Clear Boundaries**: Each system manages its domain  
✅ **CAPI Native**: Works with CAPI's design, not against it  
✅ **GitOps-First**: Crossplane deployed declaratively via ArgoCD  
✅ **Proper Namespace**: Resources created in `platform-capi` by CAPI

### Negative

⚠️ **Additional Dependency**: Requires provider-kubernetes installation and management  
⚠️ **More Verbose Composition**: Each resource wrapped in Object (adds nesting)  
⚠️ **Learning Curve**: Team needs to understand provider-kubernetes patterns  
⚠️ **Debugging**: Need to check both Crossplane and provider-kubernetes logs

### Neutral

🔄 **RBAC Changes**: provider-kubernetes ServiceAccount needs CAPI permissions  
🔄 **Composition Refactor**: Existing Composition needs Object wrapping  
🔄 **Testing**: Validate provider-kubernetes creates resources as expected

---

## Alternatives Considered

### Why Not Direct CAPI Resource Creation (Original Approach)?

**Rejected Reasons**:
- Technically impossible: Crossplane requires xpv1.Managed interface
- CAPI resources don't implement this interface
- Results in: "composed resource is not a managed resource" error
- Cannot work without provider-kubernetes bridge

### Why Not ArgoCD ApplicationSets (Option 2)?

**Rejected Reasons**:
- Loses Crossplane abstraction benefits (SpokePool XRD as unified API)
- Harder to compose clusters with other resources (secrets, IAM, etc.)
- More effort to implement (~8-12 hours vs 4-6 hours)
- Doesn't leverage Crossplane's strengths
- Still need Crossplane for other platform resources

**When to Use**: If we decide Crossplane adds no value for cluster provisioning and want pure GitOps templating.

### Why Not Custom CAPI Provider (Option 3)?

**Rejected Reasons**:
- Significant development effort (weeks, not hours)
- Maintenance burden (keep up with CAPI API changes)
- Reinvents what provider-kubernetes already provides
- No clear benefit over provider-kubernetes

**When to Use**: If provider-kubernetes proves insufficient or we need CAPI-specific features not available through generic Object wrapper.

---

## Migration Path

### Phase 1: Deploy provider-kubernetes (2 hours)

1. **Remove Imperative Installation**
   - Remove Crossplane Helm installation from `internal/hub/components/installer.go`
   - Let ArgoCD manage Crossplane lifecycle

2. **Create ArgoCD Application**
   - Deploy Crossplane + provider-kubernetes + function-patch-and-transform
   - File: `manifests/argocd/apps/platform-crossplane.yaml`
   - Sync wave 2 (after core platform)

3. **Configure provider-kubernetes**
   - Create ProviderConfig for in-cluster access
   - Grant RBAC permissions to provider-kubernetes ServiceAccount

### Phase 2: Update Composition (2 hours)

1. **Wrap Resources in Object**
   - Wrap CAPI Cluster in `kubernetes.crossplane.io/v1alpha2/Object`
   - Wrap ClusterResourceSet in Object
   - Wrap ConfigMaps in Object
   - Wrap ExternalSecret in Object

2. **Update Patch Paths**
   - Change paths to point inside `spec.forProvider.manifest`
   - Example: `metadata.name` → `spec.forProvider.manifest.metadata.name`

3. **Update RBAC**
   - Change ClusterRoleBinding subject from `crossplane` to `provider-kubernetes`
   - File: `manifests/hub-core-services/crossplane/crossplane-capi-rbac.yaml`

### Phase 3: Test and Validate (2 hours)

1. **Apply Updated Composition**
   - Commit changes to Git
   - ArgoCD syncs updated Composition

2. **Create Test SpokePool XR**
   - Apply test manifest
   - Verify Crossplane creates Object resources
   - Verify provider-kubernetes applies CAPI manifests
   - Verify CAPI creates cluster infrastructure

3. **Validate Phase 1 Acceptance Criteria**
   - Complete tasks 1.5.1-1.5.4 from spoke-pool-provisioner spec
   - Verify cluster reaches Ready state
   - Proceed to Phase 2

**Total Effort**: 6 hours  
**Risk**: Low (proven pattern, clear migration path)

---

## Related Decisions

- **ADR: Dual-Repository GitOps Pattern** - Defines where cluster manifests are stored
- **ADR: Cluster Management** - Defines bootstrap vs GitOps phases
- **Spec: Spoke Pool Provisioner** - Implements this pattern for cell-based scaling

---

## References

- CAPI Ownership Model: https://cluster-api.sigs.k8s.io/reference/api/owner-references
- Crossplane provider-kubernetes: https://marketplace.upbound.io/providers/crossplane-contrib/provider-kubernetes
- Crossplane Managed Resources: https://docs.crossplane.io/latest/concepts/managed-resources/
- Crossplane + CAPI Integration: https://www.mestredelpino.com/abstract-your-cluster-provisioning-away-with-crossplane/
- Crossplane Ownership Discussion: https://github.com/crossplane/crossplane/discussions/3116

---

**Document Version**: 2.0  
**Last Updated**: 2026-04-11  
**Next Review**: After Phase 1 completion

## Changelog

### Version 2.0 (2026-04-11)
- **BREAKING**: Changed decision from "Let CAPI Own Lifecycle" to "Wrap with provider-kubernetes"
- Added technical constraint explanation (xpv1.Managed interface requirement)
- Updated implementation approach to use provider-kubernetes Object wrapper
- Revised consequences to reflect provider-kubernetes dependency
- Updated migration path with provider-kubernetes deployment steps
- Clarified that original approach was technically impossible

### Version 1.0 (2026-04-10)
- Initial ADR proposing direct CAPI resource creation (later found to be technically invalid)
