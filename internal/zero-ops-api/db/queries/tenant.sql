-- name: UpsertTenant :one
INSERT INTO tenants (
    id,
    org_id,
    name,
    email,
    plan,
    status,
    quotas,
    metadata,
    created_at,
    updated_at
)
VALUES (
    $1,
    $2,
    $3,
    $4,
    $5,
    'creating',
    $6,
    $7,
    now(),
    now()
)
ON CONFLICT (name) WHERE deleted_at IS NULL
DO UPDATE
SET
    updated_at = tenants.updated_at
WHERE tenants.deleted_at IS NULL
RETURNING
    *,
    (tenants.created_at = tenants.updated_at) AS created;

-- name: GetTenant :one
SELECT * FROM tenants
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListTenants :many
SELECT * FROM tenants
WHERE
    ($1 = '' OR status = $1)
    AND ($2 = '' OR plan = $2)
    AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $3
OFFSET $4;

-- name: CountTenants :one
SELECT COUNT(*) FROM tenants
WHERE
    ($1 = '' OR status = $1)
    AND ($2 = '' OR plan = $2)
    AND deleted_at IS NULL;

-- name: UpdateTenant :one
UPDATE tenants
SET
    plan = COALESCE(sqlc.narg('plan')::text, plan),
    status = COALESCE(sqlc.narg('status')::text, status),
    quotas = COALESCE(sqlc.narg('quotas')::jsonb, quotas),
    metadata = COALESCE(sqlc.narg('metadata')::jsonb, metadata)
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteTenant :exec
UPDATE tenants
SET
    status = 'deleted',
    deleted_at = now()
WHERE id = $1 AND deleted_at IS NULL;
