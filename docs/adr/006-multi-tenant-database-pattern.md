# ADR 006: Multi-Tenant Database Pattern

**Date:** 2026-04-27  
**Status:** Accepted  
**Authors:** Platform Engineering Team  
**Related ADRs:** 
- [CNPG Database Migration Pattern](./cnpg-database-migration-pattern.md)
- [ESO-Infisical Pattern](./eso-infisical-pattern.md)
- [Declarative Operator State](./declarative-operator-state-over-imperative-jobs.md)
- [ADR 005: Hub-Spoke Crossplane Composition](./0005-unified-abstraction-layers-crossplane.md)

## Context

The platform provisions multi-tenant SaaS environments where each tenant requires isolated database credentials with dynamic lifecycles. Tenants are created continuously via API/MCP requests, each needing unique credentials that must be externalized to Infisical and self-healing if deleted. The existing CNPG postInitSQL pattern is suitable for static platform users but cannot handle dynamic tenant user creation.

## Decision

For multi-tenant databases, use the **Externalized Identity Pattern**: Infisical stores credentials as source of truth → ESO creates Kubernetes secrets → Crossplane provider-sql creates PostgreSQL users declaratively → Applications consume credentials.

**Flow:**
1. Kube-SBT/open-sbt Application Plane generates tenant password during onboarding and uploads to Infisical at path `/spoke-pool/<cell-id>/tenants/<tenant-id>/db-credentials`
2. Crossplane TenantDatabase XR composition creates ExternalSecret (via Object MR) that pulls credentials from Infisical
3. ESO syncs credentials and creates Kubernetes secret with username/password from Infisical plus computed fields (database, host, port)
4. Crossplane creates provider-sql Role referencing the ESO-created secret
5. provider-sql creates PostgreSQL user with credentials from secret
6. Crossplane creates Grant and DefaultPrivileges for tenant user
7. ESO creates pooler-app secret with connection URL constructed from Infisical credentials
8. AtlasMigration runs schema migrations as crossplane_admin (database owner)
9. Application connects using tenant credentials from ESO-created secret

**Key Design Points:**
- Database owner is `crossplane_admin`, not tenant user (enables migrations while tenant gets permissions via DefaultPrivileges)
- Only secrets (username, password) pulled from Infisical; configuration fields (host, port, database) computed dynamically in ESO templates
- provider-sql resources (Role, Grant, DefaultPrivileges) are native Crossplane MRs, not wrapped in Object MRs (Hub-local resources per ADR 005)
- No Crossplane-generated secrets; ESO creates all secrets from Infisical as source of truth

**Pattern Comparison:**
- **Static Platform Users**: Use CNPG postInitSQL for fixed user sets (agentregistry, mcp_server) in Hub databases (control_plane, hub) - simple, one-time bootstrap
- **Dynamic Tenant Users**: Use Infisical → ESO → provider-sql for multi-tenant SaaS with continuous tenant creation - externalized, self-healing, declarative

## Consequences

**Positive:**
- Infisical is single source of truth for all tenant credentials (ESO-Infisical ADR compliant)
- Self-healing via Crossplane reconciliation (user recreated if manually deleted)
- Declarative and GitOps-friendly (follows ADR 004)
- Scalable to 1000+ tenants without cluster recreation
- Supports password rotation via Infisical → ESO sync → provider-sql update
- Audit trail for all credential changes in Infisical
- Disaster recovery via credential restoration from Infisical

**Negative:**
- Increased complexity with multiple components (Infisical + ESO + provider-sql) vs simple postInitSQL
- Dependency chain requires ESO sync before provider-sql can create user
- Debugging failures across multiple layers (Infisical API, ESO sync, provider-sql reconcile)
- Not suitable for bootstrap users needed during cluster initialization (use postInitSQL for those)
- Requires Kube-SBT to generate and upload credentials before Crossplane reconciles
