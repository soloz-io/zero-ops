-- Migration: 002_postgrest_dashboard.sql
-- PostgREST dashboard schema: read-only views + RLS + roles

-- ============================================================
-- ROLES (5.2, 5.3)
-- ============================================================

-- Anonymous role: no permissions (blocks unauthenticated access)
DO $$ BEGIN
  CREATE ROLE postgrest_anon NOLOGIN;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Authenticated role: used by PostgREST after JWT validation
DO $$ BEGIN
  CREATE ROLE postgrest_auth NOLOGIN;
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Grant usage on dashboard schema to authenticated role
GRANT USAGE ON SCHEMA dashboard TO postgrest_auth;

-- ============================================================
-- DASHBOARD SCHEMA (5.4)
-- ============================================================
CREATE SCHEMA IF NOT EXISTS dashboard;

-- View: tenant summary for platform admin console
CREATE OR REPLACE VIEW dashboard.tenants AS
SELECT
    id,
    name,
    tier,
    status,
    argo_sync_status,
    argo_health_status,
    owner_email,
    created_at,
    updated_at,
    last_observed_at
FROM public.tenants;

-- View: tenant registrations for onboarding dashboard
CREATE OR REPLACE VIEW dashboard.tenant_registrations AS
SELECT
    id,
    tenant_id,
    status,
    name,
    email,
    tier,
    created_at,
    updated_at
FROM public.tenant_registrations;

-- View: tenant counts by status (global health overview)
CREATE OR REPLACE VIEW dashboard.tenant_status_summary AS
SELECT
    status,
    tier,
    COUNT(*) AS count
FROM public.tenants
GROUP BY status, tier;

-- View: stuck tenants (CREATING/SYNCING older than 10 minutes)
CREATE OR REPLACE VIEW dashboard.stuck_tenants AS
SELECT
    id,
    name,
    tier,
    status,
    created_at,
    updated_at,
    EXTRACT(EPOCH FROM (NOW() - updated_at)) / 60 AS stuck_minutes
FROM public.tenants
WHERE status IN ('CREATING', 'GIT_COMMITTED', 'SYNCING')
  AND updated_at < NOW() - INTERVAL '10 minutes';

-- View: unobserved tenants (no ArgoCD webhook in 15 minutes)
CREATE OR REPLACE VIEW dashboard.unobserved_tenants AS
SELECT
    id,
    name,
    tier,
    status,
    last_observed_at,
    EXTRACT(EPOCH FROM (NOW() - last_observed_at)) / 60 AS unobserved_minutes
FROM public.tenants
WHERE last_observed_at < NOW() - INTERVAL '15 minutes'
  AND status NOT IN ('CREATING', 'DELETED');

-- Grant SELECT on all dashboard views to authenticated role
GRANT SELECT ON ALL TABLES IN SCHEMA dashboard TO postgrest_auth;

-- ============================================================
-- RLS FOR POSTGREST (5.3)
-- ============================================================
-- PostgREST sets the JWT claim `tenant_id` as a GUC before each request.
-- The existing RLS policies on public.tenants and public.tenant_configs
-- already use current_setting('app.tenant_id') — they apply here too.
--
-- For platform admin requests, the JWT must include:
--   { "role": "postgrest_auth", "app.bypass_rls": "true" }
-- PostgREST will SET app.bypass_rls = 'true' from the claim.

-- Allow postgrest_auth to set the tenant context GUC
ALTER ROLE postgrest_auth SET app.bypass_rls = 'false';
