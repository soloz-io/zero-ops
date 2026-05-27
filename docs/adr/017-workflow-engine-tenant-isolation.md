# ADR 017: Workflow Engine Tenant Isolation Pattern

**Date:** 2026-05-04  
**Status:** Superseded by [ADR-019: Waypoint Shared SaaS Platform Service](./019-waypoint-shared-saas-platform-service.md)  
**Authors:** Platform Engineering Team  
**Related ADRs:** 
- [ADR 006: Multi-Tenant Database Pattern](./006-multi-tenant-database-pattern.md)
- [ADR 016: Account-Level User Isolation Pattern](./016-account-level-users-isolation-pattern.md)
- [ADR-019: Waypoint Shared SaaS Platform Service](./019-waypoint-shared-saas-platform-service.md)

## Context

The platform integrates Vercel Workflows SDK (world-postgres) as a durable workflow execution engine for SaaS applications. The world-postgres package hardcodes its tables to a specific schema (pgSchema('workflow')) and does not include tenant_id or account_id columns in its base engine tables (workflow_runs, workflow_events, workflow_steps, workflow_hooks). The package is an OSS dependency that cannot be forked without breaking upgrade compatibility. Each platform tenant (SaaS product) requires isolated workflow execution state while sharing the workflow engine infrastructure.

## Original Decision (Superseded)

Use **database-per-tenant isolation** for workflow engine state, where each platform tenant's logical database contains a dedicated workflow schema alongside their application data schema.

**Pattern:**
Each platform tenant database contains two schemas: the public schema for application data (with account_id-based isolation per ADR 016) and the workflow schema for engine state (isolated by database boundary). Worker pods receive tenant-specific database connection strings via environment variables, connecting to the appropriate tenant database. The workflow engine remains unmodified from OSS, with no tenant_id or account_id columns added to engine tables.

**Database Structure:**
Platform tenant databases contain the workflow schema with tables (workflow_runs, workflow_events, workflow_steps, workflow_hooks, workflow_waits, workflow_stream_chunks) and the public schema with application tables (accounts, users, form_data with account_id columns). Database boundary enforces platform tenant isolation. Account_id columns in public schema enforce user isolation within the tenant.

**Connection Management:**
Worker pods receive WORKFLOW_POSTGRES_URL environment variable pointing to tenant-specific database. Graphile Worker job names prefixed with tenant identifier for routing. Connection pooling configured per tenant database. No cross-tenant database queries possible.

**Isolation Levels:**
Level 1 (Platform Tenant): Database boundary isolates SaaS products (tenant_acme_db vs tenant_techstart_db). Level 2 (Account): account_id column isolates user organizations within a SaaS product (Acme Corp vs TechStart Inc within same database). Workflow engine operates at Level 1 only, account-agnostic by design.

## Why This Decision Was Superseded

The per-tenant pod model created operational overhead that did not scale: one Waypoint SDK pod per tenant per cell, one runtime database per tenant, duplicating the `workflow.*` schema for every onboarded customer. This was a consequence of treating Waypoint as a tenant-deployed runtime rather than a shared platform service.

The revised stance in ADR-019 positions Waypoint as a shared SaaS platform service — one BFF, one SDK, one runtime database per cell — serving all platform tenants. Tenant isolation is enforced at the **BFF trust boundary** (tenant_id in control-plane DB) and at **Infisical credential path scope** (per-tenant secret paths resolved at runtime), not at the database boundary.

The `world-postgres` OSS package remains unmodified. The single shared runtime database is exactly what it was designed for.

## Consequences (Historical)

**Positive:**
- No OSS fork required, maintains upgrade compatibility with world-postgres
- Database-level isolation provides strong security boundary between platform tenants
- Aligns with ADR 006 database-per-tenant pattern for consistency
- Workflow engine remains infrastructure-agnostic, no business logic coupling
- Simple connection string configuration per tenant
- No code changes to world-postgres schema or queries

**Negative (that drove supersession):**
- Pod sprawl: one SDK pod per tenant × N cells = unsustainable at scale
- Storage overhead of ~8MB per tenant database for workflow schema, duplicated per tenant
- Won't scale beyond few hundred platform tenants per CNPG cluster
- Each tenant database duplicates workflow schema structure
- Secret injection (ADR-018 init mode) required pod-per-tenant — coupling deployment topology to secret delivery model
