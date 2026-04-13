# Remaining Issues - Spoke Pool Provisioner

**Status**: Active  
**Last Updated**: 2026-04-12 23:55 UTC  
**Context**: Hub Infrastructure - ArgoCD Agent Successfully Connected via mTLS

---

## Current Issues

### ❌ Issue #31: Missing Hetzner CSI Driver - Not in ClusterResourceSet

**STATUS**: FIXED - Pending verification  
**ROOT CAUSE**: Hub got CSI via bootstrap script, Spoke clusters don't have CSI in ClusterResourceSet  
**FIX**: Created `csi-addon-template.yaml`, added to ClusterResourceSet after CCM  
**VERIFICATION**: Deleted spoke cluster, waiting for recreation with CSI  
**COMMIT**: f1fb809

---

## Resolved Issues

### ✅ Issue #30: CRD Version Mismatch - Operator 1.29 vs CRDs 1.24

**STATUS**: RESOLVED  
**ROOT CAUSE**: Spoke CRDs from `release-1.24` missing FailoverQuorum/Databases/Publications/Subscriptions  
**FIX**: Updated `targetRevision: release-1.29` in spoke-cnpg-crds ApplicationSet  
**VERIFIED**: All 10 CRDs deployed, CNPG Cluster reconciling  
**COMMIT**: dc2bc02

---

## Resolved Issues

### ✅ Issue #29: Pooler Invalid Parameter

**STATUS**: RESOLVED (2026-04-13 10:35 UTC)  
**Root Cause**: Invalid pgbouncer parameters  
**Solution**: Used Hub Pooler parameter pattern ✅  
**Verified**: Pooler created successfully ✅  
**Commits**: fd534e1

---

## Resolved Issues

### ✅ Issue #27: ClusterResourceSet Array Index Bug

**STATUS**: RESOLVED (2026-04-13 10:15 UTC)  
**Root Cause**: Composition patches wrong array indices  
**Solution**: Changed resources[9]→[8], resources[11]→[10]  
**Verified**: CRS now lists all 12 resources correctly ✅  
**Commits**: 28aa60e

---

## Resolved Issues

### ✅ Issue #28: Missing allocate-node-cidrs Configuration

**STATUS**: RESOLVED (2026-04-13 10:15 UTC)  
**Root Cause**: External cloud provider disables node CIDR allocation  
**Solution**: Added `allocate-node-cidrs: "true"` to controllerManager  
**Verified**: Nodes Ready, Cilium running ✅  
**Commits**: 28aa60e

---

## Resolved Issues

### ✅ Issue #26: Cilium CNI Not Deployed

**STATUS**: RESOLVED (2026-04-13 10:18 UTC)  
**Root Cause**: Combination of Issue #27 + #28  
**Verified**: Cilium deployed, nodes Ready ✅

---

## Resolved Issues

### ✅ Issue #25: CNPG Pooler Field Not Declared

**STATUS**: RESOLVED (2026-04-13 10:20 UTC)  
**Root Cause**: CNPG uses separate Pooler CRD  
**Solution**: Split into Cluster + Pooler CRDs ✅  
**Verified**: CNPG Cluster created (pending Pooler fix - Issue #29)  
**Commits**: 546858c

---

## Current Issues

### ❌ Issue #25: CNPG Pooler Field Not Declared in Schema

**STATUS**: FIXED - Pending verification (blocked by Issue #26)  
**Root Cause**: CNPG uses separate `Pooler` CRD, not inline field  
**Solution**: Split into Cluster + Pooler CRDs (commit 546858c) ✅  
**Files**: `manifests/spoke-catalog/infra/cnpg-cluster.yaml`, `cnpg-pooler.yaml`

---

## Current Issues

### ✅ Issue #24: Directory-Based Application Incompatible with argocd-agent Managed Mode

**STATUS**: RESOLVED (2026-04-13 08:15 UTC)  
**Root Cause**: Applications created by directory-based Application don't get `argocd-agent.argoproj-labs.io/source-uid` annotation  
**Investigation**:
- In argocd-agent Managed Mode, Applications MUST be created by the Principal (via event stream)
- Directory-based Applications are created by ArgoCD's Application controller, bypassing Principal
- Principal only adds source-uid annotation when it creates Applications via event stream
- Agent filters out Applications without source-uid annotation (they're considered "unmanaged")

**Solution**: Switched to Hub-side ApplicationSets with destination-based mapping
1. Enabled destination-based-mapping on Principal and Agent
2. Deleted directory-based ApplicationSet and `manifests/spoke-catalog/apps/` directory
3. Created 4 Hub-side ApplicationSets that generate individual Applications per spoke:
   - `spoke-cnpg-crds` (sync-wave: -1)
   - `spoke-cnpg-operator` (sync-wave: 0)
   - `spoke-atlas-operator` (sync-wave: 2)
   - `spoke-infrastructure` (sync-wave: 4)
4. Each ApplicationSet uses `destination.name: '{{.name}}'` for proper argocd-agent routing
5. Added `argocd-cmd-params-cm` to spoke bootstrap to watch `hub-platform-ops` namespace
6. Manually applied ConfigMap to existing spoke cluster and restarted application-controller

**Verified**:
- Hub ApplicationSets successfully generated individual Applications ✅
- Applications have `source-uid` annotation on Spoke ✅
- Spoke ArgoCD application-controller watching `hub-platform-ops` namespace ✅
- CNPG operator deployed and running (2 pods) ✅
- Atlas operator deployed and running (2 pods) ✅
- Applications reconciling correctly ✅

**Files Modified**:
- `manifests/argocd-principal/principal-params-cm.yaml` (destination-based-mapping: true)
- `xrds/compositions/spokepool-hetzner.yaml` (Agent config with destination-based-mapping)
- `manifests/spoke-bootstrap/argocd-core-template.yaml` (added argocd-cmd-params-cm)
- `manifests/argocd/apps/platform-spoke-catalog-appsets.yaml` (Hub-side ApplicationSets)
- Deleted: `manifests/spoke-catalog/apps/` directory

**Commits**: 761a0c4, 87108f2, 1938fc7

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

### ✅ Issue #23: ArgoCD Agent Cluster Mapping - Missing Label (PARTIALLY RESOLVED)
**Status**: PARTIALLY RESOLVED (2026-04-13 06:15 UTC)  
**Part 1 - Cluster Mapping**: ✅ RESOLVED
- **Root Cause**: Cluster Secret missing `argocd-agent.argoproj-labs.io/agent-name` label
- **Solution**: Added take-along-label to CAPI Cluster in Composition, manually labeled existing Secret
- **Verified**: Principal ClusterManager successfully mapped cluster to agent

**Part 2 - Application Sync**: ❌ BLOCKED (moved to Issue #24)
- Applications created by directory-based Application lack `source-uid` annotation
- Agent cannot process Applications without this annotation
- Requires architectural change to ApplicationSet pattern

**Commits**: 6b2d7a6, 396975e

---

## Summary

**Bootstrap Phase**: ✅ COMPLETE  
**Phase 1 Validation**: ✅ COMPLETE  
**Phase 1.8 Hub Infrastructure**: ✅ COMPLETE  
**Phase 2 Spoke Catalog**: ❌ BLOCKED (Issue #30 - Missing FailoverQuorum CRD)
**Total Issues Resolved**: 29 (Issue #29 resolved - Pooler created)  
**Current Blockers**: 1 (Issue #30 - CRD version mismatch)

**Key Achievements**:
- Spoke cluster provisioning end-to-end (23m 18s first cluster)
- ArgoCD Agent successfully connected to Principal via mTLS
- NGINX TCP proxy (port 8443) for Layer 4 passthrough
- Bidirectional event stream established
- GPG keys syncing between hub and spoke
- Cluster mapping working (Principal → Agent)
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
