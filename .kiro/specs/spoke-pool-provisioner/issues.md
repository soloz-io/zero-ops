# Remaining Issues - Spoke Pool Provisioner

**Status**: Active  
**Last Updated**: 2026-04-10  
**Context**: Task 1.7.11 - ClusterResourceSet Template Management Testing

---

## Critical Issues

### 1. ❌ Kyverno Policy Syntax Errors
**Status**: Blocking  
**Impact**: Certificate transformation not working  
**Description**: Kyverno policies in `catalog/security/argocd-agent-cert.yaml` have JMESPath syntax issues preventing automatic transformation of cert-manager secrets to ClusterResourceSet format.

**Current State**:
- Kyverno v3.2.6 installed in hub cluster
- Policies created but not applying correctly
- Manual workaround secrets created to unblock spoke cluster provisioning

**Target State**:
- Kyverno policies automatically transform cert-manager secrets
- No manual intervention required
- Works per-environment, fully GitOps-compliant

**Related Files**:
- `zero-ops/catalog/security/argocd-agent-cert.yaml`
- `zero-ops/manifests/argocd/apps/platform-security-certificates.yaml`

---

### 2. ❌ Hetzner CCM Not Applied by ClusterResourceSet
**Status**: Blocking  
**Impact**: Spoke cluster nodes have taints, ArgoCD Agent pod cannot schedule  
**Description**: ClusterResourceSet successfully applied Cilium CNI and ArgoCD Agent certificates, but Hetzner CCM was not applied to spoke cluster.

**Current State**:
- Spoke cluster `spoke-pool-eu-prod-01` has 3 Ready nodes
- Cilium CNI working (pods running in kube-system)
- ArgoCD Agent certificates exist in spoke cluster
- ArgoCD Agent pod Pending (0/1 Ready)
- Worker nodes have taints:
  - `node.cluster.x-k8s.io/uninitialized`
  - `node.cloudprovider.kubernetes.io/uninitialized`

**Target State**:
- Hetzner CCM applied by ClusterResourceSet
- CCM removes node taints
- ArgoCD Agent pod starts successfully

**Investigation Needed**:
- Compare `internal/assets/manifests/addons/ccm-rendered.yaml` with `manifests/platform-ops/cluster-bios/ccm-addon-template.yaml`
- Verify ClusterResourceSet resource references in `xrds/compositions/spokepool-hetzner.yaml`
- Check CAPI ClusterResourceSet controller logs for errors

**Related Files**:
- `zero-ops/manifests/platform-ops/cluster-bios/ccm-addon-template.yaml`
- `zero-ops/internal/assets/manifests/addons/ccm-rendered.yaml`
- `zero-ops/xrds/compositions/spokepool-hetzner.yaml`

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

- [ ] Kyverno policies apply correctly (no syntax errors)
- [ ] cert-manager secrets automatically transformed to ClusterResourceSet format
- [ ] Manual workaround secrets deleted
- [ ] Hetzner CCM applied by ClusterResourceSet
- [ ] Node taints removed by CCM
- [ ] ArgoCD Agent pod Running (1/1 Ready)
- [ ] ArgoCD Agent connected to hub (visible in ArgoCD UI)
- [ ] All ClusterResourceSet resources applied to spoke cluster
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

**Next Steps**: Focus on resolving Issue #1 (Kyverno policies) and Issue #2 (CCM application) to unblock Task 1.7.11 completion.
