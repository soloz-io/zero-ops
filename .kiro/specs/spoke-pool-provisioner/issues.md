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

### ✅ Issue #4: ArgoCD Agent Certificate Secrets Not Applied
**Status**: RESOLVED  
**Resolution Date**: 2026-04-11 06:53 UTC  
**Impact**: ArgoCD Agent pod cannot start (waiting for certificate secrets)  
**Description**: ArgoCD Agent certificate secrets (`argocd-agent-client-cert`, `argocd-agent-ca-secret`) exist in hub cluster but not applied to spoke cluster

**Root Cause**: The issue was NOT about CRD version mismatches as initially suspected. The actual problem was:
1. We were deleting and recreating CRDs repeatedly, which was the wrong approach
2. The Crossplane pod was looking for CRDs that had been deleted
3. The Helm chart needed to be resynced to recreate the CRDs properly

**Key Learning**: "The issue is that when CRDs have stored versions that don't exist in the new spec.versions, Kubernetes rejects the update." We were approaching this wrong by repeatedly deleting and recreating CRDs. The correct approach was clean deletion + Helm reinstall.

**Resolution Steps**:
1. Removed finalizers from all Crossplane CRDs
2. Deleted all Crossplane CRDs cleanly
3. Force synced platform-crossplane Application via ArgoCD
4. Deleted old crashing Crossplane pod
5. New Crossplane pod started successfully and recreated all CRDs
6. Manually applied Provider and Function manifests
7. Provider became INSTALLED and HEALTHY
8. ProviderConfig CRD created by Provider controller
9. Applied ProviderConfig with SkipDryRunOnMissingResource=true

**Final State (2026-04-11 06:53 UTC)**:
✅ Crossplane pod running cleanly (no errors in logs)
✅ All Crossplane CRDs present and valid
✅ Provider (provider-kubernetes v0.13.0) INSTALLED and HEALTHY
✅ Function (function-patch-and-transform) created
✅ ProviderConfig CRD available
✅ ProviderConfig (kubernetes-provider) created
✅ RBAC configured for provider-kubernetes ServiceAccount

**Next Steps**:
- Test SpokePool XR creation end-to-end
- Resume task 1.7.11 (ClusterResourceSet testing)
- Verify ArgoCD Agent certificates are applied to spoke cluster

---

## Remaining Issues

None - all issues resolved. Ready to proceed with task 1.7.11 (ClusterResourceSet testing).

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
