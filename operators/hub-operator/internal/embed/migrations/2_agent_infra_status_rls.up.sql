ALTER TABLE agent_infra_status ENABLE ROW LEVEL SECURITY;

-- RLS policy: Spoke controllers can only write their own cluster's status
-- PostgREST sets app.spoke_cluster_id from JWT claims
CREATE POLICY spoke_cluster_isolation ON agent_infra_status
    USING (spoke_cluster_id = current_setting('app.spoke_cluster_id', true));

-- Additional policy: Tenant isolation for dashboard reads
-- PostgREST sets app.tenant_id from JWT claims for dashboard queries
CREATE POLICY tenant_isolation ON agent_infra_status
    USING (tenant_id = current_setting('app.tenant_id', true)::UUID);
