# ADR-050: Tenant Authentication via AgentGateway

**Date:** 2026-08-19
**Status:** Proposed (under review — security contracts subject to implementation)
**Relates to:** ADR-009 (platform security architecture), ADR-010 (tenant user management), ADR-035 (enterprise PKI and delegated trust), ADR-041 (controller responsibility matrix)

---

> **Amendment 2026-08-18 — Security review: CHANGES REQUIRED.**
> The Case-2 architecture is **approved in principle**, but production-security
> sign-off is withheld until the P0 and P1 contracts in the "Security Contracts"
> section below are explicitly defined and enforced. The architecture is **not**
> redesigned (no auth-proxy in the request path, no browser-held tokens, no
> OIDC in the BFF). The gaps are contract and lifecycle definitions, several of
> which (audience restriction, BFF authorization, trusted-header enforcement,
> logout/revocation) are **security blockers**, not optional hardening.

---

## Context

Tenant frontend and BFF services on the platform were previously reachable without
authentication, except for legacy `tenant_id` query/hostname conventions. There was
no login page and no identity in the request path.

The platform authenticates users through the Ory stack in the hub:

- **Kratos** is the identity provider (identities carry `email`, `tenant_id`, `role`
  traits per ADR-010).
- **Hydra** is the OIDC/OAuth2 authorization server (issuer `https://auth.nutgraf.in`).
- **auth-proxy** is Hydra's `login`/`consent` provider: it bridges the Kratos session
  to Hydra challenges and injects identity claims (`email`, `role`) into tokens.

The hub already runs **AgentGateway** (`api.nutgraf.in`) as the platform edge gateway
with an `mcpAuthentication` policy (JWT validation against Hydra JWKS) for MCP clients.

A browser-facing authentication design was needed for tenant services that keeps a
clean trust boundary and does not put identity logic in tenant BFFs.

Tenant services integrate with platform auth primitives from `zero-ops-auth` for JWT
validation, OAuth delegation lifecycle, and tenant-scoped authorization middleware
rather than implementing these independently.

---

## Decision

### AgentGateway terminates tenant browser traffic and runs the OIDC flow

The hub AgentGateway is the browser-facing authentication/security gateway for
tenant services. Its native `oidc` policy (verified against the vendored official
codebase at `zero-ops/reference-projects/agentgateway`) performs the full browser
login:

- OIDC authorization-code + PKCE (`S256`) against Hydra
- `state`/`nonce`/CSRF validation, code exchange, ID-token validation against
  Hydra JWKS
- An encrypted, `HttpOnly` session cookie (`agw_oidc_s_*`) that carries the
  validated ID token

Hydra delegates its `login`/`consent` challenges to **auth-proxy**, which bridges
the Kratos session (ADR-010). `auth-proxy` is retained for this role
only and is **removed from the tenant request path**.

```
Browser → AgentGateway (oidc policy)
            ├── /oauth/callback          (gateway-native callback)
            ├── /api/*  → Tenant BFF     (validated ID token + claims)
            └── /*      → Tenant SPA
AgentGateway → Hydra (OIDC) → auth-proxy (login/consent) → Kratos
```

### Identity flows to the BFF as a validated ID token

AgentGateway establishes browser identity via OIDC authentication and forwards
the **validated ID token** to the BFF as a bearer credential:

```yaml
transformations:
  request:
    set:
      Authorization: "'Bearer ' + jwt.rawToken.unredacted()"
      X-Auth-Subject: "jwt.claims['sub']"
      X-Auth-Email: "jwt.claims['email']"
      X-Auth-Role: "jwt.claims['role']"
      X-Auth-Tenant: "jwt.claims['tenant_id']"
```

The `Authorization` header carries the validated ID token for independent BFF
validation. The `X-Auth-*` headers carry individual claims for convenience, but
the BFF's root of trust is the validated ID token, not the headers.

**Token semantics:**

```text
ID token:
    aud = <tenant>-public-client
    Purpose: establish browser identity only
    Forwarded to BFF via: Authorization: Bearer <ID token>

MCP access token:
    aud = <resource-server-audience>
    Purpose: authorize resource access
    Obtained via: BFF OAuth delegation (P0-4/P0-5)
```

The BFF validates the ID token independently using the `zero-ops-auth`
`JwtValidator` against Hydra JWKS, enforcing `iss=auth.nutgraf.in`,
`aud=<tenant>-public-client`, `exp`, and signature. The ID token is an identity
credential only and is never accepted as a delegated resource credential.

`tenant_id` is included in the token claims because the consent handler
(auth-proxy) copies the Kratos `tenant_id` trait into the ID/access token
session, alongside `email` and `role`.

### OAuth clients

**`<tenant>-public-client`** — PKCE-only public client (`token_endpoint_auth_method: none`):
- Registered by auth-proxy startup (Day-0).
- Redirect URI: `https://<tenant-host>/oauth/callback` (AgentGateway callback).
- Used by AgentGateway's OIDC policy for browser login.
- The resource-server audience is **server-selected, not request-selected**: the
  client and auth-proxy's consent handler never grant an audience supplied by the
  browser (see P0-1).

**`<tenant>-bff-client`** — confidential OAuth client (`client_secret_basic`):
- Registered by auth-proxy startup (Day-0); secret delivered via Infisical/ESO.
- Owned exclusively by the tenant BFF. auth-proxy holds the secret only for
  Day-0 client registration; no other platform component holds it.
- Used for delegated token lifecycle (authorization-code exchange, token refresh).

### Delegated-token lifecycle (Case 2 — approved)

AgentGateway's OIDC session persists only the ID token (upstream TODO: persist
`access_token`/`refresh_token`). It is therefore **not the owner of delegated
OAuth access/refresh tokens**. The tenant BFF owns the server-side delegated
token lifecycle:

- **Initiation:** `GET /api/v1/auth/oauth/start` — authenticated (via
  `x-auth-subject`) BFF requests redirect the browser to Hydra's
  authorization endpoint requesting `openid offline_access` and the **fixed
  resource-server audience** (server-selected; the browser cannot influence audience
  or scope, per P0-1). Hydra reuses the existing Kratos login via auth-proxy and
  prompts consent for the BFF's allowlisted scopes.
- **Callback:** `GET /api/v1/auth/oauth/callback` — exchanges the code at the
  token endpoint with the client secret, stores the access + refresh tokens
  **server-side** keyed by subject, and redirects the browser back to the SPA.
- **Use:** a BFF-side `delegatedTokenFor(subject)` helper returns a valid access
  token, refreshing on expiry (subject-scoped, never client-visible).

Implementation uses `zero-ops-auth`: `OidcClient` for
authorization/code-exchange/refresh, `RedisTokenStore` for server-side token
persistence, `JwtValidator` for ID token validation, `requireTenantBoundary` and
`requireRole` for authorization middleware.

### Platform auth package (`zero-ops-auth`)

> **Amendment 2026-08-19 — platform auth package.** The platform authentication
> primitives are delivered as a **standalone, unscoped npm package
> `zero-ops-auth`**, not as a Go in-process library under
> `opensbt/libraries/`. The package lives at `zero-ops/packages/auth/`, is
> published to the npm registry via CI on push to `main` (version-check
> pattern, mirroring `uds/.github/workflows/ci-packages.yml`), and is consumed
> by tenant services as a normal npm dependency.

`zero-ops-auth` provides **platform primitives**, not authorization policy. It
must not become a generic authorization server abstraction.

**Package contents:**

```text
jwt/    JwtValidator        JWKS-based JWT validation (RS256+ES256), issuer/audience enforcement
        JwksCache           JWKS fetch + per-key TTL + refresh rate limiting
middleware/ authMiddleware  Hono middleware: extract Bearer, validate, inject principal
oauth/  OidcClient          Discovery, authorization URL (PKCE), code exchange, refresh, revoke
        TokenStore          Pluggable token store interface
        RedisTokenStore     Redis-backed implementation (included in core)
authz/  requireTenantBoundary  principal.tenantId == resource.tenantId (P0-2 Layer 1)
        requireRole            role/capability check (P0-2 Layer 2)
        AuthenticatedPrincipal  subject, tenantId, roles, issuer
```

**OAuth lifecycle responsibility:**

```text
BeginAuthorization
CompleteAuthorization
StoreDelegatedGrant
DelegatedTokenFor
RefreshDelegatedToken
RevokeDelegatedGrant
```

**OAuth client consumes explicit configuration:**

```text
issuer
client_id
client authentication
redirect URI
allowed audiences
allowed scopes
token endpoint
```

**Application supplies the authorization policy; the package supplies the OAuth
mechanics.** This preserves the established boundary: platform provides
primitives; application owns policy.

**Design constraints:**

- The package must not introduce an authorization server or policy engine.
- JWT validation is JWKS-based with configurable issuer and audience enforcement.
- The `TokenStore` interface is pluggable; `RedisTokenStore` is provided as a
  default implementation.
- Authorization middleware (`requireTenantBoundary`, `requireRole`) operates on
  `AuthenticatedPrincipal` and does not depend on any specific identity provider
  beyond the claims structure.
- The package is framework-specific to Hono (the BFF framework used across tenant
  services). A future version may abstract the middleware layer.

### Boundary model (final)

Four distinct boundaries, each with a single owner:

| Boundary | Owner | Responsibilities |
|----------|-------|------------------|
| Browser authentication | AgentGateway | OIDC login + PKCE, HttpOnly encrypted session, ID-token validation, trusted header transformation, MCP authentication, gateway-level CEL authorization (supplementary) |
| Identity verification + application authorization + delegated credentials | Tenant BFF | Independent ID-token validation (`zero-ops-auth` JwtValidator), tenant boundary enforcement, operation authorization (P0-2), confidential OAuth client, authorization-code exchange, server-side access/refresh token lifecycle (refresh/expiry handling), server-side token use |
| Identity / token control plane | Ory (Kratos → auth-proxy → Hydra) | Identity, login/consent (with audience/scope allowlist enforcement, P0-1), token issuance |
| Platform authorization primitives | `zero-ops-auth` (npm package, `zero-ops/packages/auth/`) | Canonical JWT validation (`JwtValidator`), reusable OAuth lifecycle (`OidcClient`, `TokenStore`/`RedisTokenStore`), `TenantBoundary` + `requireRole` authorization middleware, with explicit configuration: issuer, client_id, allowed audiences, allowed scopes |

**Component roles:**

```text
AgentGateway       = browser authentication + ID token validation
auth-proxy         = Hydra login/consent adapter + client registration
Hydra              = OAuth/OIDC issuer
Kratos             = identity authority
Keto               = available platform authorization mechanism (not required by default)
zero-ops-auth      = npm package (JwtValidator + OidcClient + TokenStore + authorization middleware)
Tenant BFF         = independent identity verification + application authorization + delegated credential ownership
```

The tenant BFF is **not an identity-authentication boundary**. It is an
**application authorization boundary and delegated-credential boundary**.
AgentGateway establishes authenticated identity; the BFF independently verifies
the ID token and enforces application authorization using the authenticated
principal and resource context (P0-2). Only AgentGateway performs browser
authentication.

### Credential ownership model

The `<tenant>-bff-client` confidential OAuth client has a single credential that
**belongs exclusively to the tenant BFF**. auth-proxy's role is limited to
registering the client in Hydra (Day-0). It does not act as an OAuth consumer
for the tenant and is not a second holder of the client for any request-path use;
the secret is delivered to the BFF via Infisical/ESO and to auth-proxy only so
registration matches what the BFF holds. No other platform component holds the
BFF's client credential.

### Security enforcement

- **No alternate auth path:** the tenant BFF must not be directly reachable as
  a public endpoint. Its only ingress is through the hub AgentGateway; this is
  enforced at the ingress/DNS level (hostnames terminate at the hub) **and** by
  removing any spoke-gateway HTTPRoute that maps the public hostnames to the
  BFF/frontend services. The spoke services are reachable only in-cluster and
  via MCS export from the hub.
- **Cryptographic credential trust:** AgentGateway forwards the validated ID
  token as `Authorization: Bearer <ID token>` via transformation. The JWT
  policy removes the client-supplied `Authorization` header before the
  transformation runs (`jwt.rs:596-600`), and `OverwriteIfExistsOrAdd`
  (`transformation_cel.rs:352-354`) unconditionally overwrites any existing
  value. The BFF independently validates this token using the `zero-ops-auth`
  `JwtValidator` against Hydra JWKS. No NetworkPolicy-only trust boundary is required.
- **Header trust:** AgentGateway `transformations.request.set` overwrites
  client-supplied headers with gateway-validated claims. The transformation
  also sets `X-Auth-*` headers from ID token claims for convenience, but the
  BFF's root of trust is the validated ID token, not the headers.
- **Authentication ≠ authorization:** gateway-established identity (via validated
  ID token) is not authorization. Every tenant-scoped BFF operation derives
  authorization context from the authenticated principal and independently
  verifies the requested resource belongs to that tenant (P0-2). Two-layer
  authorization: mandatory tenant boundary + operation-specific role/capability
  where required.
- **ID token is identity-only:** The BFF's inbound ID-token credential is accepted
  only for establishing the authenticated principal. It must never be copied,
  forwarded, exchanged implicitly, or used as the bearer credential for a
  downstream resource server. Downstream OAuth credentials must originate
  exclusively from the BFF's delegated-token lifecycle.

### Alternatives considered

- **BFF-held session cookie proxied through auth-proxy** — rejected: puts
  auth-proxy in the tenant request path and duplicates what AgentGateway does.
- **Browser-held tokens (SPA store)** — rejected: exposes tokens to XSS.
- **Route the web UI through the `/mcp` resource-server path** — rejected:
  `/mcp` is for MCP clients, not browser sessions.
- **Remove auth-proxy entirely** — rejected: AgentGateway is an OIDC relying
  party, not a Hydra `login`/`consent` provider. Hydra still requires a
  login/consent app; auth-proxy remains in that role. Full removal would require
  replacing the Ory stack (ADR-009) or shipping a thin replacement.
- **Case 1 (AgentGateway owns delegated tokens via upstream token persistence)** —
  rejected as a current dependency: upstream AgentGateway does not yet persist
  access/refresh tokens, and coupling the BFF to that pending upstream change
  would block tenants. Revisit when upstream lands.
- **Case 3 (weaken the MCP resource-server boundary)** — rejected: it would
  remove JWT validation from `/mcp`.
- **Keep the in-memory `DelegatedTokenStore`** — rejected: an in-memory map does
  not survive restarts and is not shared across replicas, so a BFF rollout would
  silently revoke delegated access. Superseded by the Redis-backed store.
- **A separate BFF-owned Redis instance** — rejected: the fleet already runs
  Redis for shared infrastructure; a second instance adds operational surface for
  no isolation benefit when ACL users + namespaced keys already separate consumers.
  If stricter tenancy is ever required, move to separate logical
  credentials/instances rather than shared `default`-user access.

---

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| `zero-ops-auth` npm package | npm registry | zero-ops platform team | CI publish workflow (`.github/workflows/publish-auth.yml`) | Tenant services | Day-0 |
| OIDC browser session (AgentGateway cookie) | AgentGateway | platform-edge | AgentGateway | Tenant BFF/SPA | Day-1+ |
| `<tenant>-public-client` OAuth client | Hydra | auth-proxy (registration) | auth-proxy startup | AgentGateway `oidc` policy | Day-0 |
| `<tenant>-bff-client` confidential OAuth client | Hydra | auth-proxy (registration) | auth-proxy startup | Tenant BFF | Day-0 |
| Delegated OAuth access/refresh tokens (user) | Fleet Redis (tenant-namespaced, fleet-owned) | Tenant BFF | Tenant BFF token lifecycle | Tenant BFF server-side calls | Day-1+ |
| Identity claims (`email`, `role`, `tenant_id`) | Kratos traits | auth-proxy (consent injection) | auth-proxy consent handler | AgentGateway → BFF | Day-1+ |
| Tenant hostnames | DNS | hub ingress | external-dns | hub AgentGateway | Day-1+ |

---

## Production Hardening Checklist

The Case-2 architecture is approved. The following items are **required before
a production-security sign-off** and are tracked here:

1. **Separate OAuth client credentials** — the `<tenant>-bff-client` secret
   belongs exclusively to the BFF; auth-proxy holds it only for Day-0 client
   registration. Confirmed by design; rotate/verify delivery so no other
   component retains it.
2. **Durable, shared, encrypted token store** — shared fleet Redis
   (tenant-namespaced). Any BFF replica serves any subject, rollouts/restarts do
   not revoke delegated access, and refresh state survives rescheduling. The store
   is subject-scoped with TTLs on token/flow/lock records. Transport is TLS-only.
3. **Atomic refresh-token rotation** — when Hydra rotates refresh tokens, the
   replacement must be stored atomically with the access token. A `SET NX PX`
   cross-replica mutex serializes concurrent refresh for a subject.
4. **Explicit multi-replica behavior** — with the shared Redis store, no sticky
   sessions or per-replica routing guarantees are needed; the mutex makes refresh
   rotation safe across replicas.
5. **Direct-BFF-network-path denial** — no spoke HTTPRoute (or any other route)
   may map public tenant hostnames to the BFF/frontend outside the hub
   AgentGateway. Enforced by removing the spoke HTTPRoutes and by DNS
   termination at the hub.
6. **Negative tests for forged `X-Auth-*` headers** — prove that
   `X-Auth-Subject: attacker` / `X-Auth-Tenant: victim` sent by a client cannot
   override AgentGateway identity.
7. **Integration test: expired token → refresh → resource call** — cover the normal
   flow (login → code → exchange → store → resource), the expired-token refresh
   path, refresh rotation, and concurrent-request refresh.
8. **Logout / revocation semantics** — define and implement what happens on
   logout and on Hydra-side token revocation (BFF must delete the subject's
   stored tokens; the AgentGateway OIDC session is independent).

---

## Security Contracts (Amendment 2026-08-18)

These contracts are **mandatory ADR amendments**, not implementation TODOs.
P0 items block approval; P1 items are required hardening. Unless a contract is
already enforced by a listed component, it must be enforced before
production-security sign-off.

### P0-1: Fixed OAuth audience/scope authorization contract

Each tenant's BFF client is restricted to an explicit allowlist. The browser
must never be able to turn `GET /api/v1/auth/oauth/start?audience=<other>` into
a delegated credential for another platform resource, and auth-proxy's consent
logic must not trust an arbitrary `audience` supplied by the OAuth client.

```text
<tenant>-bff-client
    allowed audiences:
        <resource-server-audience>
    allowed scopes:
        openid
        offline_access
        <explicit scopes>
    forbidden:
        arbitrary audience supplied by browser
        arbitrary scopes supplied by browser
```

The full OAuth client authorization contract:

- exact allowed audiences (resource-server audience is **server-selected**, fixed
  server-side; not request-selected)
- exact allowed scopes
- whether `offline_access` is permitted
- exact redirect URIs
- exact grant types
- exact response types
- whether refresh-token rotation is mandatory

**Enforcement:** Hydra client config + auth-proxy consent handler
(allowlist check, reject unknown audience/scope). AgentGateway enforces the
resource side: `mcpAuthentication.audiences` is fixed server-side, and the
id-token `aud` must equal the gateway `client_id`.

### P0-2: Two-layer authorization model

Authentication and authorization are distinct. AgentGateway establishes
authenticated identity via ID token validation; that is identity propagation,
not authorization. `tenant_id` being a trusted claim is not itself
authorization, and `role` being a trusted claim does not define what the role
can do.

**Two-layer authorization contract:**

```text
AuthenticatedPrincipal
        │
        ├── Layer 1: Tenant Boundary (mandatory)
        │      principal.tenantId == resource.tenantId
        │      Enforced on: all tenant-scoped operations
        │      Failure: 403 Forbidden
        │
        └── Layer 2: Operation Authorization (where required)
               role/capability required by operation
               Enforced on: destructive/admin operations
               Failure: 403 Forbidden
```

**Initial policy matrix:**

```text
operation                    requirement
────────────────────────────────────────────────
read tenant resource         tenant match only
list tenant resources        tenant match only
modify tenant resource       tenant match + role check
delete tenant resource       tenant match + admin capability
admin/platform operations    explicit admin capability
```

**Invariant:** every tenant-scoped BFF operation derives authorization context
from the gateway-authenticated identity and independently verifies the requested
resource belongs to that tenant. A tenant-A admin reaching
`GET /api/v1/spokes/tenant-B/...` must be denied by the BFF's authorization
decision, not by claim presence.

**Enforcement:** BFF application layer using `AuthenticatedPrincipal`,
`TenantBoundary`, and `AuthorizationPolicy` compositions from `zero-ops-auth`.
Where gateway-level tenant gating is desired, AgentGateway CEL `authorization`
policy can evaluate `request.method`, `request.path`, and `jwt.*` claims — but
gateway CEL is supplementary, not a substitute for the BFF authorization decision.

**Keto scope:** Tenant services do not require Keto for their current application
authorization contract. Keto remains available for platform authorization use
cases where relationship-based authorization is required.

### P0-3: Gateway → BFF credential trust contract

The BFF does not trust arbitrary `X-Auth-*` headers. Instead, AgentGateway
forwards its **validated ID token** over the private Gateway→BFF route, and the
BFF independently validates that token.

**Credential flow:**

```text
AgentGateway → BFF:
    Credential: ID token (validated by AgentGateway OIDC policy)
    aud = <tenant>-public-client
    Purpose: establish browser identity only
    Header: Authorization: Bearer <ID token>
    Security: AgentGateway JWT policy removes client-supplied Authorization;
              transformation OVERWRITES with validated token (OverwriteIfExistsOrAdd);
              no code path for client header to survive

BFF receives:
    Validates: iss=auth.nutgraf.in, aud=<tenant>-public-client, exp, signature (JWKS)
    Constructs: AuthenticatedPrincipal { subject, tenantId, role, issuer }
    Does NOT: accept ID token as delegated resource credential
    Does NOT: forward ID token downstream to resource server

BFF → Resource Server:
    Credential: delegated access token (from zero-ops-auth OidcClient)
    aud = <resource-server-audience>
    Purpose: authorize resource access
    Lifecycle: managed by BFF OAuth delegation (P0-4/P0-5)
```

**Token semantics invariant:**

> The ID token is an identity credential only. The BFF must never accept it as
> a delegated resource credential. The BFF's resource-server access token is a
> separate credential obtained through OAuth delegation with a distinct audience.

**Security properties:**

- AgentGateway's JWT policy **removes** the client-supplied `Authorization`
  header (`jwt.rs:596-600`) before the transformation runs.
- The transformation `set` uses `OverwriteIfExistsOrAdd`
  (`transformation_cel.rs:352-354`), which always calls `headers().insert()`,
  unconditionally replacing any existing value.
- If the CEL expression evaluates to `None` (failure), the header is removed
  entirely (`mod.rs:178-180`), so a stale client header cannot remain.
- The transformation-set `Authorization` header is forwarded to the upstream
  backend (not in the hop-by-hop strip list at `httpproxy.rs:3785-3796`).
- The BFF validates the ID token against Hydra JWKS independently, using
  the `zero-ops-auth` `JwtValidator` with exact audience enforcement.

**Enforcement:** AgentGateway transformation policy + BFF `zero-ops-auth`
`JwtValidator` validation. No NetworkPolicy-only trust boundary.

### P0-4: Delegated-token lifecycle identity

Tenant BFF token stores must not implicitly equate `subject` with
"authorization context". The grant granularity is declared explicitly. One
delegated credential per `subject` is acceptable **only if** declared as a
security invariant with defined consequences. If per-session or per-context
semantics are chosen, the store key must reflect that granularity.

Defined consequences (to be documented and enforced):

- behavior of all grants for a subject when one session logs out
- concurrent refresh from multiple sessions of the same subject
- behavior when a user's tenant or role changes after token issuance (see P0-5)

### P0-5: Logout and revocation semantics

The AgentGateway OIDC session and the BFF delegated OAuth grant are independent
and must be reconciled:

- **Browser logout:** AgentGateway session terminated AND BFF delegated grant
  revoked/deleted. **Note:** AgentGateway currently has no on-demand logout
  endpoint (`clear_cookie` is only invoked for the transaction cookie,
  `callback.rs`); this is an upstream gap — browser logout is bounded by session
  TTL until a gateway logout mechanism exists.
- **Identity revocation:** Kratos identity/session revoked → Hydra token
  invalid → BFF refresh/access token invalidated.
- **Role/tenant change:** existing delegated tokens must not silently retain the
  prior authorization context; policy on token continuation vs forced
  re-delegation is explicit.

### P1-1: OAuth state → session binding

`<tenant>-bff` OAuth flow state must be bound to the authenticated session. The
flow record contains at minimum: `state_hash`, `subject`, session identifier,
`code_verifier`, `redirect_uri`, `created_at`, `expires_at`. The callback
rejects if the current authenticated identity differs from the identity that
initiated the flow.

### P1-2: Refresh-rotation failure state machine

`SET NX PX` is a lock-acquisition primitive, not a complete rotation protocol.
The documented state machine defines: lock ownership/token, lock expiry, safe
release, retry behavior, refresh-token invalidation, behavior when refresh
succeeds but the Redis write fails (replica A crashes after Hydra rotation),
`invalid_grant` handling, and deletion of the delegated grant after an
unrecoverable refresh failure.

### P1-3: Redis bearer-token threat model

Explicit statement of: Redis persistence disabled, backups disabled, token TTL
bounded, refresh-token lifetime bounded by Hydra. If Redis persistence is
enabled, encryption-at-rest is required. This is a bearer-token threat-model
decision (TLS + ACL protect transport and access, not Redis compromise, memory
dump, backup exposure, or misconfiguration).

### P1-4: Cookie/session security contract

The AgentGateway OIDC session cookie is: `Secure` (true over TLS),
`HttpOnly`, host-only (no `Domain` attribute), `Path=/`, `SameSite=Lax`,
`Max-Age` bounded by TTL, session expiry capped at min(id-token `exp`, TTL).
**Gap:** no cookie key rotation — `OIDC_COOKIE_SECRET` is a single AES-256-GCM
key with no old-key acceptance; rotation invalidates all sessions. Behavior when
the AgentGateway session expires while the BFF delegated token remains valid is
explicit (delegated token outlives the browser session by design, bounded by
P0-4/P0-5).

### P1-5: Failure-closed behavior

Explicit per-dependency semantics:

- **AgentGateway unavailable:** fail closed.
- **Hydra unavailable:** new login — reject; existing authenticated session —
  defined (gateway behavior; bounded by session TTL).
- **Redis unavailable:** BFF serves existing requests but fails all delegated
  resource-server calls (fail closed for delegation).
- **auth-proxy unavailable:** no new Hydra login/consent; existing Hydra
  sessions defined by Hydra.
- **MCS unavailable:** BFF inaccessible (fail closed).

### P1-6: Security audit events

Explicit audit events for: OIDC login success/failure, OIDC callback failure,
BFF delegated authorization initiated/completed, delegated token refresh,
refresh failure, delegated token revoked, logout, authorization denied, tenant
mismatch, role authorization denied.

**Never logged:** `access_token`, `refresh_token`, `authorization_code`,
`code_verifier`, `cookie`, `client_secret`.
**Logged identifiers:** subject, tenant_id, client_id, audience, scope,
request/correlation ID, timestamp, outcome.

**Gap:** AgentGateway's OIDC module emits `debug!` traces only (no structured
login/callback/revocation audit events) — upstream requirement; tenant BFFs must
provide their own audit trail for delegated lifecycle events regardless.

### P1-7: Local development authentication

Local dev auth is **explicit opt-in** via environment variable (or equivalent).
"Gateway absent → assume identity" is prohibited; production configuration fails
closed when the identity source is missing or misconfigured.

### Verified AgentGateway capability gaps (upstream requirements)

Recorded as upstream requirements, not accepted risk:

1. No on-demand logout endpoint (session cleared only via TTL/expiry).
2. No OIDC cookie key rotation (single AES-256-GCM key).
3. No claims revalidation within session lifetime (role/tenant changes apply
   only at expiry/TTL).
4. No structured OIDC audit events (debug traces only).
5. JWKS is static — loaded once at startup, no refresh on unknown `kid`;
   Hydra signing-key rotation requires gateway restart.

---

## Consequences

### Positive

- Single browser-facing gateway: AgentGateway owns OIDC, PKCE, session, and
  claim validation for all tenant services.
- Defense-in-depth trust: AgentGateway validates the ID token at the edge, and
  the BFF independently re-validates it using the `zero-ops-auth` `JwtValidator`.
  No single point of trust failure.
- Clean separation: AgentGateway handles browser authentication; the BFF handles
  identity verification, application authorization, and delegated credentials.
- auth-proxy stays exactly where it is needed (Hydra login/consent) and leaves
  the tenant request path entirely.
- Tenant identity comes from the verified Kratos trait (`tenant_id` claim), not
  from URL parsing, closing spoofable `?tenant_id=` conventions.
- No SPA-held tokens; XSS cannot exfiltrate a bearer credential.
- Delegated OAuth tokens never reach the browser: the BFF holds them server-side,
  so a compromised browser session cannot exfiltrate a resource-server access token.
- Explicit token semantics: ID token (identity) and resource-server access token
  are distinct credentials with distinct audiences, preventing token confusion
  attacks.
- Reusable platform auth primitives (`zero-ops-auth`): consistent JWT validation,
  OAuth lifecycle, and authorization middleware across all tenant services.

### Negative

- Cross-cluster prerequisite: AgentGateway (hub) reaches tenant BFF/SPA
  (spoke) only via Multi-Cluster Service export, which is not yet wired.
- Two OAuth clients are required per tenant (`<tenant>-public-client` for the
  AgentGateway browser session, `<tenant>-bff-client` for the BFF's delegated
  token lifecycle), so the user completes an additional consent step for the BFF.
- The BFF owns a security-critical component (token storage/refresh). Token
  material is stored in fleet Redis (tenant-namespaced) with TTLs and is
  never client-visible; refresh rotation is serialized with a cross-replica
  mutex. Redis is TLS-only with ACL-scoped users.
- The shared fleet Redis has multiple consumers; they are isolated by ACL users
  and key namespaces, so neither can read or modify the other's data. This is a
  new operational dependency for the BFF: delegated-token issuance requires Redis
  availability.
- Frontend development identity is assumed locally; any local auth bugs are
  masked until deployed behind the gateway. Local dev auth is explicit opt-in
  and fail-closed in production (P1-7).
- **Upstream AgentGateway gaps** (P0-5, P1-4, P1-6): no on-demand logout, no
  session-cookie key rotation, no claims revalidation, no structured OIDC audit
  events, and static JWKS (signing-key rotation requires gateway restart).
  These are recorded as upstream requirements and are not accepted risk; until
  logout exists, the browser session terminates by TTL/expiry only.

---

## Impact

- Establishes AgentGateway as the single browser-facing authentication gateway
  for all tenant services.
- Tenant BFFs are now reachable only through the hub AgentGateway OIDC session
  rather than directly via the spoke gateway.
- Adds a delegated-token component to tenant BFFs: a confidential OAuth
  client plus server-side token lifecycle. This is a new, explicit security
  component of the BFF, not inherited from AgentGateway.
- Introduces `zero-ops-auth` as the canonical platform auth library for tenant
  services. Tenant services should adopt `zero-ops-auth` rather than
  implementing JWT validation, OAuth lifecycle, or authorization middleware
  independently.
- No Ory component is removed. auth-proxy is redeployed in a reduced role
  (Hydra login/consent adapter only). Retiring auth-proxy's request-time JWT
  validation, and eliminating auth-proxy entirely, is a **separate identity-plane
  migration** (ADR-009 scope) and is not a prerequisite for tenant services.
- **Amendment 2026-08-18** adds the P0/P1 security contracts (audience/scope
  allowlist, two-layer BFF authorization model, cryptographic credential trust
  between Gateway and BFF, delegated-token lifecycle identity, logout/revocation,
  and hardening items). These do not alter the approved architecture; they define
  mandatory invariants and lifecycle semantics required before production-security
  sign-off.
- **Amendment 2026-08-19** names the platform auth delivery mechanism: the
  `zero-ops-auth` npm package (`zero-ops/packages/auth/`, published via CI)
  replaces the earlier `opensbt/libraries/` in-process-library framing for
  JWT validation, OAuth lifecycle, and authorization primitives. The security
  contracts are unchanged; the implementation surface for tenant BFFs is the
  npm package.

---

## References

- ADR-009: Platform Security Architecture (Ory stack)
- ADR-010: Tenant User Management Pattern (Kratos traits)
- ADR-035: Enterprise PKI and Delegated Trust via Infisical OSS
- ADR-041: Controller Responsibility Matrix
- `zero-ops/packages/auth/`: `zero-ops-auth` package source
- `zero-ops/.github/workflows/publish-auth.yml`: CI publish workflow
- `uds/.github/workflows/ci-packages.yml`: reference CI pattern
- AgentGateway `oidc` policy: `zero-ops/reference-projects/agentgateway`
  (`crates/agentgateway/src/http/oidc/`, `examples/traffic-oidc`)
