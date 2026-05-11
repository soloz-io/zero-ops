-- Migration: Spoke Controller Grants
-- Description: Grant CREATE privilege to spoke_controller role
-- Idempotent: Yes (GRANT is idempotent in PostgreSQL)

GRANT CREATE ON SCHEMA public TO spoke_controller;
