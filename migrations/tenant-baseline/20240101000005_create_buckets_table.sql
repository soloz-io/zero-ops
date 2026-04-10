-- Migration: Create buckets table with RLS
-- Description: Buckets table for object storage organization with row-level security
-- Idempotent: Yes (uses IF NOT EXISTS)

CREATE TABLE IF NOT EXISTS tenant_{{.tenant_id}}.buckets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) NOT NULL UNIQUE,
  owner_id UUID NOT NULL REFERENCES tenant_{{.tenant_id}}.users(id) ON DELETE CASCADE,
  public BOOLEAN DEFAULT FALSE,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  metadata JSONB DEFAULT '{}'::jsonb
);

-- Create index on name for fast bucket lookups
CREATE INDEX IF NOT EXISTS idx_buckets_name ON tenant_{{.tenant_id}}.buckets(name);

-- Create index on owner_id for user bucket queries
CREATE INDEX IF NOT EXISTS idx_buckets_owner_id ON tenant_{{.tenant_id}}.buckets(owner_id);

-- Create index on public for public bucket queries
CREATE INDEX IF NOT EXISTS idx_buckets_public ON tenant_{{.tenant_id}}.buckets(public);

-- Enable Row-Level Security
ALTER TABLE tenant_{{.tenant_id}}.buckets ENABLE ROW LEVEL SECURITY;

-- Drop existing policies if exist (for idempotency)
DROP POLICY IF EXISTS buckets_owner_policy ON tenant_{{.tenant_id}}.buckets;
DROP POLICY IF EXISTS buckets_public_read_policy ON tenant_{{.tenant_id}}.buckets;

-- Create RLS policy: owners can manage their buckets
CREATE POLICY buckets_owner_policy ON tenant_{{.tenant_id}}.buckets
  FOR ALL
  USING (
    owner_id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  )
  WITH CHECK (
    owner_id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid
  );

-- Create RLS policy: anyone can read public buckets
CREATE POLICY buckets_public_read_policy ON tenant_{{.tenant_id}}.buckets
  FOR SELECT
  USING (public = TRUE);

-- Grant permissions to tenant role
GRANT ALL ON tenant_{{.tenant_id}}.buckets TO tenant_{{.tenant_id}}_role;

-- Drop existing trigger if exists (for idempotency)
DROP TRIGGER IF EXISTS update_buckets_updated_at ON tenant_{{.tenant_id}}.buckets;

-- Create trigger to auto-update updated_at
CREATE TRIGGER update_buckets_updated_at
  BEFORE UPDATE ON tenant_{{.tenant_id}}.buckets
  FOR EACH ROW
  EXECUTE FUNCTION tenant_{{.tenant_id}}.update_updated_at_column();
