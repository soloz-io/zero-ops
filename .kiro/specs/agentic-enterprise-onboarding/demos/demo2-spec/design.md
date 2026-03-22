# Demo 2 Design: Keto Authorization Enforcement

## Overview

**Demo Outcome:** "The platform enforces fine-grained permissions"

This design implements Ory Keto relationship-based authorization checks that were deferred from Demo 1. AgentGateway now calls auth-proxy to query Keto for permission decisions before forwarding requests to backend services.

**Scope:** Requirement 3 ACs 6-8 (Keto permission checks)

**Prerequisites:** Demo 1 complete (OAuth2 PKCE flow, JWT validation, basic auth-proxy)

**Out of Scope:** Advanced Keto patterns (delegated permissions, wildcards), CIMD (Req 13 AC4-11)

## Architecture Changes from Demo 1

### What Changes

**auth-proxy enhancements:**
- Add Keto client (`internal/auth-proxy/client/keto.go`)
- Add permission check logic in extAuthz handler (`internal/auth-proxy/handlers/validate.go`)
- Add Keto configuration (KETO_READ_URL, KETO_WRITE_URL env vars)

**No changes to:**
- AgentGateway (already calls auth-proxy extAuthz)
- Ory Hydra (JWT issuance unchanged)
- Ory Kratos (identity management unchanged)
- mcp-server (receives X-Auth-* headers, no JWT validation)

### Component Topology

```
Cursor/Goose (MCP Client)
    │
    │ Authorization: Bearer <jwt>
    ▼
AgentGateway
    │
    │ POST /internal/validate
    ▼
auth-proxy
    │
    ├─→ Validate JWT (existing Demo 1 logic)
    │
    └─→ Query Keto for permission (NEW)
        │
        ▼
    Ory Keto
        │
        └─→ Check relationship tuple
            tenant:{tenant_id}#admin@user:{sub}
```

## Keto Permission Model

### Namespace Definition

**Namespace:** `tenants`

**Relations:**
- `admin`: Full control over tenant resources
- `member`: Read-only access to tenant resources (future)

### Relationship Tuples

**Format:** `namespace:object#relation@subject`

**Examples:**
```
tenant:acme-corp#admin@user:550e8400-e29b-41d4-a716-446655440000
tenant:acme-corp#member@user:660e8400-e29b-41d4-a716-446655440001
```

**Tuple Creation:** Created by mcp-server during tenant_create (Req 4 AC3)

### Permission Checks

**Check Format:** "Does subject have relation on object?"

**Examples:**
```
Check: user:550e8400-e29b-41d4-a716-446655440000 has admin on tenant:acme-corp
Result: allowed (tuple exists)

Check: user:660e8400-e29b-41d4-a716-446655440001 has admin on tenant:acme-corp
Result: denied (no admin tuple, only member)
```

## auth-proxy Enhancements

### Configuration

**New Environment Variables:**
```bash
KETO_READ_URL=http://ory-keto-read.ory-system.svc.cluster.local:80
KETO_WRITE_URL=http://ory-keto-write.ory-system.svc.cluster.local:80
KETO_TIMEOUT=2s
```

### Keto Client

**File:** `internal/auth-proxy/client/keto.go`

**Interface:**
```go
type KetoClient interface {
    CheckPermission(ctx context.Context, req CheckPermissionRequest) (bool, error)
}

type CheckPermissionRequest struct {
    Namespace string // "tenants"
    Object    string // tenant_id
    Relation  string // "admin"
    Subject   string // "user:{sub}"
}
```

**Implementation:**
- HTTP GET to `{KETO_READ_URL}/relation-tuples/check` with query parameters
- Query params: `?namespace=tenants&object={tenant_id}&relation=admin&subject_id=user:{sub}`
- Example: `GET /relation-tuples/check?namespace=tenants&object=acme-corp&relation=admin&subject_id=user:550e8400`
- Response: `{"allowed":true}` or `{"allowed":false}`
- Timeout: 2 seconds (KETO_TIMEOUT)
- Retry: None (fail fast for authorization checks)

**Note:** Keto v0.x (deployed version v25.4.0) uses GET with query params, not POST with body. Keto v2.x uses different API.

### extAuthz Handler Enhancement

**File:** `internal/auth-proxy/handlers/validate.go`

**Current Flow (Demo 1):**
1. Extract JWT from Authorization header
2. Validate JWT signature using cached JWKS
3. Verify exp claim
4. Verify aud claim
5. Return HTTP 200 with X-Auth-* headers

**Enhanced Flow (Demo 2):**
1. Extract JWT from Authorization header
2. Validate JWT signature using cached JWKS
3. Verify exp claim
4. Verify aud claim
5. **NEW:** Extract required permission from request path/method
6. **NEW:** Query Keto for permission check
7. **NEW:** Return HTTP 403 if Keto denies
8. Return HTTP 200 with X-Auth-* headers if allowed

### Permission Mapping

**Rule:** Map MCP tool to required permission

**Mapping Table:**

| MCP Tool | HTTP Method | Path | Required Permission | Keto Check |
|----------|-------------|------|---------------------|------------|
| tenant_create | POST | /mcp/tenant | tenant:write | Skip (no tenant_id yet) |
| environment_create | POST | /mcp/environment | cluster:write | tenant:{tenant_id}#admin@user:{sub} |
| environment_status | GET | /mcp/environment/{id} | cluster:read | tenant:{tenant_id}#admin@user:{sub} |
| environments_list | GET | /mcp/environments | cluster:read | tenant:{tenant_id}#admin@user:{sub} |
| environment_delete | DELETE | /mcp/environment/{id} | cluster:write | tenant:{tenant_id}#admin@user:{sub} |

**platform_admin Bypass (Demo 2 Scope Decision):**
- **Option B (Implemented):** auth-proxy checks `role` claim from JWT
- IF `role == "platform_admin"`, skip Keto check entirely (bypass pattern)
- Rationale: Simplest for Demo 2, no Keto wildcard tuples needed
- platform_admin can perform operations on any tenant without explicit Keto tuples
- Logged separately for audit: `"platform_admin_bypass": true`

**Cross-Tenant Access Control (Demo 2 Scope Decision):**
- **Option B (Implemented):** Demo 2 validates same-tenant access only (JWT tenant_id matches resource tenant_id)
- Keto check uses `tenant_id` from JWT claims (user's home tenant)
- Cross-tenant access attempts are blocked by mcp-server validating X-Tenant-ID header against requested resource
- Path-based tenant extraction (parsing tenant_id from environment_id) deferred to Demo 3
- Demo 2 scope: User can only access resources in their own tenant

**Implementation:**
```go
func (h *ValidateHandler) ValidateRequest(ctx context.Context, req *ValidateRequest) (*ValidateResponse, error) {
    // 1-4: JWT validation (existing Demo 1 logic)
    claims, err := h.jwtValidator.Validate(ctx, req.Token)
    if err != nil {
        return &ValidateResponse{Allowed: false, StatusCode: 401}, err
    }
    
    // 5: Extract role and check platform_admin bypass
    role := claims["role"].(string)
    if role == "platform_admin" {
        // Platform admin bypasses Keto checks
        return &ValidateResponse{
            Allowed: true,
            StatusCode: 200,
            Headers: map[string]string{
                "X-Auth-User-Id": claims["sub"].(string),
                "X-Auth-Email": claims["email"].(string),
                "X-Auth-Role": role,
                "X-Platform-Admin-Bypass": "true", // Audit flag
            },
        }, nil
    }
    
    // 6: Determine if Keto check needed
    relation, needsCheck := h.getRequiredPermission(req.Method, req.Path)
    if !needsCheck {
        // tenant_create or unknown path - skip Keto
        return &ValidateResponse{Allowed: true, StatusCode: 200, Headers: buildHeaders(claims)}, nil
    }
    
    // 7: Query Keto
    tenantID := claims["tenant_id"].(string)
    sub := claims["sub"].(string)
    allowed, err := h.ketoClient.CheckPermission(ctx, CheckPermissionRequest{
        Namespace: "tenants",
        Object:    tenantID,
        Relation:  relation,
        Subject:   fmt.Sprintf("user:%s", sub),
    })
    
    if err != nil {
        return &ValidateResponse{Allowed: false, StatusCode: 500}, err
    }
    
    if !allowed {
        return &ValidateResponse{Allowed: false, StatusCode: 403}, nil
    }
    
    return &ValidateResponse{Allowed: true, StatusCode: 200, Headers: buildHeaders(claims)}, nil
}

func (h *ValidateHandler) getRequiredPermission(method, path string) (string, bool) {
    // tenant_create has no tenant_id yet, skip Keto check
    if method == "POST" && strings.HasPrefix(path, "/mcp/tenant") {
        return "", false // skip=true
    }
    
    // All other operations require tenant_id in JWT
    if method == "POST" && strings.HasPrefix(path, "/mcp/environment") {
        return "admin", true // check tenant:{tenant_id}#admin@user:{sub}
    }
    
    if method == "GET" && strings.HasPrefix(path, "/mcp/environment") {
        return "admin", true // Demo 2: admin only, member deferred
    }
    
    if method == "DELETE" && strings.HasPrefix(path, "/mcp/environment") {
        return "admin", true
    }
    
    return "", false // unknown path, skip Keto
}
```

### Error Responses

**HTTP 403 Forbidden (Keto denies):**
```json
{
  "error": "forbidden",
  "message": "You do not have permission to perform this operation on tenant acme-corp"
}
```

**HTTP 401 Unauthorized (JWT invalid):**
```json
{
  "error": "invalid_token",
  "error_description": "Token expired"
}
```

**HTTP 500 Internal Server Error (Keto unavailable):**
```json
{
  "error": "authorization_service_unavailable",
  "message": "Unable to verify permissions. Please try again."
}
```

## Keto Deployment

**Existing from Demo 1:** Keto Helm chart already deployed in `ory-system` namespace

**Configuration (values.yaml):**
```yaml
keto:
  config:
    namespaces:
      - id: 0
        name: tenants
    serve:
      read:
        port: 80
      write:
        port: 80
  
  extraEnv:
    - name: KETO_DB_PASSWORD
      valueFrom:
        secretKeyRef:
          name: identity-postgres-passwords
          key: keto-password
    - name: DSN
      value: postgres://keto:$(KETO_DB_PASSWORD)@identity-postgres-rw.ory-system.svc.cluster.local:5432/keto_db
```

**Endpoints (verified from live cluster):**
- Read API: `ory-keto-read.ory-system.svc.cluster.local:80`
- Write API: `ory-keto-write.ory-system.svc.cluster.local:80`
- Headless (internal): ports 4466/4467

**No changes needed** - Keto is already deployed and configured from Demo 1

**API Version:** Keto v0.x (v25.4.0) - uses GET /relation-tuples/check with query params

## mcp-server Integration

**Location:** `cmd/mcp-server/` (single MCP server with all tools)

**Keto Write Operations:** mcp-server creates relationship tuples during tenant_create

**File:** `cmd/mcp-server/tools/tenant/create.go`

**Tuple Creation (Req 4 AC3):**
```go
// After successful tenant DB insert and Kratos trait update
tuple := &keto.RelationTuple{
    Namespace: "tenants",
    Object:    tenantID,
    Relation:  "admin",
    SubjectID: fmt.Sprintf("user:%s", userID),
}

err := ketoClient.CreateTuple(ctx, tuple)
if err != nil {
    // Mark tenant as INCOMPLETE_IDENTITY_SETUP
    // Retry on next tenant_create invocation
}
```

**Keto Client Dependency:**
- mcp-server uses `internal/opensbt/providers/ory/keto.go` for tuple creation
- auth-proxy uses `internal/auth-proxy/client/keto.go` for permission checks
- Both clients talk to same Keto instance, different purposes

## Demo Flow

### Success Path

**Step 1: User authenticates (Demo 1 flow)**
```
Cursor → AgentGateway → auth-proxy → Hydra/Kratos
Result: JWT with sub=550e8400, tenant_id=acme-corp, role=tenant_admin
```

**Step 2: User creates tenant (Keto tuple created)**
```
Cursor: tenant_create(name="acme-corp")
mcp-server: Creates tuple tenant:acme-corp#admin@user:550e8400
Result: HTTP 201, tenant created
```

**Step 3: User provisions environment (Keto check passes)**
```
Cursor: environment_create(tenant_id="acme-corp", tier="enterprise")
AgentGateway → auth-proxy extAuthz:
  1. Validate JWT ✓
  2. Query Keto: user:550e8400 has admin on tenant:acme-corp? → allowed ✓
  3. Return HTTP 200 with X-Auth-* headers
mcp-server: Receives request, provisions environment
Result: HTTP 202, provisioning started
```

### Failure Path (Permission Denied)

**Scenario:** User with no admin tuple tries to access their tenant's environment

**Step 1: User authenticates**
```
JWT: sub=660e8400, tenant_id=acme-corp, role=tenant_admin
```

**Step 2: User tries to access environment (no Keto tuple exists)**
```
Cursor: environment_status(environment_id="acme-corp-production")
AgentGateway → auth-proxy extAuthz:
  1. Validate JWT ✓
  2. Extract tenant_id from JWT claims → "acme-corp"
  3. Query Keto: user:660e8400 has admin on tenant:acme-corp? → denied ✗ (no tuple)
  4. Return HTTP 403 Forbidden
Cursor: Displays "You do not have permission to access this environment"
```

**Note:** Cross-tenant access (Bob accessing Alice's tenant) is blocked by mcp-server validating X-Tenant-ID header, not by Keto in Demo 2.

### Failure Path (Keto Unavailable)

**Scenario:** Keto service is down

**Step 1: User tries to provision environment**
```
Cursor: environment_create(tenant_id="acme-corp", tier="enterprise")
AgentGateway → auth-proxy extAuthz:
  1. Validate JWT ✓
  2. Query Keto: HTTP timeout after 2s
  3. Return HTTP 500 Internal Server Error
Cursor: Displays "Authorization service unavailable. Please try again."
```

## Testing Strategy

### Unit Tests

**auth-proxy Keto client:**
- `TestKetoClient_CheckPermission_Allowed`
- `TestKetoClient_CheckPermission_Denied`
- `TestKetoClient_CheckPermission_Timeout`
- `TestKetoClient_CheckPermission_InvalidResponse`

**auth-proxy extAuthz handler:**
- `TestValidateHandler_WithKetoCheck_Allowed`
- `TestValidateHandler_WithKetoCheck_Denied`
- `TestValidateHandler_WithKetoCheck_KetoUnavailable`
- `TestValidateHandler_SkipKetoForTenantCreate`

### Integration Tests

**E2E flow with Testcontainers:**
1. Start Keto container
2. Create relationship tuple
3. Call auth-proxy extAuthz with valid JWT
4. Verify HTTP 200 response
5. Call auth-proxy extAuthz with JWT for different user
6. Verify HTTP 403 response

### Manual Demo Script

**Setup:**
```bash
# Create two test users
kubectl exec -n ory-system deploy/ory-kratos -- \
  curl -X POST http://localhost:4434/admin/identities \
  -d '{"schema_id":"default","traits":{"email":"alice@acme.com","role":"tenant_admin"},"credentials":{"password":{"config":{"password":"Demo2Alice!"}}}}'

kubectl exec -n ory-system deploy/ory-kratos -- \
  curl -X POST http://localhost:4434/admin/identities \
  -d '{"schema_id":"default","traits":{"email":"bob@other.com","role":"tenant_admin"},"credentials":{"password":{"config":{"password":"Demo2Bob!"}}}}'
```

**Demo:**
1. Alice authenticates, creates tenant "acme-corp" → Keto tuple created
2. Alice provisions environment → Keto check passes, HTTP 202
3. Create Bob identity without Keto tuple, Bob authenticates with tenant_id=acme-corp → tries to access acme-corp environment → Keto check fails, HTTP 403 (no tuple)
4. Stop Keto pod, Alice tries to provision → HTTP 500 (service unavailable)
5. Restart Keto pod, Alice retries → HTTP 202 (self-healing)

**Note:** Cross-tenant demo (Bob with tenant_id=other-corp accessing acme-corp) deferred to Demo 3 when path-based tenant extraction is implemented.

## Success Criteria

1. auth-proxy queries Keto for permission checks on protected endpoints
2. Valid JWT + valid permission → HTTP 200, request forwarded
3. Valid JWT + invalid permission → HTTP 403, request blocked
4. Keto unavailable → HTTP 500, clear error message
5. tenant_create skips Keto check (no tenant_id yet)
6. All other operations require Keto check
7. mcp-server creates Keto tuples during tenant_create
8. Cursor displays permission errors clearly

## Deployment Changes

**auth-proxy Deployment:**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: auth-proxy
  namespace: identity-services
spec:
  template:
    spec:
      containers:
      - name: auth-proxy
        env:
        # Existing Demo 1 env vars
        - name: HYDRA_PUBLIC_URL
          value: http://ory-hydra-public.ory-system.svc.cluster.local:4444
        - name: HYDRA_ADMIN_URL
          value: http://ory-hydra-admin.ory-system.svc.cluster.local:4445
        - name: KRATOS_PUBLIC_URL
          value: http://ory-kratos-public.ory-system.svc.cluster.local:4433
        - name: KRATOS_ADMIN_URL
          value: http://ory-kratos-admin.ory-system.svc.cluster.local:4434
        # NEW Demo 2 env vars
        - name: KETO_READ_URL
          value: http://ory-keto-read.ory-system.svc.cluster.local:80
        - name: KETO_WRITE_URL
          value: http://ory-keto-write.ory-system.svc.cluster.local:80
        - name: KETO_TIMEOUT
          value: "2s"
```

**No other deployment changes needed** - AgentGateway, mcp-server, Ory stack unchanged

## Observability

**Metrics (Prometheus):**
```
auth_proxy_keto_checks_total{result="allowed|denied|error"}
auth_proxy_keto_check_duration_seconds
auth_proxy_keto_errors_total{reason="timeout|unavailable|invalid_response"}
```

**Logs (structured JSON):**
```json
{
  "level": "info",
  "msg": "keto_check_allowed",
  "user_id": "550e8400",
  "tenant_id": "acme-corp",
  "relation": "admin",
  "duration_ms": 45
}
```

```json
{
  "level": "warn",
  "msg": "keto_check_denied",
  "user_id": "660e8400",
  "tenant_id": "acme-corp",
  "relation": "admin"
}
```

```json
{
  "level": "error",
  "msg": "keto_unavailable",
  "error": "context deadline exceeded",
  "duration_ms": 2000
}
```

## Rollback Plan

**If Demo 2 fails:**
1. Revert auth-proxy to Demo 1 version (remove Keto client code)
2. Keto remains deployed but unused (no impact)
3. All requests pass through without permission checks (Demo 1 behavior)
4. mcp-server continues creating Keto tuples (no-op if not checked)

**No data loss** - Keto tuples persist in PostgreSQL, can be used when Demo 2 is fixed

## Future Enhancements (Out of Scope)

**Demo 3+:**
- Member role (read-only access)
- Delegated permissions (tenant admin grants access to other users)
- Resource-level permissions (per-environment access control)
- Wildcard permissions (admin on tenant:* for platform admins)
- Permission caching in auth-proxy (reduce Keto load)
