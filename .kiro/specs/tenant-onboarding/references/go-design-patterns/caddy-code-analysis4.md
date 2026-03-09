Here are 5 more advanced, enterprise-grade Go design patterns extracted directly from the Caddy codebase. These patterns solve complex distributed systems problems like cache stampedes, telemetry bottlenecks, and tight coupling, all of which are highly relevant for your `zero-ops-api`.

---

### 1. Thundering Herd Protection (`singleflight`)
**PRD Context:** Your Agent Gateway or `zero-ops-api` needs to validate JWTs against Ory Hydra's JWKS (JSON Web Key Set). If an agent fires 50 concurrent requests and the JWKS cache is expired, you do not want 50 concurrent network calls hammering Hydra.
**Caddy Pattern:** Caddy uses `golang.org/x/sync/singleflight` in its Basic Auth module to ensure that simultaneous requests verifying the same expensive hash only compute it once.
**Caddy Reference:** `modules/caddyhttp/caddyauth/basicauth.go` (Look for `singleflight.Group`).

**Application for `zero-ops-api`:**
Wrap expensive, concurrent-heavy operations (like fetching OIDC configs, JWKS, or querying K8s state that isn't cached yet) in a `singleflight` group.

```go
// internal/auth/jwks.go
import "golang.org/x/sync/singleflight"

type JWKSFetcher struct {
    cache  *jwk.Set
    mu     sync.RWMutex
    sf     singleflight.Group
    client *http.Client
}

func (f *JWKSFetcher) GetKeys(ctx context.Context) (*jwk.Set, error) {
    // 1. Fast path: check cache
    f.mu.RLock()
    keys := f.cache
    f.mu.RUnlock()
    if keys != nil {
        return keys, nil
    }

    // 2. Slow path: Fetch from Hydra, but protect against thundering herd
    // If 100 requests hit this simultaneously, only ONE HTTP call is made.
    v, err, _ := f.sf.Do("fetch_jwks", func() (interface{}, error) {
        return fetchKeysFromHydra(ctx, f.client)
    })
    if err != nil {
        return nil, err
    }

    newKeys := v.(*jwk.Set)
    f.mu.Lock()
    f.cache = newKeys
    f.mu.Unlock()

    return newKeys, nil
}
```

### 2. The `ResponseRecorder` (Interceptor) Pattern
**PRD Context:** You may need to audit log the exact JSON response sent back to the Agent, or inject specific headers (like rate-limit tracking) *after* the handler has executed but *before* the client receives the payload.
**Caddy Pattern:** Caddy rarely writes directly to the standard `http.ResponseWriter`. Instead, it wraps it in a `ResponseRecorder` that buffers the output, allowing middlewares higher up the chain to inspect the status code or mutate the body.
**Caddy Reference:** `modules/caddyhttp/responsewriter.go` (`NewResponseRecorder`) and `modules/caddyhttp/intercept/intercept.go`.

**Application for `zero-ops-api`:**
Create a custom Gin ResponseWriter to capture the response for your audit logs.

```go
// internal/api/middleware/audit.go
type auditResponseWriter struct {
    gin.ResponseWriter
    body *bytes.Buffer
}

func (w *auditResponseWriter) Write(b []byte) (int, error) {
    w.body.Write(b) // Capture the response payload
    return w.ResponseWriter.Write(b)
}

func AuditLogMiddleware(logger *zap.Logger) gin.HandlerFunc {
    return func(c *gin.Context) {
        // Wrap the writer
        w := &auditResponseWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
        c.Writer = w

        c.Next() // Execute handlers

        // Now we can securely log what the LLM agent actually received
        logger.Info("audit log",
            zap.String("path", c.Request.URL.Path),
            zap.Int("status", c.Writer.Status()),
            zap.String("response_payload", w.body.String()), // Captured!
        )
    }
}
```

### 3. Lock-Free High-Performance State Tracking (`sync/atomic`)
**PRD Context:** You need to track in-flight requests or track API limits for a tenant. Using `sync.Mutex` around a counter creates a massive bottleneck when throughput is high.
**Caddy Pattern:** Caddy uses `sync/atomic` extensively for tracking active requests, health check failures, and connections without blocking the main request thread.
**Caddy Reference:** `modules/caddyhttp/reverseproxy/hosts.go` (`atomic.AddInt64`, `atomic.LoadInt64`).

**Application for `zero-ops-api`:**
Use atomic variables for tracking tenant limits or system health metrics.

```go
// internal/core/tenant/metrics.go
import "sync/atomic"

type TenantState struct {
    InFlightRequests atomic.Int64
    ProvisionErrors  atomic.Int64
}

func (t *TenantState) AddRequest() {
    t.InFlightRequests.Add(1)
}

func (t *TenantState) DoneRequest() {
    t.InFlightRequests.Add(-1)
}

func (t *TenantState) RecordError() {
    t.ProvisionErrors.Add(1)
}

func (t *TenantState) IsOverloaded() bool {
    // Lock-free read!
    return t.InFlightRequests.Load() > 100 
}
```

### 4. The Pluggable "Registry" Pattern (Dependency Inversion)
**PRD Context:** The PRD mentions "BYOC-ready infrastructure". You will eventually need to provision tenants not just on local K8s, but on AWS EKS, Hetzner, or GCP. 
**Caddy Pattern:** Caddy is built entirely on a module registry. Modules register themselves in `init()` functions using a unique ID, meaning the core engine doesn't need to import every possible provider.
**Caddy Reference:** `modules.go` (`RegisterModule`, `GetModule`), `caddyconfig/configadapters.go` (`RegisterAdapter`).

**Application for `zero-ops-api`:**
Decouple your infrastructure provisioning logic from your Gin handlers early using a registry.

```go
// internal/core/cloud/registry.go
type Provider interface {
    ApplyTenant(ctx context.Context, tenantName string) error
}

var providers = make(map[string]Provider)

func RegisterProvider(name string, p Provider) {
    if _, exists := providers[name]; exists {
        panic("provider already registered: " + name)
    }
    providers[name] = p
}

func GetProvider(name string) (Provider, error) {
    p, ok := providers[name]
    if !ok {
        return nil, fmt.Errorf("unsupported cloud provider: %s", name)
    }
    return p, nil
}

// -----------------------------
// internal/k8s/hetzner/hetzner.go
func init() {
    // Self-registers on startup
    cloud.RegisterProvider("hetzner", &HetznerProvider{})
}

// In your API handler:
provider, _ := cloud.GetProvider(req.CloudTarget) // dynamically routed!
provider.ApplyTenant(ctx, req.Name)
```

### 5. Standardized RED Telemetry (Prometheus)
**PRD Context:** You have strict SLOs (e.g., < 500ms latency). You need to prove to the business that the platform meets this.
**Caddy Pattern:** Caddy uses Prometheus `promauto` to initialize metrics once and provides a clean middleware wrapper that extracts the status code and method to automatically observe RED (Rate, Errors, Duration) metrics.
**Caddy Reference:** `metrics.go` (`instrumentHandlerCounter`), `modules/caddyhttp/metrics.go` (`metricsInstrumentedRoute`).

**Application for `zero-ops-api`:**
Implement a Gin middleware that records the exact same high-quality telemetry as Caddy.

```go
// internal/api/telemetry/metrics.go
import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    reqCount = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "zero_ops_api_requests_total",
        Help: "Total number of HTTP requests made.",
    }, []string{"method", "path", "status"})

    reqDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Name:    "zero_ops_api_request_duration_seconds",
        Help:    "Histogram of request latencies.",
        Buckets: prometheus.DefBuckets,
    }, []string{"method", "path"})
)

func MetricsMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()

        c.Next()

        duration := time.Since(start).Seconds()
        status := strconv.Itoa(c.Writer.Status())
        path := c.FullPath() // Use FullPath (e.g. /users/:id) to prevent cardinality explosion

        if path == "" {
            path = "unknown"
        }

        reqCount.WithLabelValues(c.Request.Method, path, status).Inc()
        reqDuration.WithLabelValues(c.Request.Method, path).Observe(duration)
    }
}
```