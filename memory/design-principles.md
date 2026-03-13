# Zero-Ops Platform Design Principles

## GitOps & Deployment Patterns

### ArgoCD Application Structure
- ❌ Don't bundle everything into a single Helm chart
- ✅ Deploy each component as separate ArgoCD Application
- ✅ Use app-of-apps pattern for grouping
- Example: `platform-identity` parent app with child apps: `kratos`, `hydra`, `keto`, `agentgateway`

### Sync Wave Usage
- ❌ Avoid sync-waves for service-to-service dependencies
- ✅ Only use sync-waves for: CRDs, Operators
- ✅ Services should retry until dependencies exist
- Reason: Better failure isolation, independent upgrades

### Service Dependencies
- ❌ Don't use sync-wave orchestration for architecture dependencies
- ✅ Each service retries until dependencies ready
- ✅ Use health checks and readiness probes

## Database Patterns

### CNPG Usage
- ✅ Use CNPG (CloudNativePG), not standalone PostgreSQL
- ✅ Single CNPG cluster with multiple databases via Database CRD
- ✅ Separate databases per component
- Benefits: Cost-efficient, simpler operations, enterprise-grade

## Namespace Strategy (Enterprise Pattern)

### Multi-Namespace Isolation
- ❌ Single namespace for all components (demo/startup pattern)
- ✅ Separate namespaces per subsystem for better RBAC, blast radius isolation, independent upgrades
- ✅ Cross-namespace service discovery: `<service>.<namespace>.svc.cluster.local`
