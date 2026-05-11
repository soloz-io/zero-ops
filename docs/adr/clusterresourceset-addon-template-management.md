# ADR 0001: ClusterResourceSet Addon Template Management

**Status**: Accepted (Updated)
**Date**: 2026-05-11  
**Context**: Spoke Pool Provisioner - ClusterResourceSet Bootstrap

---

## Context

Spoke pool clusters require CNI (Cilium), CCM (Hetzner Cloud Controller Manager), and ArgoCD Agent to be applied during bootstrap via ClusterResourceSet. The Crossplane Composition references template resources that don't exist in the hub cluster, causing ClusterResourceSet to fail with `ApplyOnce` strategy.

**Root Cause**: Crossplane Composition can only reference pre-existing static resources in the hub cluster. Currently, these templates don't exist.

---

## Decision

We will use Cluster API `ClusterResourceSet` (CRS) to deliver Phase 1 (Cluster BIOS) components to newly provisioned Spoke clusters.

*   **Constraint Rule:** To comply with CAPI multi-tenancy security boundaries, all `Secret` and `ConfigMap` resources acting as CRS payloads MUST be deployed to the same namespace as the target `Cluster` objects (`platform-capi`).
*   **Asset Routing:** 
    1. Static manifests (CNI, CCM, ArgoCD Agent) are routed by ArgoCD directly to the `platform-capi` namespace via `platform-spoke-bootstrap-templates` ApplicationSet.
    2. Dynamic secrets (e.g., `infisical-auth`, `observability-ca`) originating from Hub are intercepted by Kyverno ClusterPolicies and explicitly materialized as `addons.cluster.x-k8s.io/resource-set` typed `Secrets` inside of `platform-capi` namespace.

This is the idiomatic enterprise approach used by AWS EKS Anywhere, Azure Fleet Manager, and CAPI community.

---

## Consequences

*   **Positive:** We natively satisfy CAPI's strict RBAC/security model, preventing any risk of cross-namespace payload injection.
*   **Positive:** Complete logical grouping. If a platform engineer looks at `platform-capi`, they see the complete definition of a Spoke cluster, including exact payloads that will be injected into it upon creation.
*   **Negative/Mitigation:** Platform operators must remember that while ArgoCD *controller* lives in `platform-ops`, ArgoCD Agent *bootstrap manifests* must be placed in `platform-capi` so CAPI can read them.

**Single Source of Truth**: Both hub and spoke clusters use the same CNI/CCM manifests from Git, eliminating duplication.

---

## Architecture

```
Git Repository (zero-ops/manifests/platform-ops/cluster-bios/)
  ├── cilium-addon-template.yaml (Secret)
  ├── ccm-addon-template.yaml (Secret)
  └── argocd-agent-templates.yaml (ConfigMaps + Secrets)
         ↓                                    ↓
   [Hub CLI reads]                    [ArgoCD syncs]
         ↓                                    ↓
Hub Cluster Bootstrap              Hub Cluster (platform-ops namespace)
  ├── Cilium CNI                     ├── cilium-addon-template (Secret)
  ├── Hetzner CCM                    ├── ccm-addon-template (Secret)
  └── ArgoCD Agent                   └── argocd-agent-* (ConfigMaps + Secrets)
                                              ↓
                              [Crossplane Composition references]
                                              ↓
                                    ClusterResourceSet
                                              ↓
                              [CAPI applies during bootstrap]
                                              ↓
                      Spoke Pool Cluster (kube-system, argocd namespaces)
                        ├── Cilium CNI (network ready)
                        ├── Hetzner CCM (cloud integration)
                        └── ArgoCD Agent (GitOps entry)
```

---

## Consequences

**Positive**:
- Pure declarative Kubernetes (no operators needed)
- Single source of truth for CNI/CCM (no duplication)
- Versioned templates enable staged rollouts
- Easy upgrades (update one file in Git, affects both hub and spoke)
- Idiomatic CAPI pattern (industry standard)
- Hub bootstrap CLI and spoke provisioning use identical manifests

**Negative**:
- Hub bootstrap CLI must read from Git directory (minor refactor)
- Manual template updates required for version changes (mitigated by GitOps)

---

## Alternatives Considered

**1. Custom Operator**: Rejected - adds complexity, not idiomatic  
**2. Shared Operator**: Rejected - CNI is cluster-specific, not refactorable  
**3. Dynamic Template Generation**: Rejected - Crossplane can't generate resources it references

---

## Implementation

See tasks in `zero-ops/.kiro/specs/spoke-pool-provisioner/tasks.md`:
- Phase 1.7: ClusterResourceSet Template Management
