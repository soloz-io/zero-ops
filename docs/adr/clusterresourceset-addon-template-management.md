# ADR 0001: ClusterResourceSet Addon Template Management

**Status**: Proposed  
**Date**: 2026-04-10  
**Context**: Spoke Pool Provisioner - ClusterResourceSet Bootstrap

---

## Context

Spoke pool clusters require CNI (Cilium), CCM (Hetzner Cloud Controller Manager), and ArgoCD Agent to be applied during bootstrap via ClusterResourceSet. The Crossplane Composition references template resources that don't exist in the hub cluster, causing ClusterResourceSet to fail with `ApplyOnce` strategy.

**Root Cause**: Crossplane Composition can only reference pre-existing static resources in the hub cluster. Currently, these templates don't exist.

---

## Decision

Adopt the **Static Versioned Templates in Git** pattern for ClusterResourceSet addon management:

1. **Store templates as static Kubernetes resources** in `zero-ops/manifests/platform-ops/cluster-bios/`
2. **Hub bootstrap CLI reads** from the same Git directory (replaces embedded assets)
3. **ArgoCD syncs templates** to hub cluster for spoke pool provisioning
4. **Crossplane Composition references** the synced templates
5. **ClusterResourceSet applies** templates during spoke cluster bootstrap

This is the idiomatic enterprise approach used by AWS EKS Anywhere, Azure Fleet Manager, and CAPI community.

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
Hub Cluster Bootstrap              Hub Cluster (hub-platform-ops namespace)
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
