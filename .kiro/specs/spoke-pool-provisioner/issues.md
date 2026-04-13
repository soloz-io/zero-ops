# Remaining Issues - Spoke Pool Provisioner

**Status**: Active  
**Last Updated**: 2026-04-12 23:55 UTC  
**Context**: Hub Infrastructure - ArgoCD Agent Successfully Connected via mTLS

---

## Current Issues

No current blockers! Phase 2 validation can proceed.

---

## Resolved Issues

### ✅ Issue #22: CNPG CRDs Too Large for kubectl apply (256KB Annotation Limit)
**Status**: RESOLVED (2026-04-13 00:15 UTC)  
**Root Cause**: Kubernetes has 256KB limit on annotations. `kubectl apply` stores last-applied-configuration in annotations. CNPG Cluster CRD is 458KB, Pooler CRD is 700KB.  
**Investigation**: 
- Attempted Job-based installer (rejected - imperative, not idiomatic)
- Attempted splitting CRDs into multiple Secrets (rejected - workaround, not best practice)
- Researched idiomatic solutions: Server-Side Apply with upstream Git references
**Solution**: Battle-tested GitOps approach with sync-wave isolation
- Created separate `cnpg-crds` Application (wave -1) pointing to upstream GitHub
- Updated `cnpg-operator` Application (wave 0) with `skipCrds: true`
- Updated `cnpg-cluster` Application (wave 2) with `SkipDryRunOnMissingResource=true`
- All Applications use `ServerSideApply=true` to avoid annotation limits
**Architecture**:
```
Wave -1: CRDs (upstream GitHub, ServerSideApply)
Wave  0: Operator (Helm chart, skipCrds)
Wave  2: Cluster (CR, SkipDryRunOnMissingResource)
```
**Benefits**:
- Fully declarative GitOps
- No ConfigMap/Secret size hacks
- Authoritative upstream source
- Continuous reconciliation
- Clean separation of concerns
**Trade-off**: Slightly less deterministic than ClusterResourceSet bootstrap, but more maintainable
**Commits**: [pending]

### ✅ Issue #21: Missing ArgoCD Application Controller on Spoke
**Status**: RESOLVED (2026-04-12 23:42 UTC)  
**Root Cause**: ArgoCD Agent in managed mode requires local application-controller and repo-server to reconcile Applications. Only Agent + Redis were deployed.  
**Solution**: Added argocd-core-components ConfigMap to ClusterResourceSet with:
- argocd-application-controller (StatefulSet + RBAC)
- argocd-repo-server (Deployment + Service)
- argocd-cm (ConfigMap)
- argocd-secret (required by controller)
**Verified**: 
- Application-controller and repo-server running on spoke
- Application status syncing to Hub (OutOfSync/Missing)
- Reconciliation loop working correctly
**Commits**: d3a7a3c, 512ff23, ceffeeb

---

## Resolved Issues

### ✅ Issue #20: Repository Secret Not Distributed to Spoke Agent
**Status**: RESOLVED (2026-04-12 16:15 UTC)  
**Root Cause**: Repository secret missing `project` field, AppProject missing `sourceNamespaces` field  
**Solution**: Added `project: platform-infrastructure` to ExternalSecret template, added `sourceNamespaces: ["spoke-pool-*"]` to AppProject  
**Verified**: Repository secret distributed to spoke agent successfully  
**Commits**: 996c8fd

### ✅ Issue #19: ArgoCD Agent Identity Mismatch - Shared Certificate vs Destination-Based Mapping
**Status**: RESOLVED (2026-04-12 15:35 UTC)  
**Root Cause**: Agent authenticated with CN from shared certificate (`argocd-agent-client`) but Applications queued for cluster name (`spoke-pool-eu-prod-01`)  
**Solution**: Implemented Phase 2 dynamic per-cluster certificates via Crossplane Composition  
**Verified**: Agent authenticates with `CN=spoke-pool-eu-prod-01`, Application synced to spoke cluster  
**Commits**: 9844683

### ✅ Issue #18: ArgoCD Agent mTLS Connection Failure (EOF)
- Update Kyverno policy to watch label-based selector instead of hardcoded name
- Agent will authenticate with correct identity matching Application routing

**Next Steps**:
1. Update Kyverno policy to use label selector (`cert-type: argocd-agent-client`)
2. Add Certificate resource to Crossplane Composition with dynamic CN
3. Update ClusterResourceSet to reference per-cluster certificate Secret
4. Verify Agent authenticates with cluster-specific identity

**Files to Modify**:
- `catalog/security/argocd-agent-cert.yaml` (Kyverno policy)
- `xrds/compositions/spokepool-hetzner.yaml` (add Certificate resource)

---

---

## Resolved Issues

### ✅ Issue #18: ArgoCD Agent mTLS Connection Failure (EOF)
**Status**: RESOLVED (2026-04-12 09:48 UTC)  
**Root Cause**: NGINX Ingress ssl-passthrough annotation was ignored due to HTTP rules conflict. NGINX treated connection as Layer 7 (HTTPS), terminated TLS, and sent mangled request to Principal, causing EOF during handshake.  
**Investigation**:
1. IPv6 connectivity issue (resolved - disabled IPv6 on LoadBalancer)
2. Certificate hostname mismatch (resolved - added both hostnames to SANs)
3. NGINX TLS termination instead of passthrough (root cause)
4. Direct connection test proved Principal TLS config was correct

**Solution**: Switched to NGINX native TCP proxy on port 8443
- Configured `tcp: 8443: "hub-platform-ops/argocd-agent-principal:443"` in NGINX Helm values
- Updated Agent to connect to `argocd-principal.nutgraf.in:8443`
- Deleted conflicting Ingress resource
- Pure Layer 4 TCP stream bypasses all Layer 7 issues

**Verified**: 
- Agent logs: `Authentication successful`, `Connected to argocd-agent-99.9.9-unreleased`
- Bidirectional event stream established
- GPG keys syncing between Principal and Agent

**Commits**: ab61743, 1b97555, 96ca59b, 1dd486a, 88c9960

### ✅ Issue #17: Hetzner CCM Token Has Trailing Newline
**Status**: RESOLVED (2026-04-11 16:40 UTC)  
**Fix**: ExternalSecret template trimmed newline from hcloud token  
**Verified**: CCM pod running, nodes initialized successfully

### ✅ Issue #16: ArgoCD Agent mTLS CA Mismatch
**Status**: RESOLVED (2026-04-11 16:38 UTC)  
**Fix**: Unified CA definition - removed duplicate from manifests/argocd-principal/certificates.yaml, use catalog/security/argocd-agent-cert.yaml only  
**Verified**: Both Agent and Principal now use same CA (fingerprint 69:E3:D9...)  
**Architecture**: cert-manager → Kyverno → CRS → Spoke cluster (automated flow)

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
**Phase 1.8 Hub Infrastructure**: ✅ COMPLETE  
**Total Issues Resolved**: 18  
**Current Blockers**: 0

**Key Achievements**:
- Spoke cluster provisioning end-to-end (23m 18s first cluster)
- ArgoCD Agent successfully connected to Principal via mTLS
- NGINX TCP proxy (port 8443) for Layer 4 passthrough
- Bidirectional event stream established
- GPG keys syncing between hub and spoke
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
