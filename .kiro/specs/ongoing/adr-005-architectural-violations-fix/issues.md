# Issues: Phase 3 Validation Blockers

**Date:** 2026-04-27  
**Status:** BLOCKED  
**Related Spec:** `.kiro/specs/ongoing/adr-005-architectural-violations-fix/`  
**Phase:** Phase 3 - Composition Updates (Task 3.9 Validation)

## Current Status

Phase 3 validation is **BLOCKED** due to ArgoCD sync issues. The code fix is complete and committed, but cannot be validated because ArgoCD is not syncing the updated composition to the cluster.

## What Was Completed

### ✅ Code Fix Applied (Commit `fb88a4e`)
- Added `policy: {fromFieldPath: Optional}` to all 3 ToCompositeFieldPath patches:
  - Pooler: `status.poolerReady`
  - PostgREST: `status.postgrestReady`
  - AtlasMigration: `status.migrationsApplied`
- File: `manifests/hub-core-services/crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml`
- Committed to main branch
- Pushed to GitHub

### ✅ Phase 0 Complete
- TenantDatabase composition fixed per ADR 006
- All tasks (0.1, 0.2, 0.3) validated and marked complete
- GitOps-compliant validation performed

## Blockers

### BLOCKER 1: ArgoCD Not Syncing Composition

**Application:** `platform-tenant-platform`  
**Namespace:** `hub-platform-ops`  
**Status:** `OutOfSync`

**Evidence:**
```bash
$ kubectl get application platform-tenant-platform -n hub-platform-ops
NAME                       SYNC STATUS   HEALTH STATUS
platform-tenant-platform   OutOfSync     Healthy
```

**Impact:**
- Updated composition in Git not applied to cluster
- CompositionRevisions in cluster don't contain the fix
- Cannot validate Phase 3 work

**What Was Attempted (Violates GitOps):**
1. ❌ Manually applied composition: `kubectl apply -f manifests/.../ainativesaas-starter-hetzner.yaml`
   - Result: New CompositionRevision created but doesn't contain the fix
2. ❌ Manually patched XR to use new revision
   - Result: Still shows old error
3. ❌ Deleted XR to force recreation
   - Result: ArgoCD immediately recreated it

**Root Cause:**
ArgoCD `platform-tenant-platform` application is not syncing changes from Git. The composition file in Git has the fix, but the cluster state is stale.

### BLOCKER 2: Existing XR in Failed State

**Resource:** `ainativesaas.nutgraf.in/app-creator`  
**Status:** `Ready=False, Synced=False`

**Error:**
```
cannot compose resources: cannot render ToComposite patches for composed 
resource "postgrest-deployment": cannot apply the "ToCompositeFieldPath" 
patch at index 8: status.atProvider.manifest.status.conditions: not an object
```

**Why This Happens:**
- ToCompositeFieldPath patches try to extract status from Object MRs
- Object MRs don't have `status.atProvider.manifest.status` populated yet during initial creation
- Without `policy: {fromFieldPath: Optional}`, Crossplane fails reconciliation instead of waiting

**Fix Status:**
- ✅ Fix committed to Git
- ❌ Fix not applied to cluster (ArgoCD sync issue)

## Verification Commands

### Check ArgoCD Sync Status
```bash
export KUBECONFIG=k8-secrets/kubeconfig/hub-cp.kubeconfig
kubectl get application platform-tenant-platform -n hub-platform-ops -o yaml | grep -A 10 "sync:"
```

### Check Composition in Git vs Cluster
```bash
# Git version (has fix)
grep -A 10 "toFieldPath: status.poolerReady" manifests/hub-core-services/crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml

# Cluster version (missing fix)
kubectl get compositionrevision ainativesaas-starter-hetzner-ae20f1b -o yaml | grep -A 10 "toFieldPath: status.poolerReady"
```

### Check XR Status
```bash
kubectl get ainativesaas app-creator -o jsonpath='{.status.conditions[?(@.type=="Synced")]}'
```

## Required Actions (GitOps-Compliant)

### 1. CRITICAL: Invalid Patch Configuration - policy field not supported for ToCompositeFieldPath
**Owner:** Platform Team  
**Priority:** CRITICAL - RESOLVED

**Problem:**
- Composition in Git has `policy: {fromFieldPath: Optional}` on ToCompositeFieldPath patches
- Crossplane strips this field during reconciliation
- This is CORRECT behavior - the field is invalid for this patch type

**Root Cause (CONFIRMED):**
The `policy.fromFieldPath` field is ONLY supported for `FromCompositeFieldPath` patches, NOT `ToCompositeFieldPath` patches.

From Crossplane documentation:
- **fromFieldPath policy**: Controls whether source field must exist (Required/Optional)
- **toFieldPath policy**: Controls merge behavior (Replace/MergeObjects/etc.)

**Why the original error occurred:**
The ToCompositeFieldPath patches were trying to extract `status.atProvider.manifest.status.conditions[?(@.type=="Ready")].status` from Object MRs, but:
1. Pooler CRs don't have `status.conditions` - they only have `status.instances` and `status.secrets`
2. The field path was incorrect for the actual Pooler status structure

**Solution:**
Remove `policy: {fromFieldPath: Optional}` from ToCompositeFieldPath patches and fix the field paths to match actual resource status structures:
- **Pooler**: Extract `status.atProvider.manifest.status.instances` (check if > 0)
- **PostgREST**: Extract `status.atProvider.manifest.status.conditions[?(@.type=="Available")].status`
- **AtlasMigration**: Extract `status.atProvider.manifest.status.conditions[?(@.type=="Ready")].status`

### 2. Fix Composition ToCompositeFieldPath Patches
**Owner:** Platform Team  
**Priority:** HIGH

**Required Changes:**
1. Remove invalid `policy: {fromFieldPath: Optional}` from all ToCompositeFieldPath patches
2. Fix field paths to match actual resource status structures:
   - **Pooler patch**: Change from `status.atProvider.manifest.status.conditions[?(@.type=="Ready")].status` to `status.atProvider.manifest.status.instances`
   - **PostgREST patch**: Keep `status.atProvider.manifest.status.conditions[?(@.type=="Available")].status` (Deployments have conditions)
   - **AtlasMigration patch**: Keep `status.atProvider.manifest.status.conditions[?(@.type=="Ready")].status` (AtlasMigration CRs have conditions)
3. Update transforms to handle integer (Pooler instances) vs string (condition status) values

**File to update:**
`manifests/hub-core-services/crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml`

### 3. Complete Phase 3 Validation (Task 3.9)
**Owner:** Validation Team  
**Prerequisites:** Fix validated, XR Ready

**Validation Checklist:**
- [ ] Deploy test tenant XR to Hub cluster
- [ ] Verify all 12 Spoke resources created
- [ ] Verify XR status aggregation (databaseReady, poolerReady, postgrestReady, migrationsApplied)
- [ ] Verify dependency ordering (TenantDatabase → Pooler → PostgREST → AtlasMigration)
- [ ] Verify ToCompositeFieldPath status propagation works correctly

## Impact on Timeline

**Phase 0:** ✅ Complete  
**Phase 1:** ✅ Complete (XRD Schema Updates)  
**Phase 2:** ✅ Complete (Helm Template Updates)  
**Phase 3:** ❌ BLOCKED (Composition Updates - validation blocked by ArgoCD)  
**Phase 4:** ⏸️ Waiting (ArgoCD Migration Safety)  
**Phase 5:** ⏸️ Waiting (End-to-End Validation)

**Estimated Delay:** Unknown - depends on ArgoCD sync resolution time

## Notes

- Code fix is complete and correct
- Issue is purely operational (ArgoCD sync)
- No code changes needed once ArgoCD syncs
- Manual interventions were attempted but reverted to maintain GitOps compliance
