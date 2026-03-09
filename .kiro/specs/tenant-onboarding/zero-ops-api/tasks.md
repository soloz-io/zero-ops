# Implementation Tasks: Core Tenant CRUD API

**Spec:** tenant-onboarding/zero-ops-api  
**Based On:** requirements.md v1.0, design.md v1.0  
**Test Strategy:** E2E BDD tests only (no unit tests, no property-based tests)

---

## Phase 1: Project Setup & Database Foundation

### Task 1: Initialize Go Module and Dependencies
- [x] 1.1 Initialize Go module (`go mod init`)
- [x] 1.2 Add dependencies: Gin, pgxpool, sqlc, zap, Prometheus client, testify
- [x] 1.3 Create project structure (cmd/, internal/, pkg/)
- [x] 1.4 Create Makefile with targets: sqlc-generate, migrate-up, migrate-down, test

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

### Task 2: Database Schema and Migrations
- [-] 2.1 Create migration file `001_create_tenants.sql` with partial unique index
- [~] 2.2 Create `updated_at` trigger function
- [~] 2.3 Verify schema constraints (RFC 1123 regex, plan/status enums)
- [~] 2.4 Create Atlas/golang-migrate configuration

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

### Task 3: sqlc Configuration and Query Generation
- [~] 3.1 Create `internal/db/sqlc.yaml` configuration
- [ ] 3.2 Write SQL queries in `internal/db/queries/tenant.sql`:
  - [~] 3.2.1 UpsertTenant (with `created_at = updated_at` trick)
  - [~] 3.2.2 GetTenant (exclude soft-deleted)
  - [~] 3.2.3 ListTenants (with filters and pagination)
  - [~] 3.2.4 CountTenants (for pagination total)
  - [~] 3.2.5 UpdateTenant
  - [~] 3.2.6 SoftDeleteTenant
- [~] 3.3 Generate Go code with `sqlc generate`
- [~] 3.4 Create `internal/db/db.go` with pgxpool setup

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

## Phase 2: Core Business Logic

### Task 4: Configuration Management
- [~] 4.1 Create `internal/config/config.go` with Config struct
- [~] 4.2 Implement `Load()` function (returns error, not panic)
- [~] 4.3 Add environment variable parsing

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

### Task 5: Custom Validators
- [~] 5.1 Create `pkg/validator/rfc1123.go` with RFC 1123 validator
- [~] 5.2 Implement `RegisterAll()` function for validator registration
- [~] 5.3 Integrate with Gin validator

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

### Task 6: Error Handling
- [~] 6.1 Create `internal/service/errors.go` with APIError struct
- [~] 6.2 Implement error constructors (NewValidationError, NewNotFoundError)
- [~] 6.3 Remove NewConflictError (409 not used for tenant creation)
- [~] 6.4 Add timestamp to all errors

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

### Task 7: Service Layer - Tenant Business Logic
- [~] 7.1 Create `internal/service/tenant.go` with TenantService struct (uses db.Querier interface)
- [ ] 7.2 Implement `CreateTenant()` with:
  - [~] 7.2.1 RFC 1123 validation
  - [~] 7.2.2 ResolveQuotas() function (merge plan defaults with overrides)
  - [~] 7.2.3 UUID generation
  - [~] 7.2.4 Call UpsertTenant query
  - [~] 7.2.5 Return tenant with `created` flag
- [~] 7.3 Implement `GetTenant()` (exclude soft-deleted)
- [~] 7.4 Implement `ListTenants()` with filters and pagination
- [ ] 7.5 Implement `UpdateTenant()` with:
  - [~] 7.5.1 Quota merge strategy (reset to plan defaults unless explicit quotas provided)
  - [~] 7.5.2 Status transition validation
- [~] 7.6 Implement `DeleteTenant()` with confirmation check

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

## Phase 3: HTTP Layer

### Task 8: Middleware
- [~] 8.1 Create `internal/api/middleware/logger.go` with skipPaths for /healthz and /metrics
- [~] 8.2 Create `internal/api/middleware/error.go` for APIError → JSON conversion
- [~] 8.3 Create `internal/api/middleware/metrics.go` with Prometheus instrumentation (SLO-aligned buckets)
- [~] 8.4 Create `internal/api/middleware/recovery.go` for panic recovery

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

### Task 9: HTTP Handlers
- [~] 9.1 Create `internal/api/handlers/tenant.go` with TenantHandler struct
- [ ] 9.2 Implement `Create()` handler:
  - [~] 9.2.1 Request binding with validation
  - [~] 9.2.2 500ms timeout at handler layer
  - [~] 9.2.3 Call service.CreateTenant()
  - [~] 9.2.4 Return 201 if created, 200 if existing
- [~] 9.3 Implement `Get()` handler
- [ ] 9.4 Implement `List()` handler with:
  - [~] 9.4.1 Query parameter parsing (status, plan, page, limit)
  - [~] 9.4.2 Limit capping at 100
  - [~] 9.4.3 next_cursor calculation
  - [~] 9.4.4 Error handling for CountTenants
- [~] 9.5 Implement `Update()` handler
- [~] 9.6 Implement `Delete()` handler with confirmation check
- [~] 9.7 Create `internal/api/handlers/health.go` with /healthz and /readyz endpoints

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

### Task 10: Server Setup
- [~] 10.1 Create `internal/api/server.go` with Server struct
- [~] 10.2 Implement `NewServer()` with middleware registration
- [~] 10.3 Register all routes
- [~] 10.4 Implement `Start()` and `Shutdown()` methods
- [~] 10.5 Register /metrics endpoint

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

### Task 11: Main Entrypoint
- [~] 11.1 Create `cmd/zero-ops-api/main.go`
- [~] 11.2 Implement graceful shutdown with signal handling
- [~] 11.3 Handle config.Load() error (no panic)
- [~] 11.4 Initialize database pool with 30-minute MaxConnLifetime
- [~] 11.5 Start server in goroutine

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

## Phase 4: E2E BDD Tests

### Task 12: E2E Test Suite 1 - Complete Tenant Lifecycle (Happy Path)
- [~] 12.1 Setup test database and HTTP test server
- [~] 12.2 Implement Step 1: Create tenant (POST /api/v1/tenants)
  - [ ] Assert 201 Created
  - [ ] Assert `created: true`
  - [ ] Assert `quotas.maxClusters: 1` (free plan default)
- [~] 12.3 Implement Step 2: Retrieve tenant (GET /api/v1/tenants/{id})
  - [ ] Assert 200 OK
  - [ ] Assert `status: active`
- [~] 12.4 Implement Step 3: Upgrade plan (PATCH /api/v1/tenants/{id})
  - [ ] Assert 200 OK
  - [ ] Assert `plan: professional`
  - [ ] Assert `quotas.maxClusters: 10` (recalculated)
- [~] 12.5 Implement Step 4: Safe deletion (DELETE /api/v1/tenants/{id}?confirm=true)
  - [ ] Assert 204 No Content
- [~] 12.6 Implement Step 5: Verify soft delete (GET /api/v1/tenants/{id})
  - [ ] Assert 404 Not Found

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

### Task 13: E2E Test Suite 2 - Agentic Resilience & Self-Correction
- [~] 13.1 Implement Step 1: Invalid name format (POST with `Acme Corp!`)
  - [ ] Assert 400 Bad Request
  - [ ] Assert `error.code: VALIDATION_ERROR`
  - [ ] Assert `error.field: name`
- [~] 13.2 Implement Step 2: Successful correction (POST with `acme-corp`)
  - [ ] Assert 201 Created
  - [ ] Assert `created: true`
- [~] 13.3 Implement Step 3: Idempotent retry (POST with same payload)
  - [ ] Assert 200 OK
  - [ ] Assert `created: false`
  - [ ] Assert data unchanged
- [~] 13.4 Implement Step 4: Different payload with same name (POST with different email)
  - [ ] Assert 200 OK (pure idempotency, no 409)
  - [ ] Assert existing tenant returned unchanged

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

### Task 14: E2E Test Suite 3 - Admin Overrides and Guardrails
- [ ] 14.1 Implement Step 1: Create with custom quotas
  - [ ] Assert 201 Created
  - [ ] Assert `quotas.maxClusters: 999` (override)
  - [ ] Assert `quotas.maxNodes: 50` (default)
- [ ] 14.2 Implement Step 2: Unconfirmed deletion attempt
  - [ ] Assert 400 Bad Request
  - [ ] Assert `error.code: CONFIRMATION_REQUIRED`
- [ ] 14.3 Implement Step 3: Invalid status transition
  - [ ] Delete tenant successfully
  - [ ] Attempt PATCH to reactivate
  - [ ] Assert 404 Not Found

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

### Task 15: E2E Test Suite 4 - Fleet Management (List & Pagination)
- [ ] 15.1 Seed database with 3 tenants (A: active/free, B: active/professional, C: suspended/professional)
- [ ] 15.2 Implement Step 1: Default list (GET /api/v1/tenants)
  - [ ] Assert 200 OK
  - [ ] Assert 3 items returned
  - [ ] Assert `pagination.total: 3`
- [ ] 15.3 Implement Step 2: Filter by plan (GET /api/v1/tenants?plan=professional)
  - [ ] Assert 200 OK
  - [ ] Assert 2 items (B and C)
- [ ] 15.4 Implement Step 3: Filter by status (GET /api/v1/tenants?status=active)
  - [ ] Assert 200 OK
  - [ ] Assert 2 items (A and B)
- [ ] 15.5 Implement Step 4: Pagination boundaries (GET /api/v1/tenants?limit=2&page=1)
  - [ ] Assert 200 OK
  - [ ] Assert 2 items
  - [ ] Assert `pagination.nextCursor` present
- [ ] 15.6 Implement Step 5: Follow pagination (GET /api/v1/tenants?limit=2&page=2)
  - [ ] Assert 200 OK
  - [ ] Assert 1 item
  - [ ] Assert `pagination.nextCursor: null`

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

## Phase 5: Documentation and Deployment

### Task 16: Documentation
- [ ] 16.1 Update README.md with setup instructions
- [ ] 16.2 Document environment variables
- [ ] 16.3 Add API usage examples
- [ ] 16.4 Document test execution

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

### Task 17: Deployment Preparation
- [ ] 17.1 Create Dockerfile
- [ ] 17.2 Create docker-compose.yml with PostgreSQL
- [ ] 17.3 Add health check configuration
- [ ] 17.4 Verify graceful shutdown behavior

**Testing Principle:** Tests must use exact same service classes, dependency injection, and business logic as production. No business logic in tests - only testing and asserting logic.

---

**End of Tasks**
