# ADR: Dual-Repository GitOps Pattern for Hub-Spoke Architecture

**Status**: Approved  
**Date**: 2026-04-09  
**Context**: Spoke Pool Provisioner (Phase 1)  
**Deciders**: Platform Team

---

## Context and Problem Statement

The Zero-Ops platform uses GitOps for all infrastructure and tenant provisioning. We need to decide how to organize Git repositories to support:

1. Platform code (ArgoCD ApplicationSets, platform components, Helm charts)
2. Runtime state (tenant configurations, SpokePool specs, dynamic fleet state)
3. Safe automation (MCP API writes tenant state without touching platform code)
4. Access control (Platform team vs Operations team vs MCP API)
5. Independent lifecycle management (platform updates vs tenant onboarding)

**Key Question**: Should we use a single monorepo or separate repositories for platform code and runtime state?

---

## Decision Drivers

- **Separation of Concerns**: Platform code changes infrequently; tenant state changes continuously
- **Access Control**: MCP API should write tenant state but NOT modify platform code
- **Blast Radius**: Tenant onboarding errors should not break platform deployments
- **GitOps Clarity**: ArgoCD should clearly distinguish between static platform and dynamic state
- **Audit Trail**: Separate commit history for platform changes vs tenant operations
- **Scalability**: Support 10,000+ tenants without polluting platform repo

---

## Considered Options

### Option 1: Single Monorepo
```
zero-ops/
├── argocd/
│   ├── applicationsets/
│   └── platform-apps/
├── spokepools/
│   ├── spokepool-01.yaml
│   └── spokepool-02.yaml
└── tenants/
    ├── tenant-acme/
    └── tenant-xyz/
```

### Option 2: Dual Repository (SELECTED)
```
Repo 1: hub-infra (platform code)
├── argocd/
│   ├── applicationsets/
│   └── platform-apps/
└── charts/
    └── universal-tenant/

Repo 2: fleet-registry (runtime state)
├── spokepools/
│   ├── spokepool-01.yaml
│   └── spokepool-02.yaml
└── tenants/
    ├── tenant-acme/
    └── tenant-xyz/
```

### Option 3: Multi-Repository (per component)
```
Repo 1: hub-infra
Repo 2: spoke-pools
Repo 3: tenants
Repo 4: platform-charts
```

---

## Decision Outcome

**Chosen Option**: Option 2 - Dual Repository Pattern

We will maintain two separate Git repositories:

1. **hub-infra**: Platform code (static, infrequent changes)
2. **fleet-registry**: Runtime state (dynamic, continuous changes)

---

## Rationale

### 1. Clear Separation of Concerns

**hub-infra = Platform Code**
- ArgoCD ApplicationSets
- Platform component definitions (CNPG, NATS, Crossplane)
- Helm charts (universal-tenant, spoke-pool)
- Kyverno policies
- Platform configuration

**fleet-registry = Platform State**
- SpokePool XR instances
- Tenant configurations (values.yaml)
- Runtime fleet topology
- Tenant-specific overrides

### 2. Safer Access Control

| Actor | hub-infra Access | fleet-registry Access |
|-------|------------------|----------------------|
| Platform Team | Read + Write | Read + Write |
| Operations Team | Read only | Read + Write |
| MCP API | No access | Write only (tenants/) |
| CI/CD | Read only | No access |

**Security Benefit**: MCP API cannot accidentally modify platform code during tenant provisioning.

### 3. Cleaner GitOps Model

**ArgoCD watches both repositories with different purposes**:

```yaml
# App-of-Apps (from hub-infra)
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: platform-bootstrap
spec:
  source:
    repoURL: https://github.com/soloz-io/hub-infra
    path: argocd/platform-apps

---
# ApplicationSet (watches fleet-registry)
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: tenant-fleet
spec:
  generators:
  - git:
      repoURL: https://github.com/soloz-io/fleet-registry
      directories:
      - path: tenants/*
  template:
    spec:
      source:
        repoURL: https://github.com/soloz-io/hub-infra  # Pulls charts from platform repo
        path: charts/universal-tenant
```

**Key Pattern**: ApplicationSet watches `fleet-registry` for tenant directories, but pulls Helm charts from `hub-infra`.

### 4. Independent Lifecycle

**Platform Updates** (hub-infra):
- Upgrade ArgoCD version
- Update Crossplane Compositions
- Modify platform Helm charts
- Change sync wave ordering

**Tenant Operations** (fleet-registry):
- Onboard new tenant
- Provision new SpokePool
- Update tenant resource quotas
- Scale tenant workloads

**Benefit**: Platform team can update charts without triggering tenant re-deployments.

### 5. Reduced Blast Radius

**Scenario**: MCP API writes malformed tenant YAML

**With Monorepo**:
- Breaks ArgoCD sync for entire repository
- Platform components may fail to deploy
- Requires platform team intervention

**With Dual Repo**:
- Only affects fleet-registry sync
- Platform components unaffected
- Tenant-specific failure, isolated impact

### 6. Audit Trail Clarity

**hub-infra commits**:
```
feat: upgrade CNPG operator to v1.23.0
fix: correct sync wave for Atlas operator
docs: update Crossplane Composition schema
```

**fleet-registry commits**:
```
tenant: onboard acme (tier: starter, region: fsn1)
spokepool: provision spokepool-03 (region: hel1)
tenant: scale acme resources (cpu: 2 -> 4)
```

**Benefit**: Clear separation in Git history for compliance and debugging.

---

## Implementation Details

### Repository Structure

**hub-infra** (https://github.com/soloz-io/hub-infra):
```
hub-infra/
├── argocd/
│   ├── applicationsets/
│   │   ├── spoke-pool-fleet.yaml
│   │   └── tenant-fleet.yaml
│   └── platform-apps/
│       ├── cnpg-operator.yaml
│       ├── crossplane.yaml
│       └── atlas-operator.yaml
├── charts/
│   ├── universal-tenant/
│   │   ├── Chart.yaml
│   │   ├── values.yaml
│   │   └── templates/
│   │       ├── atlasmigration.yaml
│   │       └── namespace.yaml
│   └── spoke-pool/
│       ├── Chart.yaml
│       └── templates/
├── kyverno-policies/
│   └── spoke-pool-cluster-discovery.yaml
└── crossplane/
    ├── xrds/
    │   └── spokepool-v1.yaml
    └── compositions/
        └── spokepool-hetzner-v1.yaml
```

**fleet-registry** (https://github.com/soloz-io/fleet-registry):
```
fleet-registry/
├── spokepools/
│   ├── spokepool-01.yaml
│   ├── spokepool-02.yaml
│   └── spokepool-03.yaml
└── tenants/
    ├── tenant-acme/
    │   └── values.yaml
    ├── tenant-xyz/
    │   └── values.yaml
    └── tenant-foo/
        └── values.yaml
```

### ArgoCD Configuration

**Bootstrap Application** (deployed manually once):
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: platform-bootstrap
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/soloz-io/hub-infra
    path: argocd/platform-apps
    targetRevision: main
  destination:
    server: https://kubernetes.default.svc
    namespace: argocd
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
```

**ApplicationSet for Tenants** (in hub-infra):
```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: tenant-fleet
  namespace: argocd
spec:
  generators:
  - git:
      repoURL: https://github.com/soloz-io/fleet-registry
      revision: main
      directories:
      - path: tenants/*
  template:
    metadata:
      name: 'tenant-{{path.basename}}'
    spec:
      project: tenants
      source:
        repoURL: https://github.com/soloz-io/hub-infra
        path: charts/universal-tenant
        targetRevision: main
        helm:
          valueFiles:
          - https://raw.githubusercontent.com/soloz-io/fleet-registry/main/{{path}}/values.yaml
      destination:
        server: https://kubernetes.default.svc
        namespace: '{{path.basename}}'
      syncPolicy:
        automated:
          prune: true
          selfHeal: true
```

### MCP API Integration

**Tenant Creation Flow**:
```
1. MCP API receives: tenant_create(tenant_id="acme", tier="starter")
2. MCP API generates: values.yaml from template
3. MCP API commits to fleet-registry:
   - Path: tenants/tenant-acme/values.yaml
   - Commit message: "tenant: onboard acme (tier: starter)"
4. ArgoCD ApplicationSet detects new directory
5. ArgoCD creates Application: tenant-acme
6. Application pulls Helm chart from hub-infra
7. Application uses values.yaml from fleet-registry
8. Helm renders templates → AtlasMigration CR, namespace, RBAC
9. ArgoCD deploys to Spoke Pool cluster
```

**Key Point**: MCP API only writes to `fleet-registry/tenants/`, never touches `hub-infra`.

---

## Consequences

### Positive

✅ **Clear Separation**: Platform code vs runtime state clearly distinguished  
✅ **Safe Automation**: MCP API cannot break platform by writing tenant state  
✅ **Access Control**: Fine-grained permissions per repository  
✅ **Independent Lifecycle**: Platform updates don't trigger tenant re-deployments  
✅ **Reduced Blast Radius**: Tenant errors isolated from platform  
✅ **Audit Trail**: Clear Git history per concern  
✅ **Scalability**: 10,000+ tenants without polluting platform repo

### Negative

⚠️ **Complexity**: Two repositories to manage instead of one  
⚠️ **Cross-Repo References**: ApplicationSets must reference both repos  
⚠️ **Sync Coordination**: Changes spanning both repos require careful ordering  
⚠️ **Tooling**: CI/CD must handle two repositories

### Neutral

🔄 **Migration Path**: Existing monorepo can be split using Git filter-branch  
🔄 **Backup Strategy**: Both repos must be backed up independently  
🔄 **Disaster Recovery**: Restore process involves two repositories

---

## Alternatives Considered

### Why Not Single Monorepo?

**Rejected Reasons**:
- MCP API would have write access to platform code (security risk)
- Tenant onboarding errors could break platform deployments
- Git history polluted with thousands of tenant commits
- No clear separation between platform and state

### Why Not Multi-Repository (per component)?

**Rejected Reasons**:
- Over-engineering for current scale (< 1000 tenants in Phase 1)
- Increased complexity in ArgoCD ApplicationSet configuration
- More repositories to manage, backup, and secure
- Harder to reason about system state

**Future Consideration**: If fleet grows beyond 10,000 tenants, consider splitting `fleet-registry` into:
- `fleet-registry-spokepools`
- `fleet-registry-tenants-a-m`
- `fleet-registry-tenants-n-z`

---

## Related Decisions

- **ADR: Namespace Alignment** - Defines Hub platform namespace structure
- **ADR: GitOps-First Approach** - Mandates all changes via Git commits
- **Spec: Spoke Pool Provisioner** - Implements dual-repo pattern for tenant provisioning

---

## References

- ArgoCD ApplicationSet Documentation: https://argo-cd.readthedocs.io/en/stable/user-guide/application-set/
- GitOps Principles: https://opengitops.dev/
- Fleet Management Best Practices: https://www.weave.works/blog/managing-thousands-of-clusters-and-their-workloads

---

**Document Version**: 1.0  
**Last Updated**: 2026-04-09  
**Next Review**: After Phase 1 completion (1000 tenants milestone)
