# Remaining Issues - Spoke Pool Provisioner

**Status**: Active  
**Last Updated**: 2026-04-10  
**Context**: Task 1.7.11 - ClusterResourceSet Template Management Testing

---

## Critical Issues

### 1. ✅ Kyverno Policy JMESPath Syntax Errors - RESOLVED
**Status**: Resolved  
**Impact**: Certificate transformation now working  
**Description**: Kyverno policies had invalid JMESPath syntax for accessing keys with dots. Fixed by using context variables to extract values first.

**Solution Applied**:
- Used `context` section with `jmesPath: 'request.object.data."ca.crt"'` to extract values
- Referenced context variables in stringData: `{{caCrt}}`
- Deleted old policies and recreated with correct syntax

**Verification**:
- ✅ Kyverno policies created successfully
- ✅ `argocd-agent-ca-secret` generated (type: addons.cluster.x-k8s.io/resource-set)
- ✅ `argocd-agent-client-cert` generated (type: addons.cluster.x-k8s.io/resource-set)

**Files Fixed**:
- `zero-ops/catalog/security/argocd-agent-cert.yaml`

---

### 2. ❌ ClusterResourceSet ApplyOnce Strategy Limitation
**Status**: Blocking  
**Impact**: ClusterResourceSet won't retry after initial failure, resources not applied to spoke cluster  
**Description**: ClusterResourceSet uses `ApplyOnce` strategy which means it attempts to apply resources only once. If ANY resource is missing during the first attempt, it fails and never retries - even after the missing resources are created.

**Root Cause**:
- ClusterResourceSet was created before Kyverno-generated secrets existed
- First attempt failed with "argocd-agent-client-cert not found"
- With `ApplyOnce` strategy, it never retries even though secrets now exist
- Deleting and recreating ClusterResourceSet causes new timing issues (resources created by Crossplane in sequence)

**Current State**:
- Spoke cluster `spoke-pool-eu-prod-01` has 3 Ready nodes
- Cilium CNI working (applied during initial bootstrap, before ClusterResourceSet)
- ClusterResourceSet status: `ResourcesApplied: False`
- Latest error: `configmaps "argocd-agent-rbac" not found` (timing issue during recreation)
- Hetzner CCM pod exists but has `CreateContainerConfigError` (missing hetzner secret)
- ArgoCD Agent pod Pending (nodes have taints)
- Worker nodes have taints:
  - `node.cluster.x-k8s.io/uninitialized`
  - `node.cloudprovider.kubernetes.io/uninitialized`

**Target State**:
- Change ClusterResourceSet strategy from `ApplyOnce` to `Reconcile` (allows retries)
- ClusterResourceSet successfully applies all 9 resources
- Hetzner secret applied to spoke cluster kube-system namespace
- CCM starts and removes node taints
- ArgoCD Agent pod starts successfully

**Solution**:
- Update Crossplane Composition to use `strategy: Reconcile` instead of `ApplyOnce`
- This allows ClusterResourceSet to retry when resources become available

**Related Files**:
- `zero-ops/xrds/compositions/spokepool-hetzner.yaml` (ClusterResourceSet strategy)

---

### 3. ⚠️ Manual Workaround Secrets in Production
**Status**: Technical Debt  
**Impact**: Not GitOps-compliant, requires cleanup  
**Description**: Manual ClusterResourceSet-compatible secrets were created to unblock spoke cluster provisioning while Kyverno policies are being fixed.

**Current State**:
- Manual secrets exist in `hub-platform-ops` namespace:
  - `argocd-agent-ca-secret`
  - `argocd-agent-client-cert`
- These secrets have type `addons.cluster.x-k8s.io/resource-set`

**Target State**:
- Remove manual secrets once Kyverno policies work correctly
- All secrets generated automatically by Kyverno

**Cleanup Required**:
- Delete manual secrets after Issue #1 is resolved
- Verify Kyverno-generated secrets work correctly

---

## Non-Blocking Issues

### 4. ⚠️ ArgoCD Agent Pod Pending
**Status**: Blocked by Issue #2  
**Impact**: ArgoCD Agent cannot connect to hub  
**Description**: ArgoCD Agent pod exists in spoke cluster but is Pending due to node taints.

**Current State**:
- Pod exists: `argocd-agent-*` in `argocd` namespace
- Status: Pending (0/1 Ready)
- Reason: Node taints prevent scheduling

**Target State**:
- Pod Running (1/1 Ready)
- Agent connected to hub cluster
- Visible in ArgoCD UI

**Dependency**: Resolves automatically when Issue #2 is fixed

---

## Validation Checklist

Once all issues are resolved, verify:

- [x] Kyverno policies apply correctly (no syntax errors)
- [x] cert-manager secrets automatically transformed to ClusterResourceSet format
- [x] `argocd-agent-client-cert` secret exists in `hub-platform-ops` namespace
- [x] `argocd-agent-ca-secret` secret exists in `hub-platform-ops` namespace
- [ ] ClusterResourceSet strategy changed to `Reconcile`
- [ ] ClusterResourceSet status: `ResourcesApplied: True`
- [ ] All 9 resources applied to spoke cluster (verify in spoke cluster)
- [ ] Hetzner secret exists in spoke cluster kube-system namespace
- [ ] Hetzner CCM pod Running (1/1 Ready)
- [ ] Node taints removed by CCM
- [ ] ArgoCD Agent pod Running (1/1 Ready)
- [ ] ArgoCD Agent connected to hub (visible in ArgoCD UI)
- [ ] Manual workaround secrets deleted
- [ ] Task 1.7.11 validation steps pass

---

## Related Documentation

- ADR: `zero-ops/docs/adr/0001-clusterresourceset-addon-template-management.md`
- Design Spec: `zero-ops/.kiro/specs/spoke-pool-provisioner/design.md`
- Tasks: `zero-ops/.kiro/specs/spoke-pool-provisioner/tasks.md` (Task 1.7.11)
- Integration Docs:
  - `zero-ops/.kiro/specs/spoke-pool-provisioner/resources/integrations/03-argocd-agent-integration.md`
  - `zero-ops/.kiro/specs/spoke-pool-provisioner/resources/integrations/10-cert-manager-integration.md`

---

**Next Steps**: 
1. ✅ Issue #1 resolved - Kyverno policies fixed and secrets generated
2. Change ClusterResourceSet strategy from `ApplyOnce` to `Reconcile` in Crossplane Composition
3. Verify ClusterResourceSet applies all resources to spoke cluster
4. Verify CCM starts and removes node taints
5. Verify ArgoCD Agent starts and connects to hub
6. Clean up manual workaround secrets (Issue #3)
