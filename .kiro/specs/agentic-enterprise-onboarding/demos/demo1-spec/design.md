# Demo 1 Design: OAuth2 Authorization Code Flow with PKCE

## Overview

**Demo Outcome:** "The platform knows who you are"

This design implements OAuth 2.1 Authorization Code Flow with PKCE for MCP client authentication, enabling Cursor/Goose to authenticate users via browser redirect and obtain JWTs for subsequent API calls.

**Scope:** Requirements 2, 10, 12, 13 (partial - pre-registration only)

**Out of Scope:** CIMD (Req 13 AC4-11), token refresh (Req 14), authorization enforcement (Req 3)

## Architecture

### Component Topology

```
Cursor/Goose (MCP Client)
    │
    │ 1. Discover OAuth endpoints
    ▼
AgentGateway
    │ /.well-known/oauth-protected-resource
    │
    │ 2. Fetch authorization server metadata
    ▼
identity-service
    │ /.well-known/oauth-authorization-server (proxy to Hydra)
    │
    │ 3. Authorization Code Flow with PKCE
    ▼
Ory Hydra (OAuth2 Server)
    │
    │ 4. User authentication
    ▼
Ory Kratos (Identity Provider)
    │
    │ 5. JWT issuance
    ▼
Cursor/Goose (stores tokens in OS keychain)
```

### Namespace Deployment


**Namespaces:**
- `ory-system`: Hydra, Kratos, Keto, CNPG cluster
- `identity-services`: identity-service
- `api-gateway`: AgentGateway

**Rationale:** Multi-namespace isolation provides better RBAC, blast radius control, and independent upgrade paths (enterprise pattern per memory/design-principles.md).

## Component Specifications

### 1. CNPG Database Cluster

**Resource:** CloudNativePG Cluster CR

**Namespace:** `ory-system`

**Configuration:**
```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: identity-postgres
  namespace: ory-system
spec:
  instances: 3
  storage:
    size: 20Gi
```

**Databases (via Database CRD):**
- `hydra-db`: OAuth2 clients, sessions, tokens
- `kratos-db`: Identities, credentials, sessions
- `keto-db`: Relationships, permissions

**Connection Pattern:**
- Service DNS: `identity-postgres-rw.ory-system.svc.cluster.local:5432`
- Each Ory component connects via dedicated database

### 2. Ory Hydra

**Deployment:** Helm chart `ory/hydra` v25.4.0

**Namespace:** `ory-system`

**Configuration (values.yaml):**
```yaml
hydra:
  config:
    dsn: postgres://hydra:${HYDRA_DB_PASSWORD}@identity-postgres-rw.ory-system.svc.cluster.local:5432/hydra_db
    urls:
      self:
        issuer: https://auth.zero-ops.io
      login: https://identity-service.identity-services.svc.cluster.local:8080/login
      consent: https://identity-service.identity-services.svc.cluster.local:8080/consent
    oauth2:
      expose_internal_errors: false
    ttl:
      access_token: 24h
      refresh_token: 720h  # 30 days
```

**Endpoints:**
- Public API: `ory-hydra-public.ory-system.svc.cluster.local:4444`
  - `/oauth2/auth` - Authorization endpoint
  - `/oauth2/token` - Token endpoint
  - `/.well-known/jwks.json` - JWKS endpoint
- Admin API: `ory-hydra-admin.ory-system.svc.cluster.local:4445`
  - `/admin/clients` - Client management

**Custom Claims Injection:**
- Hydra redirects to identity-service login/consent endpoints
- identity-service calls `acceptOAuth2ConsentRequest` with session parameter:
  ```json
  {
    "session": {
      "id_token": {
        "tenant_id": "...",
        "email": "...",
        "role": "..."
      },
      "access_token": {
        "tenant_id": "..."
      }
    }
  }
  ```
- Claims fetched from Kratos identity traits during consent flow

### 3. Ory Kratos

**Deployment:** Helm chart `ory/kratos` v25.4.0

**Namespace:** `ory-system`

**Configuration (values.yaml):**
```yaml
kratos:
  config:
    dsn: postgres://kratos:${KRATOS_DB_PASSWORD}@identity-postgres-rw.ory-system.svc.cluster.local:5432/kratos_db
    identity:
      default_schema_id: default
      schemas:
        - id: default
          url: file:///etc/config/identity.schema.json
    selfservice:
      default_browser_return_url: https://console.zero-ops.io/
      flows:
        login:
          ui_url: https://console.zero-ops.io/login
        registration:
          ui_url: https://console.zero-ops.io/registration
```

**Identity Schema (identity.schema.json):**
```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "traits": {
      "type": "object",
      "properties": {
        "email": {
          "type": "string",
          "format": "email"
        },
        "tenant_id": {
          "type": "string"
        },
        "role": {
          "type": "string",
          "enum": ["tenant_admin", "platform_admin"]
        }
      },
      "required": ["email"],
      "additionalProperties": false
    }
  }
}
```

**Endpoints:**
- Public API: `ory-kratos-public.ory-system.svc.cluster.local:4433`
- Admin API: `ory-kratos-admin.ory-system.svc.cluster.local:4434`

### 4. Ory Keto

**Deployment:** Helm chart `ory/keto` v25.4.0

**Namespace:** `ory-system`

**Configuration (values.yaml):**
```yaml
keto:
  config:
    dsn: postgres://keto:${KETO_DB_PASSWORD}@identity-postgres-rw.ory-system.svc.cluster.local:5432/keto_db
    namespaces:
      - id: 0
        name: tenants
```

**Endpoints:**
- Read API: `ory-keto-read.ory-system.svc.cluster.local:4466`
- Write API: `ory-keto-write.ory-system.svc.cluster.local:4467`

### 5. identity-service

**Technology:** Python FastAPI service

**Namespace:** `identity-services`

**Responsibilities:**
- Proxy OAuth metadata from Hydra to AgentGateway
- Own login/consent endpoints (Hydra redirects here)
- Call Hydra Admin API for OAuth client lifecycle
- Call Kratos Admin API for identity trait updates
- Call Keto APIs for permission checks
- Inject custom JWT claims during consent flow

**Endpoints:**
- `GET /.well-known/oauth-authorization-server` - Proxy Hydra metadata
- `GET /login` - Login UI (Hydra redirect target)
- `POST /login` - Process login, call Kratos, accept/reject login request
- `GET /consent` - Consent UI (Hydra redirect target)
- `POST /consent` - Process consent, inject claims, accept/reject consent request

**Dependencies:**
- Hydra Admin API: `http://ory-hydra-admin.ory-system.svc.cluster.local:4445`
- Kratos Admin API: `http://ory-kratos-admin.ory-system.svc.cluster.local:4434`
- Keto Read API: `http://ory-keto-read.ory-system.svc.cluster.local:4466`

**Configuration:**
```python
HYDRA_ADMIN_URL = "http://ory-hydra-admin.ory-system.svc.cluster.local:4445"
KRATOS_ADMIN_URL = "http://ory-kratos-admin.ory-system.svc.cluster.local:4434"
KETO_READ_URL = "http://ory-keto-read.ory-system.svc.cluster.local:4466"
```

### 6. AgentGateway

**Technology:** Go service

**Namespace:** `api-gateway`

**Responsibilities:**
- Expose OAuth resource metadata
- Validate JWTs using JWKS from Hydra
- Cache JWKS with 1-hour TTL, refresh on key-id mismatch
- Forward authenticated requests to backend MCP servers

**Endpoints:**
- `GET /.well-known/oauth-protected-resource` - Resource metadata
- `POST /mcp/*` - MCP tool endpoints (JWT validation enforced)

**JWT Validation Flow:**
1. Extract JWT from `Authorization: Bearer <token>` header
2. Fetch JWKS from `http://ory-hydra-public.ory-system.svc.cluster.local:4444/.well-known/jwks.json`
3. Cache JWKS in memory (1-hour TTL)
4. Verify JWT signature using cached JWKS (RS256/ES256)
5. Verify `exp` claim > current time
6. On signature failure, refresh JWKS once and retry
7. On validation success, extract claims and forward to backend

**JWKS Caching Strategy:**
- Initial fetch on startup
- 1-hour in-memory cache
- Refresh on key-id mismatch (handles key rotation)
- 5-second timeout on JWKS fetch

**Configuration:**
```go
JWKS_URL = "http://ory-hydra-public.ory-system.svc.cluster.local:4444/.well-known/jwks.json"
JWKS_CACHE_TTL = 1h
JWKS_FETCH_TIMEOUT = 5s
```

## OAuth Client Pre-Registration

**Client ID:** `mcp-public-client`

**Registration Method:** identity-service calls Hydra Admin API on startup

**Client Specification:**
```json
{
  "client_id": "mcp-public-client",
  "client_name": "Zero-Ops MCP Client",
  "grant_types": ["authorization_code", "refresh_token"],
  "response_types": ["code"],
  "redirect_uris": [
    "http://127.0.0.1:54321/callback",
    "http://localhost:54321/callback",
    "http://127.0.0.1:18999/callback",
    "http://localhost:18999/callback",
    "http://127.0.0.1:3000/callback",
    "http://localhost:3000/callback",
    "cursor://anysphere.cursor-mcp/oauth/callback"
  ],
  "token_endpoint_auth_method": "none",
  "scope": "tenant:read tenant:write cluster:read cluster:write offline_access openid"
}
```

**Redirect URI Normalization:**
- identity-service normalizes `localhost` ↔ `127.0.0.1` during authorization
- Hydra validates exact match after normalization

## PKCE Flow Sequence

### Step 1: Metadata Discovery

**Trigger:** Cursor receives HTTP 401 from AgentGateway

**Request:**
```http
GET /.well-known/oauth-protected-resource HTTP/1.1
Host: api.zero-ops.io
```

**Response (AgentGateway):**
```json
{
  "resource": "https://api.zero-ops.io",
  "authorization_servers": ["https://auth.zero-ops.io"],
  "bearer_methods_supported": ["header"],
  "scopes_supported": ["tenant:read", "tenant:write", "cluster:read", "cluster:write"]
}
```

### Step 2: Authorization Server Metadata

**Request:**
```http
GET /.well-known/oauth-authorization-server HTTP/1.1
Host: auth.zero-ops.io
```

**Response (identity-service proxying Hydra):**
```json
{
  "issuer": "https://auth.zero-ops.io",
  "authorization_endpoint": "https://auth.zero-ops.io/oauth2/auth",
  "token_endpoint": "https://auth.zero-ops.io/oauth2/token",
  "jwks_uri": "https://auth.zero-ops.io/.well-known/jwks.json",
  "response_types_supported": ["code"],
  "grant_types_supported": ["authorization_code", "refresh_token"],
  "code_challenge_methods_supported": ["S256"],
  "token_endpoint_auth_methods_supported": ["none"]
}
```

### Step 3: Authorization Request

**Cursor Actions:**
1. Generate `code_verifier`: 43-128 chars, base64url-encoded random string
2. Compute `code_challenge = BASE64URL(SHA256(code_verifier))`
3. Generate `state`: 32-byte hex-encoded random string
4. Bind loopback listener on first available port: 54321 → 18999 → 3000
5. Open system browser to authorization endpoint

**Browser URL:**
```
https://auth.zero-ops.io/oauth2/auth?
  client_id=mcp-public-client&
  response_type=code&
  redirect_uri=http://127.0.0.1:54321/callback&
  code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&
  code_challenge_method=S256&
  state=af0ifjsldkj&
  scope=tenant:read+tenant:write+cluster:read+cluster:write+offline_access+openid&
  resource=https://api.zero-ops.io
```

### Step 4: User Authentication

**Flow:**
1. Hydra redirects to identity-service login endpoint
2. identity-service displays login form
3. User enters email/password
4. identity-service validates credentials via Kratos
5. identity-service calls Hydra `acceptOAuth2LoginRequest` with `subject`
6. Hydra redirects to identity-service consent endpoint

### Step 5: Consent

**Flow:**
1. identity-service displays consent screen (optional, can be skipped)
2. User approves scopes
3. identity-service fetches identity traits from Kratos
4. identity-service calls Hydra `acceptOAuth2ConsentRequest` with session:
   ```json
   {
     "grant_scope": ["tenant:read", "tenant:write", "cluster:read", "cluster:write", "offline_access", "openid"],
     "grant_access_token_audience": ["https://api.zero-ops.io"],
     "session": {
       "id_token": {
         "email": "admin@acme.com",
         "tenant_id": "acme-corp",
         "role": "tenant_admin"
       },
       "access_token": {
         "tenant_id": "acme-corp"
       }
     }
   }
   ```
5. Hydra redirects to callback URL with authorization code

### Step 6: Token Exchange

**Callback URL:**
```
http://127.0.0.1:54321/callback?code=ory_ac_...&state=af0ifjsldkj
```

**Cursor Actions:**
1. Validate `state` matches original request
2. Exchange authorization code for tokens

**Token Request:**
```http
POST /oauth2/token HTTP/1.1
Host: auth.zero-ops.io
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code&
code=ory_ac_...&
redirect_uri=http://127.0.0.1:54321/callback&
code_verifier=dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk&
client_id=mcp-public-client&
resource=https://api.zero-ops.io
```

**Token Response:**
```json
{
  "access_token": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9...",
  "token_type": "Bearer",
  "expires_in": 86400,
  "refresh_token": "ory_rt_...",
  "scope": "tenant:read tenant:write cluster:read cluster:write offline_access openid",
  "id_token": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9..."
}
```

**JWT Claims (access_token):**
```json
{
  "iss": "https://auth.zero-ops.io",
  "sub": "user-uuid",
  "aud": ["https://api.zero-ops.io"],
  "exp": 1710000000,
  "iat": 1709913600,
  "scope": "tenant:read tenant:write cluster:read cluster:write offline_access openid",
  "tenant_id": "acme-corp",
  "email": "admin@acme.com",
  "role": "tenant_admin"
}
```

### Step 7: Token Storage

**Cursor Actions:**
1. Store `access_token`, `refresh_token`, `expires_in` in OS keychain
2. Close browser
3. Retry original MCP tool call with `Authorization: Bearer <access_token>` header

## Deployment Artifacts

### ArgoCD Applications

**Parent Application:** `platform-identity`

**Child Applications:**
1. `ory-hydra` - Helm chart deployment
2. `ory-kratos` - Helm chart deployment
3. `ory-keto` - Helm chart deployment
4. `identity-postgres` - CNPG Cluster CR
5. `identity-service` - Kustomize deployment
6. `agentgateway` - Kustomize deployment

**No sync-wave dependencies** - services retry until dependencies ready

### Secrets Management

**Database Passwords:**
- Stored as Kubernetes Secrets in `ory-system` namespace
- Referenced by Ory Helm charts via `existingSecret`

**Example:**
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: identity-postgres-passwords
  namespace: ory-system
type: Opaque
data:
  hydra-password: <base64>
  kratos-password: <base64>
  keto-password: <base64>
```

## Error Handling

### JWT Validation Failures

**Scenario 1: Missing JWT**
- AgentGateway returns HTTP 401
- `WWW-Authenticate: Bearer realm="api.zero-ops.io", resource_metadata="https://api.zero-ops.io/.well-known/oauth-protected-resource"`
- Cursor initiates PKCE flow

**Scenario 2: Expired JWT**
- AgentGateway returns HTTP 401
- `error="invalid_token", error_description="Token expired"`
- Cursor attempts token refresh (out of scope for Demo 1)

**Scenario 3: Invalid Signature**
- AgentGateway refreshes JWKS once
- Retries validation
- If still fails, returns HTTP 401

### PKCE Flow Failures

**Scenario 1: All Ports Occupied**
- Cursor displays: "Authentication failed - ports 54321, 18999, 3000 are all in use. Close conflicting applications and retry."

**Scenario 2: Authorization Code Expired**
- Hydra returns `error="invalid_grant"`
- Cursor restarts PKCE flow

**Scenario 3: State Mismatch**
- Cursor detects CSRF attack
- Aborts flow, displays error

## Testing Strategy

### Unit Tests

**AgentGateway:**
- JWT signature validation with valid/invalid keys
- JWKS cache hit/miss scenarios
- Key-id mismatch triggers refresh
- Expired token detection

**identity-service:**
- OAuth metadata proxy correctness
- Client registration idempotency
- Consent session claim injection

### Integration Tests

**PKCE Flow:**
- End-to-end authorization code flow
- Token exchange with valid code_verifier
- Invalid code_verifier rejection
- State parameter validation

**JWKS Caching:**
- Initial fetch on startup
- Cache expiry after 1 hour
- Refresh on key rotation

### Demo Validation

**Success Criteria:**
1. Cursor sends MCP tool call without JWT → receives 401
2. Cursor discovers OAuth endpoints via metadata
3. Browser opens to Hydra authorization endpoint
4. User logs in via Kratos
5. User approves consent (or skipped for trusted client)
6. Browser redirects to callback URL
7. Cursor exchanges code for tokens
8. Cursor stores tokens in OS keychain
9. Cursor retries MCP tool call with JWT → succeeds

**Demo Script:**
"I type 'create tenant acme' in Cursor. The browser opens Kratos login. I log in. The browser closes. Cursor says it's authenticated."

## Security Considerations

### PKCE Protection
- Prevents authorization code interception attacks
- code_verifier never transmitted over network
- code_challenge binds authorization request to token exchange

### JWT Validation
- Signature verification using JWKS
- Expiry check prevents replay attacks
- Audience claim validation ensures token intended for this resource

### Redirect URI Validation
- Exact match required (after normalization)
- Loopback addresses only (no wildcards)
- Custom schemes for IDE compatibility

### JWKS Caching
- Reduces load on Hydra
- Handles key rotation gracefully
- Timeout prevents SSRF attacks

## Performance Characteristics

### Latency Targets

**PKCE Flow (one-time):**
- Metadata discovery: <100ms
- Authorization request: <200ms (browser open)
- Token exchange: <300ms
- Total: ~5-10 seconds (user interaction time)

**JWT Validation (per request):**
- Cache hit: <1ms
- Cache miss: <50ms (JWKS fetch)
- Key rotation: <100ms (refresh + retry)

### Scalability

**AgentGateway:**
- Stateless JWT validation
- In-memory JWKS cache (no external dependencies)
- Horizontally scalable

**Hydra:**
- PostgreSQL-backed (CNPG 3-node cluster)
- Supports 1000+ req/s per instance

**identity-service:**
- Stateless (no session storage)
- Horizontally scalable

## Monitoring and Observability

### Metrics

**AgentGateway:**
- `jwt_validation_total{result="success|failure"}`
- `jwks_cache_hit_total`
- `jwks_cache_miss_total`
- `jwks_refresh_total{result="success|failure"}`

**identity-service:**
- `oauth_login_total{result="success|failure"}`
- `oauth_consent_total{result="accept|reject"}`
- `hydra_api_call_duration_seconds{endpoint}`

**Hydra:**
- `hydra_oauth2_token_issued_total{grant_type}`
- `hydra_oauth2_token_validation_total{result}`

### Logs

**Structured Logging (JSON):**
- AgentGateway: JWT validation failures, JWKS refresh events
- identity-service: Login attempts, consent decisions, Hydra API errors
- Hydra: Authorization requests, token issuance, client errors

### Alerts

**Critical:**
- JWKS fetch failures > 5 in 5 minutes
- JWT validation failure rate > 10%
- Hydra database connection failures

**Warning:**
- JWKS cache miss rate > 50%
- Token exchange latency > 500ms

## Rollout Plan

### Phase 1: Infrastructure (Day 1 Morning)
1. Deploy CNPG cluster
2. Create Database CRDs
3. Deploy Ory Helm charts
4. Verify database connectivity

### Phase 2: Services (Day 1 Afternoon)
1. Deploy identity-service
2. Pre-register `mcp-public-client`
3. Deploy AgentGateway
4. Verify OAuth metadata endpoints

### Phase 3: Integration (Day 1 Evening)
1. Configure Cursor with AgentGateway URL
2. Test PKCE flow end-to-end
3. Verify JWT validation
4. Validate token storage in keychain

### Phase 4: Demo (Day 2 Morning)
1. Run demo script with stakeholders
2. Show browser redirect
3. Show JWT in keychain
4. Show authenticated API call

## Future Enhancements (Out of Scope)

1. **Token Refresh (Req 14):** Transparent refresh on 401
2. **CIMD Support (Req 13):** Dynamic client metadata documents
3. **Authorization Enforcement (Req 3):** Keto permission checks
4. **Multi-Factor Authentication:** TOTP, WebAuthn via Kratos
5. **Social Login:** Google, GitHub via Kratos OIDC providers
