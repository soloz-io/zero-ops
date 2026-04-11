# Remaining Issues - Spoke Pool Provisioner

**Status**: Active  
**Last Updated**: 2026-04-11 05:15 UTC  
**Context**: Task 1.7.11 - ClusterResourceSet Template Management Testing

---

## Resolved Issues

### ✅ Issue #1: Kyverno Policy JMESPath Syntax Errors
**Status**: Resolved  
**Fix**: Updated Kyverno policies with correct JMESPath syntax using context variables

### ✅ Issue #2: Hetzner Secret Namespace Mismatch
**Status**: Resolved  
**Fix**: Changed secret namespace from `kube-system` to `hub-cloud-system` in Crossplane Composition  
**Verified**: CCM pod Running, node taints removed, nodes Ready

### ✅ Issue #3: ClusterResourceSet Strategy
**Status**: Resolved  
**Fix**: Composition already uses `strategy: Reconcile`

---

## Remaining Issues

### ❌ Issue #4: ArgoCD Agent Certificate Secrets Not Applied
**Status**: In Progress  
**Impact**: ArgoCD Agent pod cannot start (waiting for certificate secrets)  
**Description**: ArgoCD Agent certificate secrets (`argocd-agent-client-cert`, `argocd-agent-ca-secret`) exist in hub cluster but not applied to spoke cluster

**Root Cause**: Crossplane CRD version conflicts prevented deployment

**Current State**:
- Crossplane was installed imperatively (April 9) with v1beta1 CRDs
- Attempted GitOps deployment via ArgoCD failed due to CRD version mismatch
- Crossplane 1.14.5 expects v1alpha1 but existing CRDs had v1beta1 stored versions
- **Resolution in Progress**: Uninstalled Crossplane completely, reinstalling clean

**Actions Taken**:
1. Consolidated all Crossplane manifests under `manifests/crossplane/`
2. Created single ArgoCD Application with multi-source (Helm chart + manifests)
3. Added proper sync-wave annotations:
   - Wave 0: RBAC, ServiceAccount
   - Wave 1: Provider, Function
   - Wave 2: ProviderConfig
4. Deleted conflicting CRDs (environmentconfigs, functionrevisions, locks)
5. Reinstalled Crossplane via ArgoCD - pods now Running
6. Applied provider-kubernetes and function manifests
7. Waiting for provider-kubernetes to install CRDs before applying ProviderConfig

**Next Steps**:
- Wait for provider-kubernetes to become Healthy
- Apply ProviderConfig once CRDs are installed
- Apply updated Composition v2 with provider-kubernetes Object wrappers
- Test SpokePool XR creation end-to-end

---

### ⚠️ Issue #5: Manual Workaround Secrets
**Status**: Technical Debt  
**Impact**: Not GitOps-compliant  
**Description**: Manual secrets created for testing need cleanup

**Cleanup Required**:
- Remove manual `hetzner` secret from spoke cluster `hub-cloud-system` namespace
- Remove manual certificate secrets from hub cluster (if any)
- Ensure all secrets generated via GitOps flow

---

## Next Steps

1. Apply ArgoCD Agent certificate secrets to spoke cluster
2. Verify ArgoCD Agent pod starts and connects to hub
3. Clean up manual workaround secrets
4. Complete Task 1.7.11 validation

---

## Related Documentation

- ADR: `docs/adr/0001-clusterresourceset-addon-template-management.md`
- ADR: `docs/adr/namespace-alignment.md`
- Design: `.kiro/specs/spoke-pool-provisioner/design.md`
- Tasks: `.kiro/specs/spoke-pool-provisioner/tasks.md` (Task 1.7.11)
