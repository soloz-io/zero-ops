-- Schema: agents (Zero-Ops platform-specific extensions)

CREATE SCHEMA IF NOT EXISTS agents;

GRANT USAGE ON SCHEMA agents TO mcp_server;
GRANT CREATE ON SCHEMA agents TO mcp_server;
GRANT USAGE ON SCHEMA public TO mcp_server;

-- Authorized tools per tenant
-- tenant_id references public.tenants per design.md section 2.2
CREATE TABLE IF NOT EXISTS agents.authorized_tools (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES public.tenants(id),
    tool_name VARCHAR(255) NOT NULL,
    category VARCHAR(100),
    enabled BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(tenant_id, tool_name)
);

ALTER TABLE agents.authorized_tools ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON agents.authorized_tools
    USING (tenant_id = current_setting('app.tenant_id')::UUID);

CREATE INDEX IF NOT EXISTS idx_authorized_tools_tenant ON agents.authorized_tools(tenant_id);
