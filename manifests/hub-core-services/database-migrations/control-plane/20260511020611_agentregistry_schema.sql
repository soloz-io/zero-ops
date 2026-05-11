-- Migration: Agent Registry Schema
-- Description: Create agentregistry schema with agent_definitions and deployments tables
-- Idempotent: Yes (uses IF NOT EXISTS, conditional policy creation)

GRANT CREATE ON SCHEMA public TO mcp_server;
GRANT CREATE ON SCHEMA public TO agentregistry;

CREATE SCHEMA IF NOT EXISTS agentregistry;

GRANT USAGE ON SCHEMA agentregistry TO agentregistry;
GRANT CREATE ON SCHEMA agentregistry TO agentregistry;
GRANT USAGE ON SCHEMA agentregistry TO mcp_server;

CREATE TABLE IF NOT EXISTS agentregistry.agent_definitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    agent_type TEXT NOT NULL,
    system_message TEXT NOT NULL,
    tool_access JSONB NOT NULL,
    memory_config JSONB NOT NULL,
    guardrail_policies JSONB NOT NULL,
    model_config JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(tenant_id, name, version)
);

ALTER TABLE agentregistry.agent_definitions ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE schemaname = 'agentregistry'
      AND tablename = 'agent_definitions'
      AND policyname = 'tenant_isolation'
  ) THEN
    CREATE POLICY tenant_isolation ON agentregistry.agent_definitions
      USING (tenant_id = current_setting('app.tenant_id')::UUID);
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_agent_definitions_tenant ON agentregistry.agent_definitions(tenant_id);

CREATE TABLE IF NOT EXISTS agentregistry.deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL REFERENCES agentregistry.agent_definitions(id),
    provider_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'deploying',
    provider_metadata JSONB,
    deployed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

ALTER TABLE agentregistry.deployments ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE schemaname = 'agentregistry'
      AND tablename = 'deployments'
      AND policyname = 'tenant_isolation'
  ) THEN
    CREATE POLICY tenant_isolation ON agentregistry.deployments
      USING (tenant_id = current_setting('app.tenant_id')::UUID);
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_deployments_tenant ON agentregistry.deployments(tenant_id);
CREATE INDEX IF NOT EXISTS idx_deployments_agent_id ON agentregistry.deployments(agent_id);
CREATE INDEX IF NOT EXISTS idx_deployments_status ON agentregistry.deployments(status);
