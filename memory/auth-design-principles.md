---
purpose: Authentication and authorization design patterns and principles
scope: OAuth, JWT, OIDC, enterprise auth patterns, Ory stack decisions
topics: [oauth-patterns, jwt-validation, client-registration, custom-claims, service-architecture]
update_criteria: Auth pattern decisions, security principle changes, Ory stack configuration updates
---

# Authentication & Authorization Design Principles

## OAuth Metadata Endpoints
- ❌ Don't proxy OAuth metadata through AgentGateway
- ✅ OAuth metadata must come from Hydra directly
- Endpoints belong to Hydra: `/.well-known/openid-configuration`, `/oauth2/token`, `/oauth2/auth`

## Token Validation (Enterprise Pattern)
- ❌ Avoid token introspection for every request (network roundtrip, latency)
- ✅ Use JWKS JWT verification (local validation, better performance, scalable)
- AgentGateway should: verify JWT locally using Hydra `/.well-known/jwks.json`
- Introspection only for: revocation checks, special cases

## OAuth Client Registration (Enterprise Pattern)
- ❌ hydra-maester (deprecated, not recommended for new platforms)
- ✅ Manage clients via Hydra Admin API
- ✅ Use platform operator/automation service (e.g., identity-service calls Hydra Admin API)
- Benefits: Better lifecycle control, auditing, enterprise-grade

## Custom JWT Claims (Enterprise Pattern)
- ❌ Token webhook at issuance time (runtime dependencies, non-deterministic)
- ✅ Inject claims during OIDC login/consent flow
- ✅ Claims set during user authentication, not token generation
- Benefits: Deterministic tokens, fewer runtime dependencies

## CNPG for Identity Stack
- ✅ Single CNPG cluster with multiple databases via Database CRD
- ✅ Separate databases per component: `hydra_db`, `kratos_db`, `keto_db`
- Benefits: Cost-efficient, simpler operations, enterprise-grade

## PostgREST with Ory Stack
- ❌ Avoid exposing database through PostgREST with Ory stack
- Reason: Kratos/Hydra already expose APIs, increases attack surface and operational complexity
- ✅ Use PostgREST only when: auto-generated REST APIs needed for custom app databases

## Service Architecture

### AgentGateway Role
- ✅ AgentGateway only validates tokens (via JWKS JWT verification)
- ❌ AgentGateway does NOT need Kratos Admin API access
- ❌ AgentGateway does NOT proxy OAuth endpoints

### auth-proxy Scope
- ✅ Thin orchestration layer for: onboarding, user management, org management
- ✅ Calls Hydra Admin API for client lifecycle
- ✅ Owns login/consent endpoints
- ❌ Not a full auth provider (delegate to Ory stack)

## Enterprise Architecture

```
Cursor/Goose
    │
    ▼
AgentGateway (verify JWT via JWKS)
    │
    ▼
Ory Hydra (OAuth/OIDC)
    │
    ▼
Ory Kratos (Identity)
    │
    ▼
Ory Keto (Authorization)
    │
    ▼
CloudNativePG Cluster
```

## Demo 1 Enterprise Decisions

| Component | Enterprise Choice |
|-----------|------------------|
| Hydra clients | Admin API (not hydra-maester) |
| Token validation | JWKS JWT verification |
| DB topology | CNPG cluster with Database CRD |
| Namespaces | Multiple (`ory-system`, `identity-services`, `api-gateway`) |
| Custom claims | Login/consent flow injection |
| PostgREST | Avoid (Ory APIs sufficient) |

## Implementation Cautions

### JWKS Caching
- Cache JWKS with 5-minute TTL
- **CRITICAL**: Refresh on key-id mismatch to handle key rotation
- Prevents token validation failures during Hydra key rotation

### Consent Service Ownership
- auth-proxy owns the consent endpoint (not Hydra directly)
- Flow: `Hydra → redirect to consent app → auth-proxy evaluates → acceptOAuth2ConsentRequest`
- Keeps business logic outside Hydra
- Allows custom consent rules per tenant/organization
