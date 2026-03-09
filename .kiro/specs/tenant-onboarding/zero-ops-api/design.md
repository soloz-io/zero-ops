# Design: Core Tenant CRUD API (No K8s, No Auth)

**Version:** 1.0  
**Status:** DRAFT  
**Based On:** requirements.md v1.0  
**Technology Stack:** Go 1.21+, Gin, sqlc, pgxpool, Atlas

---

## 1. Architecture Overview

### 1.1 High-Level Design

```
┌─────────────────────────────────────────────────────────────┐
│                    HTTP Layer (Gin)                          │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  Middleware Chain                                     │   │
│  │  • Request Logger (zap)                               │   │
│  │  • Error Handler (APIError → JSON)                    │   │
│  │  • Metrics (Prometheus)                               │   │
│  │  • Recovery (panic → 500)                             │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  Handlers                                             │   │
│  │  • POST   /api/v1/tenants                             │   │
│  │  • GET    /api/v1/tenants/:id                         │   │
│  │  • GET    /api/v1/tenants                             │   │
│  │  • PATCH  /api/v1/tenants/:id                         │   │
│  │  • DELETE /api/v1/tenants/:id                         │   │
│  └──────────────────────────────────────────────────────┘   │
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│                  Service Layer (Business Logic)              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  TenantService                                        │   │
│  │  • Validation (RFC 1123, email format)                │   │
│  │  • Quota defaulting (plan → limits)                   │   │
│  │  • Status transition rules                            │   │
│  └──────────────────────────────────────────────────────┘   │
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│                  Data Layer (sqlc + pgxpool)                 │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  Generated Queries (sqlc)                             │   │
│  │  • UpsertTenant (ON CONFLICT + xmax trick)            │   │
│  │  • GetTenant                                          │   │
│  │  • ListTenants (pagination + filters)                 │   │
│  │  • UpdateTenant                                       │   │
│  │  • SoftDeleteTenant                                   │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  Connection Pool (pgxpool.Pool)                       │   │
│  │  • Min: 10 connections                                │   │
│  │  • Max: 50 connections                                │   │
│  │  • Health checks enabled                              │   │
│  └──────────────────────────────────────────────────────┘   │
└────────────────────────┬────────────────────────────────────┘
                         │
                         ▼
                  ┌──────────────┐
                  │  PostgreSQL  │
                  │  Database    │
                  └──────────────┘
```

### 1.2 Component Responsibilities

| Component | Technology | Responsibility |
|-----------|------------|----------------|
| **HTTP Router** | Gin | Request routing, middleware chain, response serialization |
| **Handlers** | Go (Gin handlers) | Request binding, validation, service orchestration |
| **Service Layer** | Go (business logic) | Validation rules, quota calculation, status transitions |
| **Data Layer** | sqlc + pgxpool | Type-safe SQL queries, connection pooling |
| **Database** | PostgreSQL 15+ | Persistent storage, constraints, indexes |
| **Migrations** | Atlas or golang-migrate | Schema versioning, rollback support |
| **Observability** | zap + Prometheus | Structured logging, RED metrics |

---

## 2. Project Structure

```
cmd/
└── zero-ops-api/
    └── main.go                    # Entrypoint, graceful shutdown

internal/
├── api/
│   ├── server.go                  # Gin server setup, middleware registration
│   ├── handlers/
│   │   ├── tenant.go              # Tenant CRUD handlers
│   │   └── health.go              # /health, /readyz endpoints
│   └── middleware/
│       ├── logger.go              # Request logging (zap)
│       ├── error.go               # APIError → JSON conversion
│       ├── metrics.go             # Prometheus instrumentation
│       └── recovery.go            # Panic recovery
│
├── service/
│   ├── tenant.go                  # Business logic, validation
│   └── errors.go                  # APIError definitions
│
├── db/
│   ├── queries/
│   │   └── tenant.sql             # SQL queries for sqlc
│   ├── migrations/
│   │   ├── 001_create_tenants.sql
│   │   └── atlas.sum              # Atlas checksum file
│   ├── sqlc.yaml                  # sqlc configuration
│   ├── queries.sql.go             # Generated by sqlc
│   ├── models.go                  # Generated by sqlc
│   └── db.go                      # pgxpool setup
│
└── config/
    └── config.go                  # Configuration struct, env loading

pkg/
└── validator/
    └── rfc1123.go                 # Custom RFC 1123 validator

go.mod
go.sum
Makefile                           # sqlc generate, migrate, test targets
```

---

## 3. Technology Stack Details

### 3.1 Database Layer

**Choice: sqlc + pgxpool**

**Rationale:**
- sqlc generates type-safe Go code from SQL at compile time (zero runtime overhead)
- pgxpool provides production-grade connection pooling with health checks
- Direct SQL control for complex queries (ON CONFLICT, xmax trick)
- No ORM magic, explicit query behavior

**Dependencies:**
```go
github.com/jackc/pgx/v5/pgxpool  // Connection pooling
github.com/sqlc-dev/sqlc         // Code generation (dev tool)
```

**sqlc Configuration (`internal/db/sqlc.yaml`):**
```yaml
version: "2"
sql:
  - engine: "postgresql"
    queries: "queries/"
    schema: "migrations/"
    gen:
      go:
        package: "db"
        out: "."
        emit_json_tags: true
        emit_prepared_queries: false
        emit_interface: true
        emit_exact_table_names: false
```

### 3.2 HTTP Framework

**Choice: Gin**

**Rationale:**
- High performance (fastest Go HTTP framework in benchmarks)
- Built-in validation via `binding` tags
- Middleware ecosystem
- Context-based request scoping

**Dependencies:**
```go
github.com/gin-gonic/gin v1.10.0
github.com/go-playground/validator/v10  // Included with Gin
```

### 3.3 Database Migrations

**Choice: Atlas (recommended) or golang-migrate**

**Rationale:**
- Atlas: Modern, declarative, supports schema diffing
- golang-migrate: Battle-tested, imperative SQL migrations
- Both support rollback and version tracking

**Dependencies:**
```go
// Option A: Atlas (recommended)
ariga.io/atlas v0.15.0

// Option B: golang-migrate
github.com/golang-migrate/migrate/v4
```

### 3.4 Observability

**Logging: zap**
```go
go.uber.org/zap v1.26.0
```

**Metrics: Prometheus**
```go
github.com/prometheus/client_golang v1.18.0
```

---

## 4. Core Implementation Patterns

### 4.1 Idempotent Tenant Creation (sqlc + created_at comparison)

**SQL Query (`internal/db/queries/tenant.sql`):**
```sql
-- name: UpsertTenant :one
INSERT INTO tenants (
    id,
    org_id,
    name,
    email,
    plan,
    status,
    quotas,
    metadata,
    created_at,
    updated_at
)
VALUES (
    @id,
    @org_id,
    @name,
    @email,
    @plan,
    'active',
    @quotas,
    @metadata,
    now(),
    now()
)
ON CONFLICT (name)
DO UPDATE
SET
    updated_at = updated_at  -- No-op write, preserves true idempotency
WHERE tenants.deleted_at IS NULL
RETURNING
    *,
    (created_at = updated_at) AS created;
```

**Rationale:**
- Pure idempotent create: same name always returns existing record with `created: false`
- No 409 Conflict on duplicate names - designed for agentic LLM consumers that need self-correctable errors
- `created_at = updated_at` is pooler-safe (unlike `xmax = 0`)
- `WHERE` clause in `DO UPDATE` prevents updating soft-deleted tenants
- Matches partial unique index `idx_tenants_name_active` in schema

**Generated Go Code (sqlc):**
```go
type UpsertTenantRow struct {
    Tenant
    Created bool `json:"created"`
}

func (q *Queries) UpsertTenant(ctx context.Context, arg UpsertTenantParams) (UpsertTenantRow, error)
```

**Handler Usage:**
```go
func (h *TenantHandler) Create(c *gin.Context) {
    var req CreateTenantRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.Error(NewValidationError(err))
        return
    }

    // Apply timeout at handler layer (covers pool acquisition + query)
    ctx, cancel := context.WithTimeout(c.Request.Context(), 500*time.Millisecond)
    defer cancel()

    // Service layer validates and defaults quotas
    tenant, created, err := h.service.CreateTenant(ctx, req)
    if err != nil {
        c.Error(err)
        return
    }

    status := http.StatusOK
    if created {
        status = http.StatusCreated
    }

    c.JSON(status, TenantResponse{
        Tenant:  tenant,
        Created: created,
    })
}
```

### 4.2 Error Handling Pattern

**Error Definition (`internal/service/errors.go`):**
```go
type APIError struct {
    HTTPStatus int    `json:"-"`
    Code       string `json:"code"`
    Message    string `json:"message"`
    Field      string `json:"field,omitempty"`
    Timestamp  string `json:"timestamp"`
}

func (e *APIError) Error() string {
    return e.Message
}

// Constructors
func NewValidationError(field, message string) *APIError {
    return &APIError{
        HTTPStatus: http.StatusBadRequest,
        Code:       "VALIDATION_ERROR",
        Message:    message,
        Field:      field,
        Timestamp:  time.Now().UTC().Format(time.RFC3339),
    }
}

func NewNotFoundError(resourceType, id string) *APIError {
    return &APIError{
        HTTPStatus: http.StatusNotFound,
        Code:       fmt.Sprintf("%s_NOT_FOUND", strings.ToUpper(resourceType)),
        Message:    fmt.Sprintf("%s with ID '%s' not found", resourceType, id),
        Timestamp:  time.Now().UTC().Format(time.RFC3339),
    }
}

// Note: 409 Conflict not used for tenant creation (pure idempotency)
// Reserved for future use cases where client cannot self-correct
```

**Middleware (`internal/api/middleware/error.go`):**
```go
func ErrorMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Next()

        if len(c.Errors) == 0 {
            return
        }

        err := c.Errors.Last().Err

        if apiErr, ok := err.(*service.APIError); ok {
            c.JSON(apiErr.HTTPStatus, apiErr)
            return
        }

        // Fallback for unexpected errors
        c.JSON(http.StatusInternalServerError, service.APIError{
            Code:      "INTERNAL_ERROR",
            Message:   "An unexpected error occurred",
            Timestamp: time.Now().UTC().Format(time.RFC3339),
        })
    }
}
```

### 4.3 Validation Pattern

**RFC 1123 Validator (`pkg/validator/rfc1123.go`):**
```go
var rfc1123Regex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func ValidateRFC1123(fl validator.FieldLevel) bool {
    name := fl.Field().String()
    if len(name) == 0 || len(name) > 63 {
        return false
    }
    return rfc1123Regex.MatchString(name)
}

// RegisterAll registers all custom validators
func RegisterAll(v *validator.Validate) error {
    if err := v.RegisterValidation("rfc1123", ValidateRFC1123); err != nil {
        return fmt.Errorf("failed to register rfc1123 validator: %w", err)
    }
    return nil
}
```

**Request Struct:**
```go
type Quotas struct {
    MaxClusters int    `json:"maxClusters"`
    MaxNodes    int    `json:"maxNodes"`
    MaxCPU      string `json:"maxCPU"`
    MaxMemory   string `json:"maxMemory"`
}

type QuotaRequest struct {
    MaxClusters *int    `json:"maxClusters,omitempty"`
    MaxNodes    *int    `json:"maxNodes,omitempty"`
    MaxCPU      *string `json:"maxCPU,omitempty"`
    MaxMemory   *string `json:"maxMemory,omitempty"`
}

type CreateTenantRequest struct {
    Name     string        `json:"name" binding:"required,rfc1123"`
    Email    string        `json:"email" binding:"required,email"`
    Plan     string        `json:"plan" binding:"required,oneof=free professional enterprise"`
    Quotas   *QuotaRequest `json:"quotas"`
    Metadata map[string]string `json:"metadata"`
}
```

### 4.4 Service Layer Pattern

**Business Logic (`internal/service/tenant.go`):**
```go
type TenantService struct {
    queries db.Querier  // Interface for testability
    logger  *zap.Logger
}

func (s *TenantService) CreateTenant(ctx context.Context, req CreateTenantRequest) (*db.Tenant, bool, error) {
    // 1. Validate name format (redundant with Gin, but defense in depth)
    if !isValidRFC1123(req.Name) {
        return nil, false, NewValidationError("name", 
            "Name must consist of lower case alphanumeric characters or '-', "+
            "and must start and end with an alphanumeric character")
    }

    // 2. Resolve quotas: merge plan defaults with user overrides
    quotas := ResolveQuotas(req.Plan, req.Quotas)

    // 3. Generate UUID
    tenantID := uuid.New()

    // 4. Prepare params
    params := db.UpsertTenantParams{
        ID:       tenantID,
        Name:     req.Name,
        Email:    req.Email,
        Plan:     req.Plan,
        Quotas:   quotas,
        Metadata: req.Metadata,
    }

    // 5. Execute idempotent upsert (timeout handled at handler layer)
    result, err := s.queries.UpsertTenant(ctx, params)
    if err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            return nil, false, &APIError{
                HTTPStatus: http.StatusServiceUnavailable,
                Code:       "DATABASE_TIMEOUT",
                Message:    "Database operation timed out",
            }
        }
        s.logger.Error("failed to upsert tenant", zap.Error(err))
        return nil, false, &APIError{
            HTTPStatus: http.StatusInternalServerError,
            Code:       "DATABASE_ERROR",
            Message:    "Failed to create tenant",
        }
    }

    return &result.Tenant, result.Created, nil
}

// Plan-based quota defaults
var planDefaults = map[string]Quotas{
    "free": {
        MaxClusters: 1,
        MaxNodes:    5,
        MaxCPU:      "10",
        MaxMemory:   "20Gi",
    },
    "professional": {
        MaxClusters: 10,
        MaxNodes:    50,
        MaxCPU:      "200",
        MaxMemory:   "500Gi",
    },
    "enterprise": {
        MaxClusters: 100,
        MaxNodes:    500,
        MaxCPU:      "2000",
        MaxMemory:   "5000Gi",
    },
}

// ResolveQuotas merges plan defaults with user-provided overrides
func ResolveQuotas(plan string, overrides *QuotaRequest) Quotas {
    base := planDefaults[plan]
    
    if overrides == nil {
        return base
    }
    
    // Apply overrides (pointer fields allow partial updates)
    if overrides.MaxClusters != nil {
        base.MaxClusters = *overrides.MaxClusters
    }
    if overrides.MaxNodes != nil {
        base.MaxNodes = *overrides.MaxNodes
    }
    if overrides.MaxCPU != nil {
        base.MaxCPU = *overrides.MaxCPU
    }
    if overrides.MaxMemory != nil {
        base.MaxMemory = *overrides.MaxMemory
    }
    
    return base
}
```

### 4.5 Pagination Pattern

**Note:** Using offset-based pagination (not cursor-based). For tenant tables with expected scale < 10,000 records, offset pagination provides acceptable performance. The `nextPage` field returns the next page number for clarity (not a true cursor).

**SQL Query:**
```sql
-- name: ListTenants :many
SELECT * FROM tenants
WHERE
    (@status::text IS NULL OR status = @status)
    AND (@plan::text IS NULL OR plan = @plan)
    AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT @limit_val
OFFSET @offset_val;

-- name: CountTenants :one
SELECT COUNT(*) FROM tenants
WHERE
    (@status::text IS NULL OR status = @status)
    AND (@plan::text IS NULL OR plan = @plan)
    AND deleted_at IS NULL;
```

**Handler:**
```go
func (h *TenantHandler) List(c *gin.Context) {
    page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
    limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
    
    if limit > 100 {
        limit = 100
    }
    
    offset := (page - 1) * limit
    
    params := db.ListTenantsParams{
        Status:    sql.NullString{String: c.Query("status"), Valid: c.Query("status") != ""},
        Plan:      sql.NullString{String: c.Query("plan"), Valid: c.Query("plan") != ""},
        LimitVal:  int32(limit),
        OffsetVal: int32(offset),
    }
    
    tenants, err := h.queries.ListTenants(c.Request.Context(), params)
    if err != nil {
        c.Error(err)
        return
    }
    
    total, err := h.queries.CountTenants(c.Request.Context(), db.CountTenantsParams{
        Status: params.Status,
        Plan:   params.Plan,
    })
    if err != nil {
        c.Error(err)
        return
    }
    
    var nextPage *int
    if len(tenants) == limit && page*limit < int(total) {
        next := page + 1
        nextPage = &next
    }
    
    c.JSON(http.StatusOK, ListTenantsResponse{
        Tenants: tenants,
        Pagination: Pagination{
            Page:       page,
            Limit:      limit,
            Total:      int(total),
            TotalPages: (int(total) + limit - 1) / limit,
            NextPage:   nextPage,
        },
    })
}
```

### 4.6 Graceful Shutdown Pattern

**Main Entrypoint (`cmd/zero-ops-api/main.go`):**
```go
func main() {
    cfg, err := config.Load()
    if err != nil {
        fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
        os.Exit(1)
    }
    
    logger := zap.Must(zap.NewProduction())
    defer logger.Sync()

    // Initialize database pool
    dbPool, err := db.NewPool(cfg.DatabaseURL)
    if err != nil {
        logger.Fatal("failed to connect to database", zap.Error(err))
    }
    defer dbPool.Close()

    // Initialize server
    srv := api.NewServer(cfg, dbPool, logger)
    
    // Start server in goroutine
    go func() {
        if err := srv.Start(); err != nil && err != http.ErrServerClosed {
            logger.Fatal("server failed", zap.Error(err))
        }
    }()

    // Wait for interrupt signal
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
    <-quit

    logger.Info("shutting down server...")

    // Graceful shutdown with 10s timeout
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    if err := srv.Shutdown(ctx); err != nil {
        logger.Fatal("server forced to shutdown", zap.Error(err))
    }

    logger.Info("server exited")
}
```

### 4.7 Observability Pattern

**Structured Logging Middleware:**
```go
var skipPaths = map[string]bool{
    "/healthz": true,
    "/metrics": true,
}

func LoggerMiddleware(logger *zap.Logger) gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        path := c.Request.URL.Path
        query := c.Request.URL.RawQuery

        c.Next()

        // Skip logging for infrastructure endpoints
        if skipPaths[path] {
            return
        }

        latency := time.Since(start)
        
        logger.Info("request",
            zap.String("method", c.Request.Method),
            zap.String("path", path),
            zap.String("query", query),
            zap.Int("status", c.Writer.Status()),
            zap.Duration("latency", latency),
            zap.String("ip", c.ClientIP()),
            zap.String("user_agent", c.Request.UserAgent()),
        )
    }
}
```

**Prometheus Metrics:**
```go
var (
    httpRequestsTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "zero_ops_api_http_requests_total",
            Help: "Total number of HTTP requests",
        },
        []string{"method", "path", "status"},
    )

    httpRequestDuration = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "zero_ops_api_http_request_duration_seconds",
            Help:    "HTTP request latency",
            Buckets: []float64{0.005, 0.010, 0.025, 0.050, 0.075, 0.100, 0.150, 0.200, 0.500}, // Aligned with <100ms p99 SLO
        },
        []string{"method", "path"},
    )
)

func MetricsMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        path := c.FullPath()
        if path == "" {
            path = "unknown"
        }

        c.Next()

        duration := time.Since(start).Seconds()
        status := strconv.Itoa(c.Writer.Status())

        httpRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
        httpRequestDuration.WithLabelValues(c.Request.Method, path).Observe(duration)
    }
}
```

---

## 5. Database Schema

### 5.1 Migration File (`internal/db/migrations/001_create_tenants.sql`)

```sql
-- Create tenants table
CREATE TABLE IF NOT EXISTS tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID,
    name VARCHAR(63) NOT NULL,
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

-- Partial unique index: allows soft-deleted names to be reused
CREATE UNIQUE INDEX idx_tenants_name_active ON tenants(name) WHERE deleted_at IS NULL;

-- Indexes for performance
CREATE INDEX idx_tenants_org_id ON tenants(org_id) WHERE org_id IS NOT NULL;
CREATE INDEX idx_tenants_status ON tenants(status);
CREATE INDEX idx_tenants_plan ON tenants(plan);
CREATE INDEX idx_tenants_created_at ON tenants(created_at DESC);

-- Trigger to update updated_at automatically
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_tenants_updated_at
    BEFORE UPDATE ON tenants
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
```

---

## 6. Configuration Management

### 6.1 Configuration Struct (`internal/config/config.go`)

```go
type Config struct {
    ServerPort  string `env:"SERVER_PORT" envDefault:"8080"`
    DatabaseURL string `env:"DATABASE_URL" envDefault:"postgres://localhost:5432/zeroops?sslmode=disable"`
    LogLevel    string `env:"LOG_LEVEL" envDefault:"info"`
    Environment string `env:"ENVIRONMENT" envDefault:"development"`
}

func Load() (*Config, error) {
    cfg := &Config{}
    if err := env.Parse(cfg); err != nil {
        return nil, fmt.Errorf("failed to parse environment variables: %w", err)
    }
    return cfg, nil
}
```

### 6.2 Environment Variables

```bash
# Server
SERVER_PORT=8080

# Database
DATABASE_URL=postgres://user:pass@localhost:5432/zeroops?sslmode=disable

# Observability
LOG_LEVEL=info

# Environment
ENVIRONMENT=production
```

---

## 7. Testing Strategy

### 7.1 Unit Tests

**Service Layer Test:**
```go
func TestTenantService_CreateTenant(t *testing.T) {
    // Setup mock database
    mockDB := setupMockDB(t)
    defer mockDB.Close()

    service := &TenantService{
        queries: db.New(mockDB),
        logger:  zap.NewNop(),
    }

    // Test valid creation
    req := CreateTenantRequest{
        Name:  "test-corp",
        Email: "admin@test.com",
        Plan:  "professional",
    }

    tenant, created, err := service.CreateTenant(context.Background(), req)
    
    assert.NoError(t, err)
    assert.True(t, created)
    assert.Equal(t, "test-corp", tenant.Name)
    assert.Equal(t, "professional", tenant.Plan)
}

func TestTenantService_CreateTenant_InvalidName(t *testing.T) {
    service := &TenantService{logger: zap.NewNop()}

    req := CreateTenantRequest{
        Name:  "Invalid Name!",
        Email: "admin@test.com",
        Plan:  "free",
    }

    _, _, err := service.CreateTenant(context.Background(), req)
    
    assert.Error(t, err)
    apiErr, ok := err.(*APIError)
    assert.True(t, ok)
    assert.Equal(t, "VALIDATION_ERROR", apiErr.Code)
}
```

### 7.2 Integration Tests (Using Testcontainers-Go)

**Testcontainers Setup:**
```go
import (
    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/modules/postgres"
    "github.com/testcontainers/testcontainers-go/wait"
)

func setupTestDB(t *testing.T) *pgxpool.Pool {
    ctx := context.Background()
    
    // Start ephemeral PostgreSQL container
    pgContainer, err := postgres.RunContainer(ctx,
        testcontainers.WithImage("postgres:15-alpine"),
        postgres.WithDatabase("testdb"),
        postgres.WithUsername("testuser"),
        postgres.WithPassword("testpass"),
        testcontainers.WithWaitStrategy(
            wait.ForLog("database system is ready to accept connections").
                WithOccurrence(2).
                WithStartupTimeout(5*time.Second)),
    )
    require.NoError(t, err)
    t.Cleanup(func() {
        require.NoError(t, pgContainer.Terminate(ctx))
    })
    
    connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
    require.NoError(t, err)
    
    // Run migrations
    require.NoError(t, runMigrations(connStr))
    
    // Create connection pool
    pool, err := pgxpool.New(ctx, connStr)
    require.NoError(t, err)
    
    return pool
}
```

**Database Integration Test:**
```go
func TestIntegration_UpsertTenant(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    dbPool := setupTestDB(t)
    defer dbPool.Close()

    queries := db.New(dbPool)

    // First insert
    params := db.UpsertTenantParams{
        ID:    uuid.New(),
        Name:  "test-tenant",
        Email: "test@example.com",
        Plan:  "free",
        Quotas: map[string]interface{}{
            "maxClusters": 1,
        },
    }

    result1, err := queries.UpsertTenant(context.Background(), params)
    assert.NoError(t, err)
    assert.True(t, result1.Created)

    // Second insert (idempotent - no mutation)
    result2, err := queries.UpsertTenant(context.Background(), params)
    assert.NoError(t, err)
    assert.False(t, result2.Created)
    assert.Equal(t, "free", result2.Plan)  // Plan unchanged due to true idempotency
}
```

**Benefits:**
- Self-contained: `go test ./...` works on any machine with Docker
- No manual `docker-compose up` required
- Automatic cleanup after test completion
- Parallel test execution with isolated containers

---

## 8. Performance Considerations

### 8.1 Database Connection Pooling

```go
func NewPool(databaseURL string) (*pgxpool.Pool, error) {
    config, err := pgxpool.ParseConfig(databaseURL)
    if err != nil {
        return nil, err
    }

    // Production settings
    config.MinConns = 10
    config.MaxConns = 50
    config.MaxConnLifetime = 30 * time.Minute  // Shorter than default to handle connection churn
    config.MaxConnIdleTime = 30 * time.Minute
    config.HealthCheckPeriod = time.Minute

    return pgxpool.NewWithConfig(context.Background(), config)
}
```

### 8.2 Query Timeouts

All database queries use context with 200ms timeout:
```go
ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
defer cancel()
```

### 8.3 Index Strategy

- `name` (unique): Primary lookup key
- `org_id`: Future multi-tenancy queries
- `status`: Filtering active/suspended tenants
- `plan`: Billing queries
- `created_at DESC`: Pagination ordering

---

## 9. Security Considerations

### 9.1 Input Validation

- RFC 1123 validation at Gin binding layer
- Email format validation via `validator` tags
- Plan enum validation (free/professional/enterprise)
- SQL injection prevention via parameterized queries (sqlc)

### 9.2 Database Constraints

- `CHECK` constraints on `plan` and `status` enums
- `UNIQUE` constraint on `name`
- Regex constraint on `name` format at database level

### 9.3 Error Message Safety

- Never expose internal database errors to API responses
- Generic "database error" message for unexpected failures
- Specific validation errors for user-correctable issues

---

## 10. Deployment Considerations

### 10.1 Health Checks

```go
func (s *Server) RegisterHealthChecks() {
    s.router.GET("/healthz", func(c *gin.Context) {
        c.JSON(http.StatusOK, gin.H{"status": "healthy"})
    })

    s.router.GET("/readyz", func(c *gin.Context) {
        // Check database connectivity
        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()

        if err := s.dbPool.Ping(ctx); err != nil {
            c.JSON(http.StatusServiceUnavailable, gin.H{
                "status": "not ready",
                "error":  "database unreachable",
            })
            return
        }

        c.JSON(http.StatusOK, gin.H{"status": "ready"})
    })
}
```

### 10.2 Metrics Endpoint

```go
s.router.GET("/metrics", gin.WrapH(promhttp.Handler()))
```

### 10.3 Build & Run

```bash
# Generate sqlc code
make sqlc-generate

# Run migrations (local development)
make migrate-up

# Run tests (uses Testcontainers - requires Docker)
go test ./... -v

# Build binary
go build -o bin/zero-ops-api cmd/zero-ops-api/main.go

# Run locally (requires DATABASE_URL)
export DATABASE_URL="postgres://user:pass@localhost:5432/zeroops?sslmode=disable"
./bin/zero-ops-api

# Run in production (connects to CNPG)
export DATABASE_URL="postgres://user:pass@zero-ops-db-rw.zero-ops-system.svc:5432/zeroops"
./bin/zero-ops-api
```

**Database Strategy:**
- **Local Development:** Docker Compose or local Postgres instance
- **Testing:** Testcontainers-Go (ephemeral containers per test)
- **Production:** CloudNativePG (CNPG) service in Management Cluster

---

## 11. Future Considerations (Out of Scope)

### 11.1 Kubernetes Integration (Phase 2)
- Add K8s client with SharedInformerFactory
- Implement typed applyconfigurations for SSA
- Use errgroup for parallel resource creation
- Add /readyz gate on informer cache sync

### 11.2 Authentication (Phase 3)
- JWT validation middleware
- Ory Kratos integration
- OAuth Device Flow support

### 11.3 Soft-Delete Restoration (Phase 4)
- `POST /api/v1/tenants/{id}/restore` endpoint (platform-admin only)
- Requires auth to prevent unauthorized restoration
- Manual DB intervention required until Phase 4

### 11.4 Hard Delete / GDPR Purge (Phase 4)
- `DELETE /api/v1/tenants/{id}/purge` endpoint (super-admin only)
- Physical row deletion for GDPR Article 17 compliance
- Separate endpoint from soft-delete to prevent accidental data loss
- Manual purge via DB required until Phase 4

### 11.5 Audit Logging (Phase 4)
- Separate audit_logs table
- Capture all mutations with user context
- GDPR compliance exports

---

**End of Design Document**
