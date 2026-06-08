# ADR 010: Tenant User Management Pattern

## Status
Accepted

## Date
2026-04-30

## Context

Zero-Ops provisions multi-tenant SaaS environments where each tenant (SaaS builder) needs to manage their own end-users. These end-users are the customers of the tenant's application, not platform administrators or Zero-Ops operators.

Our current architecture has several identity management layers:

1. **Platform Identity (Ory Stack in Hub):** Manages Zero-Ops platform users (tenant admins, developers)
2. **Tenant Application Identity:** Needs to manage end-users of tenant applications
3. **Database-Level Isolation:** PostgreSQL Row-Level Security (RLS) for data isolation

We face several challenges in tenant user management:

1. **Identity Ownership:** Should tenant end-users be managed in the Hub's Ory instance, or should each tenant have their own identity system?

2. **Data Residency:** Where should tenant user records be stored? Hub database, Spoke database, or both?

3. **Access Control:** How do we enforce that Tenant A cannot access or modify Tenant B's users?

4. **API Surface:** Should tenants call Ory APIs directly, or should we provide an abstraction layer?

5. **RLS Integration:** How do we connect identity tokens to PostgreSQL RLS policies for data isolation?

## Decision

We will implement a **Dual-Layer Identity Pattern** where:

1. **Hub Identity (Ory Kratos):** Manages tenant end-user identities centrally
2. **Spoke Database (PostgreSQL + RLS):** Stores tenant-specific user metadata and enforces data isolation
3. **kube-sbt Abstraction:** Provides secure multi-tenant user management APIs

### Architecture Components

#### 1. Identity Creation Flow

```
Tenant App → kube-sbt API → Ory Kratos (Hub) → User Created
                ↓
         PostgreSQL (Spoke)
         public.users table
```

**Steps:**
1. Tenant application calls `POST /api/v1/tenants/{tenantID}/users` on kube-sbt
2. kube-sbt validates tenant JWT and extracts `tenant_id`
3. kube-sbt creates identity in Ory Kratos with `tenant_id` in metadata
4. kube-sbt inserts user record in tenant's Spoke database (`public.users` table)
5. Both operations succeed or rollback (transactional consistency)

#### 2. Database Schema

**Spoke Database: `public.users` table**
```sql
CREATE TABLE public.users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    email VARCHAR(255) NOT NULL,
    email_verified BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    metadata JSONB DEFAULT '{}'::jsonb,
    CONSTRAINT users_tenant_email_unique UNIQUE (tenant_id, email)
);

-- Enable Row-Level Security
ALTER TABLE public.users ENABLE ROW LEVEL SECURITY;

-- RLS Policy: Users can only see their own tenant's users
CREATE POLICY tenant_isolation_policy ON public.users
    USING (tenant_id::text = current_setting('request.jwt.claims', true)::json->>'tenant_id');

-- Index for performance
CREATE INDEX idx_users_tenant_id ON public.users(tenant_id);
CREATE INDEX idx_users_email ON public.users(email);

-- Auto-update timestamp trigger
CREATE TRIGGER update_users_updated_at
    BEFORE UPDATE ON public.users
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
```

#### 3. JWT Claims Structure

**Ory Hydra issues JWTs with:**
```json
{
  "sub": "user-uuid",
  "tenant_id": "tenant-uuid",
  "email": "user@example.com",
  "iat": 1234567890,
  "exp": 1234571490
}
```

**PostgreSQL RLS uses:**
```sql
current_setting('request.jwt.claims', true)::json->>'tenant_id'
```

#### 4. kube-sbt API Abstraction

**Endpoints:**
- `POST /api/v1/tenants/{tenantID}/users` - Create user
- `GET /api/v1/tenants/{tenantID}/users/{userID}` - Get user
- `PUT /api/v1/tenants/{tenantID}/users/{userID}` - Update user
- `DELETE /api/v1/tenants/{tenantID}/users/{userID}` - Delete user
- `GET /api/v1/tenants/{tenantID}/users` - List users (paginated)

**Security Enforcement:**
1. Extract `tenant_id` from JWT
2. Validate `{tenantID}` in URL matches JWT claim
3. Return 403 Forbidden if mismatch
4. Inject `tenant_id` into all database queries

#### 5. Access Patterns

**Pattern A: Tenant Application → kube-sbt → Ory + PostgreSQL**
- Used for: User CRUD operations
- Authentication: JWT from Ory Hydra
- Isolation: kube-sbt validates tenant_id in JWT

**Pattern B: Tenant Application → PostgREST → PostgreSQL**
- Used for: Application-specific user metadata queries
- Authentication: JWT from Ory Hydra
- Isolation: PostgreSQL RLS enforces tenant_id filtering

### Key Design Decisions

1. **Centralized Identity (Ory Kratos in Hub):**
   - Single source of truth for authentication
   - Consistent password policies, MFA, account recovery
   - Simplified token issuance (Ory Hydra)

2. **Distributed User Metadata (PostgreSQL in Spoke):**
   - Tenant-specific user attributes stored locally
   - RLS provides defense-in-depth isolation
   - Supports tenant-specific schema extensions

3. **kube-sbt as Security Boundary:**
   - Prevents direct Ory API access from tenants
   - Enforces tenant isolation at application layer
   - Provides audit logging and rate limiting

4. **No Tenant-Local Ory Instances:**
   - Avoids operational complexity of per-tenant identity systems
   - Reduces infrastructure costs
   - Simplifies token validation (single JWKS endpoint)

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Tenant Identity | Infisical | Tenant Identity Service | Kube-SBT | Tenant Apps, provider-sql | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

* **Strong Tenant Isolation:** Multi-layer isolation (JWT validation + RLS) prevents cross-tenant data access

* **Centralized Authentication:** Single Ory instance simplifies token management, SSO integration, and security updates

* **Flexible User Metadata:** Tenants can extend `public.users.metadata` JSONB column without schema migrations

* **Defense in Depth:** Even if application code bypasses kube-sbt, RLS prevents unauthorized access

* **Audit Trail:** All user operations flow through kube-sbt, enabling centralized logging

* **Scalability:** Ory Kratos scales horizontally; PostgreSQL RLS has minimal performance overhead

* **Standard Protocols:** Uses OAuth2/OIDC (Ory Hydra) and PostgreSQL RLS (industry-standard patterns)

### Negative / Risks

* **Hub Dependency:** Tenant applications cannot authenticate users if Hub Ory is unavailable
  
  *Mitigation: Deploy Ory with HA (3+ replicas), use CNPG for database HA, implement circuit breakers in kube-sbt*

* **Cross-Cluster Latency:** User creation requires Hub (Ory) + Spoke (PostgreSQL) round-trips
  
  *Mitigation: Acceptable for user management operations (not hot path). Typical latency <100ms within same region*

* **RLS Performance:** Complex RLS policies can impact query performance
  
  *Mitigation: Keep RLS policies simple (single tenant_id check). Use composite indexes (tenant_id, id). Monitor query plans*

* **Schema Migration Coordination:** Changes to `public.users` table require migrations across all Spoke databases
  
  *Mitigation: Use Atlas migrations with idempotent scripts. Deploy via ArgoCD with sync waves. Test in staging Spoke first*

* **JWT Size Growth:** Adding claims increases JWT size and network overhead
  
  *Mitigation: Keep JWT claims minimal (tenant_id, user_id, email). Store extended attributes in database, not JWT*

* **Token Revocation Complexity:** Revoking a user's access requires invalidating all active JWTs
  
  *Mitigation: Use short-lived access tokens (15 min) with refresh tokens. Implement token revocation list in Redis if needed*

### Operational Considerations

**User Deletion:**
- Soft delete in PostgreSQL (`deleted_at` timestamp)
- Hard delete in Ory Kratos (GDPR compliance)
- Cascade delete user's application data via foreign keys

**Tenant Offboarding:**
- Delete all users in Ory Kratos (batch operation)
- Drop tenant database in Spoke (CNPG cluster deletion)
- Purge tenant namespace

**Monitoring:**
- Track user creation/deletion rates per tenant
- Alert on RLS policy violations (should never happen)
- Monitor Ory Kratos response times and error rates

**Backup/Recovery:**
- Ory Kratos data backed up via CNPG (Hub database)
- Tenant user metadata backed up via CNPG (Spoke database)
- Point-in-time recovery available for both