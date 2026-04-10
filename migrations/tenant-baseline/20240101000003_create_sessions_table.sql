-- Migration: Create sessions table with RLS
-- Description: Sessions table for user authentication state with row-level security
-- Idempotent: Yes (uses IF NOT EXISTS)

CREATE TABLE IF NOT EXISTS tenant_{{.tenant_id}}.sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES tenant_{{.tenant_id}}.users(id) ON DELETE CASCADE,
  token_hash VARCHAR(255) NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  metadata JSONB DEFAULT '{}'::jsonb
);

-- Create index on user_id for fast user session lookups
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON tenant_{{.tenant_id}}.sessions(user_id);

-- Create index on token_hash for fast token validation
CREATE INDEX IF NOT EXISTS idx_sessions_token_hash ON tenant_{{.tenant_id}}.sessions(token_hash);

-- Create index on expires_at for cleanup queries
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON tenant_{{.tenant_id}}.sessions(expires_at);

-- Enable Row-Level Security
ALTER TABLE tenant_{{.tenant_id}}.sessions ENABLE ROW LEVEL SECURITY;

-- Drop existing policy if exists (for idempotency)
DROP POLICY IF EXISTS sessions_isolation_policy ON tenant_{{.tenant_id}}.sessions;

-- Create RLS policy: users can only access their own sessions
CREATE POLICY sessions_isolation_policy ON tenant_{{.tenant_id}}.sessions
  FOR ALL
  USING (
    user_id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  )
  WITH CHECK (
    user_id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  );

-- Grant permissions to tenant role
GRANT ALL ON tenant_{{.tenant_id}}.sessions TO tenant_{{.tenant_id}}_role;
