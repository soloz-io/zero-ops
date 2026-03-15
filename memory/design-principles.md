---
purpose: Platform-wide design principles and architectural patterns
scope: GitOps, deployment patterns, database patterns, namespace strategies
topics: [gitops-patterns, argocd-structure, cnpg-usage, namespace-isolation]
update_criteria: Architectural pattern decisions, deployment strategy changes, infrastructure patterns
---

# Zero-Ops Platform Design Principles

## GitOps & Deployment Patterns

### Declarative-Only Operations
- ❌ Imperative kubectl commands or direct API calls in production
- ✅ All changes via declarative manifests in Git
- ✅ GitOps reconciliation handles all cluster state
- ✅ Exception: Bootstrap operations only (initial cluster setup)
- ✅ Upgrades via declarative version changes in Git (image tags, Helm chart versions, operator versions)
- ✅ Rollbacks via Git revert operations, not imperative commands
- Benefits: Audit trail, reproducibility, drift detection, rollback capability, upgrade consistency

### ArgoCD Application Structure
- ❌ Monolithic charts for independent services
- ✅ Deploy each component as separate ArgoCD Application
- ✅ Use app-of-apps pattern for grouping
- ✅ Component bundles acceptable for tightly coupled services (e.g., platform-identity chart with kratos/hydra/keto)
- Example: `platform-identity` parent app with child apps: `kratos`, `hydra`, `keto`, `agentgateway`

### Sync Wave Usage
- ❌ Service orchestration via sync-waves
- ✅ Use sync-waves for: CRDs, Operators, database migrations, bootstrap jobs, cluster bootstrapping
- ✅ Services use retry logic, readiness probes, health checks for dependencies
- Reason: Better failure isolation, independent upgrades

## Platform Architecture Principles

### Control Plane Separation
- ✅ Management cluster vs tenant clusters (SaaS factory pattern)
- ✅ Control plane handles: tenant provisioning, policy distribution, fleet observability
- ✅ Data plane handles: workload execution, tenant isolation
- Benefits: Scalability, blast radius isolation, multi-tenancy

### Immutable Infrastructure
- ✅ All deployments versioned and immutable
- ✅ Use content-addressed artifacts: OCI digests, Helm versions, Git commit SHAs
- ✅ Rollback via artifact version changes, not configuration drift
- Benefits: Audit trail, reproducible deployments, drift detection

### Identity as Platform Service
- ✅ Authentication and authorization centralized across all tenants
- ✅ Single identity stack serves multiple tenant environments
- ✅ Tenant-specific customization via claims and policies
- Benefits: Consistent security, reduced operational overhead

### Platform API Boundaries
- ✅ Services interact through well-defined APIs, not direct database access
- ✅ Database schemas owned by single service
- ✅ Cross-service communication via REST/gRPC/events
- Benefits: Service independence, schema evolution, testing isolation

## Database Patterns

### CNPG Usage
- ✅ Use CNPG (CloudNativePG), not standalone PostgreSQL
- ✅ Single CNPG cluster for platform services (Tenant onboarding metadata, Ory stack: hydra-db, kratos-db, keto-db)
- ✅ Separate CNPG cluster for tenant workloads (different ownership model, lifecycle, backup SLA)
- ✅ Multiple databases per cluster via Database CRD
- ✅ Enterprise naming: `<platform>-<domain>-<resource>` (e.g., `zero-ops-platform-postgres`, `zero-ops-tenant-postgres`)
- Boundary principle: Platform services vs tenant workloads, not identity vs everything else
- Benefits: Correct operational boundaries, shared platform service management, cost-efficient, clear ownership

## Namespace Strategy (Enterprise Pattern)

### Multi-Namespace Isolation (Enterprise Pattern)
- ❌ Single namespace for all components (demo/startup pattern)
- ✅ Separate namespaces per subsystem: `ory-system`, `identity-services`, `api-gateway`, `observability`
- ✅ Cross-namespace service discovery: `<service>.<namespace>.svc.cluster.local`
- Benefits: RBAC boundaries, blast radius isolation, independent upgrades
