-- Migration: Agent Infra Status RLS Policies
-- Description: Enable RLS and create isolation policies for agent_infra_status table
-- Idempotent: Yes (uses conditional policy creation)

ALTER TABLE agent_infra_status ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE schemaname = 'public'
      AND tablename = 'agent_infra_status'
      AND policyname = 'spoke_cluster_isolation'
  ) THEN
    CREATE POLICY spoke_cluster_isolation ON agent_infra_status
      USING (spoke_cluster_id = current_setting('app.spoke_cluster_id', true));
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE schemaname = 'public'
      AND tablename = 'agent_infra_status'
      AND policyname = 'tenant_isolation'
  ) THEN
    CREATE POLICY tenant_isolation ON agent_infra_status
      USING (tenant_id = current_setting('app.tenant_id', true)::UUID);
  END IF;
END $$;
