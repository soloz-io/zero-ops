-- Migration: 001_initial_schema.sql
-- PostgreSQL schema with RLS policies and composite indexes

-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ============================================================
-- TENANTS
-- ============================================================
CREATE TABLE IF NOT EXISTS tenants (
    id                  TEXT        PRIMARY KEY,
    name                TEXT        NOT NULL,
    tier                TEXT        NOT NULL,
    status              TEXT        NOT NULL DEFAULT 'CREATING',
    argo_sync_status    TEXT        NOT NULL DEFAULT '',
    argo_health_status  TEXT        NOT NULL DEFAULT '',
    owner_email         TEXT        NOT NULL,
    config              JSONB       NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_observed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Composite indexes for tenant queries (4.18)
CREATE INDEX IF NOT EXISTS idx_tenants_status_created  ON tenants (status, created_at);
CREATE INDEX IF NOT EXISTS idx_tenants_tier_status     ON tenants (tier, status);
CREATE INDEX IF NOT EXISTS idx_tenants_last_observed   ON tenants (last_observed_at);

-- RLS (4.1) — platform admin role bypasses; app role filters by set tenant context
ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenants_isolation ON tenants
    USING (
        current_setting('app.bypass_rls', true) = 'true'
        OR id = current_setting('app.tenant_id', true)
    );

-- ============================================================
-- TENANT REGISTRATIONS
-- ============================================================
CREATE TABLE IF NOT EXISTS tenant_registrations (
    id          TEXT        PRIMARY KEY,
    tenant_id   TEXT        REFERENCES tenants(id) ON DELETE SET NULL,
    status      TEXT        NOT NULL DEFAULT 'pending',
    name        TEXT        NOT NULL,
    email       TEXT        NOT NULL,
    tier        TEXT        NOT NULL,
    config      JSONB       NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_registrations_status ON tenant_registrations (status);
CREATE INDEX IF NOT EXISTS idx_registrations_tier   ON tenant_registrations (tier);

-- ============================================================
-- TENANT CONFIGS
-- ============================================================
CREATE TABLE IF NOT EXISTS tenant_configs (
    tenant_id   TEXT        PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    config      JSONB       NOT NULL DEFAULT '{}',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE tenant_configs ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_configs_isolation ON tenant_configs
    USING (
        current_setting('app.bypass_rls', true) = 'true'
        OR tenant_id = current_setting('app.tenant_id', true)
    );

-- ============================================================
-- PROCESSED EVENTS (Inbox Pattern — 4.10)
-- ============================================================
CREATE TABLE IF NOT EXISTS processed_events (
    event_id    TEXT        PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Auto-expire old processed events after 7 days (prevents unbounded growth)
CREATE INDEX IF NOT EXISTS idx_processed_events_time ON processed_events (processed_at);
