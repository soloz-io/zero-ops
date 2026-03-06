# Scope Clarification: Management Cluster vs Tenant Cluster Services

**Date:** 2025-01-XX  
**Status:** APPROVED  
**Impact:** Design and Requirements updated

---

## Summary

The design.md and requirements.md have been updated to clarify the architectural distinction between:
1. **Management Cluster Bootstrap (Phase 1 - Journey A)** - Fixed, required components
2. **Tenant Cluster Services (Phase 2 - Journey C)** - Selectable, catalog-driven services

## The Fundamental Distinction

### Management Cluster (Phase 1)
**Purpose:** Internal platform infrastructure  
**Audience:** Platform operators (not customers)  
**Components:** Fixed, opinionated, always installed  
**Selection:** None - all components are required dependencies

```
Management Cluster
├── CAPI controllers          ← Required, always
├── CAPH controllers          ← Required, always
├── Talos providers           ← Required, always
├── Hetzner CCM               ← Required for node/LB management
├── Hetzner CSI               ← Required for storage
├── ArgoCD                    ← Required for GitOps of tenant clusters
├── capi2argo                 ← Required for auto-registering tenant clusters
└── CloudNativePG             ← Required for zero-ops-api database
```

### Tenant Clusters (Phase 2)
**Purpose:** Customer-facing Kubernetes clusters  
**Audience:** Tenant developers (customers)  
**Components:** Selectable from catalog  
**Selection:** Customer picks via UI or CLI flag

```
Tenant Cluster
├── CNI: cilium OR calico                    ← Customer choice
├── GitOps: argocd OR flux                   ← Customer choice
├── Database: cloudnative-pg OR zalando-pg   ← Customer choice
├── Autoscaling: keda                        ← Optional
└── Observability: prometheus-stack          ← Optional
```

## Changes Made

### 1. design.md Updates

**Section 3.9 - Renamed and Simplified:**
- **Old:** "Service Registry & Selection (Syself Model)"
- **New:** "Management Cluster Component Installer (Phase 1 - Fixed, Required)"
- **Change:** Replaced ServiceRegistry pattern with simple `ManagementClusterInstaller`
- **Rationale:** Management cluster has no meaningful service selection

**Section 3.10 - Added with Phase 2 Scope:**
- **New:** "Tenant Service Registry (Phase 2 - Selectable Services for Tenant Clusters)"
- **Content:** Preserved all ServiceRegistry, ServiceSelector, catalog/ design work
- **Scope Banner:** Clear warning that this applies to Phase 2, not Phase 1
- **Rationale:** Preserve valuable design work for future implementation

**BootstrapConfig - Simplified:**
- **Removed:** `Services []string` field
- **Removed:** `DefaultManagementServices()` function
- **Removed:** `--services` flag documentation
- **Added:** Comment explaining components are fixed (see Section 3.9)

### 2. requirements.md Updates

**Section 3.8 - Post-Bootstrap Components:**
- **Added:** Scope note clarifying components are REQUIRED and FIXED
- **Added:** REQ-POSTBOOT-001 (Hetzner CCM) - was missing
- **Added:** REQ-POSTBOOT-002 (Hetzner CSI) - was missing
- **Renumbered:** ArgoCD, capi2argo, CloudNativePG to 003-005
- **Added:** REQ-POSTBOOT-006 (Installation Order) - dependency sequence
- **Added:** REQ-POSTBOOT-007 (No Service Selection) - explicit prohibition

**Section 3.9 - Upgrade & Reconciliation:**
- **Updated:** Component list to include CCM and CSI
- **Updated:** ClusterClass names to Talos versions (hetzner-mgmt-talos-v1, etc.)

## Directory Structure Clarification

```
pkg/assets/
├── manifests/
│   ├── core/                   ← Phase 1: Management cluster fixed components
│   │   ├── gitops/argocd/      ← ArgoCD for management cluster
│   │   ├── gitops/capi2argo/   ← capi2argo operator
│   │   ├── databases/cnpg/     ← CloudNativePG operator
│   │   └── cloud-providers/hetzner/
│   │       ├── ccm/            ← Hetzner CCM
│   │       └── csi/            ← Hetzner CSI
│   └── classes/                ← Phase 1: ClusterClass library
│       ├── hetzner-mgmt-talos-v1.yaml
│       ├── hetzner-prod-talos-v1.yaml
│       └── hetzner-dev-talos-v1.yaml
│
└── catalog/                    ← Phase 2: Tenant cluster selectable services
    ├── gitops/argocd/          ← ArgoCD for tenant clusters (different config!)
    ├── databases/cloudnative-pg/
    ├── autoscaling/keda/
    └── ...
```

**Note:** ArgoCD appears twice intentionally:
- `manifests/core/gitops/argocd/` - Management cluster (configured for CAPI management)
- `catalog/gitops/argocd/` - Tenant clusters (user-configurable)

## CLI Command Comparison

### Phase 1 (Current - Journey A)
```bash
# Bootstrap with fixed components (no selection)
zero-ops mgmt bootstrap --name=mothership --region=fsn1

# All required components installed automatically:
# - Hetzner CCM + CSI
# - ArgoCD
# - capi2argo
# - CloudNativePG operator
```

### Phase 2 (Future - Journey C)
```bash
# Tenant cluster with service selection
zero-ops tenant cluster create \
  --name=prod-api \
  --class=hetzner-prod-v1 \
  --services=argocd,prometheus,postgres
```

## Why This Matters

### Problem 1: Wrong Audience
The catalog pattern is designed for customers to pick services for their clusters. The management cluster bootstrap has no customer - it's an internal system with fixed requirements.

### Problem 2: False Optionality
Treating CCM, CSI, ArgoCD as "selectable" creates a footgun where someone runs `--services=argocd` and gets a broken cluster missing critical infrastructure.

### Problem 3: Auto-Install Confusion
For the management cluster, you don't want auto-install from a catalog. You want a fixed, tested, version-pinned set of components that are always installed. Reliability > flexibility.

## What Was Preserved

All the Service Registry design work was preserved in Section 3.10:
- ✅ ServiceRegistry with catalog-driven auto-discovery
- ✅ ServiceSelector with topological dependency resolution
- ✅ ServiceInstaller with partial success model
- ✅ CatalogService implementation
- ✅ service.yaml metadata schema
- ✅ catalog/ directory structure

This work is valuable and correct - it's just targeted at Phase 2 (tenant cluster provisioning), not Phase 1 (management cluster bootstrap).

## Verification

The spec now correctly focuses on Journey A (Platform Bootstrap) with:
- ✅ Fixed, opinionated component installation
- ✅ No false optionality or service selection
- ✅ Clear separation between Phase 1 and Phase 2 concerns
- ✅ Preserved design work for future phases

## Next Steps

1. ✅ Design and requirements updated
2. ⏭️ Create tasks.md for implementation breakdown
3. ⏭️ Phase 2: Create spec for tenant cluster provisioning (Journey C)
4. ⏭️ Phase 2: Implement ServiceRegistry for tenant clusters

---

**Approved By:** Platform Architecture Team  
**Related Documents:**
- `.kiro/specs/management-cluster/requirements.md`
- `.kiro/specs/management-cluster/design.md`
- `.kiro/specs/syself/prds/prd.md` (Journey A)
