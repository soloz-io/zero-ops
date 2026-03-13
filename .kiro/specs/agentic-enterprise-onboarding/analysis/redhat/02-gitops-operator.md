# OpenShift GitOps Operator - Applicability Analysis

## Project Overview
**Source:** `.kiro/specs/agentic-enterprise-onboarding/references/redhat/gitops-operator/`

OpenShift-native ArgoCD operator with cluster-scoped GitOps, console integration, and RBAC patterns.

## Applicability to Zero-Ops Platform

### ⚠️ PARTIALLY APPLICABLE (Patterns Only)

**Use Case:** ArgoCD Deployment Patterns and Multi-Tenant RBAC

**Justification:**
1. **OpenShift-Specific**: Operator tightly coupled to OpenShift APIs (Routes, ConsoleLinks, OAuth integration).
2. **Zero-Ops Uses Vanilla Kubernetes**: PRD specifies Ubuntu/kubeadm clusters on Hetzner, not OpenShift.
3. **Valuable Patterns**: Multi-tenant ArgoCD RBAC, cluster-scoped vs namespace-scoped instances, GitOps security models.

## Recommended Adoption

### ✅ Adopt: RBAC Patterns
**Apply to:**
- Management cluster ArgoCD (watches tenant control plane repos)
- Tenant cluster ArgoCD (edge GitOps, OCI catalog pull)

**Patterns to Extract:**
```go
// Source: .kiro/specs/agentic-enterprise-onboarding/references/redhat/gitops-operator/controllers/argocd/argocd.go

// Cluster-scoped ArgoCD with restricted RBAC
func getClusterScopedRBAC() []rbacv1.PolicyRule {
    return []rbacv1.PolicyRule{
        {
            APIGroups: []string{"*"},
            Resources: []string{"*"},
            Verbs:     []string{"get", "list", "watch"}, // Read-only cluster config
        },
        {
            APIGroups: []string{"argoproj.io"},
            Resources: []string{"applications", "appprojects"},
            Verbs:     []string{"*"}, // Full control over ArgoCD resources
        },
    }
}
```

**Zero-Ops Adaptation:**
- Management cluster ArgoCD: ClusterRole with read-only cluster access + full control over tenant namespaces
- Tenant cluster ArgoCD: Namespace-scoped Role (no cluster-level access)

**Source Reference:**
```
.kiro/specs/agentic-enterprise-onboarding/references/redhat/gitops-operator/controllers/argocd/argocd.go (lines 186-190)
```

### ✅ Adopt: Multi-Instance ArgoCD Pattern
**Pattern:**
- One "cluster" ArgoCD instance in `openshift-gitops` namespace (management)
- Additional ArgoCD instances in tenant namespaces (isolated)

**Zero-Ops Mapping:**
```
Management Cluster:
  - argocd (namespace: argocd-system) → watches all tenant control plane repos
  
Tenant Cluster (Enterprise):
  - argocd (namespace: argocd) → pulls OCI catalog, tenant app manifests
```

**Source Reference:**
```
.kiro/specs/agentic-enterprise-onboarding/references/redhat/gitops-operator/controllers/gitopsservice_controller.go (lines 113-120)
```

### ❌ Do NOT Adopt: OpenShift-Specific Features
**Exclude:**
- Route CRs (use Ingress instead)
- ConsoleLink integration (Zero-Ops has custom Platform Console)
- OpenShift OAuth (Zero-Ops uses Ory Hydra)
- Dex with OpenShiftOAuth (Zero-Ops uses Ory Kratos)

**Rationale:** These features require OpenShift APIs unavailable in vanilla Kubernetes.

## Alignment with Zero-Ops Architecture

| Zero-Ops Requirement | GitOps Operator Pattern | Adoption Decision |
|---|---|---|
| Management cluster ArgoCD | Cluster-scoped instance | ✅ Adopt RBAC pattern |
| Tenant cluster ArgoCD | Namespace-scoped instance | ✅ Adopt isolation pattern |
| ArgoCD ApplicationSet | Watches fleet-registry | ✅ Already in PRD |
| Multi-tenant RBAC | Per-tenant AppProject | ✅ Adopt AppProject scoping |
| Console integration | OpenShift ConsoleLink | ❌ Use Platform Console instead |
| SSO | Dex + OpenShift OAuth | ❌ Use Ory Hydra instead |

## Implementation Priority
**Priority:** MEDIUM (Sprint 1 - Day 6)

**Rationale:** ArgoCD deployment is Day 1 infrastructure, but RBAC refinement can follow initial bootstrap.

## Risks if NOT Adopted
- Overly permissive ArgoCD RBAC (cluster-admin by default)
- Tenant ArgoCD instances with cluster-level access (security risk)
- No AppProject isolation between tenants

## Specific Code References

### 1. Cluster-Scoped RBAC Pattern
**File:** `controllers/argocd/argocd.go`
**Lines:** 186-190
**Pattern:** Separate ClusterRole for cluster config vs namespace resources

### 2. Multi-Instance Detection
**File:** `controllers/gitopsservice_controller.go`
**Lines:** 113-120
**Pattern:** Predicate filters for `openshift-gitops` namespace vs others

### 3. AppProject Scoping
**File:** `controllers/argocd_controller.go`
**Lines:** 235-240
**Pattern:** Per-tenant AppProject with sourceRepos and destinations restrictions

## Next Steps
1. Extract RBAC PolicyRules from gitops-operator
2. Define Zero-Ops ArgoCD ClusterRole (management cluster)
3. Define Zero-Ops ArgoCD Role (tenant cluster)
4. Create AppProject template for tenant isolation
5. Document RBAC model in `.kiro/specs/agentic-enterprise-onboarding/demos/demo1-spec/design.md`
