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
AgentGateway (api.nutgraf.in)
    │ /.well-known/oauth-protected-resource
    │
    │ 2. Fetch authorization server metadata
    ▼
auth-proxy (auth.nutgraf.in)
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
- `ory-system`: Hydra, Kratos, Keto, kratos-selfservice-ui
- `identity-services`: auth-proxy
- `api-gateway`: AgentGateway, demo-echo
- `zero-ops-system`: CNPG identity-postgres cluster

## Component Specifications

### 1. CNPG Database Cluster

**Resource:** CloudNativePG Cluster

**Namespace:** `zero-ops-system`

**Cluster Name:** `identity-postgres`

**Databases:** hydra, kratos, keto (via CNPG Database CRDs)

**Connection Pattern:**
- Service DNS: `identity-postgres-rw.zero-ops-system.svc.cluster.local:5432`

### 2. Ory Hydra

**Deployment:** Helm chart `ory/hydra` v25.4.0

**Namespace:** `ory-system`

**Endpoints (actual service names):**
- Public API: `ory-hydra-public.ory-system.svc.cluster.local:4444`
- Admin API: `ory-hydra-admin.ory-system.svc.cluster.local:4445`

**Note:** Legacy `hydra-public`/`hydra-admin` services also exist from a prior deployment but are not used.

### 3. Ory Kratos

**Deployment:** Helm chart `ory/kratos` v25.4.0

**Namespace:** `ory-system`

**Endpoints (actual service names):**
- Public API: `kratos-public.ory-system.svc.cluster.local:80`
- Admin API: `kratos-admin.ory-system.svc.cluster.local:80`

**Identity Schema:** email (required), role (tenant_admin | platform_admin), tenant_id (optional)

### 4. Ory Keto

**Deployment:** Helm chart `ory/keto` v25.4.0

**Namespace:** `ory-system`

**Endpoints:**
- Read API: `ory-keto-read.ory-system.svc.cluster.local:80`
- Write API: `ory-keto-write.ory-system.svc.cluster.local:80`

**Note:** Keto dependency deferred to Demo 2.

### 5. auth-proxy

**Location:** `cmd/auth-proxy/`

**Namespace:** `identity-services`

**Service DNS:** `auth-proxy.identity-services.svc.cluster.local:8080`

**Configuration (actual env vars):**
```bash
LISTEN_ADDR=:8080
HYDRA_PUBLIC_URL=http://ory-hydra-public.ory-system.svc.cluster.local:4444
HYDRA_ADMIN_URL=http://ory-hydra-admin.ory-system.svc.cluster.local:4445
KRATOS_PUBLIC_URL=http://ory-kratos-public.ory-system.svc.cluster.local:4433
KRATOS_ADMIN_URL=http://ory-kratos-admin.ory-system.svc.cluster.local:4434
JWKS_CACHE_TTL=1h
JWKS_FETCH_TIMEOUT=5s
JWKS_REFRESH_MIN_INTERVAL=10s
EXPECTED_JWT_AUDIENCE=https://api.nutgraf.in
MCP_GATEWAY_BASE_URL=https://api.nutgraf.in
```

**Responsibilities:**
- Proxy OAuth metadata and JWKS from Hydra
- Handle login challenge (session re-use via Kratos whoami)
- Handle consent (headless claim injection for trusted clients)
- Pre-register `mcp-public-client` on startup
- extAuthz validation endpoint (`POST /internal/validate`)
- Cache JWKS with 1h TTL, refresh on key-id mismatch

**extAuthz Response Headers:**
```
X-Auth-User-Id: {sub}
X-Auth-Email: {email}
X-Auth-Role: {role}
# X-Auth-Tenant-Id omitted for Demo 1
```

### 6. AgentGateway

**Namespace:** `api-gateway`

**Service DNS:** `agentgateway.api-gateway.svc.cluster.local:3000`

**JWKS URL (actual):**
```
http://ory-hydra-public.ory-system.svc.cluster.local:4444/.well-known/jwks.json
```

**Routes:**
- `GET /health` → direct 200
- `GET /.well-known/oauth-protected-resource` → static JSON
- `GET /.well-known/oauth-authorization-server` → static JSON
- `POST /mcp/*` → extAuthz → demo-echo

**extAuthz:** `POST http://auth-proxy.identity-services.svc.cluster.local:8080/internal/validate`

### 7. kratos-selfservice-ui

**Image:** `oryd/kratos-selfservice-ui-node:v1.3.0`

**Namespace:** `ory-system`

**Configuration (env vars):**
```bash
KRATOS_PUBLIC_URL=http://ory-kratos-public.ory-system.svc.cluster.local:4433
KRATOS_BROWSER_URL=https://console.nutgraf.in
COOKIE_SECRET=<from secret kratos-ui-secrets>
CSRF_COOKIE_NAME=__HOST-csrf
CSRF_COOKIE_SECRET=<from secret kratos-ui-secrets>
```

**Secret:** `kratos-ui-secrets` in `ory-system` (created by bootstrap CLI)

### 8. demo-echo

**Image:** `ealen/echo-server:latest`

**Namespace:** `api-gateway`

**Purpose:** Backend MCP stub — echoes all received headers to prove `X-Auth-*` injection works end-to-end.

## DNS and Ingress

**Domain:** `nutgraf.in` (Hetzner Cloud DNS, zone id=471876)

**LB IP:** `167.235.217.188` (Hetzner LB id=5965657, region fsn1)

**DNS A Records:**
- `api.nutgraf.in` → `167.235.217.188`
- `auth.nutgraf.in` → `167.235.217.188`
- `console.nutgraf.in` → `167.235.217.188`

**Ingress Routes:**
- `api.nutgraf.in` → `agentgateway.api-gateway:3000` (TLS: `api-zero-ops-tls`)
- `auth.nutgraf.in` → `auth-proxy.identity-services:8080` (TLS: `auth-zero-ops-tls`)
- `console.nutgraf.in` → `kratos-selfservice-ui-node.ory-system:3000` (TLS: `console-zero-ops-tls`)

**TLS:** cert-manager HTTP-01 via Let's Encrypt (`letsencrypt-prod` ClusterIssuer)

**Ingress Class:** `nginx` (ingress-nginx, `ssl-redirect: false` during cert issuance)

## Bootstrap CLI

**Command:** `go run ./cmd/zero-ops demo bootstrap`

**Flags:**
```
--kubeconfig       path to kubeconfig (default: secrets/mothership.kubeconfig)
--dns-token        Hetzner Cloud API token (validated against api.hetzner.cloud)
--hcloud-token     Hetzner Cloud token for CCM (defaults to dns-token)
--ghcr-username    GHCR username (for image pull secret)
--ghcr-token       GHCR token (for image pull secret)
--github-token     GitHub PAT for ArgoCD repo access
```

**Secrets Created:**
| Secret | Namespace | Purpose |
|--------|-----------|---------|
| `hetzner-dns` | `cert-manager` | cert-manager webhook |
| `hetzner-dns` | `kube-system` | ExternalDNS |
| `hcloud` | `kube-system` | Hetzner CCM |
| `identity-postgres-passwords` | `zero-ops-system` | CNPG DB passwords |
| `kratos-ui-secrets` | `ory-system` | cookie/CSRF secrets |
| `ghcr-pull-secret` | `identity-services` | GHCR image pull |
| `repo-soloz-io-zero-ops` | `argocd` | GitHub repo access |

**Post-secret actions:** Waits for all ArgoCD apps `Synced/Healthy` and all 3 TLS certs `Ready: True` before exiting.

## ArgoCD App-of-Apps Structure

```
manifests/argocd/
  app-of-apps.yaml              # demo1 parent — source: manifests/argocd/apps
  apps/
    api-gateway.yaml
    cert-manager-webhook-hetzner.yaml
    external-dns.yaml
    hcloud-ccm.yaml             # Hetzner CCM v1.20.0
    ingress-config.yaml
    ingress-nginx.yaml
    network-policies.yaml
    platform-identity.yaml      # points to manifests/platform-identity/argocd/
manifests/platform-identity/argocd/
  app-of-apps.yaml
  ory-hydra.yaml, ory-kratos.yaml, ory-keto.yaml
  identity-postgres.yaml, auth-proxy.yaml, kratos-ui.yaml, demo-echo.yaml
```

## Infrastructure

**Cluster:** Hetzner Cloud, region `fsn1`

**CCM:** `hcloud-cloud-controller-manager` v1.20.0 — manages LB targets automatically via `hcloud` secret in `kube-system`

**ExternalDNS:** `docker.io/hetzner/external-dns-hetzner-webhook:v0.3.2` — syncs DNS A records via Hetzner Cloud API

## PKCE Flow Sequence

### Step 1: Metadata Discovery
Cursor hits `https://api.nutgraf.in` → AgentGateway returns HTTP 401 with `WWW-Authenticate` header pointing to `/.well-known/oauth-protected-resource`.

### Step 2: Authorization Server Metadata
Cursor fetches `https://auth.nutgraf.in/.well-known/oauth-authorization-server` → auth-proxy proxies from Hydra.

### Step 3: Authorization Request
Cursor generates PKCE `code_verifier`/`code_challenge`, opens browser to:
```
https://auth.nutgraf.in/oauth2/auth?client_id=mcp-public-client&response_type=code&...
```

### Step 4: User Authentication
Hydra → auth-proxy login handler → Kratos UI (`console.nutgraf.in/login`) → user logs in → Kratos redirects back to auth-proxy → auth-proxy accepts login challenge.

### Step 5: Consent (Headless)
auth-proxy detects `mcp-public-client` as trusted, fetches Kratos identity traits, injects `email`+`role` into JWT session, accepts consent programmatically.

### Step 6: Token Exchange
Cursor exchanges auth code + `code_verifier` at `https://auth.nutgraf.in/oauth2/token`.

### Step 7: Authenticated Request
Cursor calls `https://api.nutgraf.in/mcp` with `Authorization: Bearer <jwt>` → AgentGateway extAuthz → auth-proxy validates → `X-Auth-*` headers injected → demo-echo responds.

## Demo Success Criteria

1. `https://api.nutgraf.in` returns HTTP 401 with `WWW-Authenticate` header
2. Browser opens to `https://console.nutgraf.in/login` automatically
3. User logs in with `demo@nutgraf.in` / `Demo1Password!` — no consent screen
4. Browser redirects to Cursor callback and closes
5. Second request returns HTTP 200 from demo-echo showing `X-Auth-User-Id`, `X-Auth-Email`, `X-Auth-Role` headers

## Seed Data

```bash
kubectl exec -n ory-system deploy/ory-kratos -- \
  curl -X POST http://localhost:4434/admin/identities \
  -H "Content-Type: application/json" \
  -d '{"schema_id":"default","traits":{"email":"demo@nutgraf.in","role":"tenant_admin"},"credentials":{"password":{"config":{"password":"Demo1Password!"}}}}'
```

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
auth-proxy
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
- `identity-services`: auth-proxy
- `api-gateway`: AgentGateway

**Rationale:** Multi-namespace isolation provides better RBAC, blast radius control, and independent upgrade paths (enterprise pattern per memory/design-principles.md).

## Component Specifications

### 1. CNPG Database Cluster

**Resource:** Shared CloudNativePG Cluster (pre-existing)

**Namespace:** `zero-ops-system`

**Cluster Name:** `zero-ops-platform-db`

**Databases (via Database CRD):**
- `hydra-db`: OAuth2 clients, sessions, tokens
- `kratos-db`: Identities, credentials, sessions
- `keto-db`: Relationships, permissions

**Connection Pattern:**
- Service DNS: `zero-ops-platform-db-rw.zero-ops-system.svc.cluster.local:5432`
- Each Ory component connects via dedicated database

### 2. Ory Hydra

**Deployment:** Helm chart `ory/hydra` v25.4.0

**Namespace:** `ory-system`

**Configuration (values.yaml) (G-03, G-05, G-09 Resolution):**
```yaml
hydra:
  config:
    urls:
      self:
        issuer: https://auth.nutgraf.in
      login: https://auth.nutgraf.in/login  # auth-proxy handles login challenge
      consent: https://auth.nutgraf.in/consent  # auth-proxy handles consent
    oauth2:
      expose_internal_errors: false
    strategies:
      scope: exact
      access_token: jwt  # Issue JWT access tokens (required for AgentGateway JWT validation)
    ttl:
      access_token: 24h
      refresh_token: 720h  # 30 days
      auth_code: 10m       # Explicit 10-minute timeout
  
  # Password injection via existingSecret
  extraEnv:
    - name: HYDRA_DB_PASSWORD
      valueFrom:
        secretKeyRef:
          name: identity-postgres-passwords
          key: hydra-password
    - name: DSN
      value: postgres://hydra:$(HYDRA_DB_PASSWORD)@zero-ops-platform-db-rw.zero-ops-system.svc.cluster.local:5432/hydra_db
```

**Endpoints (actual service names):**
- Public API: `ory-hydra-public.ory-system.svc.cluster.local:4444`
- Admin API: `ory-hydra-admin.ory-system.svc.cluster.local:4445`

**Custom Claims Injection:**
Custom claims (`email`, `role`, optionally `tenant_id`) are injected by auth-proxy's consent handler via `acceptOAuth2ConsentRequest`. See the auth-proxy Consent Flow description and Step 5 of the PKCE Flow Sequence for the canonical session object structure.

### 3. Ory Kratos

**Deployment:** Helm chart `ory/kratos` v25.4.0

**Namespace:** `ory-system`

**Configuration (values.yaml) (G-03, G-04 Resolution):**
```yaml
kratos:
  config:
    session:
      cookie:
        domain: nutgraf.in
        name: ory_kratos_session
    identity:
      default_schema_id: default
      schemas:
        - id: default
          url: file:///etc/config/identity.schema.json
    selfservice:
      default_browser_return_url: https://console.nutgraf.in/
      allowed_return_urls:
        - https://auth.nutgraf.in/login
      flows:
        login:
          ui_url: https://console.nutgraf.in/login
        registration:
          ui_url: https://console.nutgraf.in/registration
  
  # Password injection via existingSecret
  extraEnv:
    - name: KRATOS_DB_PASSWORD
      valueFrom:
        secretKeyRef:
          name: identity-postgres-passwords
          key: kratos-password
    - name: DSN
      value: postgres://kratos:$(KRATOS_DB_PASSWORD)@zero-ops-platform-db-rw.zero-ops-system.svc.cluster.local:5432/kratos_db
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

**Endpoints (actual service names):**
- Public API: `ory-kratos-public.ory-system.svc.cluster.local:4433`
- Admin API: `ory-kratos-admin.ory-system.svc.cluster.local:4434`

### 4. Ory Keto

**Deployment:** Helm chart `ory/keto` v25.4.0

**Namespace:** `ory-system`

**Configuration (values.yaml) (G-03 Resolution):**
```yaml
keto:
  config:
    namespaces:
      - id: 0
        name: tenants
  
  # Password injection via existingSecret
  extraEnv:
    - name: KETO_DB_PASSWORD
      valueFrom:
        secretKeyRef:
          name: identity-postgres-passwords
          key: keto-password
    - name: DSN
      value: postgres://keto:$(KETO_DB_PASSWORD)@zero-ops-platform-db-rw.zero-ops-system.svc.cluster.local:5432/keto_db
```

**Endpoints:**
- Read API: `keto-read.ory-system.svc.cluster.local:80`
- Write API: `keto-write.ory-system.svc.cluster.local:80`

### 5. auth-proxy

**Location:** `cmd/auth-proxy/` (new service in monorepo)

**Technology:** Go service (lightweight, minimal dependencies)

**Namespace:** `identity-services`

**Service DNS:** `auth-proxy.identity-services.svc.cluster.local:8080`

**Responsibilities:**
- Proxy OAuth metadata from Hydra to MCP clients
- Proxy JWKS from Hydra (for MCP clients during token exchange)
- Handle login challenge acceptance via return_to pattern with Kratos
- Handle consent flow and inject custom JWT claims
- Pre-register OAuth client on startup with idempotent 409 handling
- **Validate Ory JWTs via extAuthz endpoint** (primary responsibility)
- Cache Ory JWKS with 1-hour TTL, refresh on key-id mismatch
- Return identity claims to AgentGateway via HTTP headers (OIDC pass-through pattern)
- Call Hydra Admin API for OAuth client lifecycle
- Call Kratos Admin API for session validation and identity trait fetching

**Code Structure:**
```
cmd/auth-proxy/
  main.go                    # Entrypoint
internal/auth/
  handlers/
    login.go                 # Login challenge handler (session validation only)
    consent.go               # Consent flow handler
    metadata.go              # OAuth metadata proxy
    jwks.go                  # JWKS proxy
    validate.go              # extAuthz validation endpoint (NEW)
  client/
    hydra.go                 # Hydra Admin API client
    kratos.go                # Kratos Admin API client
  jwt/
    validator.go             # Ory JWT validation with JWKS caching (NEW)
  config/
    config.go                # Configuration
```

**Endpoints:**
- `GET /.well-known/oauth-authorization-server` - Proxy Hydra metadata
- `GET /.well-known/jwks.json` - Proxy Hydra JWKS (for MCP clients)
- `GET /login?login_challenge={challenge}` - Accept login challenge, redirect to Kratos with return_to
- `GET /consent?consent_challenge={challenge}` - Display consent screen (skipped for trusted client)
- `POST /consent` - Process consent, inject claims, accept consent request
- `POST /internal/validate` - **extAuthz validation endpoint**
- `GET /health/ready` - Readiness probe (Returns 200 ONLY after client registration AND initial JWKS fetch complete successfully)

**extAuthz Validation Flow (OIDC Pass-Through Pattern):**
1. Receive request from AgentGateway with `Authorization: Bearer <ory_jwt>` header
2. Extract Ory-issued JWT from Bearer token
3. Verify Ory JWT signature using cached JWKS (RS256/ES256)
4. Verify `exp` claim > current time
5. Verify `aud` claim contains `https://api.nutgraf.in` (EXPECTED_JWT_AUDIENCE)
6. On signature failure: refresh JWKS once, reset TTL to now+1h, retry validation
7. On success: Extract claims from validated Ory JWT and return HTTP 200 with headers. IF a claim is missing (e.g., `tenant_id` for Demo 1 users), the corresponding header MUST be omitted entirely.
   ```
   X-Auth-User-Id: {sub}
   X-Auth-Email: {email}
   X-Auth-Role: {role}
   # X-Auth-Tenant-Id is omitted for Demo 1
   ```
8. On failure, auth-proxy MUST return the correct HTTP status code to ensure proper MCP client behavior:
   - **HTTP 401 Unauthorized:** JWT is missing, malformed, expired (`exp`), has an invalid signature, or fails `aud` validation. (Triggers Cursor to silently refresh the token).
   - **HTTP 403 Forbidden:** JWT is structurally valid and active, but the `scope` or `role` claim is insufficient for the requested operation.

**Rationale (OIDC Pass-Through Pattern):**
- Backend services receive X-Auth headers only, no JWT validation needed
- Platform can migrate from Ory to any OIDC provider (Auth0, Okta, Keycloak)
- Migration only requires changing JWKS endpoint, issuer, and audience
- No custom JWT minting reduces latency and complexity
- Standard OIDC pattern used by enterprise auth gateways

**JWKS Caching Strategy:**
- Initial fetch on startup
- 1-hour in-memory cache
- Refresh on key-id mismatch (handles key rotation)
- TTL resets to now+1h after mismatch-triggered refresh
- 5-second timeout on JWKS fetch
- Minimum 10-second interval between mismatch-triggered refreshes (prevents thundering herd)

**Login Flow (login_challenge Pattern & Session Re-use):**
1. Hydra redirects browser to `https://auth.nutgraf.in/login?login_challenge={challenge}`
2. auth-proxy checks for an existing Kratos session cookie.
3. IF session exists: auth-proxy calls Kratos Public API `GET /sessions/whoami`. On HTTP 200, it extracts `identity.id`, calls Hydra `acceptOAuth2LoginRequest`, and skips to Step 5.
4. IF NO session exists: auth-proxy redirects browser to `https://console.nutgraf.in/login?login_challenge={challenge}`. Kratos handles the OAuth2 login flow natively via `oauth2_provider` integration.
5. User authenticates via Kratos self-service UI (email/password).
6. Kratos redirects back to auth-proxy with the challenge intact.
7. auth-proxy validates session, calls `acceptOAuth2LoginRequest`, Hydra redirects to consent.

**Consent Flow (Headless Claim Injection):**
- This is the EXCLUSIVE location for injecting custom JWT claims.
- auth-proxy fetches identity traits from Kratos Admin API.
- If Kratos Admin API fails, auth-proxy calls Hydra `rejectOAuth2ConsentRequest` with error `access_denied`.
- auth-proxy reads `requested_scope` from the consent request and uses it as `grant_scope`.
- If `requested_scope` is empty, auth-proxy rejects with `error: invalid_scope`.
- Consent is accepted programmatically for all DCR clients — no UI rendered, no static client whitelist.
- auth-proxy builds the `session` object injecting `email` and `role` into both `session.access_token` and `session.id_token`, and sets `grant_access_token_audience: ["https://api.nutgraf.in/mcp"]`.

**Client Registration (Declarative Startup):**
- On startup: `GET /admin/clients/mcp-public-client`
- If 404: `POST /admin/clients` with full spec. If POST returns 409 (race condition with another replica), treat as success and continue.
- If 200: Unconditionally `PUT /admin/clients/mcp-public-client` with full spec to ensure state matches.
- If any other error occurs (e.g., Hydra unavailable, 500), `auth-proxy` triggers `log.Fatal()` to utilize Kubernetes CrashLoopBackOff until Hydra is healthy.

**Dependencies:**
- Hydra Public API: `http://ory-hydra-public.ory-system.svc.cluster.local:4444`
- Hydra Admin API: `http://ory-hydra-admin.ory-system.svc.cluster.local:4445`
- Kratos Public API: `http://ory-kratos-public.ory-system.svc.cluster.local:4433`
- Kratos Admin API: `http://ory-kratos-admin.ory-system.svc.cluster.local:4434`

**Configuration (environment variables):**
```bash
HYDRA_PUBLIC_URL=http://ory-hydra-public.ory-system.svc.cluster.local:4444
HYDRA_ADMIN_URL=http://ory-hydra-admin.ory-system.svc.cluster.local:4445
KRATOS_PUBLIC_URL=http://ory-kratos-public.ory-system.svc.cluster.local:4433
KRATOS_ADMIN_URL=http://ory-kratos-admin.ory-system.svc.cluster.local:4434
JWKS_CACHE_TTL=1h
JWKS_FETCH_TIMEOUT=5s
JWKS_REFRESH_MIN_INTERVAL=10s
EXPECTED_JWT_AUDIENCE=https://api.nutgraf.in
MCP_GATEWAY_BASE_URL=https://api.nutgraf.in
LISTEN_ADDR=:8080
```

**Note:** Keto dependency deferred to Demo 2 (G-11 resolution)

### 6. AgentGateway

**Source:** External repository (not in zero-ops monorepo)

**Deployment:** Helm chart or Kustomize from external repo

**Namespace:** `api-gateway`

**Service DNS:** `agentgateway.api-gateway.svc.cluster.local:3000`

**Responsibilities:**
- Expose OAuth resource metadata
- Route requests to auth-proxy for validation via extAuthz
- Forward authenticated requests with injected auth headers to backend MCP servers
- NO direct JWT validation (delegated to auth-proxy)

**Endpoints:**
- `GET /.well-known/oauth-protected-resource` - Resource metadata (static response)
- `GET /health` - Health check
- `POST /mcp/*` - MCP tool endpoints (extAuthz enforced)

**extAuthz Validation Flow (G-03/G-08/G-16 Resolution):**
1. AgentGateway receives request with `Authorization: Bearer <token>` header
2. AgentGateway calls auth-proxy extAuthz endpoint: `POST http://auth-proxy.identity-services.svc.cluster.local:8080/internal/validate`
3. AgentGateway forwards headers: `Authorization`
4. auth-proxy validates JWT (fetches JWKS from Hydra, caches with 1h TTL)
5. auth-proxy returns auth decision with headers:
   - `X-Auth-User-Id: {sub}`
   - `X-Auth-Tenant-Id: {tenant_id}` (if present)
   - `X-Auth-Email: {email}`
   - `X-Auth-Role: {role}`
6. On success (HTTP 200): AgentGateway forwards request to backend with auth headers
7. On failure (HTTP 401/403): AgentGateway returns error to client

**Configuration (ConfigMap):**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: agentgateway-config
  namespace: api-gateway
data:
  config.yaml: |
    binds:
      - port: 3000
        listeners:
          - name: main-listener
            protocol: HTTP
            routes:
              # Health check
              - name: health
                matches:
                  - path: 
                      exact: "/health"
                policies:
                  directResponse:
                    status: 200
                    body: "OK"

              # OAuth metadata (static response)
              - name: oauth-metadata
                matches:
                  - path: 
                      exact: "/.well-known/oauth-protected-resource"
                policies:
                  directResponse:
                    status: 200
                    body: '{"resource": "https://api.nutgraf.in", "authorization_servers": ["https://auth.nutgraf.in"], "bearer_methods_supported": ["header"], "scopes_supported": ["tenant:read", "tenant:write", "cluster:read", "cluster:write", "offline_access", "openid"]}'
                    headers:
                      Content-Type: "application/json"

              # MCP API routes (protected via extAuthz)
              - name: mcp-api
                matches:
                  - path: 
                      pathPrefix: "/mcp"
                policies:
                  extAuthz:
                    host: "auth-proxy.identity-services.svc.cluster.local:8080"
                    timeout: "500ms"
                    includeRequestHeaders:
                      - authorization
                    protocol:
                      http:
                        path: '"/internal/validate"'
                        includeResponseHeaders:
                          - X-Auth-User-Id
                          - X-Auth-Tenant-Id
                          - X-Auth-Email
                          - X-Auth-Role
                  requestHeaderModifier:
                    remove: ["Authorization"]
                backends:
                  # Routes to the echo stub in the api-gateway namespace
                  - host: "demo-echo.api-gateway.svc.cluster.local:8080"
```

**Note:** JWKS caching, TTL management, and key rotation handled entirely by auth-proxy, not AgentGateway.

## OAuth Client Registration

**Method:** Dynamic Client Registration (DCR) — RFC 7591

MCP clients (Cursor, Goose) register dynamically at `https://auth.nutgraf.in/oauth2/register` before initiating the authorization flow. auth-proxy proxies DCR requests to Hydra, injecting `audience: ["https://api.nutgraf.in/mcp"]` into the request body so the issued tokens are accepted by AgentGateway.

**DCR Request (sent by MCP client):**
```json
{
  "redirect_uris": ["http://localhost:39639/oauth/callback"],
  "grant_types": ["authorization_code"],
  "response_types": ["code"],
  "token_endpoint_auth_method": "none"
}
```

**auth-proxy injects before forwarding to Hydra:**
```json
{
  "audience": ["https://api.nutgraf.in/mcp"]
}
```

**Security:** DCR is open (no registration token required). Security comes from PKCE (mandatory S256), redirect URI validation by Hydra, and user authentication via Kratos. Any client that completes the full OAuth + PKCE flow is trusted — no static client whitelist.

**Consent Behavior:** auth-proxy accepts consent programmatically for all DCR clients without rendering a UI (headless claim injection). The `TRUSTED_CLIENT_IDS` env var and static client whitelist have been removed.

## PKCE Flow Sequence

### Step 1: Metadata Discovery

**Trigger:** Cursor receives HTTP 401 from AgentGateway

**Request:**
```http
GET /.well-known/oauth-protected-resource HTTP/1.1
Host: api.nutgraf.in
```

**Response (AgentGateway):**
```json
{
  "resource": "https://api.nutgraf.in",
  "authorization_servers": ["https://auth.nutgraf.in"],
  "bearer_methods_supported": ["header"],
  "scopes_supported": ["tenant:read", "tenant:write", "cluster:read", "cluster:write", "offline_access", "openid"]
}
```

### Step 2: Authorization Server Metadata

**Request:**
```http
GET /.well-known/oauth-authorization-server HTTP/1.1
Host: auth.nutgraf.in
```

**Response (auth-proxy proxying Hydra):**
```json
{
  "issuer": "https://auth.nutgraf.in",
  "authorization_endpoint": "https://auth.nutgraf.in/oauth2/auth",
  "token_endpoint": "https://auth.nutgraf.in/oauth2/token",
  "jwks_uri": "https://auth.nutgraf.in/.well-known/jwks.json",
  "response_types_supported": ["code"],
  "grant_types_supported": ["authorization_code", "refresh_token"],
  "code_challenge_methods_supported": ["S256"],
  "token_endpoint_auth_methods_supported": ["none"]
}
```

### Step 3: Dynamic Client Registration + Authorization Request

**Cursor first registers dynamically:**
```http
POST /oauth2/register HTTP/1.1
Host: auth.nutgraf.in
Content-Type: application/json

{"redirect_uris":["http://localhost:39639/oauth/callback"],"grant_types":["authorization_code"],"response_types":["code"],"token_endpoint_auth_method":"none"}
```
auth-proxy injects `"audience": ["https://api.nutgraf.in/mcp"]` before forwarding to Hydra.

**Cursor then generates PKCE and opens browser:**

**Browser URL:**
```
https://auth.nutgraf.in/oauth2/auth?
  client_id=mcp-public-client&
  response_type=code&
  redirect_uri=http://127.0.0.1:54321/callback&
  code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&
  code_challenge_method=S256&
  state=af0ifjsldkj&
  scope=tenant:read+tenant:write+cluster:read+cluster:write+offline_access+openid&
  resource=https://api.nutgraf.in
```

### Step 4: User Authentication

**Flow (return_to Pattern & Session Re-use):**
1. Hydra redirects browser to `https://auth.nutgraf.in/login?login_challenge={challenge}`
2. auth-proxy checks for an existing Kratos session cookie.
3. IF session exists: auth-proxy calls Kratos Public API `GET /sessions/whoami`. On HTTP 200, it extracts `identity.id`, calls Hydra `acceptOAuth2LoginRequest` with `identity.id` as the `subject`, and skips to Step 5.
4. IF NO session exists: auth-proxy redirects the browser directly to Kratos UI with the `login_challenge` parameter: `https://console.nutgraf.in/login?login_challenge={challenge}`. Kratos handles the OAuth2 login flow natively via the `oauth2_provider` integration.
5. User authenticates via Kratos self-service UI (email/password)
6. Kratos redirects the browser back to auth-proxy with the challenge intact: `https://auth.nutgraf.in/login?login_challenge={challenge}`
7. auth-proxy validates the Kratos session cookie via `GET /sessions/whoami`
8. auth-proxy calls Hydra `acceptOAuth2LoginRequest` with the `identity.id` (subject)
9. Hydra redirects the browser to the consent endpoint

### Step 5: Consent

**Flow (Headless Claim Injection):**
1. Hydra redirects the browser to `https://auth.nutgraf.in/consent?consent_challenge={challenge}`
2. auth-proxy fetches the consent request from Hydra.
3. auth-proxy DOES NOT render an HTML consent screen for any DCR client. It immediately fetches identity traits from the **Kratos Admin API** using the subject UUID.
5. auth-proxy builds the session object, injecting custom claims into both id_token and access_token, and explicitly setting the audience:
```json
{
  "grant_scope": ["tenant:read", "tenant:write", "cluster:read", "cluster:write", "offline_access", "openid"],
  "grant_access_token_audience": ["https://api.nutgraf.in"],
  "session": {
    "id_token": {
      "email": "admin@acme.com",
      "role": "tenant_admin"
    },
    "access_token": {
      "email": "admin@acme.com",
      "role": "tenant_admin"
    }
  }
}
```
*(Note: `grant_scope` should be set to the scopes from the consent request's `requested_scope` field, not hardcoded. The above list is illustrative for Demo 1.)*
6. auth-proxy calls Hydra `acceptOAuth2ConsentRequest` with the session.
7. Hydra redirects the browser to the local Cursor callback URL with the authorization code.

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
Host: auth.nutgraf.in
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code&
code=ory_ac_...&
redirect_uri=http://127.0.0.1:54321/callback&
code_verifier=dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk&
client_id=mcp-public-client
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

**JWT Claims (access_token) (G-07 Note):**
```json
{
  "iss": "https://auth.nutgraf.in",
  "sub": "user-uuid",
  "aud": ["https://api.nutgraf.in"],
  "exp": 1710000000,
  "iat": 1709913600,
  "scope": "tenant:read tenant:write cluster:read cluster:write offline_access openid",
  "email": "admin@acme.com",
  "role": "tenant_admin"
  // tenant_id omitted for Demo 1 users (no tenant created yet)
}
```

### Step 7: Token Storage

**Cursor Actions:**
1. Store `access_token`, `refresh_token`, `id_token`, `expires_in` in OS keychain
2. Close browser
3. Retry original MCP tool call with `Authorization: Bearer <access_token>` header

**Note:** For Demo 1 demo, display `access_token` to stakeholders as proof of authentication.

## Deployment Artifacts

### Deployment Structure

**Manifests Location:** `manifests/platform-identity/`

**Structure:**
```
manifests/platform-identity/
  cnpg-cluster.yaml          # CNPG Cluster CR
  databases/
    hydra-db.yaml            # Database CRD
    kratos-db.yaml           # Database CRD
    keto-db.yaml             # Database CRD
  ory-hydra/
    values.yaml              # Helm values
  ory-kratos/
    values.yaml              # Helm values
    identity-schema.json     # Identity schema
  ory-keto/
    values.yaml              # Helm values
  auth-proxy/
    deployment.yaml          # Deployment
    service.yaml             # Service
    configmap.yaml           # Configuration
manifests/api-gateway/
  demo-echo/
    deployment.yaml          # Simple NGINX/Go echo server
    service.yaml             # demo-echo.api-gateway.svc.cluster.local:8080
```

### Ingress and DNS Configuration

For Demo 1, PKCE flows require HTTPS termination. The following Ingress resources MUST be deployed to the management cluster, mapping public DNS to internal services.

**Ingress Routes:**
- `api.nutgraf.in` → routes to `agentgateway.api-gateway.svc.cluster.local:3000`
- `auth.nutgraf.in` → routes to `auth-proxy.identity-services.svc.cluster.local:8080`
- `console.nutgraf.in` → routes to `kratos-selfservice-ui-node.ory-system.svc.cluster.local:3000`

*(Note: `console.nutgraf.in` MUST route to the Ory Kratos Self-Service UI NodeJS application [Docker image: `oryd/kratos-selfservice-ui-node`], NOT the Kratos Public JSON API. The UI node connects internally to the Kratos Public API).*

*(Note: TLS termination can be handled by the cloud load balancer, or via cert-manager if deployed. For local Day 1 testing, `/etc/hosts` mapping to a local NGINX ingress with self-signed certs is acceptable).*

**`kratos-selfservice-ui-node` Service Specification:**
- **Namespace:** `ory-system`
- **Image:** `oryd/kratos-selfservice-ui-node:v1.3.0` (Pinned to match Kratos v25.4.0)
- **Configuration (Env Vars):**
  - `KRATOS_PUBLIC_URL=http://ory-kratos-public.ory-system.svc.cluster.local:4433`
  - `KRATOS_BROWSER_URL=https://console.nutgraf.in`
- **Behavior:** Renders the login HTML form. Automatically forwards unknown query parameters (like `return_to`) to Kratos during the flow.

**`demo-echo` Service Specification:**
- **Namespace:** `api-gateway`
- **Image:** `ealen/echo-server:latest`
- **Behavior:** Returns HTTP 200 containing a JSON payload of all received HTTP headers. Used exclusively to prove AgentGateway successfully stripped the JWT and injected the `X-Auth-*` headers.

**ArgoCD Applications:**
1. `ory-hydra` - Helm chart from `ory/hydra`
2. `ory-kratos` - Helm chart from `ory/kratos`
3. `ory-keto` - Helm chart from `ory/keto`
4. `kratos-selfservice-ui-node` - Deployment from `oryd/kratos-selfservice-ui-node`
5. `identity-postgres` - CNPG Cluster CR
6. `auth-proxy` - Kustomize from `manifests/platform-identity/auth-proxy/`
7. `agentgateway` - External Helm chart or Kustomize

**Startup Ordering (G-10 Resolution):**
- Option 1: Use Helm initContainer in Ory charts waiting for CNPG readiness:
  ```yaml
  initContainers:
    - name: wait-for-postgres
      image: postgres:15
      command: ['sh', '-c', 'until pg_isready -h identity-postgres-rw.ory-system.svc.cluster.local -p 5432; do sleep 2; done']
  ```
- Option 2: Document expected 10-minute startup time with crash-loop backoff as normal behavior
- Recommendation: Use initContainer for Day 1 to avoid confusion

### Secrets Management (G-04 Resolution)

**Database Passwords (Demo 1 Bootstrap):**
- Created imperatively via `kubectl create secret` during Day 1 setup
- Stored as Kubernetes Secret in `ory-system` namespace
- Referenced by Ory Helm charts via `existingSecret` and `extraEnv`
- Migration to KSOPS deferred to Demo 3 (requires Master Platform Age Key from Req 15)

**Bootstrap Command:**
```bash
kubectl create secret generic identity-postgres-passwords \
  --namespace=ory-system \
  --from-literal=hydra-password=$(openssl rand -base64 32) \
  --from-literal=kratos-password=$(openssl rand -base64 32) \
  --from-literal=keto-password=$(openssl rand -base64 32)
```

**CNPG Database CRD Integration:**
```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Database
metadata:
  name: hydra-db
  namespace: zero-ops-system
spec:
  cluster:
    name: zero-ops-platform-db
  name: hydra_db
  owner: hydra
  ownerPasswordSecret:
    name: identity-postgres-passwords
    key: hydra-password
```

**Example Secret Structure:**
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
- `WWW-Authenticate: Bearer realm="api.nutgraf.in", resource_metadata="https://api.nutgraf.in/.well-known/oauth-protected-resource"`
- Cursor initiates PKCE flow

**Scenario 2: Expired JWT**
- AgentGateway returns HTTP 401
- `error="invalid_token", error_description="Token expired"`
- Cursor attempts token refresh (out of scope for Demo 1)

**Scenario 3: Invalid Signature**
- auth-proxy receives JWT with unrecognised key-id
- auth-proxy refreshes JWKS from Hydra (once, after 10s minimum interval)
- Retries JWT validation with refreshed JWKS
- If still fails: auth-proxy returns HTTP 401 to AgentGateway
- AgentGateway forwards HTTP 401 to Cursor

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

### Unit Tests (auth-proxy)
- JWKS cache hit returns immediately without network fetch
- `aud` claim mismatch returns HTTP 401
- Expired `exp` claim returns HTTP 401
- Missing `role` in Kratos traits → omitted from JWT session (not a zero-value)
- Client registration logic: 404 → POST (409 on POST → continue), 200 → unconditional PUT, other errors → log.Fatal

### Integration Tests
- **Full PKCE flow:** cold start, no session → login → token issued with correct custom claims.
- **Re-auth path:** existing Kratos session → no UI redirect → token issued directly.
- **Invalid `code_verifier`:** Hydra rejects token exchange.
- **`state` mismatch:** Cursor aborts flow.

### Demo Success Criteria (Observable Outcomes)
1. Cursor tool call with no JWT → AgentGateway returns HTTP 401 with `WWW-Authenticate: Bearer realm="api.nutgraf.in", resource_metadata="https://api.nutgraf.in/.well-known/oauth-protected-resource"`.
2. Browser opens to `https://console.nutgraf.in/login` automatically (Cursor stores `state` parameter in memory).
3. User logs in with seeded credentials (`demo@nutgraf.in` / `Demo1Password!`) — no consent screen appears.
4. Browser redirects back to Cursor callback URL (`127.0.0.1:54321`) and safely closes.
5. `access_token` is written to OS keychain. (Verified via CLI: `security find-generic-password -s "Cursor MCP" -w | jwt decode -`).
6. Second Cursor tool call with JWT → AgentGateway returns HTTP 200, routing to `demo-echo` which replies showing `X-Auth-User-Id`, `X-Auth-Email`, and `X-Auth-Role` headers successfully injected.

*(Note: Callback timeout is 5 minutes. If the user abandons the browser, Cursor logs a timeout error and closes the loopback listener).*

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
- Stateless request routing — delegates JWT validation to auth-proxy via extAuthz
- No local state or caching required
- Horizontally scalable

**Hydra:**
- PostgreSQL-backed (CNPG 3-node cluster)
- Supports 1000+ req/s per instance

**auth-proxy:**
- Stateless (no session storage)
- Horizontally scalable

## Monitoring and Observability

*Note: All metrics are exposed via standard `/metrics` endpoints. As per the PRD, the pre-existing Grafana Alloy agents deployed on the management cluster will automatically scrape these endpoints and ship them to VictoriaMetrics. No new collectors are required for Demo 1.*

### Metrics

**AgentGateway:**
- `extauthz_call_total{result="allowed|denied|error"}`
- `extauthz_duration_seconds`
- `extauthz_timeout_total`

**auth-proxy:**
- `jwt_validation_total{result="success|failure"}`
- `jwks_cache_hit_total`
- `jwks_cache_miss_total`
- `jwks_refresh_total{result="success|failure"}`
- `oauth_login_total{result="success|failure"}`
- `oauth_consent_total{result="accept|skip"}`
- `hydra_api_call_duration_seconds{endpoint}`
- `kratos_api_call_duration_seconds{endpoint}`

**Hydra:**
- `hydra_oauth2_token_issued_total{grant_type}`
- `hydra_oauth2_token_validation_total{result}`

### Logs

**Structured Logging (JSON):**
- AgentGateway: extAuthz timeouts, backend routing errors
- auth-proxy: Login attempts, consent decisions, Hydra/Kratos API errors, JWT validation failures, JWKS refresh events
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
1. Create database password Secret via `kubectl create secret`
2. Deploy CNPG cluster
3. Create Database CRDs with password references
4. Deploy Ory Helm charts with initContainers waiting for CNPG readiness
5. Deploy Ingress routes and verify DNS resolution for api, auth, and console subdomains. For local Day 1 testing, use `mkcert` to generate locally-trusted TLS certificates for `api.nutgraf.in`, `auth.nutgraf.in`, and `console.nutgraf.in`. Install the local CA into the host OS and browser trust stores to prevent PKCE flow termination due to invalid SSL. *(Note: For Electron-based clients like Cursor, verify that the application trusts the OS certificate store. If not, additional Electron-specific certificate configuration may be required.)*
6. Verify database connectivity

### Phase 2: Services & Seed Data (Day 1 Afternoon)
1. Build and deploy auth-proxy (`cmd/auth-proxy/`). Deployment MUST include an `initContainer` that waits for `http://ory-hydra-public.ory-system.svc.cluster.local:4444/health/ready` before starting the auth-proxy binary.
2. Verify auth-proxy pod reaches `Ready` state (confirms client pre-registration and JWKS fetch)
3. Deploy demo-echo service (`manifests/platform-identity/demo-echo/`)
4. Deploy AgentGateway
5. Verify OAuth metadata endpoints
6. Seed demo user via Kratos Admin API:
   ```bash
   curl -X POST http://kratos-admin.ory-system.svc.cluster.local:80/admin/identities \
     -H "Content-Type: application/json" \
     -d '{"schema_id": "default","traits": {"email": "demo@nutgraf.in","role": "tenant_admin"},"credentials": {"password": { "config": { "password": "Demo1Password!" } }}}'
   ```

### Phase 3: Integration (Day 1 Evening) (G-14 Resolution)
1. Configure Cursor MCP settings in `~/.cursor/mcp.json`:
   ```json
   {
     "servers": {
       "zero-ops": {
         "url": "https://api.nutgraf.in",
         "auth": {
           "type": "oauth2",
           "discovery_url": "https://api.nutgraf.in/.well-known/oauth-protected-resource"
         }
       }
     }
   }
   ```
2. Test PKCE flow end-to-end
3. Verify JWT validation via AgentGateway
4. Validate token storage in OS keychain (access_token, refresh_token, id_token)

### Phase 4: Demo (Day 2 Morning)
1. Run demo script with stakeholders
2. Show browser redirect to Kratos UI
3. Show JWT in keychain (display access_token)
4. Show authenticated status (criterion 9 ends at token storage)

## Future Enhancements (Out of Scope)

1. **Token Refresh (Req 14):** Transparent refresh on 401
2. **CIMD Support (Req 13):** Dynamic client metadata documents
3. **Authorization Enforcement (Req 3):** Keto permission checks
4. **Multi-Factor Authentication:** TOTP, WebAuthn via Kratos
5. **Social Login:** Google, GitHub via Kratos OIDC providers

---

*Implementation Note on Third-Party Clients:* The PKCE mechanics described herein (port priority `54321 → 18999 → 3000`, 5-minute timeout, and OS keychain storage) are based on the Model Context Protocol (MCP) reference implementations and expected behavior of Cursor. If the third-party client deviates from these standards, the platform remains secure, but user-facing error messages within the IDE may vary.
