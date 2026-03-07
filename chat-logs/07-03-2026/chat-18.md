# Zero-Ops Management Cluster Bootstrap - Session Summary
**Date**: March 7, 2026  
**Time**: 18:00  
**Session**: Context Transfer + Recovery Implementation

---

## Overview

This session continued the zero-ops management cluster bootstrap implementation, focusing on:
1. Reviewing pending tasks from management cluster specs
2. Updating documentation from Flatcar to Ubuntu
3. Implementing state recovery for idempotency
4. Fixing pivot issues (clusterctl download, operator installation)
5. Making provider wait logic OS-adaptive

---

## Key Decisions & Rationale

### 1. State Recovery Implementation
**Decision**: Add idempotency by checking completed phases before executing each phase  
**Rationale**: Bootstrap should resume from where it left off on failure, not restart from scratch  
**Status**: Implemented for Phases 3-8

### 2. OS-Adaptive Provider Logic
**Decision**: Pivot should wait for different providers based on OS type (kubeadm for Ubuntu, talos for Talos)  
**Rationale**: Ubuntu clusters use kubeadm bootstrap/controlplane providers, not talos providers  
**Implementation**: Added `OSType` field to pivot orchestrator

### 3. Full CAPI Operator Installation
**Decision**: Use full official operator manifest with cert-manager instead of minimal manifest  
**Rationale**: Operator webhooks require TLS certificates provisioned by cert-manager  
**Implementation**: Download and install cert-manager v1.16.2 first, then full operator v0.13.0

### 4. Binary Download Retry Logic
**Decision**: Add 3 retry attempts with exponential backoff for binary downloads  
**Rationale**: Transient network errors (HTTP/2 protocol errors) should not fail bootstrap  
**Implementation**: Updated `pkg/binaries/manager.go` with retry logic

---

## Code Changes

### File: `pkg/bootstrap/orchestrator.go`
**Changes**: Added state recovery logic for all phases
```go
// Load existing state at start of Run()
bootstrapState, err := stateMgr.Load()
if err == nil && bootstrapState != nil {
    fmt.Printf("\n[recovery] Found existing state for cluster '%s'\n", o.ClusterName)
    fmt.Printf("[recovery] Last completed phase: %s\n", bootstrapState.CurrentPhase)
    fmt.Printf("[recovery] Resuming from next phase...\n")
}

// Wrap each phase with recovery check
if !contains(bootstrapState.CompletedPhases, state.PhaseBootstrapCreate) {
    // Execute phase
    // ...
    // Update state
    bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhaseBootstrapCreate)
    bootstrapState.CurrentPhase = state.PhaseCAPIInit
    if err := stateMgr.Save(bootstrapState); err != nil {
        return fmt.Errorf("failed to save state: %w", err)
    }
} else {
    fmt.Println("[bootstrap-create] ✓ Skipped (already completed)")
}
```

**Phases with Recovery**:
- Phase 3: Bootstrap Create
- Phase 4: CAPI Init
- Phase 5: Cluster Provision
- Phase 6: Pivot
- Phase 7: ClusterClass Deploy
- Phase 8: PostBoot

### File: `pkg/pivot/orchestrator.go`
**Changes**: 
1. Added `OSType` field to struct
2. Made `waitForProvidersReady()` OS-adaptive
```go
type Orchestrator struct {
    BootstrapKubeconfig string
    ClusterName         string
    Namespace           string
    OSType              string // "ubuntu" or "talos"
}

func (o *Orchestrator) waitForProvidersReady(ctx context.Context, kubeconfig string) error {
    var bootstrapProvider, controlPlaneProvider string
    
    if o.OSType == "ubuntu" {
        bootstrapProvider = "kubeadm"
        controlPlaneProvider = "kubeadm"
    } else {
        bootstrapProvider = "talos"
        controlPlaneProvider = "talos"
    }
    // ... wait for providers
}
```

3. Added clusterctl installation in `Execute()` method
4. Changed to full cert-manager + operator installation

### File: `pkg/binaries/manager.go`
**Changes**: Added retry logic to `downloadFile()`
```go
func (m *Manager) downloadFile(url, dest string) error {
    maxRetries := 3
    for attempt := 1; attempt <= maxRetries; attempt++ {
        err := m.attemptDownload(url, dest)
        if err == nil {
            return nil
        }
        
        if attempt < maxRetries {
            backoff := time.Duration(attempt*2) * time.Second
            fmt.Printf("Download failed (attempt %d/%d), retrying in %v: %v\n", 
                attempt, maxRetries, backoff, err)
            time.Sleep(backoff)
            os.Remove(dest) // Clean up partial download
        }
    }
    return fmt.Errorf("failed after %d attempts", maxRetries)
}
```

### File: `internal/assets/manifests/addons/crs.yaml`
**Changes**: Added missing CCM secret keys
```yaml
data:
  hcloud: <base64-encoded-token>
  network: <base64-encoded-network-id>
  robot-user: ""  # Added
  robot-password: ""  # Added
```

### File: `docs/build-ubuntu-image.md`
**Changes**: Renamed from `build-flatcar-image.md` and updated content
- Changed title to "Building Ubuntu Images"
- Documented that Hetzner's official ubuntu-24.04 image is used by default
- Added optional custom image building via Packer

### Files Downloaded:
- `internal/assets/manifests/core/cert-manager/install.yaml` (v1.16.2)
- `internal/assets/manifests/core/capi-operator/install.yaml` (v0.13.0)

---

## Technical Details

### Recovery Phases Implemented
```
Phase 3: bootstrap-create (KinD cluster + namespace)
Phase 4: capi-init (CAPI operator + Hetzner secret)
Phase 5: cluster-provision (Cluster + CRS for CNI/CCM)
Phase 6: pivot (Move CAPI to mgmt cluster)
Phase 7: clusterclass-deploy (Deploy ClusterClass library)
Phase 8: postboot (ArgoCD + capi2argo + CNPG + CSI)
```

### State Structure
```go
type BootstrapState struct {
    Version           string
    ClusterName       string
    Region            string
    TalosImageId      string
    NetworkCIDR       string
    CurrentPhase      BootstrapPhase
    CompletedPhases   []BootstrapPhase
    BootstrapContext  string
    MgmtKubeconfig    string
}
```

### Bootstrap Flow
```
KinD → CAPI/CAPH install → ClusterClass apply → 
Cluster apply + CRS for CNI/CCM → Pivot → 
ArgoCD + capi2argo + CNPG install → Exit
```

---

## Issues Resolved

### Issue 1: Cluster Stuck at 2/3 Control Planes
**Root Cause**: CCM pod failing due to missing secret keys (`robot-user`, `robot-password`)  
**Resolution**: Added empty string values for missing keys in CRS secret template

### Issue 2: Pivot Failure - clusterctl Not Found
**Root Cause**: Binary download logic exists but not called during pivot  
**Resolution**: Added clusterctl installation in pivot `Execute()` method

### Issue 3: Pivot Failure - Operator Installation Timeout
**Root Cause**: Minimal operator manifest missing cert-manager, webhooks, RBAC, CRDs  
**Resolution**: 
- Downloaded full official manifests
- Install cert-manager first, wait for webhook
- Then install full operator with all components

### Issue 4: Pivot Waiting for Wrong Providers
**Root Cause**: Hardcoded to wait for `talos` providers, but Ubuntu uses `kubeadm`  
**Resolution**: Made provider selection OS-adaptive based on `OSType` field

### Issue 5: Binary Download HTTP/2 Errors
**Root Cause**: Transient network errors causing single-attempt downloads to fail  
**Resolution**: Added 3 retry attempts with exponential backoff (2s, 4s, 6s)

### Issue 6: Bootstrap Always Starts from Scratch
**Root Cause**: State management exists but not used for recovery  
**Resolution**: Implemented state loading and phase skip logic for idempotency

---

## Unresolved Issues / Follow-up Items

1. **Phase 9 Recovery**: Config management phase (kubeconfig/talosconfig) not wrapped with recovery check
2. **State Restoration**: Need to verify kubeconfig/context restoration from state works correctly
3. **Testing**: Full end-to-end test of recovery by interrupting bootstrap at different phases
4. **Validation**: Verify Phase 8 components (ArgoCD, capi2argo, CNPG, CSI) install correctly
5. **ArgoCD Password**: Verify password retrieval and display works as expected

---

## Message History

### Message 1-3: Task Review and Documentation Update
**User**: Review pending tasks, update Flatcar docs for Ubuntu  
**Assistant**: 
- Reviewed `.kiro/specs/management-cluster/tasks.md`
- Confirmed Phases 1-9 complete, Phases 10-12 pending
- Renamed `docs/build-flatcar-image.md` → `docs/build-ubuntu-image.md`
- Updated content for Ubuntu 24.04

### Message 4-8: Debug Cluster Provisioning
**User**: Cluster stuck at 2/3 control planes, investigate  
**Assistant**:
- Identified 3rd machine created but node not joining
- Found CCM pod failing due to missing secret keys
- Fixed CRS secret template to include `robot-user` and `robot-password`

### Message 9-20: Pivot Failures and Fixes
**User**: Fresh bootstrap test, pivot failures  
**Assistant**:
- Fixed clusterctl not in PATH by adding installation in pivot
- Fixed operator installation by using full manifests with cert-manager
- Made provider wait logic OS-adaptive
- Added retry logic for binary downloads

### Message 21-23: OS-Adaptive Logic and Retry
**User**: Flow must be adaptive to OS, add retry for downloads  
**Assistant**:
- Added `OSType` field to pivot orchestrator
- Updated provider selection based on OS type
- Implemented 3-retry logic with exponential backoff

### Message 24-25: State Recovery Implementation
**User**: Why bootstrap always from scratch? Support idempotency  
**Assistant**:
- Implemented state loading at start of `Run()`
- Wrapped Phases 3-8 with recovery checks
- Added `contains()` helper function
- State saves after each phase completion

### Message 26: Recovery Phases Report
**User**: Report diff recovery phases  
**Assistant**: Listed all phases with recovery implementation status

### Message 27: Create Summary
**User**: Create comprehensive summary and save to chat log  
**Assistant**: Creating this summary document

---

## Commands Executed

```bash
# Check current hour for filename
date +%H
```

---

## Environment State

**HCLOUD_TOKEN**: lHc58bsakrr9csMJtcDOn8raxgjcJMiuhrl9JZKJiEK8ekNTryDYToDcOlZ3xN3z  
**Kind Cluster**: bootstrap-zero-ops (running)  
**Kubeconfig Context**: kind-bootstrap-zero-ops  
**clusterctl Binary**: ~/.zero-ops/bin/clusterctl (manually downloaded)

**Last Bootstrap Status**:
- Cluster provisioned successfully (3 control planes, 2 workers, all Ready)
- Pivot failed due to clusterctl download HTTP/2 error (transient)
- Ready to retry with all fixes applied

---

## Next Steps

1. Test full bootstrap with recovery logic
2. Verify Phase 8 components install correctly
3. Test recovery by interrupting at different phases
4. Add Phase 9 recovery check
5. Validate ArgoCD password retrieval
6. Move to Phases 10-12 (error handling, teardown, upgrade)

---

## Files Modified

- `pkg/bootstrap/orchestrator.go` - State recovery implementation
- `pkg/pivot/orchestrator.go` - OS-adaptive providers, clusterctl install, full operator
- `pkg/binaries/manager.go` - Retry logic for downloads
- `internal/assets/manifests/addons/crs.yaml` - Added missing CCM secret keys
- `docs/build-ubuntu-image.md` - Renamed and updated from Flatcar to Ubuntu
- `internal/assets/manifests/core/cert-manager/install.yaml` - Downloaded v1.16.2
- `internal/assets/manifests/core/capi-operator/install.yaml` - Downloaded v0.13.0

---

## Key Learnings

1. **CAPI Operator Webhooks**: Require cert-manager for TLS certificate provisioning
2. **OS-Specific Providers**: Ubuntu uses kubeadm, Talos uses talos providers
3. **State Management**: Critical for idempotency in long-running bootstrap processes
4. **Network Resilience**: Retry logic essential for production-grade tooling
5. **CRS Requirements**: CCM secret must include all keys even if empty
6. **Recovery Design**: Each phase must be independently resumable with state checks

---

*End of Session Summary*
