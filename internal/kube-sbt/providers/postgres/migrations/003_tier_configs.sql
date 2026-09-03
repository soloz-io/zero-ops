-- 003_tier_configs.sql
-- Tier configuration table (17.11, 17.12, 17.13)
CREATE TABLE IF NOT EXISTS tier_configs (
    name         VARCHAR(50)  PRIMARY KEY,
    display_name VARCHAR(100) NOT NULL,
    description  TEXT         NOT NULL DEFAULT '',
    quotas       JSONB        NOT NULL DEFAULT '{}',
    features     JSONB        NOT NULL DEFAULT '[]',
    pricing      JSONB        NOT NULL DEFAULT '{}',
    metadata     JSONB        NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Seed default tiers (17.12 — -1 = unlimited)
INSERT INTO tier_configs (name, display_name, description, quotas, features, pricing) VALUES
('basic', 'Basic', 'Entry-level shared tier',
 '{"users":10,"storage_gb":10,"api_requests":10000,"cpu":"1","memory":"2Gi"}',
 '["basic_support","email_notifications"]',
 '{"monthly_usd":29,"annual_usd":290}'),
('standard', 'Standard', 'Standard shared tier',
 '{"users":50,"storage_gb":50,"api_requests":100000,"cpu":"2","memory":"4Gi"}',
 '["priority_support","email_notifications","api_access","webhooks"]',
 '{"monthly_usd":99,"annual_usd":990}'),
('premium', 'Premium', 'Dedicated namespace tier',
 '{"users":100,"storage_gb":100,"api_requests":1000000,"cpu":"4","memory":"8Gi"}',
 '["priority_support","email_notifications","api_access","webhooks","sso","custom_domain"]',
 '{"monthly_usd":299,"annual_usd":2990}'),
('enterprise', 'Enterprise', 'Dedicated cluster tier',
 '{"users":-1,"storage_gb":-1,"api_requests":-1,"cpu":"8","memory":"16Gi"}',
 '["dedicated_support","email_notifications","api_access","webhooks","sso","custom_domain","sla","audit_logs"]',
 '{"monthly_usd":999,"annual_usd":9990}')
ON CONFLICT (name) DO NOTHING;
