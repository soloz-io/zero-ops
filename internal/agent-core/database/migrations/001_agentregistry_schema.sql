-- Schema: agentregistry (managed by AgentRegistry OSS)
-- Adds tenant isolation columns and RLS policies on top of AgentRegistry OSS schema

CREATE SCHEMA IF NOT EXISTS agentregistry;

-- Agent definitions table with tenant isolation
-- Matches requirements.md FR-1 schema exactly
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

CREATE POLICY tenant_isolation ON agentregistry.agent_definitions
    USING (tenant_id = current_setting('app.tenant_id')::UUID);

CREATE INDEX IF NOT EXISTS idx_agent_definitions_tenant ON agentregistry.agent_definitions(tenant_id);

-- Deployments table with tenant isolation
-- Matches requirements.md FR-2 schema exactly, with agent_id FK to agent_definitions
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

CREATE POLICY tenant_isolation ON agentregistry.deployments
    USING (tenant_id = current_setting('app.tenant_id')::UUID);

CREATE INDEX IF NOT EXISTS idx_deployments_tenant ON agentregistry.deployments(tenant_id);
CREATE INDEX IF NOT EXISTS idx_deployments_agent_id ON agentregistry.deployments(agent_id);
CREATE INDEX IF NOT EXISTS idx_deployments_status ON agentregistry.deployments(status);
