To build the `zero-ops-api` into a production-grade, highly reliable REST API that perfectly serves LLM agents via MCP, we can structure the project using the best architectural patterns found in Caddy. 

Below is the proposed directory structure, followed by specific implementation patterns with direct references to the provided Caddy source files. This will serve as a definitive guide for your developers.

---

### Proposed Directory Structure

```text
.
├── cmd/
│   └── zero-ops-api/
│       └── main.go                 # App entrypoint, graceful shutdown, signal trapping
├── internal/
│   ├── api/
│   │   ├── server.go               # Gin router setup, route definitions
│   │   ├── handlers/               # Tenant handlers (Create, Update, Delete)
│   │   └── middleware/             # Agentic error formatting, auth validation, logging
│   ├── core/
│   │   ├── tenant/                 # Business logic, idempotency checks
│   │   └── errors/                 # Standardized Agent/LLM-facing errors
│   ├── db/
│   │   ├── migrations/             # SQL files managed by pressly/goose
│   │   └── postgres.go             # jackc/pgx connection pool management
│   └── k8s/
│       └── client.go               # SharedInformerFactory, Server-Side Apply wrappers
├── pkg/
│   └── config/
│       └── config.go               # App configuration (ports, DB DSN, Hydra endpoints)
├── go.mod
└── go.sum
```

**New Dependencies:**
*   **Router:** `github.com/gin-gonic/gin` (High performance, excellent validation integration)
*   **Database:** `github.com/jackc/pgx/v5` (Native Postgres driver, high performance)
*   **Migrations:** `github.com/pressly/goose/v3` (Clean SQL-based migrations)
*   **Validation:** `github.com/go-playground/validator/v10` (Included with Gin)

---

### 1. Application Entrypoint & Graceful Shutdown
**Path:** `cmd/zero-ops-api/main.go`
**Caddy Reference:** `sigtrap.go` (`TrapSignals()`), `caddy.go` (`Stop()`, `exitProcess()`)

Like Caddy, the API should trap OS signals to gracefully shut down the Gin server, drain Postgres connection pools, and cleanly stop Kubernetes Informers without dropping active agent requests.

```go
// Pattern reference: caddy.go (exitProcess) and sigtrap.go (TrapSignals)
func main() {
    cfg := config.Load()
    logger := logger.New()

    // 1. Initialize components (DB, K8s, API)
    dbPool := db.NewPostgresPool(cfg.DatabaseURL)
    k8sClient := k8s.NewClient(cfg.KubeConfig)
    
    // 2. Start API Server
    srv := api.NewServer(cfg, dbPool, k8sClient, logger)
    go srv.Start()

    // 3. Graceful Shutdown (Inspired by caddy/sigtrap.go)
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
    <-quit
    
    logger.Info("shutting down zero-ops-api...")
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    if err := srv.Stop(ctx); err != nil {
        logger.Fatal("server forced to shutdown", zap.Error(err))
    }
    dbPool.Close()
}
```

### 2. Standardized Agentic Error Handling
**Path:** `internal/core/errors/errors.go` and `internal/api/middleware/error.go`
**Caddy Reference:** `admin.go` (`APIError`), `modules/caddyhttp/errors.go` (`HandlerError`)

Agents rely entirely on HTTP status codes and deterministic JSON schemas to know if a tool call succeeded or if it needs to self-correct. We must strictly enforce Caddy's `APIError` pattern.

```go
// Pattern reference: admin.go - type APIError struct
package errors

// AgentError provides a deterministic JSON schema for the LLM
type AgentError struct {
    HTTPStatus int    `json:"-"`
    Code       string `json:"code"`
    Message    string `json:"error"`     // Maps to Caddy's `error` JSON key
    Actionable bool   `json:"actionable"` // Tells the agent if it should retry
}

func (e AgentError) Error() string { return e.Message }

// Pattern reference: admin.go - handleError()
func ErrorMiddleware(logger *zap.Logger) gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Next()

        if len(c.Errors) > 0 {
            err := c.Errors.Last().Err
            if agentErr, ok := err.(AgentError); ok {
                logger.Warn("request error", 
                    zap.Int("status", agentErr.HTTPStatus),
                    zap.String("code", agentErr.Code),
                )
                c.JSON(agentErr.HTTPStatus, agentErr)
            } else {
                // Fallback for unhandled panics/errors
                c.JSON(500, AgentError{
                    Code:    "internal_error",
                    Message: "An unexpected platform error occurred.",
                })
            }
        }
    }
}
```

### 3. Module Provisioning & Lifecycle (K8s & DB)
**Path:** `internal/k8s/client.go`
**Caddy Reference:** `modules.go` (`Provisioner` and `CleanerUpper` interfaces)

Caddy uses a `Provision(ctx Context)` phase to safely allocate resources, build caches, and prepare before taking traffic. We use this for the Kubernetes Informer cache.

```go
// Pattern reference: modules.go - type Provisioner interface
type Provisioner interface {
    Provision(ctx context.Context, logger *zap.Logger) error
}

type K8sClient struct {
    informers informers.SharedInformerFactory
    logger    *zap.Logger
}

func (k *K8sClient) Provision(ctx context.Context, logger *zap.Logger) error {
    k.logger = logger.Named("k8s")
    
    // Start informers in background
    k.informers.Start(ctx.Done())
    
    // Crucial: Wait for cache sync before returning. 
    // Prevents the API from serving 404s for resources that exist but aren't cached yet.
    k.logger.Info("waiting for K8s informer caches to sync...")
    for typ, ok := range k.informers.WaitForCacheSync(ctx.Done()) {
        if !ok {
            return fmt.Errorf("failed to sync K8s cache for %v", typ)
        }
    }
    return nil
}
```

### 4. Context-Aware Structured Logging
**Path:** `internal/api/middleware/logger.go`
**Caddy Reference:** `context.go` (`Logger()`), `modules/caddyhttp/server.go` (`logRequest()`)

Caddy never uses standard `log.Printf`. It relies on injected `*zap.Logger` instances that carry context (e.g., Request ID, Client IP). For agent interactions, logging the `TraceID` and `Agent/User ID` is critical for auditing.

```go
// Pattern reference: modules/caddyhttp/server.go - s.logRequest
func AgentLoggerMiddleware(rootLogger *zap.Logger) gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        
        // Extract Auth info (set by Auth middleware)
        agentID := c.GetString("agent_id")
        
        // Inject a request-scoped logger into the Gin context
        reqLogger := rootLogger.With(
            zap.String("method", c.Request.Method),
            zap.String("path", c.Request.URL.Path),
            zap.String("agent_id", agentID),
        )
        c.Set("logger", reqLogger)

        c.Next()

        // Log completion
        reqLogger.Info("handled agent request",
            zap.Int("status", c.Writer.Status()),
            zap.Duration("latency", time.Since(start)),
        )
    }
}
```

### 5. Idempotent Core Logic (Tenant Creation)
**Path:** `internal/core/tenant/service.go`
**Caddy Reference:** `caddy.go` (`changeConfig` & `unsyncedDecodeAndRun` — blind apply architecture)

Caddy avoids "drift" by blindly loading and applying the state passed to it. In Kubernetes, this is achieved using Server-Side Apply (SSA), and in PostgreSQL via `ON CONFLICT`.

```go
// Handler
func (h *Handler) Create(c *gin.Context) {
    logger := c.MustGet("logger").(*zap.Logger)
    
    var req CreateTenantRequest // Uses validator tags
    if err := c.ShouldBindJSON(&req); err != nil {
        c.Error(errors.AgentError{
            HTTPStatus: 400,
            Code:       "invalid_schema",
            Message:    err.Error(),
            Actionable: true, // LLM knows it can fix the JSON and retry
        })
        return
    }

    // Apply Caddy's philosophy: normalize, then blindly apply target state
    normalizedName := normalizeRFC1123(req.Name)
    
    // Idempotent Postgres Insert
    if err := h.db.UpsertTenant(c.Request.Context(), normalizedName, req.Plan); err != nil {
        c.Error(err)
        return
    }

    // Idempotent Kubernetes Server-Side Apply
    if err := h.k8s.ApplyTenantBoundaries(c.Request.Context(), normalizedName, req.Plan); err != nil {
        c.Error(err)
        return
    }

    c.JSON(201, gin.H{
        "tenant_id": normalizedName,
        "namespace": "tenant-" + normalizedName,
        "status":    "provisioned",
    })
}
```

### Summary for Developers
When developers look at the Caddy source files referenced above, they should take away the following core principles:
1. **Never Panic, Never Leak:** Review `UsagePool` in `usagepool.go` and `context.go` cleanup funcs.
2. **Predictable Errors:** Review `APIError` in `admin.go`. If an LLM reads a `500` HTML page, it fails. If it reads a deterministic JSON error code, it fixes its prompt and succeeds.
3. **Stateless Operations:** Like Caddy's config reloads (`caddy.Load()`), the `zero-ops-api` endpoints should take a JSON blob, normalize it, and force the infrastructure to match that state using SSA. No complex "if exists then X else Y" logic.