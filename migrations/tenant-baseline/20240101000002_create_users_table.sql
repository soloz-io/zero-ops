-- Migration: Create users table with RLS
-- Description: Users table for tenant identity management with row-level security
-- Idempotent: Yes (uses IF NOT EXISTS)

CREATE TABLE IF NOT EXISTS tenant_{{.tenant_id}}.users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email VARCHAR(255) NOT NULL UNIQUE,
  email_verified BOOLEAN DEFAULT FALSE,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  metadata JSONB DEFAULT '{}'::jsonb
);

-- Create index on email for fast lookups
CREATE INDEX IF NOT EXISTS idx_users_email ON tenant_{{.tenant_id}}.users(email);

-- Create index on created_at for time-based queries
CREATE INDEX IF NOT EXISTS idx_users_created_at ON tenant_{{.tenant_id}}.users(created_at);

-- Enable Row-Level Security
ALTER TABLE tenant_{{.tenant_id}}.users ENABLE ROW LEVEL SECURITY;

-- Drop existing policy if exists (for idempotency)
DROP POLICY IF EXISTS users_isolation_policy ON tenant_{{.tenant_id}}.users;

-- Create RLS policy: users can only access their own record
CREATE POLICY users_isolation_policy ON tenant_{{.tenant_id}}.users
  FOR ALL
  USING (
    id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  )
  WITH CHECK (
    id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  );

-- Grant permissions to tenant role
GRANT ALL ON tenant_{{.tenant_id}}.users TO tenant_{{.tenant_id}}_role;

-- Create updated_at trigger function if not exists
CREATE OR REPLACE FUNCTION tenant_{{.tenant_id}}.update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Drop existing trigger if exists (for idempotency)
DROP TRIGGER IF EXISTS update_users_updated_at ON tenant_{{.tenant_id}}.users;

-- Create trigger to auto-update updated_at
CREATE TRIGGER update_users_updated_at
  BEFORE UPDATE ON tenant_{{.tenant_id}}.users
  FOR EACH ROW
  EXECUTE FUNCTION tenant_{{.tenant_id}}.update_updated_at_column();
