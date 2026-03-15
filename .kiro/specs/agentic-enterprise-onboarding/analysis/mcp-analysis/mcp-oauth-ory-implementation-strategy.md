# MCP OAuth Implementation Strategy with Ory Stack

**Version:** 1.0  
**Status:** DRAFT  
**Date:** March 11, 2026  
**Author:** Analysis based on systemprompt-mcp-server, http-oauth-mcp-server, and Ory stack codebases

---

## Executive Summary

This document provides a comprehensive implementation strategy for integrating MCP OAuth 2.1 with PKCE into the zero-ops platform using the Ory stack (Hydra + Kratos + Keto). The design uses Authorization Code Flow with PKCE (RFC 7636) for secure authentication without client secrets, ensures credentials never transit through AI agents, implements browser-based authentication with automatic redirect, and provides backend token exchange for secure API access.

**Key Architecture Decisions:**
- **OAuth Flow:** Authorization Code + PKCE (RFC 7636) per MCP Specification 2025-11-25
- **Client Registration:** CIMD (Client ID Metadata Documents) primary, DCR fallback
- **Redirect URIs:** Fixed localhost ports (54321, 18999, 3000), NOT ephemeral
- **Normalization:** Support both localhost AND 127.0.0.1 (expand during registration)
- Ory Hydra replaces custom OAuth server
- Ory Kratos handles user authentication and identity management
- Ory Keto provides fine-grained authorization/RBAC for tenant isolation
- zero-ops-api integrates as OAuth-protected resource server
- MCP clients remain public clients (no secrets) with PKCE security
- JWT tokens contain tenant context for multi-tenancy enforcement
- Custom URI schemes (cursor://, goose://) optional for IDE compatibility

---

## 1. Architecture Overview

### 1.1 Component Mapping

| MCP Reference Pattern | Ory Stack Component | Responsibility |
|----------------------|---------------------|----------------|
| Custom OAuth Server (systemprompt) | **Ory Hydra** | OAuth 2.1 authorization server, token issuance, PKCE validation |
| Reddit OAuth Integration | **Ory Kratos** | User authentication, identity management, session handling |
| JWT Token Generation | **Ory Hydra** | JWT access tokens with custom claims (tenant_id, user_id) |
| Session Management | **Ory Kratos Sessions** | Browser-based authentication state |
| Authorization Logic | **Ory Keto** | Tenant-scoped RBAC, permission checks |
| Auth Enforcement | **AgentGateway** | Single auth enforcement point, validates JWTs, enforces RBAC |
| Ory Stack Interface | **identity-service (Python)** | Proxy layer between AgentGateway and Ory stack (Hydra, Kratos, Keto) |
| MCP Server (zero-ops-api) | **zero-ops-api** | Backend MCP server, receives pre-authenticated requests with user context headers |
| Agent Gateway | **Rust Gateway** | Token validation, rate limiting, request routing to MCP servers |


### 1.2 High-Level Flow Diagram

```
┌─────────────────┐
│   MCP Client    │  (Cursor/Goose - Public Client, No Secrets)
│   (Desktop App) │
└────────┬────────┘
         │ 1. POST /mcp (no auth)
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Rust Gateway                              │
│  • Validates Bearer tokens (Hydra introspection)             │
│  • Rate limiting (per tenant_id from JWT)                    │
│  • Routes to zero-ops-api                                    │
└────────┬────────────────────────────────────────────────────┘
         │ 2. 401 Unauthorized + WWW-Authenticate header
         │    WWW-Authenticate: Bearer realm="MCP",
         │    resource_metadata="https://api.nutgraf.in/.well-known/oauth-protected-resource"
         ▼
┌─────────────────┐
│   MCP Client    │  3. Discovers OAuth endpoints via metadata
└────────┬────────┘
         │ 4. Generates PKCE code_verifier + code_challenge (S256)
         │ 5. Starts local HTTP server on 127.0.0.1:54321 (FIXED PORT per MCP spec)
         │ 6. GET /oauth/authorize?client_id=https://cursor.com/.well-known/client-metadata.json&code_challenge=...&redirect_uri=http://127.0.0.1:54321/callback
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Ory Hydra                                 │
│  • Validates PKCE parameters                                 │
│  • Redirects to Kratos login UI                              │
└────────┬────────────────────────────────────────────────────┘
         │ 6. Redirect to Kratos login
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Ory Kratos                                │
│  • Browser-based authentication (email/password, SSO, etc.)  │
│  • User enters credentials in BROWSER (never in agent)       │
│  • Creates Kratos session                                    │
└────────┬────────────────────────────────────────────────────┘
         │ 7. Redirect back to Hydra with session cookie
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Ory Hydra                                 │
│  • Validates Kratos session                                  │
│  • Shows consent screen (optional, can be skipped)           │
│  • Generates authorization code                              │
│  • Redirects to loopback: http://127.0.0.1:{port}/callback?code=...&state=... │
└────────┬────────────────────────────────────────────────────┘
         │ 8. Authorization code returned to client
         ▼
┌─────────────────┐
│   MCP Client    │  9. POST /oauth/token
│                 │     grant_type=authorization_code
│                 │     code=...
│                 │     code_verifier=... (PKCE verification)
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Ory Hydra                                 │
│  • Verifies PKCE: SHA256(code_verifier) == code_challenge    │
│  • Issues JWT access token with custom claims:               │
│    {                                                          │
│      "sub": "user-uuid",                                      │
│      "tenant_id": "tenant-uuid",                              │
│      "email": "user@example.com",                             │
│      "aud": "zero-ops-api",                                   │
│      "scope": "tenant:read tenant:write"                      │
│    }                                                          │
│  • Returns access_token + refresh_token                       │
└────────┬────────────────────────────────────────────────────┘
         │ 10. JWT access token
         ▼
┌─────────────────┐
│   MCP Client    │  11. POST /mcp
│                 │      Authorization: Bearer <jwt_token>
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Rust Gateway                              │
│  • Validates JWT signature (Hydra JWKS endpoint)             │
│  • Extracts tenant_id from JWT claims                        │
│  • Checks Keto for tenant permissions                        │
│  • Forwards to zero-ops-api with tenant context              │
└────────┬────────────────────────────────────────────────────┘
         │ 12. Authenticated request with tenant_id
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    zero-ops-api                              │
│  • Reads tenant_id from request context                      │
│  • Enforces tenant isolation in database queries             │
│  • Returns tenant-scoped data                                │
└─────────────────────────────────────────────────────────────┘
```



---

## 2. Ory Hydra Configuration

### 2.1 Hydra as OAuth 2.1 Authorization Server

Ory Hydra replaces the custom OAuth server from systemprompt-mcp-server. It provides production-grade OAuth 2.1 with PKCE support out of the box.

**Hydra Configuration (`hydra.yml`):**
```yaml
serve:
  public:
    port: 4444
    host: 0.0.0.0
  admin:
    port: 4445
    host: 0.0.0.0

urls:
  self:
    issuer: https://auth.nutgraf.in
  consent: https://auth.nutgraf.in/consent
  login: https://auth.nutgraf.in/login  # Kratos login UI
  logout: https://auth.nutgraf.in/logout

strategies:
  access_token: jwt  # Use JWT tokens (not opaque)
  scope: exact

ttl:
  access_token: 24h  # 86400 seconds - covers 15-min provisioning + buffer
  refresh_token: 720h  # 30 days - enables long-running workflows
  id_token: 1h
  auth_code: 10m  # Authorization Code Flow: code expires in 10 minutes

oauth2:
  pkce:
    enforced: true  # CRITICAL: Enforce PKCE for all public clients
    enforced_for_public_clients: true
  
  expose_internal_errors: false  # Security: hide internal errors in production

secrets:
  system:
    - "CHANGE_ME_PRODUCTION_SECRET_32_CHARS"  # Used for signing tokens

dsn: postgres://hydra:password@postgres:5432/hydra?sslmode=disable

oidc:
  subject_identifiers:
    supported_types:
      - public
    pairwise:
      salt: "CHANGE_ME_SALT"
```

**Key Differences from systemprompt-mcp-server:**
- Hydra handles PKCE validation natively (no custom `generateCodeChallenge` function needed)
- JWT signing managed by Hydra (no manual `jose` library usage)
- Authorization code storage handled by Hydra's database (no in-memory `Map`)
- Refresh token rotation supported out of the box



### 2.2 Client Registration (CIMD Primary, DCR Fallback)

**CRITICAL:** Per MCP Specification 2025-11-25, Client ID Metadata Documents (CIMD) is the PRIMARY registration method. Dynamic Client Registration (DCR) is FALLBACK only.

**CIMD Registration (Primary):**
```json
{
  "client_id": "https://cursor.com/.well-known/client-metadata.json",
  "client_name": "Cursor IDE",
  "client_uri": "https://cursor.com",
  "logo_uri": "https://cursor.com/logo.png",
  "redirect_uris": [
    "http://127.0.0.1:54321/callback",
    "http://localhost:54321/callback",
    "cursor://anysphere.cursor-mcp/oauth/callback"
  ],
  "grant_types": ["authorization_code", "refresh_token"],
  "response_types": ["code"],
  "token_endpoint_auth_method": "none"
}
```

**Hydra Configuration for CIMD:**
```yaml
# hydra.yml
oauth2:
  client_id_metadata_document:
    enabled: true  # Enable CIMD support
    cache_ttl: 3600  # Cache metadata for 1 hour
    fetch_timeout: 5s  # Timeout for fetching metadata
    
  # SSRF protections
  allowed_schemes: ["https"]  # Only HTTPS for metadata URLs
  blocked_networks:
    - "10.0.0.0/8"
    - "172.16.0.0/12"
    - "192.168.0.0/16"
    - "127.0.0.0/8"
```

**DCR Registration (Fallback):**
```bash
# DCR Fallback - Register MCP public client via Admin API
curl -X POST https://auth.nutgraf.in/admin/clients \
  -H "Content-Type: application/json" \
  -d '{
    "client_id": "mcp-public-client",
    "client_name": "Zero-Ops MCP Client",
    "grant_types": ["authorization_code", "refresh_token"],
    "response_types": ["code"],
    "redirect_uris": [
      "http://127.0.0.1:54321/callback",
      "http://localhost:54321/callback"
    ],
    "token_endpoint_auth_method": "none",
    "application_type": "native",
    "subject_type": "public",
    "scope": "tenant:read tenant:write offline_access"
  }'
```

**Redirect URI Normalization (Critical):**
```go
// internal/hydra/redirect_uri.go
func NormalizeRedirectURIs(uris []string) []string {
    normalized := make([]string, 0, len(uris)*2)
    
    for _, uri := range uris {
        parsed, _ := url.Parse(uri)
        
        // Add original URI
        normalized = append(normalized, uri)
        
        // Expand localhost ↔ 127.0.0.1
        if parsed.Hostname() == "localhost" {
            normalized = append(normalized, 
                strings.Replace(uri, "localhost", "127.0.0.1", 1))
        } else if parsed.Hostname() == "127.0.0.1" {
            normalized = append(normalized, 
                strings.Replace(uri, "127.0.0.1", "localhost", 1))
        }
    }
    
    return normalized
}
```

**Comparison with MCP Specification 2025-11-25:**
| Feature | MCP Spec 2025-11-25 | Ory Hydra |
|---------|---------------------|-----------|
| Client Registration | CIMD (SHOULD), DCR (MAY) | CIMD via config, DCR via Admin API |
| Client Storage | N/A | PostgreSQL (persistent) |
| Redirect URI Validation | localhost OR HTTPS | Built-in validation + normalization |
| PKCE Enforcement | Required for public clients | Automatic enforcement via config |
| Fixed Ports | 54321, 18999, 3000 | Supported (no wildcard needed) |



### 2.3 Custom JWT Claims (Tenant Context)

Hydra supports custom JWT claims via login/consent hooks. This replaces the `createAccessToken` function from systemprompt-mcp-server.

**Login Hook Implementation (Go service):**
```go
// internal/hydra/login_hook.go
package hydra

import (
    "context"
    "encoding/json"
    "net/http"
    
    "github.com/google/uuid"
    client "github.com/ory/hydra-client-go/v2"
)

type LoginHookHandler struct {
    hydraAdmin *client.APIClient
    tenantRepo TenantRepository
}

// AcceptLoginRequest handles Hydra login challenge
func (h *LoginHookHandler) AcceptLoginRequest(w http.ResponseWriter, r *http.Request) {
    challenge := r.URL.Query().Get("login_challenge")
    
    // Get user from Kratos session (via cookie)
    kratosSession, err := h.getKratosSession(r)
    if err != nil {
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return
    }
    
    userID := kratosSession.Identity.ID
    email := kratosSession.Identity.Traits["email"].(string)
    
    // Lookup tenant for user
    tenant, err := h.tenantRepo.GetTenantByUserID(context.Background(), userID)
    if err != nil {
        http.Error(w, "Tenant not found", http.StatusForbidden)
        return
    }
    
    // Accept login with custom claims
    acceptReq := client.NewAcceptOAuth2LoginRequest(userID)
    acceptReq.SetContext(map[string]interface{}{
        "tenant_id": tenant.ID.String(),
        "email":     email,
        "org_id":    tenant.OrgID.String(),
    })
    
    resp, _, err := h.hydraAdmin.OAuth2API.AcceptOAuth2LoginRequest(context.Background()).
        LoginChallenge(challenge).
        AcceptOAuth2LoginRequest(*acceptReq).
        Execute()
    
    if err != nil {
        http.Error(w, "Failed to accept login", http.StatusInternalServerError)
        return
    }
    
    // Redirect to Hydra
    http.Redirect(w, r, resp.RedirectTo, http.StatusFound)
}
```

**Resulting JWT Token:**
```json
{
  "iss": "https://auth.nutgraf.in",
  "sub": "550e8400-e29b-41d4-a716-446655440000",
  "aud": ["zero-ops-api"],
  "exp": 1710259200,
  "iat": 1710172800,
  "scope": "tenant:read tenant:write cluster:read cluster:write",
  "tenant_id": "acme-corp-uuid",
  "email": "admin@acme.com",
  "org_id": "org-uuid"
}
```

**Token Response (Device Flow):**
```json
{
  "access_token": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9...",
  "token_type": "Bearer",
  "expires_in": 86400,
  "refresh_token": "ory_rt_abc123...",
  "scope": "tenant:read tenant:write cluster:read cluster:write offline_access"
}
```

**Comparison with systemprompt-mcp-server:**
| Feature | systemprompt-mcp-server | Ory Hydra |
|---------|-------------------------|-----------|
| Custom Claims | Manual `SignJWT` with `reddit_access_token` | Login hook with `context` field |
| Token Signing | Manual `jose` library | Hydra's built-in JWT signing |
| Token Storage | In-memory refresh tokens | PostgreSQL-backed refresh tokens |



---

## 3. Ory Kratos Integration

### 3.1 Kratos as Identity Provider

Ory Kratos replaces the Reddit OAuth integration from systemprompt-mcp-server. It provides user authentication, identity management, and session handling.

**Kratos Configuration (`kratos.yml`):**
```yaml
version: v1.0.0

dsn: postgres://kratos:password@postgres:5432/kratos?sslmode=disable

serve:
  public:
    base_url: https://auth.nutgraf.in
    port: 4433
  admin:
    base_url: http://kratos:4434
    port: 4434

selfservice:
  default_browser_return_url: https://console.nutgraf.in/
  allowed_return_urls:
    - https://console.nutgraf.in
    - https://auth.nutgraf.in
  
  flows:
    login:
      ui_url: https://auth.nutgraf.in/login
      lifespan: 10m
      after:
        default_browser_return_url: https://console.nutgraf.in/dashboard
    
    registration:
      ui_url: https://auth.nutgraf.in/registration
      lifespan: 10m
      after:
        default_browser_return_url: https://console.nutgraf.in/onboarding
    
    logout:
      after:
        default_browser_return_url: https://auth.nutgraf.in/login

identity:
  default_schema_id: default
  schemas:
    - id: default
      url: file:///etc/config/kratos/identity.schema.json

session:
  lifespan: 24h
  cookie:
    domain: nutgraf.in
    same_site: Lax
    secure: true
    http_only: true

courier:
  smtp:
    connection_uri: smtps://smtp.sendgrid.net:465/?username=apikey&password=YOUR_SENDGRID_API_KEY
```

**Identity Schema (`identity.schema.json`):**
```json
{
  "$id": "https://schemas.nutgraf.in/identity.schema.json",
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "Zero-Ops User",
  "type": "object",
  "properties": {
    "traits": {
      "type": "object",
      "properties": {
        "email": {
          "type": "string",
          "format": "email",
          "title": "Email",
          "ory.sh/kratos": {
            "credentials": {
              "password": {
                "identifier": true
              }
            },
            "verification": {
              "via": "email"
            },
            "recovery": {
              "via": "email"
            }
          }
        },
        "name": {
          "type": "string",
          "title": "Full Name"
        },
        "tenant_id": {
          "type": "string",
          "format": "uuid",
          "title": "Tenant ID"
        }
      },
      "required": ["email"],
      "additionalProperties": false
    }
  }
}
```



### 3.2 Kratos Login Flow Integration

**Comparison with systemprompt-mcp-server Reddit OAuth:**
| Step | systemprompt-mcp-server | Ory Kratos |
|------|-------------------------|------------|
| User Authentication | Redirect to Reddit OAuth | Redirect to Kratos login UI |
| Credential Entry | Reddit username/password | Email/password (or SSO) |
| Callback Handler | Custom `/oauth/reddit/callback` | Hydra handles Kratos session |
| Token Exchange | Fetch Reddit access/refresh tokens | Kratos session cookie |
| User Info | `GET https://oauth.reddit.com/api/v1/me` | Kratos identity traits |

**Kratos Session Validation in Login Hook:**
```go
func (h *LoginHookHandler) getKratosSession(r *http.Request) (*kratos.Session, error) {
    sessionCookie, err := r.Cookie("ory_kratos_session")
    if err != nil {
        return nil, fmt.Errorf("no session cookie: %w", err)
    }
    
    // Validate session with Kratos
    session, _, err := h.kratosClient.FrontendAPI.ToSession(context.Background()).
        Cookie(sessionCookie.String()).
        Execute()
    
    if err != nil {
        return nil, fmt.Errorf("invalid session: %w", err)
    }
    
    return session, nil
}
```

---

## 4. Ory Keto for Authorization

### 4.1 Tenant-Scoped RBAC

Ory Keto provides fine-grained authorization using Zanzibar-style relation tuples. This enables tenant isolation and role-based access control.

**Keto Configuration (`keto.yml`):**
```yaml
version: v0.11.0

dsn: postgres://keto:password@postgres:5432/keto?sslmode=disable

serve:
  read:
    port: 4466
  write:
    port: 4467

namespaces:
  - id: 0
    name: tenants
  - id: 1
    name: clusters
```

**Permission Model (`keto_namespaces.ts`):**
```typescript
// Tenant namespace
class Tenant implements Namespace {
  related: {
    admins: User[]
    members: User[]
  }
  
  permits = {
    read: (ctx: Context): boolean => 
      this.related.admins.includes(ctx.subject) ||
      this.related.members.includes(ctx.subject),
    
    write: (ctx: Context): boolean =>
      this.related.admins.includes(ctx.subject),
    
    delete: (ctx: Context): boolean =>
      this.related.admins.includes(ctx.subject)
  }
}

// Cluster namespace (inherits tenant permissions)
class Cluster implements Namespace {
  related: {
    tenant: Tenant
  }
  
  permits = {
    read: (ctx: Context): boolean =>
      this.related.tenant.permits.read(ctx),
    
    write: (ctx: Context): boolean =>
      this.related.tenant.permits.write(ctx)
  }
}
```

**Creating Relation Tuples:**
```bash
# Grant user admin access to tenant
keto relation-tuple create \
  --namespace tenants \
  --object acme-corp-uuid \
  --relation admins \
  --subject-id user-uuid

# Link cluster to tenant
keto relation-tuple create \
  --namespace clusters \
  --object cluster-uuid \
  --relation tenant \
  --subject-set "tenants:acme-corp-uuid#members"
```



### 4.2 Gateway Authorization Check

**Rust Gateway Integration:**
```rust
// src/middleware/authorization.rs
use keto_client::{KetoClient, CheckRequest};

pub async fn check_tenant_permission(
    keto: &KetoClient,
    user_id: &str,
    tenant_id: &str,
    permission: &str,
) -> Result<bool, Error> {
    let check = CheckRequest {
        namespace: "tenants".to_string(),
        object: tenant_id.to_string(),
        relation: permission.to_string(),
        subject_id: Some(user_id.to_string()),
        ..Default::default()
    };
    
    let response = keto.check(&check).await?;
    Ok(response.allowed)
}

// Usage in request handler
pub async fn authorize_request(
    req: &Request,
    keto: &KetoClient,
) -> Result<(), StatusCode> {
    let claims = extract_jwt_claims(req)?;
    let tenant_id = claims.get("tenant_id").ok_or(StatusCode::FORBIDDEN)?;
    let user_id = claims.get("sub").ok_or(StatusCode::UNAUTHORIZED)?;
    
    let allowed = check_tenant_permission(
        keto,
        user_id.as_str(),
        tenant_id.as_str(),
        "write"
    ).await?;
    
    if !allowed {
        return Err(StatusCode::FORBIDDEN);
    }
    
    Ok(())
}
```

---

## 5. zero-ops-api Integration

### 5.1 OAuth-Protected Resource Metadata

**CRITICAL:** Metadata is exposed by AgentGateway, NOT by individual MCP servers.

**Metadata Endpoint (AgentGateway):**
```rust
// src/handlers/oauth_metadata.rs
pub async fn protected_resource_metadata() -> Json<ProtectedResourceMetadata> {
    Json(ProtectedResourceMetadata {
        resource: "https://api.nutgraf.in".to_string(),
        authorization_servers: vec!["https://auth.nutgraf.in".to_string()],
        bearer_methods_supported: vec!["header".to_string()],
        resource_documentation: Some("https://docs.nutgraf.in/api".to_string()),
        scopes_supported: vec![
            "tenant:read".to_string(),
            "tenant:write".to_string(),
            "cluster:read".to_string(),
            "cluster:write".to_string(),
        ],
    })
}
```

### 5.2 Token Validation Middleware

**CRITICAL:** Token validation happens ONLY in AgentGateway. Backend MCP servers (zero-ops-api) receive pre-authenticated requests and trust the headers.

**AgentGateway JWT Validation (Rust):**
```rust
// src/middleware/auth.rs
use jsonwebtoken::{decode, DecodingKey, Validation, Algorithm};

pub struct AuthMiddleware {
    jwks_cache: Arc<RwLock<JwksCache>>,
    identity_service_url: String,
}

impl AuthMiddleware {
    pub async fn validate_token(&self, token: &str) -> Result<Claims, AuthError> {
        // Extract kid from JWT header
        let header = jsonwebtoken::decode_header(token)?;
        let kid = header.kid.ok_or(AuthError::MissingKid)?;
        
        // Get JWKS from identity-service (cached)
        let jwks = self.get_jwks(&kid).await?;
        let decoding_key = DecodingKey::from_jwk(&jwks)?;
        
        // Validate JWT
        let mut validation = Validation::new(Algorithm::RS256);
        validation.set_audience(&["zero-ops-api"]);
        validation.set_issuer(&["https://auth.nutgraf.in"]);
        
        let token_data = decode::<Claims>(token, &decoding_key, &validation)?;
        Ok(token_data.claims)
    }
    
    async fn get_jwks(&self, kid: &str) -> Result<Jwk, AuthError> {
        // Check cache first
        if let Some(jwk) = self.jwks_cache.read().await.get(kid) {
            return Ok(jwk.clone());
        }
        
        // Fetch from identity-service
        let response = reqwest::get(format!("{}/jwks", self.identity_service_url)).await?;
        let jwks: JwkSet = response.json().await?;
        
        // Update cache
        let mut cache = self.jwks_cache.write().await;
        for jwk in jwks.keys {
            cache.insert(jwk.kid.clone(), jwk.clone());
        }
        
        cache.get(kid).cloned().ok_or(AuthError::KeyNotFound)
    }
}

pub async fn auth_middleware(
    req: Request,
    auth: &AuthMiddleware,
    identity_service: &IdentityServiceClient,
) -> Result<Request, StatusCode> {
    // Extract JWT
    let auth_header = req.headers().get("Authorization")
        .ok_or(StatusCode::UNAUTHORIZED)?;
    let token = auth_header.to_str()
        .map_err(|_| StatusCode::UNAUTHORIZED)?
        .strip_prefix("Bearer ")
        .ok_or(StatusCode::UNAUTHORIZED)?;
    
    // Validate JWT
    let claims = auth.validate_token(token).await
        .map_err(|_| StatusCode::UNAUTHORIZED)?;
    
    // Check permissions via identity-service (Keto)
    let allowed = identity_service.check_permission(
        &claims.sub,
        &claims.tenant_id,
        "tenant:write"
    ).await?;
    
    if !allowed {
        return Err(StatusCode::FORBIDDEN);
    }
    
    // Forward request with user context headers
    let mut req = req;
    req.headers_mut().insert("X-User-ID", claims.sub.parse().unwrap());
    req.headers_mut().insert("X-Tenant-ID", claims.tenant_id.parse().unwrap());
    req.headers_mut().insert("X-User-Email", claims.email.parse().unwrap());
    req.headers_mut().insert("X-Scopes", claims.scope.parse().unwrap());
    
    Ok(req)
}
```

**Backend MCP Server (zero-ops-api) - NO JWT Validation:**
```go
// internal/api/middleware/context.go
package middleware

import (
    "context"
    "github.com/gin-gonic/gin"
)

// ExtractUserContext reads user context from AgentGateway headers
// NO JWT validation - trusts AgentGateway
func ExtractUserContext() gin.HandlerFunc {
    return func(c *gin.Context) {
        userID := c.GetHeader("X-User-ID")
        tenantID := c.GetHeader("X-Tenant-ID")
        email := c.GetHeader("X-User-Email")
        scopes := c.GetHeader("X-Scopes")
        
        // Store in context for handlers
        c.Set("user_id", userID)
        c.Set("tenant_id", tenantID)
        c.Set("email", email)
        c.Set("scopes", scopes)
        
        c.Next()
    }
}
```



### 5.3 Tenant Isolation in Queries

**Enforcing Tenant Context:**
```go
// internal/service/tenant.go
func (s *TenantService) GetTenant(ctx context.Context, id uuid.UUID) (*db.Tenant, error) {
    // Extract tenant_id from JWT claims (set by middleware)
    tenantID := ctx.Value("tenant_id").(string)
    
    tenant, err := s.queries.GetTenant(ctx, id)
    if err != nil {
        return nil, err
    }
    
    // Enforce tenant isolation
    if tenant.ID.String() != tenantID {
        return nil, &APIError{
            HTTPStatus: http.StatusForbidden,
            Code:       "FORBIDDEN",
            Message:    "Access denied to tenant resource",
        }
    }
    
    return tenant, nil
}
```

---

## 6. MCP Client Flow (Cursor/Goose)

### 6.1 Client-Side PKCE Implementation

**Comparison with systemprompt-mcp-server client expectations:**
```typescript
// MCP Client (TypeScript/JavaScript)
import { randomBytes, createHash } from 'crypto';

// 1. Generate PKCE parameters
function generatePKCE() {
  const verifier = randomBytes(32).toString('base64url');
  const challenge = createHash('sha256')
    .update(verifier)
    .digest('base64url');
  
  return { verifier, challenge };
}

// 2. Discover OAuth endpoints
async function discoverOAuthEndpoints(resourceUrl: string) {
  const metadataUrl = `${resourceUrl}/.well-known/oauth-protected-resource`;
  const metadata = await fetch(metadataUrl).then(r => r.json());
  
  const authServerUrl = metadata.authorization_servers[0];
  const authServerMetadata = await fetch(
    `${authServerUrl}/.well-known/oauth-authorization-server`
  ).then(r => r.json());
  
  return {
    authorizationEndpoint: authServerMetadata.authorization_endpoint,
    tokenEndpoint: authServerMetadata.token_endpoint,
  };
}

// 3. Start authorization flow
async function authorize() {
  const { verifier, challenge } = generatePKCE();
  const { authorizationEndpoint } = await discoverOAuthEndpoints(
    'https://api.nutgraf.in'
  );
  
  const state = randomBytes(16).toString('hex');
  
  const authUrl = new URL(authorizationEndpoint);
  // Start local HTTP server on loopback interface
  const port = await startLocalServer(); // OS assigns ephemeral port
  
  authUrl.searchParams.set('client_id', 'mcp-public-client');
  authUrl.searchParams.set('response_type', 'code');
  authUrl.searchParams.set('redirect_uri', `http://127.0.0.1:${port}/callback`);
  authUrl.searchParams.set('code_challenge', challenge);
  authUrl.searchParams.set('code_challenge_method', 'S256');
  authUrl.searchParams.set('state', state);
  authUrl.searchParams.set('scope', 'tenant:read tenant:write offline_access');
  
  // Open browser for user authentication
  await openBrowser(authUrl.toString());
  
  // Wait for callback
  const { code, state: returnedState } = await waitForCallback();
  
  if (state !== returnedState) {
    throw new Error('State mismatch');
  }
  
  return { code, verifier };
}

// 4. Exchange code for token
async function exchangeToken(code: string, verifier: string) {
  const { tokenEndpoint } = await discoverOAuthEndpoints(
    'https://api.nutgraf.in'
  );
  
  const response = await fetch(tokenEndpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'authorization_code',
      code,
      redirect_uri: `http://127.0.0.1:${port}/callback`, // MUST match authorization request
      code_verifier: verifier,
      client_id: 'mcp-public-client',
    }),
  });
  
  const tokens = await response.json();
  return tokens; // { access_token, refresh_token, expires_in }
}
```



---

## 7. Security Patterns

### 7.1 Credentials Never Transit Agent

**Critical Security Principle (validated across all reference implementations):**

| Scenario | Correct Pattern | Anti-Pattern (FORBIDDEN) |
|----------|----------------|--------------------------|
| User Authentication | Browser-based Kratos login UI | Agent prompts for password |
| Cloud Provider Credentials | Console UI → backend encryption → S3 | Agent collects API token |
| OAuth Tokens | Hydra issues JWT to client | Agent stores/forwards tokens |
| Tenant Secrets | Backend encryption with Age | Agent handles plaintext secrets |

**Implementation in zero-ops-api:**
```go
// POST /api/v1/tenants/:id/credentials
func (h *TenantHandler) InitiateCredentialUpload(c *gin.Context) {
    tenantID := c.Param("id")
    
    // Generate one-time upload URL (expires in 10 minutes)
    uploadToken := generateSecureToken()
    uploadURL := fmt.Sprintf("https://console.nutgraf.in/credentials/upload?token=%s", uploadToken)
    
    // Store token with tenant association
    h.cache.Set(uploadToken, tenantID, 10*time.Minute)
    
    // Return URL to agent (agent NEVER handles credentials)
    c.JSON(http.StatusOK, gin.H{
        "upload_url": uploadURL,
        "expires_in": 600,
        "instructions": "User must enter credentials in browser at this URL",
    })
}

// Agent polls for completion
// GET /api/v1/tenants/:id/credentials/status
func (h *TenantHandler) GetCredentialStatus(c *gin.Context) {
    tenantID := c.Param("id")
    
    status, err := h.credentialRepo.GetStatus(c.Request.Context(), tenantID)
    if err != nil {
        c.JSON(http.StatusOK, gin.H{"status": "pending"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "status": "ready",
        "encrypted_at": status.EncryptedAt,
    })
}
```

### 7.2 PKCE Security

**Why PKCE is Critical for Public Clients:**
- MCP clients (Cursor, Goose) cannot securely store client secrets
- PKCE prevents authorization code interception attacks
- Code verifier never leaves client, only SHA256 hash transmitted

**Hydra PKCE Enforcement:**
```yaml
# hydra.yml
oauth2:
  pkce:
    enforced: true  # Reject any authorization without code_challenge
    enforced_for_public_clients: true
```

### 7.3 Token Validation Performance

**Gateway Token Validation (< 50ms requirement):**
```rust
// src/middleware/jwt_validation.rs
use jsonwebtoken::{decode, DecodingKey, Validation, Algorithm};
use lru::LruCache;

pub struct JWTValidator {
    jwks_cache: Arc<RwLock<LruCache<String, DecodingKey>>>,
    validation: Validation,
}

impl JWTValidator {
    pub fn new(issuer: &str) -> Self {
        let mut validation = Validation::new(Algorithm::RS256);
        validation.set_issuer(&[issuer]);
        validation.set_audience(&["zero-ops-api"]);
        
        Self {
            jwks_cache: Arc::new(RwLock::new(LruCache::new(100))),
            validation,
        }
    }
    
    pub async fn validate(&self, token: &str) -> Result<Claims, Error> {
        // Extract kid from JWT header
        let header = jsonwebtoken::decode_header(token)?;
        let kid = header.kid.ok_or(Error::MissingKid)?;
        
        // Check cache first
        let key = {
            let cache = self.jwks_cache.read().await;
            cache.peek(&kid).cloned()
        };
        
        let decoding_key = match key {
            Some(k) => k,
            None => {
                // Fetch from JWKS endpoint
                let key = self.fetch_jwks_key(&kid).await?;
                let mut cache = self.jwks_cache.write().await;
                cache.put(kid.clone(), key.clone());
                key
            }
        };
        
        // Validate token (< 1ms with cached key)
        let token_data = decode::<Claims>(token, &decoding_key, &self.validation)?;
        Ok(token_data.claims)
    }
}
```



---

## 8. Implementation Phases

### Phase 1: Ory Stack Deployment (Week 1-2)
- [ ] Deploy Ory Hydra with PostgreSQL backend
- [ ] Deploy Ory Kratos with email/password authentication
- [ ] Deploy Ory Keto with tenant namespace configuration
- [ ] Configure Hydra for JWT tokens with custom claims
- [ ] Implement login/consent hooks for tenant context injection
- [ ] Create Kratos identity schema with tenant_id trait

### Phase 2: zero-ops-api OAuth Integration (Week 3)
- [ ] Add OAuth metadata endpoints (`.well-known/oauth-protected-resource`)
- [ ] Implement JWT validation middleware using Hydra JWKS
- [ ] Add tenant isolation enforcement in service layer
- [ ] Update all CRUD endpoints to require Bearer token
- [ ] Implement credential upload flow (console UI, not agent)
- [ ] Add polling endpoint for credential status

### Phase 3: Rust Gateway Enhancement (Week 4)
- [ ] Integrate JWT validation with JWKS caching
- [ ] Add Keto permission checks for tenant isolation
- [ ] Implement rate limiting per tenant_id from JWT
- [ ] Add metrics for token validation latency (target < 50ms)
- [ ] Implement token refresh handling

### Phase 4: MCP Client Testing (Week 5)
- [ ] Test PKCE flow with Cursor/Goose clients
- [ ] Validate browser-based authentication (credentials never in agent)
- [ ] Test token refresh flow
- [ ] Verify tenant isolation (user A cannot access tenant B resources)
- [ ] Load testing: 1000 concurrent authenticated requests

### Phase 5: Production Hardening (Week 6)
- [ ] Enable Hydra token rotation
- [ ] Configure Kratos account recovery flows
- [ ] Set up Keto relation tuple backups
- [ ] Implement audit logging for all OAuth events
- [ ] Security review: penetration testing, OWASP compliance

---

## 9. Comparison Table: Reference Implementations vs Ory Stack

| Feature | systemprompt-mcp-server | http-oauth-mcp-server | Ory Stack |
|---------|-------------------------|----------------------|-----------|
| **OAuth Server** | Custom Express.js | MCP SDK OAuth Proxy | Ory Hydra (production-grade) |
| **PKCE Support** | Manual implementation | MCP SDK built-in | Native Hydra support |
| **Token Type** | JWT (manual signing) | Opaque or JWT | JWT with custom claims |
| **User Auth** | Reddit OAuth | Auth0 integration | Ory Kratos (self-hosted) |
| **Session Storage** | In-memory Map | Redis or In-memory | PostgreSQL (persistent) |
| **Client Registration** | Custom endpoint | Dynamic Client Registration | Hydra Admin API |
| **Token Validation** | Manual JWT verify | Bearer auth middleware | JWKS endpoint + caching |
| **Authorization** | None (Reddit API only) | None | Ory Keto (Zanzibar RBAC) |
| **Multi-tenancy** | Not supported | Not supported | Native (tenant_id in JWT) |
| **Scalability** | Single instance | Horizontal (stateless) | Horizontal (DB-backed) |
| **Production Ready** | Demo/prototype | Production (with Redis) | Enterprise-grade |

---

## 10. File Structure

```
zero-ops-platform/
├── deployments/
│   ├── hydra/
│   │   ├── hydra.yml
│   │   ├── deployment.yaml
│   │   └── service.yaml
│   ├── kratos/
│   │   ├── kratos.yml
│   │   ├── identity.schema.json
│   │   ├── deployment.yaml
│   │   └── service.yaml
│   └── keto/
│       ├── keto.yml
│       ├── keto_namespaces.ts
│       ├── deployment.yaml
│       └── service.yaml
│
├── cmd/
│   ├── zero-ops-api/
│   │   └── main.go
│   └── hydra-hooks/
│       └── main.go  # Login/consent hook service
│
├── internal/
│   ├── api/
│   │   ├── handlers/
│   │   │   ├── tenant.go
│   │   │   ├── oauth.go  # Metadata endpoints
│   │   │   └── credentials.go  # Credential upload flow
│   │   └── middleware/
│   │       ├── auth.go  # JWT validation
│   │       └── tenant_isolation.go
│   │
│   ├── hydra/
│   │   ├── login_hook.go  # Accept login with tenant context
│   │   ├── consent_hook.go
│   │   └── client.go  # Hydra Admin API client
│   │
│   ├── kratos/
│   │   ├── session.go  # Session validation
│   │   └── identity.go  # Identity management
│   │
│   └── keto/
│       ├── permissions.go  # Permission checks
│       └── relations.go  # Relation tuple management
│
└── gateway/  # Rust Gateway
    └── src/
        ├── middleware/
        │   ├── jwt_validation.rs
        │   ├── authorization.rs  # Keto integration
        │   └── rate_limit.rs
        └── main.rs
```

---

## 11. Key Takeaways

1. **Ory Hydra replaces custom OAuth server**: Production-grade OAuth 2.1 with PKCE, no custom implementation needed
2. **Ory Kratos replaces Reddit OAuth**: Self-hosted identity provider with email/password, SSO, and recovery flows
3. **Ory Keto enables tenant isolation**: Zanzibar-style permissions for fine-grained access control
4. **JWT tokens carry tenant context**: Custom claims injected via login hooks enable multi-tenancy
5. **Security principle validated**: Credentials NEVER transit agent - browser-based authentication only
6. **Gateway validates tokens < 50ms**: JWKS caching + JWT validation meets performance SLO
7. **Scalable architecture**: PostgreSQL-backed state enables horizontal scaling of all Ory components

---

## 12. Next Steps

1. Review this strategy with security team
2. Set up development environment with Ory stack (docker-compose)
3. Implement Phase 1: Deploy Ory components
4. Create proof-of-concept: MCP client → Hydra → Kratos → zero-ops-api
5. Validate security: penetration testing, credential flow audit
6. Document API integration guide for MCP client developers

---

**Document Status:** Ready for review and implementation planning


---

## 13. Token Lifecycle & Scope Management

### 13.1 Token TTL Strategy

**Design Rationale:**
- 24-hour access token eliminates token refresh during typical onboarding (15-min provisioning)
- 30-day refresh token supports multi-day workflows without re-authentication
- 10-minute authorization code enforces quick token exchange for security (PKCE flow)

| Token Type | TTL | Use Case |
|------------|-----|----------|
| Access Token | 24h (86400s) | API authentication, covers full provisioning window |
| Refresh Token | 30d (720h) | Long-running workflows, transparent token renewal |
| Authorization Code | 10m (600s) | PKCE Flow, exchanged for access token |

### 13.2 Scope Definitions

**Scope Hierarchy:**
```
tenant:read    → Read tenant metadata (GET /api/v1/tenants)
tenant:write   → Create/update/delete tenants (POST/PATCH/DELETE /api/v1/tenants)
cluster:read   → Read environment status (GET /api/v1/environments/:id/status)
cluster:write  → Create/delete environments (POST/DELETE /api/v1/environments)
offline_access → Request refresh token during authorization
```

**MCP Tool → Scope Mapping:**
```go
// internal/gateway/authorization.go
var toolScopes = map[string][]string{
    "tenant_create":      {"tenant:write"},
    "tenant_get":         {"tenant:read"},
    "tenant_update":      {"tenant:write"},
    "tenant_delete":      {"tenant:write"},
    "environment_create": {"cluster:write"},
    "environment_get":    {"cluster:read"},
    "environment_delete": {"cluster:write"},
}

func (g *Gateway) validateToolScope(toolName string, jwtScopes []string) error {
    requiredScopes := toolScopes[toolName]
    for _, required := range requiredScopes {
        if !contains(jwtScopes, required) {
            return fmt.Errorf("missing required scope: %s", required)
        }
    }
    return nil
}
```

### 13.3 Token Refresh Flow

**Transparent Refresh (No User Interaction):**
```typescript
// MCP Client Token Refresh Logic
async function callMCPTool(toolName: string, params: any) {
  let response = await fetch('/mcp', {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${accessToken}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ tool: toolName, params }),
  });
  
  // Handle token expiry
  if (response.status === 401) {
    const error = await response.json();
    if (error.error === 'invalid_token' && error.error_description === 'Token expired') {
      // Transparent refresh
      const newTokens = await refreshAccessToken(refreshToken);
      accessToken = newTokens.access_token;
      refreshToken = newTokens.refresh_token; // Hydra rotates refresh tokens
      
      // Retry original request
      response = await fetch('/mcp', {
        method: 'POST',
        headers: {
          'Authorization': `Bearer ${accessToken}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ tool: toolName, params }),
      });
    }
  }
  
  return response;
}

async function refreshAccessToken(refreshToken: string): Promise<TokenResponse> {
  const response = await fetch('https://auth.nutgraf.in/oauth2/token', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'refresh_token',
      refresh_token: refreshToken,
      client_id: 'mcp-public-client',
      scope: 'tenant:read tenant:write cluster:read cluster:write offline_access',
    }),
  });
  
  if (!response.ok) {
    // Refresh token expired or revoked - restart device flow
    throw new Error('Refresh failed - re-authentication required');
  }
  
  return await response.json();
}
```

### 13.4 Token Expiry Scenarios

**Scenario 1: Normal Onboarding (< 24h)**
```
T=0:     Device auth completes, access_token expires T=24h
T=0:     tenant_create (scope: tenant:write) ✓
T=5min:  environment_create (scope: cluster:write) ✓
T=20min: Polling status (scope: cluster:read) ✓
Result:  No refresh needed, all operations within 24h window
```

**Scenario 2: Token Expires During Polling**
```
T=23h:   User starts onboarding, access_token expires T=47h
T=23h:   tenant_create ✓
T=23h:   environment_create ✓
T=24h:   Polling status → 401 "Token expired"
T=24h:   Client refreshes token transparently
T=24h:   Polling continues with new access_token ✓
Result:  User sees no error, seamless experience
```

**Scenario 3: Multi-Day Workflow**
```
Day 1:   tenant_create, access_token expires Day 2
Day 3:   environment_create → 401 "Token expired"
Day 3:   Client refreshes (refresh_token valid 30 days)
Day 3:   environment_create retried with new token ✓
Result:  No re-authentication needed
```

**Scenario 4: Refresh Token Expired**
```
Day 35:  User returns (refresh_token expired Day 30)
Day 35:  tenant_create → 401 "Token expired"
Day 35:  Client attempts refresh → 400 "invalid_grant"
Day 35:  Client displays: "Session expired. Please authenticate."
Day 35:  Client restarts device flow
Result:  User must re-authenticate (expected after 30 days)
```

### 13.5 Hydra Token Configuration

**Hydra Configuration for Token Lifecycle:**
```yaml
# hydra.yml
ttl:
  access_token: 24h
  refresh_token: 720h
  authorization_code: 10m  # Authorization Code Flow
  
oauth2:
  refresh_token_rotation:
    enabled: true  # Rotate refresh tokens on each use (security best practice)
  
  pkce:
    enforced: true  # Enforce PKCE for all authorization code flows
    enforced_for_public_clients: true
```

**Token Rotation Strategy:**
- Access tokens: NOT rotated (stateless JWT, expires after 24h)
- Refresh tokens: Rotated on each use (Hydra issues new refresh token, invalidates old one)
- Authorization codes: Single-use (consumed during token exchange)

### 13.6 Answers to Token Lifecycle Questions

**Q1: JWT access token TTL longer than 15 minutes?**
- Yes, 24 hours (86400 seconds) covers the 15-minute provisioning window with significant buffer.

**Q2: Refresh token issued during device auth? Transparent usage?**
- Yes, 30-day refresh token issued with `offline_access` scope during Authorization Code + PKCE flow. Cursor uses it transparently on 401 errors. User sees no interruption.

**Q3: Scopes for tenant_create vs environment_create?**
- Different scopes: `tenant_create` requires `tenant:write`, `environment_create` requires `cluster:write`. Both included in default scope request.

**Q4: JWT expires between tenant_create and environment_create?**
- Cursor transparently refreshes token using refresh token. No user interaction unless refresh token also expired (after 30 days).

---

**Document Updated:** March 11, 2026 - Added comprehensive token lifecycle and scope specifications


---

## 14. OAuth Flow Decision: Authorization Code + PKCE

### 14.1 Why Authorization Code + PKCE?

**Decision:** Use Authorization Code Flow with PKCE (RFC 7636) instead of Device Flow (RFC 8628).

**Rationale:**

| Criteria | Authorization Code + PKCE | Device Flow | Winner |
|----------|---------------------------|-------------|--------|
| **Reference Implementations** | systemprompt-mcp-server, http-oauth-mcp-server, remote-mcp-server-with-auth ALL use PKCE | None of the reference implementations use Device Flow | ✅ PKCE |
| **User Experience** | Automatic redirect to IDE after browser auth (RFC 8252 loopback) | Manual code entry required, no automatic redirect | ✅ PKCE |
| **Security** | PKCE prevents authorization code interception | Secure, but designed for input-constrained devices | ✅ PKCE |
| **Client Support** | RFC 8252 loopback works universally (Windows, macOS, Linux) | Requires polling, more complex client implementation | ✅ PKCE |
| **Industry Standard** | OAuth 2.1 recommends PKCE for all public clients | Device Flow for TVs, IoT devices, not desktop apps | ✅ PKCE |
| **MCP Specification** | MCP OAuth spec examples use PKCE | Device Flow not mentioned in MCP spec | ✅ PKCE |

### 14.2 PKCE Flow Diagram

```
┌─────────────────┐
│   MCP Client    │  1. Generate code_verifier (random 43-128 chars)
│   (Cursor)      │  2. Compute code_challenge = SHA256(code_verifier)
└────────┬────────┘  3. Open browser with code_challenge
         │
         │ 4. Start local HTTP server on 127.0.0.1:{ephemeral_port}
         │ 5. GET /oauth2/auth?client_id=...&code_challenge=...&code_challenge_method=S256&redirect_uri=http://127.0.0.1:{port}/callback
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Ory Hydra                                 │
│  • Validates PKCE parameters                                 │
│  • Stores code_challenge for later verification              │
│  • Redirects to Kratos login UI                              │
└────────┬────────────────────────────────────────────────────┘
         │ 5. Redirect to Kratos
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Ory Kratos                                │
│  • User enters credentials in BROWSER (never in agent)       │
│  • Creates Kratos session                                    │
└────────┬────────────────────────────────────────────────────┘
         │ 6. Redirect back to Hydra with session
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Ory Hydra                                 │
│  • Validates Kratos session                                  │
│  • Generates authorization code                              │
│  • Redirects: http://127.0.0.1:{port}/callback?code=...&state=... │
└────────┬────────────────────────────────────────────────────┘
         │ 7. Authorization code returned to client
         ▼
┌─────────────────┐
│   MCP Client    │  8. POST /oauth2/token
│   (Cursor)      │     grant_type=authorization_code
│                 │     code=...
│                 │     code_verifier=... (PKCE verification)
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────┐
│                    Ory Hydra                                 │
│  • Verifies: SHA256(code_verifier) == stored code_challenge  │
│  • Issues JWT access token (24h TTL)                         │
│  • Issues refresh token (30d TTL)                            │
└─────────────────────────────────────────────────────────────┘
```

### 14.3 PKCE Security Benefits

**Attack Prevention:**
1. **Authorization Code Interception:** Even if attacker intercepts authorization code, they cannot exchange it without the code_verifier (which never leaves the client)
2. **No Client Secret Required:** Public clients (desktop apps) don't need to store secrets, eliminating secret extraction attacks
3. **CSRF Protection:** State parameter prevents cross-site request forgery
4. **Replay Attacks:** Authorization codes are single-use, expire in 10 minutes

**Comparison with Device Flow:**
- Device Flow: Secure, but designed for devices without browsers (smart TVs, IoT)
- PKCE: Designed for native apps with browsers (desktop apps, mobile apps)
- Cursor/Goose are desktop apps with browser access → PKCE is the correct choice

### 14.4 Implementation Checklist

**Hydra Configuration:**
- [x] Enable PKCE enforcement: `oauth2.pkce.enforced: true`
- [x] Set authorization code TTL: `ttl.auth_code: 10m`
- [x] Configure redirect URI validation (allow cursor://, goose://, localhost)

**Client Implementation (Cursor/Goose):**
- [ ] Generate cryptographically random code_verifier (43-128 chars, base64url)
- [ ] Compute code_challenge using SHA256
- [ ] Start local HTTP server on 127.0.0.1 with ephemeral port (RFC 8252 Section 7.3)
- [ ] Open system browser with authorization URL
- [ ] Receive redirect on loopback interface (http://127.0.0.1:{port}/callback)
- [ ] Validate state parameter on redirect
- [ ] Stop local HTTP server after receiving authorization code
- [ ] Exchange authorization code with code_verifier
- [ ] Store tokens in OS keychain

**Backend Implementation (zero-ops-api):**
- [ ] Expose OAuth metadata endpoints (Requirement 12)
- [ ] Validate JWT tokens using Hydra JWKS
- [ ] Extract tenant_id from JWT claims
- [ ] Enforce scope-based authorization

---

**Document Status:** Complete - Authorization Code + PKCE chosen as OAuth flow
**Last Updated:** March 11, 2026
