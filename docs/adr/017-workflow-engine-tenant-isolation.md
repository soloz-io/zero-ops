# ADR 017: Workflow Engine Tenant Isolation Pattern

**Date:** 2026-05-04  
**Status:** Accepted  
**Authors:** Platform Engineering Team  
**Related ADRs:** 
- [ADR 006: Multi-Tenant Database Pattern](./006-multi-tenant-database-pattern.md)
- [ADR 016: Account-Level User Isolation Pattern](./016-account-level-users-isolation-pattern.md)

## Context

The platform integrates Vercel Workflows SDK (world-postgres) as a durable workflow execution engine for SaaS applications. The world-postgres package hardcodes its tables to a specific schema (pgSchema('workflow')) and does not include tenant_id or account_id columns in its base engine tables (workflow_runs, workflow_events, workflow_steps, workflow_hooks). The package is an OSS dependency that cannot be forked without breaking upgrade compatibility. Each platform tenant (SaaS product) requires isolated workflow execution state while sharing the workflow engine infrastructure.

## Decision

Use **database-per-tenant isolation** for workflow engine state, where each platform tenant's logical database contains a dedicated workflow schema alongside their application data schema.

**Pattern:**
Each platform tenant database contains two schemas: the public schema for application data (with account_id-based isolation per ADR 016) and the workflow schema for engine state (isolated by database boundary). Worker pods receive tenant-specific database connection strings via environment variables, connecting to the appropriate tenant database. The workflow engine remains unmodified from OSS, with no tenant_id or account_id columns added to engine tables.

**Database Structure:**
Platform tenant databases contain the workflow schema with tables (workflow_runs, workflow_events, workflow_steps, workflow_hooks, workflow_waits, workflow_stream_chunks) and the public schema with application tables (accounts, users, form_data with account_id columns). Database boundary enforces platform tenant isolation. Account_id columns in public schema enforce user isolation within the tenant.

**Connection Management:**
Worker pods receive WORKFLOW_POSTGRES_URL environment variable pointing to tenant-specific database. Graphile Worker job names prefixed with tenant identifier for routing. Connection pooling configured per tenant database. No cross-tenant database queries possible.

**Isolation Levels:**
Level 1 (Platform Tenant): Database boundary isolates SaaS products (tenant_acme_db vs tenant_techstart_db). Level 2 (Account): account_id column isolates user organizations within a SaaS product (Acme Corp vs TechStart Inc within same database). Workflow engine operates at Level 1 only, account-agnostic by design.

## Consequences

**Positive:**
- No OSS fork required, maintains upgrade compatibility with world-postgres
- Database-level isolation provides strong security boundary between platform tenants
- Aligns with ADR 006 database-per-tenant pattern for consistency
- Workflow engine remains infrastructure-agnostic, no business logic coupling
- Simple connection string configuration per tenant
- No code changes to world-postgres schema or queries

**Negative:**
- Connection pooling calculated per database (PlanetScale limitation applies)
- Storage overhead of ~8MB per tenant database for workflow schema
- Won't scale beyond few hundred platform tenants per CNPG cluster
- No cross-tenant workflow analytics without external data warehouse
- Each tenant database duplicates workflow schema structure
