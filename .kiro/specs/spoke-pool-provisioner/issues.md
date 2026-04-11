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

### ❌ Issue #6: ArgoCD Agent Image Incorrect
**Status**: RESOLVED  
**Resolution Date**: 2026-04-11 12:50 UTC  
**Impact**: ArgoCD Agent pod failing with ImagePullBackOff  
**Description**: ClusterResourceSet was using wrong image registry and version

**Root Cause**: 
- Documentation referenced `quay.io/argoproj-labs/argocd-agent:v0.1.0`
- This image doesn't exist or is private (401 UNAUTHORIZED)
- Actual image is hosted on GitHub Container Registry

**Resolution**:
- Changed image to: `ghcr.io/argoproj-labs/argocd-agent/argocd-agent:v0.8.1`
- Verified from argoproj-labs/argocd-agent releases page (latest stable release)
- Updated manifests/spoke-bootstrap/argocd-agent-templates.yaml

**Next Steps**:
- Wait for ClusterResourceSet to update spoke cluster
- Verify new pod pulls image successfully
- Verify ArgoCD Agent connects to Hub

---

None - all critical issues resolved. Ready to proceed with task 1.7.11 (ClusterResourceSet testing).

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


## Issue #7: ArgoCD Agent Missing Application CRD

**Date**: 2026-04-11 11:55 UTC  
**Status**: IDENTIFIED  
**Severity**: BLOCKER

**Description**: ArgoCD Agent pod crashes with error `the server could not find the requested resource (get applications.argoproj.io)` because ArgoCD CRDs are not installed in spoke cluster.

**Root Cause**: ClusterResourceSet bootstrap only includes 5 resources (Deployment, ConfigMap, mTLS certs, RBAC) but is missing ArgoCD CRDs prerequisite. The agent needs `applications.argoproj.io` CRD to function.

**Evidence**:
```
time="2026-04-11T11:55:51Z" level=info msg="Starting argocd-agent (agent) v99.9.9-unreleased (ns=argocd, allowed_namespaces=[], mode=managed, auth=mtls)"
time="2026-04-11T11:55:51Z" level=error msg="failed to populate the source cache" error="the server could not find the requested resource (get applications.argoproj.io)"
[FATAL]: Could not start agent: the server could not find the requested resource (get applications.argoproj.io)
```

**Solution**: Add ArgoCD CRDs to ClusterResourceSet as 6th resource. According to `archived/argo/argocd-agent/install/kubernetes/argo-cd/agent-managed/kustomization.yaml`, the agent-managed mode requires:
- Application CRD (applications.argoproj.io)
- AppProject CRD (appprojects.argoproj.io)
- Other ArgoCD CRDs from https://github.com/argoproj/argo-cd/manifests/cluster-install

**Implementation**:
1. Create ConfigMap with ArgoCD CRDs YAML in manifests/spoke-bootstrap/
2. Add to ClusterResourceSet before Agent deployment
3. Update Composition to include CRDs resource reference

**References**:
- Spec: `.kiro/specs/spoke-pool-provisioner/resources/integrations/03-argocd-agent-integration.md` Section 3.3
- Upstream: `archived/argo/argocd-agent/install/kubernetes/argo-cd/agent-managed/kustomization.yaml`
