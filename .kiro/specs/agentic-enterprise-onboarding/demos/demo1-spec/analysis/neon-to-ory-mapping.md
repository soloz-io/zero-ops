# Neon Auth to Ory Stack Integration Pattern Analysis

## Executive Summary

The identity-service with Neon Auth provides a clear reference architecture for implementing OAuth2 authentication with AgentGateway. The pattern should be replicated with Ory stack (Hydra + Kratos + Keto) replacing Neon Auth.

## Architecture Pattern

### Current (Neon Auth)
```
MCP Client (Cursor/Goose)
    ↓
AgentGateway (JWT validation, resource metadata)
    ↓
identity-service (abstraction layer)
    ↓
Neon Auth (OAuth2 provider)
```

### Target (Ory Stack)
```
MCP Client (Cursor/Goose)
    ↓
AgentGateway (JWT validation, resource metadata)
    ↓
auth-proxy (abstraction layer - replaces identity-service)
    ↓
Ory Hydra (OAuth2/OIDC)
Ory Kratos (Identity)
Ory Keto (Authorization)
```

## Key Components Mapping

### 1. identity-service → auth-proxy

**Current Role (identity-service):**
- Abstracts Neon Auth REST API
- Handles OAuth flow initiation
- Processes OAuth callbacks
- Manages session creation
- Provides JWT minting
- Exposes validation endpoints

**Target Role (auth-proxy):**
- Abstract Ory stack (Hydra/Kratos/Keto)
- Own login/consent endpoints (Hydra redirects here)
- Handle OAuth callbacks from Hydra
- Inject custom JWT claims during consent
- Proxy OAuth metadata to AgentGateway
- Manage OAuth client lifecycle via Hydra Admin API

**Technology:**
- Current: TypeScript/Fastify
- Target: Go (lightweight, minimal dependencies per project-structure.md)

### 2. NeonAuthClient → Ory Admin API Clients

**Current (NeonAuthClient):**
```typescript
class NeonAuthClient {
  signInWithSocial(provider, callbackURL)
  validateSessionVerifier(verifier)
  signOut()
  healthCheck()
}
```

**Target (Ory Clients in auth-proxy):**
```go
// Hydra Admin API Client
type HydraClient struct {
  AcceptLoginRequest(challenge, subject)
  AcceptConsentRequest(challenge, grantScope, session)
  GetOAuthClient(clientID)
  CreateOAuthClient(client)
}

// Kratos Admin API Client
type KratosClient struct {
  GetIdentity(identityID)
  UpdateIdentityTraits(identityID, traits)
  CreateIdentity(traits)
}

// Keto API Client
type KetoClient struct {
  CheckPermission(namespace, object, relation, subject)
  CreateRelationTuple(tuple)
}
```

### 3. Session Management Pattern

**Current Flow:**
1. User authenticates via Neon Auth
2. identity-service receives sessionVerifier
3. Validates verifier with Neon Auth
4. JIT provisions user in PostgreSQL
5. Creates platform session in Redis
6. Mints Platform JWT
7. Caches JWT in Redis

**Target Flow (SAME PATTERN):**
1. User authenticates via Kratos (through auth-proxy)
2. Hydra redirects to auth-proxy consent endpoint
3. auth-proxy fetches identity from Kratos
4. JIT provisions user in PostgreSQL (SAME)
5. Creates platform session in Redis (SAME)
6. Injects claims into Hydra consent acceptance
7. Hydra issues JWT with custom claims
8. Caches JWT in Redis (SAME)

**Key Insight:** Session management, JWT caching, and database provisioning remain IDENTICAL. Only the OAuth provider changes.

## Critical Integration Points

### 1. OAuth Metadata Discovery

**Current (identity-service):**
```typescript
// Proxies Neon Auth metadata
GET /.well-known/oauth-authorization-server
→ Fetches from Neon Auth
→ Returns to AgentGateway
```

**Target (auth-proxy):**
```go
// Proxies Hydra metadata
GET /.well-known/oauth-authorization-server
→ Fetches from Hydra Public API
→ Returns to AgentGateway
```

### 2. Login Flow

**Current:**
```
GET /auth/login/google
→ identity-service calls NeonAuthClient.signInWithSocial()
→ Redirects to Neon Auth OAuth endpoint
```

**Target:**
```
GET /auth/login
→ auth-proxy displays login UI
→ User submits credentials
→ auth-proxy validates via Kratos
→ auth-proxy calls Hydra acceptLoginRequest
→ Hydra redirects to consent
```

### 3. Consent Flow (NEW - Ory specific)

**Ory Hydra Requirement:**
```
Hydra → auth-proxy /consent?consent_challenge=...
→ auth-proxy fetches identity from Kratos
→ auth-proxy displays consent UI (or auto-accepts)
→ auth-proxy calls Hydra acceptConsentRequest with session:
  {
    "session": {
      "id_token": { "tenant_id": "...", "email": "...", "role": "..." },
      "access_token": { "tenant_id": "..." }
    }
  }
→ Hydra issues JWT with custom claims
```

**This is the KEY difference:** Neon Auth doesn't have explicit consent flow. Ory Hydra requires it for custom claim injection.

### 4. Callback Handling

**Current:**
```typescript
GET /auth/callback?sessionVerifier=...
→ identity-service validates verifier with Neon Auth
→ Extracts user data
→ JIT provisions user
→ Creates session
→ Mints JWT
```

**Target:**
```go
GET /auth/callback?code=...&state=...
→ auth-proxy exchanges code for tokens (Hydra)
→ Hydra returns JWT with custom claims (already injected during consent)
→ auth-proxy extracts claims from JWT
→ JIT provisions user (SAME)
→ Creates session (SAME)
```

## Database Schema (UNCHANGED)

The PostgreSQL schema from identity-service remains IDENTICAL:
- `users` table (id, external_id, email, default_org_id)
- `organizations` table (id, name, slug)
- `memberships` table (user_id, org_id, role, version)
- `api_tokens` table (for CLI/API access)

**Mapping:**
- `users.external_id` = Kratos identity ID (was Neon Auth user ID)
- All other fields remain the same

## Redis Caching (UNCHANGED)

Session and JWT caching patterns remain IDENTICAL:
- `session:{sessionId}` → session data
- `jwt:{sessionId}:{orgId}` → cached Platform JWT
- `permission:{userId}:{orgId}` → cached permissions

## AgentGateway Integration

**Current:**
```yaml
# AgentGateway config for Neon Auth
mcpAuthentication:
  issuer: https://neon-auth.example.com
  jwksUrl: https://neon-auth.example.com/.well-known/jwks.json
  audience: https://api.zero-ops.io
```

**Target:**
```yaml
# AgentGateway config for Ory Hydra
mcpAuthentication:
  issuer: https://auth.zero-ops.io  # Hydra issuer
  jwksUrl: https://auth.zero-ops.io/.well-known/jwks.json  # Hydra JWKS
  audience: https://api.zero-ops.io
```

**Key Point:** AgentGateway configuration is IDENTICAL. It doesn't know or care whether the OAuth provider is Neon Auth or Ory Hydra.

## Test Pattern Replication

### Integration Test Structure (SAME)

**Current (test_e2e_authentication.ts):**
1. Create test user in PostgreSQL
2. Initialize production services (SessionManager, JWTManager, etc.)
3. Create session via SessionManager
4. Validate session via ValidationService
5. Mint JWT via JWTManager
6. Cache JWT via JWTCache
7. Verify JWT claims

**Target (IDENTICAL):**
1. Create test user in PostgreSQL (SAME)
2. Initialize production services (SAME)
3. Create session via SessionManager (SAME)
4. Validate session via ValidationService (SAME)
5. Mint JWT via JWTManager (SAME)
6. Cache JWT via JWTCache (SAME)
7. Verify JWT claims (SAME)

**Only difference:** Mock Ory API calls instead of Neon Auth API calls.

## Implementation Checklist

### Phase 1: Infrastructure (Day 1)
- [ ] Deploy CNPG cluster (3 nodes)
- [ ] Create databases: hydra_db, kratos_db, keto_db
- [ ] Deploy Ory Hydra (Helm chart)
- [ ] Deploy Ory Kratos (Helm chart)
- [ ] Deploy Ory Keto (Helm chart)
- [ ] Verify database connectivity

### Phase 2: auth-proxy Service (Day 1)
- [ ] Create `cmd/auth-proxy/` in monorepo
- [ ] Implement Hydra Admin API client
- [ ] Implement Kratos Admin API client
- [ ] Implement Keto API client
- [ ] Implement login endpoint (Hydra redirect target)
- [ ] Implement consent endpoint (Hydra redirect target)
- [ ] Implement OAuth metadata proxy
- [ ] Pre-register `mcp-public-client` on startup

### Phase 3: Session Management (Day 1)
- [ ] Copy SessionManager from identity-service (UNCHANGED)
- [ ] Copy JWTManager from identity-service (UNCHANGED)
- [ ] Copy JWTCache from identity-service (UNCHANGED)
- [ ] Copy ValidationService from identity-service (UNCHANGED)
- [ ] Copy database repositories (UNCHANGED)

### Phase 4: AgentGateway Integration (Day 1)
- [ ] Configure AgentGateway with Hydra issuer
- [ ] Configure AgentGateway with Hydra JWKS URL
- [ ] Verify JWT validation works
- [ ] Verify resource metadata endpoint

### Phase 5: Testing (Day 2)
- [ ] Port integration tests from identity-service
- [ ] Test PKCE flow end-to-end
- [ ] Test JWT validation
- [ ] Test session management
- [ ] Test JIT provisioning
- [ ] Test multi-tenant isolation

## Key Differences: Neon Auth vs Ory

| Aspect | Neon Auth | Ory Stack |
|--------|-----------|-----------|
| **OAuth Provider** | Single service | Hydra (OAuth2/OIDC) |
| **Identity Store** | Built-in | Kratos (separate) |
| **Authorization** | Built-in | Keto (separate) |
| **Custom Claims** | Via sessionVerifier | Via consent session parameter |
| **Login UI** | Neon-hosted | auth-proxy-hosted |
| **Consent Flow** | Implicit | Explicit (required for claims) |
| **Client Registration** | Dynamic | Pre-registered + CIMD |
| **Metadata Discovery** | Single endpoint | Multiple endpoints |

## Critical Success Factors

1. **Consent Flow Implementation:** This is the MOST CRITICAL difference. Ory Hydra REQUIRES explicit consent flow for custom claim injection. auth-proxy MUST implement login and consent endpoints.

2. **Claim Injection Timing:** With Neon Auth, claims come from sessionVerifier validation. With Ory, claims are injected during consent acceptance BEFORE token issuance.

3. **Session Management Reuse:** The entire session management, JWT caching, and database provisioning logic can be COPIED VERBATIM from identity-service. This is 70% of the codebase.

4. **Test Pattern Reuse:** Integration tests can be COPIED with minimal changes (mock Ory APIs instead of Neon Auth APIs).

5. **AgentGateway Transparency:** AgentGateway doesn't need ANY changes. It just validates JWTs using JWKS, regardless of issuer.

## Recommended Approach

1. **Copy identity-service structure** to `cmd/auth-proxy/`
2. **Keep all session/JWT/cache logic UNCHANGED**
3. **Replace NeonAuthClient with Ory clients**
4. **Add login/consent endpoints** (new requirement for Ory)
5. **Port integration tests** with Ory mocks
6. **Deploy and test** against real Ory stack

## Conclusion

The identity-service provides an EXCELLENT reference architecture. The migration to Ory is primarily:
1. Replacing REST API client (NeonAuthClient → Ory clients)
2. Adding login/consent endpoints (Ory requirement)
3. Adjusting claim injection timing (consent vs callback)

All session management, JWT handling, caching, and database logic remains IDENTICAL. This significantly reduces implementation risk.
