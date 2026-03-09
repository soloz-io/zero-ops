# Requirements: Core Tenant CRUD API (No K8s, No Auth)

**Version:** 1.0  
**Status:** DRAFT  
**Scope:** PostgreSQL-backed tenant metadata API only  
**Excluded:** Kubernetes provisioning, OAuth/JWT authentication, token generation

---

## 1. Executive Summary

This spec defines a minimal REST API for tenant lifecycle management (Create, Read, Update, Delete) backed by PostgreSQL. The API handles tenant metadata, plan management, and quota tracking without touching Kubernetes or authentication systems. This isolation allows testing core business logic, database schema, and error handling before integrating high-risk external dependencies.

**Core Principle:** API returns success/failure based solely on database operations. No K8s namespace creation, no RBAC provisioning, no token minting.

---

## 2. Functional Requirements

### 2.1 Tenant Creation
- Accept tenant metadata (name, email, plan, optional quotas)
- Validate name against RFC 1123 DNS label rules (lowercase alphanumeric + hyphens, start/end with alphanumeric)
- Generate unique tenant ID (UUID v4)
- Store in PostgreSQL with timestamps
- Return 201 Created with tenant details on first creation
- Return 200 OK with `created: false` for duplicate name (pure idempotency - no 409 Conflict)
- Return 400 Bad Request for validation errors

### 2.2 Tenant Retrieval
- Get single tenant by ID
- List all tenants with pagination
- Filter by status (active, suspended, deleted)
- Filter by plan (free, professional, enterprise)
- Return 404 Not Found if tenant doesn't exist

### 2.3 Tenant Update
- Update plan (triggers quota recalculation to new plan baseline)
- Update quotas (admin override - persists across plan changes if explicitly provided)
- Update status (active → suspended → deleted)
- **Quota Merge Strategy:** Plan upgrade resets quotas to new plan defaults UNLESS `quotas` field is explicitly provided in PATCH request
- Return 200 OK with updated tenant
- Return 404 Not Found if tenant doesn't exist
- Return 400 Bad Request for invalid transitions

### 2.4 Tenant Deletion
- Soft delete: set status to "deleted", set `deleted_at` timestamp, preserve data
- Hard delete: deferred to Phase 4 (requires auth, separate `/purge` endpoint)
- **Note:** Soft-delete restoration deferred to Phase 4 - manual DB intervention required until then
- Return 204 No Content on success
- Return 404 Not Found if tenant doesn't exist

---

## 3. Data Model

### 3.1 Tenant Entity

```
Tenant {
  id: UUID (primary key, generated)
  org_id: UUID (future: maps to Kratos identity trait, nullable for now)
  name: string (unique, RFC 1123 compliant, max 63 chars)
  email: string (admin contact, validated format)
  plan: enum (free, professional, enterprise)
  status: enum (active, suspended, deleted)
  quotas: JSON {
    maxClusters: integer
    maxNodes: integer
    maxCPU: string (e.g., "200")
    maxMemory: string (e.g., "500Gi")
  }
  metadata: JSON (optional key-value pairs)
  created_at: timestamp
  updated_at: timestamp
  deleted_at: timestamp (nullable, for soft delete)
}
```

### 3.2 Plan Defaults

```
free:
  maxClusters: 1
  maxNodes: 5
  maxCPU: "10"
  maxMemory: "20Gi"

professional:
  maxClusters: 10
  maxNodes: 50
  maxCPU: "200"
  maxMemory: "500Gi"

enterprise:
  maxClusters: 100
  maxNodes: 500
  maxCPU: "2000"
  maxMemory: "5000Gi"
```

### 3.3 Validation Rules

**Name:**
- Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
- Length: 1-63 characters
- Must not start or end with hyphen
- Examples: ✅ `acme-corp`, `startup123` | ❌ `Acme Corp`, `-invalid`, `test_org`

**Email:**
- Pattern: RFC 5322 simplified (basic @ validation)
- Example: ✅ `admin@acme.com` | ❌ `invalid.email`

**Plan:**
- Enum: `free`, `professional`, `enterprise`
- Case-sensitive

**Status:**
- Enum: `active`, `suspended`, `deleted`
- Transitions: `active` ↔ `suspended` → `deleted` (one-way to deleted)

---

## 4. API Contracts

### 4.1 Create Tenant

**Endpoint:** `POST /api/v1/tenants`

**Request Body:**
```json
{
  "name": "acme-corp",
  "email": "admin@acme.com",
  "plan": "professional",
  "quotas": {
    "maxClusters": 15,
    "maxNodes": 75
  },
  "metadata": {
    "company": "Acme Corporation",
    "industry": "Technology"
  }
}
```

**Required Fields:** `name`, `email`, `plan`  
**Optional Fields:** `quotas` (uses plan defaults if omitted), `metadata`

**Success Response (201 Created):**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "org_id": null,
  "name": "acme-corp",
  "email": "admin@acme.com",
  "plan": "professional",
  "status": "active",
  "quotas": {
    "maxClusters": 15,
    "maxNodes": 75,
    "maxCPU": "200",
    "maxMemory": "500Gi"
  },
  "metadata": {
    "company": "Acme Corporation",
    "industry": "Technology"
  },
  "created_at": "2026-03-09T10:00:00Z",
  "updated_at": "2026-03-09T10:00:00Z",
  "created": true
}
```

**Idempotent Response (200 OK):**
Same as 201, but `"created": false`

**Error Response (400 Bad Request):**
```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Name must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character",
    "field": "name",
    "value": "Acme Corp!",
    "timestamp": "2026-03-09T10:00:00Z"
  }
}
```

**Error Response (409 Conflict):**
Not used for tenant creation. Reserved for future use cases where client cannot self-correct (e.g., resource locks, concurrent modification conflicts).

### 4.2 Get Tenant

**Endpoint:** `GET /api/v1/tenants/{id}`

**Success Response (200 OK):**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "org_id": null,
  "name": "acme-corp",
  "email": "admin@acme.com",
  "plan": "professional",
  "status": "active",
  "quotas": {
    "maxClusters": 15,
    "maxNodes": 75,
    "maxCPU": "200",
    "maxMemory": "500Gi"
  },
  "metadata": {
    "company": "Acme Corporation"
  },
  "created_at": "2026-03-09T10:00:00Z",
  "updated_at": "2026-03-09T10:00:00Z"
}
```

**Error Response (404 Not Found):**
```json
{
  "error": {
    "code": "TENANT_NOT_FOUND",
    "message": "Tenant with ID '550e8400-e29b-41d4-a716-446655440000' not found",
    "timestamp": "2026-03-09T10:00:00Z"
  }
}
```

### 4.3 List Tenants

**Endpoint:** `GET /api/v1/tenants`

**Query Parameters:**
- `status` (optional): `active`, `suspended`, `deleted`
- `plan` (optional): `free`, `professional`, `enterprise`
- `page` (optional, default: 1): Page number
- `limit` (optional, default: 50, max: 100): Items per page

**Success Response (200 OK):**
```json
{
  "tenants": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "name": "acme-corp",
      "email": "admin@acme.com",
      "plan": "professional",
      "status": "active",
      "created_at": "2026-03-09T10:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 50,
    "total": 127,
    "totalPages": 3
  }
}
```

### 4.4 Update Tenant

**Endpoint:** `PATCH /api/v1/tenants/{id}`

**Request Body (partial update):**
```json
{
  "plan": "enterprise"
}
```

**Success Response (200 OK) - Plan upgrade resets quotas:**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "acme-corp",
  "plan": "enterprise",
  "quotas": {
    "maxClusters": 100,
    "maxNodes": 500,
    "maxCPU": "2000",
    "maxMemory": "5000Gi"
  },
  "updated_at": "2026-03-09T11:00:00Z"
}
```

**Request Body (plan upgrade with quota override):**
```json
{
  "plan": "enterprise",
  "quotas": {
    "maxClusters": 50
  }
}
```

**Success Response (200 OK) - Explicit quotas preserved:**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "acme-corp",
  "plan": "enterprise",
  "quotas": {
    "maxClusters": 50,
    "maxNodes": 500,
    "maxCPU": "2000",
    "maxMemory": "5000Gi"
  },
  "updated_at": "2026-03-09T11:00:00Z"
}
```

**Error Response (400 Bad Request - Invalid Transition):**
```json
{
  "error": {
    "code": "INVALID_STATUS_TRANSITION",
    "message": "Cannot transition from 'deleted' to 'active'",
    "field": "status",
    "timestamp": "2026-03-09T10:00:00Z"
  }
}
```

### 4.5 Delete Tenant

**Endpoint:** `DELETE /api/v1/tenants/{id}`

**Query Parameters:**
- `confirm` (required): `true` (safety check)

**Success Response (204 No Content)**

**Error Response (400 Bad Request - Missing Confirmation):**
```json
{
  "error": {
    "code": "CONFIRMATION_REQUIRED",
    "message": "Must provide 'confirm=true' query parameter to delete tenant",
    "timestamp": "2026-03-09T10:00:00Z"
  }
}
```

---

## 5. Error Handling

### 5.1 Standard Error Envelope

All errors follow this structure:
```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "Human-readable description",
    "field": "fieldName",
    "value": "invalidValue",
    "timestamp": "2026-03-09T10:00:00Z"
  }
}
```

### 5.2 Error Codes

| HTTP Status | Error Code | Description |
|-------------|------------|-------------|
| 400 | `VALIDATION_ERROR` | Input validation failed |
| 400 | `INVALID_STATUS_TRANSITION` | Status change not allowed |
| 400 | `CONFIRMATION_REQUIRED` | Delete confirmation missing |
| 404 | `TENANT_NOT_FOUND` | Tenant ID doesn't exist |
| 500 | `INTERNAL_ERROR` | Database or server error |

**Note:** 409 Conflict removed from tenant creation (pure idempotency). Reserved for future use cases where client cannot self-correct.

---

## 6. Database Schema

### 6.1 Tenants Table

```sql
CREATE TABLE tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID,
    name VARCHAR(63) UNIQUE NOT NULL,
    email VARCHAR(255) NOT NULL,
    plan VARCHAR(20) NOT NULL CHECK (plan IN ('free', 'professional', 'enterprise')),
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'deleted')),
    quotas JSONB NOT NULL,
    metadata JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP,
    
    CONSTRAINT name_format CHECK (name ~ '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$')
);

CREATE INDEX idx_tenants_name ON tenants(name);
CREATE INDEX idx_tenants_org_id ON tenants(org_id);
CREATE INDEX idx_tenants_status ON tenants(status);
CREATE INDEX idx_tenants_plan ON tenants(plan);
CREATE INDEX idx_tenants_created_at ON tenants(created_at DESC);
```

### 6.2 Migration Notes

- `org_id` is nullable for now (future: Kratos integration)
- `quotas` JSONB allows flexible quota structure without schema changes
- `metadata` JSONB for arbitrary tenant-specific data
- `deleted_at` supports soft delete pattern
- Constraint on `name` enforces RFC 1123 at database level

---

## 7. Non-Functional Requirements

### 7.1 Performance
- List endpoint: < 100ms (p99) for 10,000 tenants
- Create/Update: < 50ms (p99)
- Database connection pooling (min 10, max 50 connections)

### 7.2 Idempotency
- Create tenant with duplicate name: return existing tenant (200 OK, `created: false`) - pure idempotency, no 409 Conflict
- Update with no changes: return 200 OK without database write
- Delete already-deleted tenant: return 204 No Content

### 7.3 Data Integrity
- All writes in transactions
- Optimistic locking via `updated_at` timestamp
- Foreign key constraints (when adding related tables)

### 7.4 Observability
- Structured JSON logging (request ID, tenant ID, duration)
- Metrics: request count, latency, error rate per endpoint
- Health check endpoint: `GET /health` (checks DB connection)

---

## 8. Testing Requirements

### 8.1 Unit Tests
- Validation logic (name format, email format, plan enum)
- Quota defaulting per plan
- Status transition rules
- Error message formatting

### 8.2 Integration Tests
- Create tenant → verify in database
- Idempotent create (call twice, check `created` flag)
- Update tenant → verify changes persisted
- Soft delete → verify `deleted_at` set, status = deleted
- List with filters → verify correct results
- Pagination → verify page boundaries

### 8.3 Error Cases
- Invalid name format → 400 with descriptive message
- Duplicate name → 200 OK with `created: false` (idempotent, not error)
- Non-existent tenant ID → 404 Not Found
- Invalid status transition → 400 with transition error
- Missing required fields → 400 with field name
- Database connection failure → 500 Internal Error

---

## 9. Out of Scope (Explicitly Excluded)

### 9.1 Kubernetes Integration
- No namespace creation
- No RBAC provisioning
- No ResourceQuota objects
- No ServiceAccount generation

### 9.2 Authentication/Authorization
- No JWT validation
- No OAuth Device Flow
- No session management
- No RBAC policy enforcement
- API accepts all requests (testing only)

### 9.3 Token Generation
- No API token minting
- No kubeconfig generation
- No credential storage

### 9.4 Billing/Metering
- No usage tracking
- No invoice generation
- No payment processing

### 9.5 Audit Logging
- No audit trail table
- No compliance exports

### 9.6 Soft-Delete Restoration
- No `POST /api/v1/tenants/{id}/restore` endpoint (requires Phase 4 auth)
- Manual DB intervention required for accidental deletions

### 9.7 Hard Delete / GDPR Purge
- No physical row deletion via API
- No `DELETE /api/v1/tenants/{id}/purge` endpoint (requires Phase 4 super-admin auth)
- Manual DB purge required for GDPR Article 17 compliance

**Rationale:** These features require external dependencies (K8s API, Kratos, Stripe). Testing core CRUD logic in isolation ensures database schema, validation, and error handling are solid before adding complexity.

---

## 10. Success Criteria

- [ ] Create tenant with valid data returns 201 Created
- [ ] Create tenant with duplicate name returns 200 OK with `created: false` (idempotent, no 409)
- [ ] Create tenant with invalid name returns 400 with RFC 1123 error message
- [ ] Get tenant by ID returns correct data
- [ ] Get non-existent tenant returns 404 Not Found
- [ ] List tenants with pagination returns correct page
- [ ] Update tenant plan without quotas field resets quotas to new plan defaults
- [ ] Update tenant plan with explicit quotas field preserves quota overrides
- [ ] Delete tenant sets status to deleted and deleted_at timestamp
- [ ] All endpoints respond in < 100ms (p99)
- [ ] Database constraints prevent invalid data
- [ ] Integration tests cover all CRUD operations
- [ ] Error messages are descriptive enough for LLM to self-correct

---

**End of Requirements**
