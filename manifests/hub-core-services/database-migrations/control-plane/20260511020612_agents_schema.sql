-- Migration: Agents Schema
-- Description: Create agents schema with authorized_tools table
-- Idempotent: Yes (uses IF NOT EXISTS, conditional policy creation)

CREATE SCHEMA IF NOT EXISTS agents;

GRANT USAGE ON SCHEMA agents TO mcp_server;
GRANT CREATE ON SCHEMA agents TO mcp_server;
GRANT USAGE ON SCHEMA public TO mcp_server;

CREATE TABLE IF NOT EXISTS agents.authorized_tools (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    tool_name VARCHAR(255) NOT NULL,
    category VARCHAR(100),
    description TEXT,
    required_tier VARCHAR(50),
    enabled BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(tenant_id, tool_name)
);

ALTER TABLE agents.authorized_tools ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE schemaname = 'agents'
      AND tablename = 'authorized_tools'
      AND policyname = 'tenant_isolation'
  ) THEN
    CREATE POLICY tenant_isolation ON agents.authorized_tools
      USING (tenant_id = current_setting('app.tenant_id')::UUID);
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_authorized_tools_tenant ON agents.authorized_tools(tenant_id);
