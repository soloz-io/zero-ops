-- Migration 004: Metering tables (Task 28)
CREATE TABLE IF NOT EXISTS meters (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    unit        TEXT NOT NULL DEFAULT '',
    type        TEXT NOT NULL DEFAULT 'counter',
    config      JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS usage_events (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    meter_id    TEXT NOT NULL REFERENCES meters(id) ON DELETE CASCADE,
    value       DOUBLE PRECISION NOT NULL,
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    properties  JSONB NOT NULL DEFAULT '{}',
    cancelled   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_usage_events_tenant_meter ON usage_events (tenant_id, meter_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_usage_events_meter_time   ON usage_events (meter_id, timestamp);
