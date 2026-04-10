-- Migration: Create tenant schema and role
-- Description: Creates isolated schema and role for tenant with proper permissions
-- Idempotent: Yes (uses IF NOT EXISTS)

-- Create tenant schema
CREATE SCHEMA IF NOT EXISTS tenant_{{.tenant_id}};

-- Create tenant role
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'tenant_{{.tenant_id}}_role') THEN
    CREATE ROLE tenant_{{.tenant_id}}_role;
  END IF;
END
$$;

-- Grant schema permissions to tenant role
GRANT ALL ON SCHEMA tenant_{{.tenant_id}} TO tenant_{{.tenant_id}}_role;

-- Grant usage on schema to tenant role
GRANT USAGE ON SCHEMA tenant_{{.tenant_id}} TO tenant_{{.tenant_id}}_role;

-- Set default privileges for future tables
ALTER DEFAULT PRIVILEGES IN SCHEMA tenant_{{.tenant_id}} 
  GRANT ALL ON TABLES TO tenant_{{.tenant_id}}_role;

ALTER DEFAULT PRIVILEGES IN SCHEMA tenant_{{.tenant_id}} 
  GRANT ALL ON SEQUENCES TO tenant_{{.tenant_id}}_role;

ALTER DEFAULT PRIVILEGES IN SCHEMA tenant_{{.tenant_id}} 
  GRANT EXECUTE ON FUNCTIONS TO tenant_{{.tenant_id}}_role;

-- Set search_path for tenant role
ALTER ROLE tenant_{{.tenant_id}}_role SET search_path TO tenant_{{.tenant_id}}, public;
