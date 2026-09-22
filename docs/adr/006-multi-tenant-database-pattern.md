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
1. Tenant passwords are generated during onboarding and stored in Infisical (System of Record) at `/spoke-pool/<cell-id>/tenants/<tenant-id>/db-credentials`. Lifecycle owned by Tenant Identity Service per ADR-039.
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

**Critical Security Boundary:**
- **BANNED**: Applications must NEVER use postInitSQL for tenant database creation
- **BANNED**: Applications must NEVER have `CREATEDB` or `CREATEROLE` permissions
- **REQUIRED**: All tenant databases created declaratively via Crossplane provider-sql
- **REQUIRED**: Database ownership remains with `crossplane_admin` for lifecycle management

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Database Roles / Grants | PostgreSQL | Crossplane | Crossplane provider-sql | Tenant Apps | Day-1+ |
| Tenant Passwords | Infisical | Tenant Identity Service | ESO | Tenant Apps, provider-sql | Day-1+ |

See ADR-039 for the complete ownership matrix.

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

## Addendum (2026-09-23) — the Pooler's authentication is CNPG's, not this platform's

**Decision.** A tenant's Pooler declares `poolMode` and `parameters` and nothing
else. `authQuery`, `authQuerySecret` and any variation on them are left unset, so
CNPG performs its built-in Pooler integration: it creates the
`cnpg_pooler_pgbouncer` role and its lookup function in the Cluster, issues the
client certificate, and writes PgBouncer's authentication configuration.
Supplying an auth-query secret disables that integration and makes this platform
responsible for all of it, including the authentication SQL. Nothing here needs
that ownership.

This is step 7 of the flow above, and the addendum exists because that step
described a connection URL whose Pooler did not exist.

**What was wrong.** The tenant composition set
`spec.pgbouncer.authQueryUser.secretRef.name`. `authQueryUser` is not a field in
CNPG's Pooler schema in any version — the schema has `authQuerySecret` — so every
Pooler create was rejected by the API server:

```
.spec.pgbouncer.authQueryUser: field not declared in schema
```

No Pooler has ever existed on a spoke built by this composition.

**Why nothing reported it.** Step 7's ExternalSecret templates the connection URL
from `db-credentials` rather than from the Pooler, so it resolves and reports
`SecretSynced` while naming `<tenant>-pooler.platform-data.svc` — a Service with
nothing behind it. Applications read that as `DATABASE_URL`. The secret is
healthy, the ExternalSecret is healthy, and the address does not exist. Only the
Crossplane Object carried the error, and only the composite's readiness reflected
it, which is a surface nobody watches until something else asks why a tenant is
not Ready.

**Why the fix is removal and not a rename.** `authQuerySecret` is not the
corrected spelling of the same intent — it starts a different, manually managed
authentication path and requires `authQuery` alongside it. The comment on the
original field said it was there to reference tenant-specific credentials rather
than a shared app user, and that is precisely what the built-in integration
already provides.

**The precedent was already in the repository.** The hub's own pooler declares
exactly `poolMode` and `parameters` and has run since the hub was built. The
tenant composition was the outlier, not the pattern — so this is a correction
toward something already proven here, not a new position.

**Consequence.** A composite is not Ready until its Pooler is, which is the
behaviour that surfaced this. That readiness is worth keeping: the alternative is
a tenant reporting Ready while the address its applications connect to resolves
to nothing.
