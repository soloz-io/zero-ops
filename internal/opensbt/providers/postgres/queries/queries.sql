-- name: CreateTenant :exec
INSERT INTO tenants (id, name, tier, status, owner_email, config, created_at, updated_at, last_observed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetTenant :one
SELECT id, name, tier, status, argo_sync_status, argo_health_status, owner_email, config, created_at, updated_at, last_observed_at
FROM tenants WHERE id = $1;

-- name: UpdateTenant :exec
UPDATE tenants SET
    name               = COALESCE($2, name),
    tier               = COALESCE($3, tier),
    status             = COALESCE($4, status),
    argo_sync_status   = COALESCE($5, argo_sync_status),
    argo_health_status = COALESCE($6, argo_health_status),
    config             = COALESCE($7, config),
    updated_at         = NOW()
WHERE id = $1;

-- name: DeleteTenant :exec
DELETE FROM tenants WHERE id = $1;

-- name: ListTenants :many
SELECT id, name, tier, status, argo_sync_status, argo_health_status, owner_email, config, created_at, updated_at, last_observed_at
FROM tenants
WHERE ($1::text IS NULL OR status = $1)
  AND ($2::text IS NULL OR tier = $2)
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: UpdateTenantStatus :exec
UPDATE tenants SET status = $2, updated_at = NOW() WHERE id = $1;

-- name: UpdateTenantArgoStatus :exec
UPDATE tenants SET
    argo_sync_status   = $2,
    argo_health_status = $3,
    updated_at         = NOW()
WHERE id = $1;

-- name: TouchTenantObservation :exec
UPDATE tenants SET last_observed_at = NOW() WHERE id = $1;

-- name: ListStuckTenants :many
SELECT id, name, tier, status, argo_sync_status, argo_health_status, owner_email, config, created_at, updated_at, last_observed_at
FROM tenants
WHERE status = ANY($1::text[])
  AND updated_at < $2;

-- name: ListUnobservedTenants :many
SELECT id, name, tier, status, argo_sync_status, argo_health_status, owner_email, config, created_at, updated_at, last_observed_at
FROM tenants
WHERE last_observed_at < $1
  AND status NOT IN ('CREATING', 'DELETED');

-- name: CreateTenantRegistration :exec
INSERT INTO tenant_registrations (id, tenant_id, status, name, email, tier, config, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetTenantRegistration :one
SELECT id, tenant_id, status, name, email, tier, config, created_at, updated_at
FROM tenant_registrations WHERE id = $1;

-- name: UpdateTenantRegistration :exec
UPDATE tenant_registrations SET
    tenant_id  = COALESCE($2, tenant_id),
    status     = COALESCE($3, status),
    config     = COALESCE($4, config),
    updated_at = NOW()
WHERE id = $1;

-- name: DeleteTenantRegistration :exec
DELETE FROM tenant_registrations WHERE id = $1;

-- name: ListTenantRegistrations :many
SELECT id, tenant_id, status, name, email, tier, config, created_at, updated_at
FROM tenant_registrations
WHERE ($1::text IS NULL OR status = $1)
  AND ($2::text IS NULL OR tier = $2)
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: SetTenantConfig :exec
INSERT INTO tenant_configs (tenant_id, config, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (tenant_id) DO UPDATE SET config = $2, updated_at = NOW();

-- name: GetTenantConfig :one
SELECT config FROM tenant_configs WHERE tenant_id = $1;

-- name: DeleteTenantConfig :exec
DELETE FROM tenant_configs WHERE tenant_id = $1;

-- name: RecordProcessedEvent :exec
INSERT INTO processed_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING;

-- name: IsEventProcessed :one
SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1);
