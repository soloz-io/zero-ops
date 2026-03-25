-- name: GetAuthorizedTools :many
SELECT tool_name, category, enabled
FROM agents.authorized_tools
WHERE tenant_id = $1
  AND enabled = true
ORDER BY tool_name;

-- name: GetAuthorizedToolsByCategory :many
SELECT tool_name, category, enabled
FROM agents.authorized_tools
WHERE tenant_id = $1
  AND category = $2
  AND enabled = true
ORDER BY tool_name;

-- name: ValidateToolAccess :one
SELECT COUNT(*) > 0 AS authorized
FROM agents.authorized_tools
WHERE tenant_id = $1
  AND tool_name = $2
  AND enabled = true;
