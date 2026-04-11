# Remaining Issues - Spoke Pool Provisioner

**Status**: Active  
**Last Updated**: 2026-04-11 14:21 UTC  
**Context**: Hub Infrastructure - ArgoCD Principal Deployment Complete

---

## Current Issues

### ⚠️ Issue #16: ArgoCD Agent mTLS CA Mismatch
**Status**: BLOCKING | **Date**: 2026-04-11 15:09 UTC | **Severity**: HIGH  
**Description**: Agent cannot verify Principal cert - different CA fingerprints (Hub: 9A:80:7A..., Spoke: 69:E3:D9...)  
**Root Cause**: CRS injects separate CA during spoke bootstrap, Agent and Principal don't share same CA  
**Blocks**: Task 1.8.9 (Agent connection verification)  
**Completed**: Redis deployed, NodePort service (188.34.180.61:31784), Principal cert with IP SAN  
**Next**: Sync Hub CA to spoke cluster or regenerate with shared CA

---

## Resolved Issues

### ✅ Issue #15: ArgoCD Principal JWT Key Parse Error
**Status**: RESOLVED (2026-04-11 14:21 UTC)  
**Fix**: Changed JWT certificate encoding from PKCS#1 to PKCS#8 in certificates.yaml, deleted/recreated certificate  
**Verified**: Principal pod Running 1/1, logs show successful startup with all informers synced

### ✅ Issue #14: ArgoCD Agent Cannot Connect to Hub
**Status**: RESOLVED (2026-04-11 14:21 UTC) - Redis and Principal deployed and running

### ✅ Issue #13: ArgoCD Agent Missing Application CRD
**Status**: RESOLVED (2026-04-11 12:30 UTC)  
**Fix**: Added `argocd-crds-template` Secret with Application and AppProject CRDs to ClusterResourceSet  
**Verified**: CRDs injected, ArgoCD Agent pod Running 1/1

### ✅ Issue #12: ClusterResourceSetBinding Not Reconciling
**Status**: RESOLVED (2026-04-11 12:05 UTC)  
**Fix**: Delete ClusterResourceSetBinding to force re-injection  
**Learning**: Always delete binding after updating ClusterResourceSet resources

### ✅ Issue #11: ArgoCD Agent TLS Secret Name Mismatch
**Status**: RESOLVED (2026-04-11 12:00 UTC)  
**Fix**: Updated ConfigMap to reference correct secret names

### ✅ Issue #10: ArgoCD Agent ConfigMap Name Mismatch
**Status**: RESOLVED (2026-04-11 11:55 UTC)  
**Fix**: Changed embedded manifest name to `argocd-agent-config`

### ✅ Issue #9: ArgoCD Agent ConfigMap Formatting Error
**Status**: RESOLVED (2026-04-11 11:50 UTC)  
**Fix**: Moved static ConfigMap YAML to `base` section, removed problematic transform

### ✅ Issue #8: Hetzner Credentials ExternalSecret 404
**Status**: RESOLVED (2026-04-11 11:45 UTC)  
**Fix**: Updated ExternalSecret to fetch from `key: hcloud` (plain secret, no property)

### ✅ Issue #7: provider-kubernetes RBAC Permissions
**Status**: RESOLVED (2026-04-11 11:15 UTC)  
**Fix**: Added `serviceAccountName: provider-kubernetes` to DeploymentRuntimeConfig

### ✅ Issue #6: Crossplane XRD Schema Validation Error
**Status**: RESOLVED (2026-04-11 11:00 UTC)  
**Fix**: Used `provider-kubernetes` `Object` resource (cluster-scoped) to wrap namespaced resources

### ✅ Issue #5: ArgoCD Agent Image Incorrect
**Status**: RESOLVED (2026-04-11 10:50 UTC)  
**Fix**: Changed to `ghcr.io/argoproj-labs/argocd-agent/argocd-agent:v0.8.1`

### ✅ Issue #4: Crossplane CRD Version Conflicts
**Status**: RESOLVED (2026-04-11 06:53 UTC)  
**Fix**: Clean deletion + Helm reinstall approach

### ✅ Issue #3: ClusterResourceSet Strategy
**Status**: RESOLVED - Composition already uses `strategy: Reconcile`

### ✅ Issue #2: Hetzner Secret Namespace Mismatch  
**Status**: RESOLVED (2026-04-11 12:00 UTC)  
**Fix**: Changed CCM namespace from `hub-cloud-system` to `kube-system` in spoke clusters

### ✅ Issue #1: Kyverno Policy JMESPath Syntax Errors
**Status**: RESOLVED - Updated Kyverno policies with correct JMESPath syntax

---

## Summary

**Bootstrap Phase**: ✅ COMPLETE  
**Phase 1 Validation**: ✅ COMPLETE  
**Phase 1.8 Hub Infrastructure**: 🔄 IN PROGRESS (blocked by Issue #16)  
**Total Issues Resolved**: 15  
**Current Blockers**: 1 (Issue #16 - Agent connection config)

**Key Achievements**:
- Spoke cluster provisioning end-to-end (23m 18s first cluster)
- ArgoCD Agent pod Running 1/1 with CRDs
- Kyverno cluster discovery working
- Redis StatefulSet deployed and running
- ArgoCD Principal deployed and running with cert-manager PKI automation
- All manifests committed via GitOps

---

## Related Documentation

- ADR: `docs/adr/0001-clusterresourceset-addon-template-management.md`
- ADR: `docs/adr/namespace-alignment.md`
- Design: `.kiro/specs/spoke-pool-provisioner/design.md`
- Tasks: `.kiro/specs/spoke-pool-provisioner/tasks.md`
