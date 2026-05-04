# ADR 016: Account-Level User Isolation Pattern

**Date:** 2026-05-04  
**Status:** Accepted  
**Authors:** Platform Engineering Team  
**Related ADRs:** 
- [ADR 006: Multi-Tenant Database Pattern](./006-multi-tenant-database-pattern.md)
- [CNPG Database Migration Pattern](./cnpg-database-migration-pattern.md)
**Related Article:** 
https://planetscale.com/blog/approaches-to-tenancy-in-postgres

## Terminology Clarification

The platform has two distinct isolation levels:

**Level 1: Platform Tenant (SaaS Product) - Covered by ADR 006**
- A SaaS product provisioned on the Zero-Ops platform (e.g., "app-builder", "crm", "inventory")
- Each platform tenant gets its own logical database (e.g., `tenant_app-builder_db`)
- Isolation: Database-per-tenant within shared CNPG cluster
- Credentials managed via Infisical → ESO → provider-sql

**Level 2: Account (Customer Organization) - Covered by THIS ADR**
- A customer organization using a SaaS product (e.g., "Acme Corp", "TechStart Inc")
- Multiple accounts share the same platform tenant database
- Isolation: Shared-schema with `account_id` column filtering
- This ADR uses PlanetScale's terminology: `account_id` replaces their `tenant_id` to avoid confusion with platform tenants

**Example Hierarchy:**
```
Zero-Ops Platform (PaaS)
└── Platform Tenant: "app-builder" (SaaS product)
    └── Database: tenant_app-builder_db
        ├── Account: "Acme Corp" (account_id=1)
        │   ├── User: alice@acme.com
        │   └── User: bob@acme.com
        ├── Account: "TechStart Inc" (account_id=2)
        │   ├── User: john@techstart.com
        │   └── User: jane@techstart.com
        └── Account: "RetailCo" (account_id=3)
            └── Users...
```

## Context

Each platform tenant database serves multiple customer accounts. Each account has a super admin who creates users with access to forms, applications, and reports within that account. Users within an account must be isolated from users in other accounts sharing the same database. The baseline migrations provide core tables (users, sessions, identities, buckets, objects), and accounts create dynamic tables for their application logic.

## Decision

Use **shared-schema with `account_id` column** enforced at the application layer, following PlanetScale's recommended multi-tenancy pattern.

**Pattern:**
- All tables within a platform tenant database include an `account_id` column
- JWT claims contain `account_id` extracted during authentication
- Application layer (PostgREST, API Gateway, middleware) automatically injects `WHERE account_id = ?` in all queries
- No Row-Level Security (RLS) policies for account isolation
- Security logic enforced in application code, not database policies

**Schema Structure:**
All tables include `account_id` as a BIGINT foreign key referencing the accounts table. The `account_id` column leads composite indexes for query performance. User-created dynamic tables follow the same pattern with `account_id` as a required column.

**Authentication Flow:**
1. User authenticates with email/password or OAuth provider
2. Authentication service validates credentials and looks up user's `account_id`
3. JWT issued with claims: `user_id`, `account_id`, `email`, `role`
4. Every API request extracts `account_id` from JWT
5. Application layer injects `WHERE account_id = <jwt_account_id>` in all queries
6. Database returns only data belonging to the authenticated user's account

**Enforcement Layers:**
- **API Gateway/Middleware**: Extracts `account_id` from JWT and validates token
- **ORM/Query Builder**: Global scope automatically adds `WHERE account_id = ?` to all queries
- **PostgREST**: Custom request handler injects account filter based on JWT claims
- **Application Code**: Explicit filtering in business logic as defense-in-depth

**Dynamic Table Creation:**
When accounts create forms or applications requiring new tables, the schema generation enforces `account_id` column inclusion. Table templates include `account_id BIGINT NOT NULL` with appropriate indexes and foreign key constraints.

**Partitioning for Scale:**
For large tables with millions of rows across thousands of accounts, partition by `account_id` using Postgres LIST partitioning. Partitioning provides performance benefits of database-per-account with lower operational overhead. New partitions created automatically during account onboarding.

## Consequences

**Positive:**
- Aligns with PlanetScale's recommended shared-schema pattern for multi-tenancy
- Scales to thousands of accounts within a single database
- Simple schema migrations apply to all accounts simultaneously
- Explicit filtering in application code is debuggable and testable
- Cross-account analytics queries possible for platform insights
- No RLS policy misconfiguration risk or silent failures
- Connection pooling works efficiently (single database, no per-account pools)
- Partitioning by `account_id` provides isolation benefits without separate databases

**Negative:**
- Application code must consistently enforce `account_id` filtering (no database-level enforcement)
- Risk of developer error omitting `WHERE account_id = ?` in queries
- Requires ORM global scopes or middleware to inject filters automatically
- Shared tables mean one account's query load can impact others (noisy neighbor)
- Requires `statement_timeout` and application-level rate limiting for protection

**Mitigation:**
- Use ORM global scopes to automatically inject `account_id` filters
- Code review checklist requires `account_id` filtering verification
- Integration tests validate account isolation for all endpoints
- Monitoring alerts on queries missing `account_id` filters
- Set `statement_timeout` and `idle_in_transaction_session_timeout` at database level
- Application-level rate limiting per account prevents resource monopolization
