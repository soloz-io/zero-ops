---
inclusion: always
---
<!------------------------------------------------------------------------------------
   Add rules to this file or a short description and have Kiro refine them for you.
   
   Learn about inclusion modes: https://kiro.dev/docs/steering/#inclusion-modes
-------------------------------------------------------------------------------------> 

# open-sbt Multi-Tenant Microservice Libraries

## Overview

Multi-tenant microservice libraries provide reusable components that tenant-facing microservices can use to automatically handle tenant context, isolation, and security. These libraries abstract away the complexity of multi-tenancy so that application developers can focus on business logic.

**Inspired by SBT-AWS Token Vending Machine** - which provides tenant-scoped AWS credentials using ABAC (Attribute-Based Access Control).

## SBT-AWS Observability Analysis

### SBT-AWS Approach
The SBT-AWS toolkit has minimal built-in observability, following these patterns:

1. **Infrastructure-First**: Relies on AWS and Kubernetes native monitoring
2. **Cost-Focused**: Primary observability concern is cost attribution via KubeCost
3. **Basic Logging**: Simple application logging with tenant context
4. **External Integration**: Expects users to add their own observability stack

### open-sbt Approach
open-sbt follows the **interface-based abstraction** principle, providing:

1. **Batteries Included**: Default implementations for comprehensive observability
2. **Pluggable Architecture**: Interfaces allow custom implementations
3. **Best of Both Worlds**: Use defaults for rapid development, swap implementations for specific needs
4. **Tenant-Aware by Design**: All observability components automatically include tenant context

**Key Architectural Decision:**
```go
// open-sbt provides interfaces, not concrete implementations
type IObservability interface {
    Logger() ILogger
    Metrics() IMetrics
    Tracer() ITracer
    CostTracker() ICostTracker
}

// Users can choose implementations
observability := defaultobservability.New() // Default stack
// OR
observability := customobservability.New()  // Custom stack
// OR mix and match
observability := &CustomObservability{
    logger: defaultlogging.New(),      // Use default logger
    metrics: datadogmetrics.New(),     // Use Datadog metrics
    tracer: jaegertracing.New(),       // Use Jaeger tracing
    cost: customcost.New(),            // Use custom cost tracking
}
```

### Limitations Found in SBT-AWS
- **No Distributed Tracing**: No AWS X-Ray, Jaeger, or Zipkin integration
- **No APM Integration**: No New Relic, Datadog, or Dynatrace support
- **No Centralized Logging**: No CloudWatch Logs or ELK/EFK stack
- **No Custom Application Metrics**: No Prometheus metrics in application code
- **No Alerting Mechanisms**: No SLA/SLO monitoring or incident response
- **Limited Observability Libraries**: Only basic Log4j2 logging with tenant context

### SBT-AWS Observability Evidence
From the SBT-AWS codebase analysis:

**Basic Tenant Logging (Java):**
```java
// OrderController.java - Simple tenant-aware logging
logger.error("TenantId: " + tenantId + "-get orders failed: ", e);
```

**Cost Monitoring (Optional KubeCost):**
```yaml
# values-eks-cost-monitoring.yaml
kubecostFrontend:
  image: public.ecr.aws/kubecost/frontend
prometheus:
  server:
    image:
      repository: public.ecr.aws/kubecost/prometheus
```

**Kubernetes Labels for Cost Attribution:**
```yaml
# kustomization.yaml
commonLabels:
  saas/tenant: "true"
  saas/service: order
```

### open-sbt Enhancement Strategy
open-sbt addresses these limitations by providing comprehensive observability libraries that SBT-AWS lacks.

## Core Libraries

**Architecture Pattern:** open-sbt follows **interface-based abstraction** - providing default implementations while allowing users to plug in their own observability stack.

### Observability Interfaces

```go
// Core observability interfaces
type ILogger interface {
    WithContext(ctx context.Context) ILogEntry
    Info(ctx context.Context, msg string, fields map[string]interface{})
    Error(ctx context.Context, msg string, err error, fields map[string]interface{})
    Warn(ctx context.Context, msg string, fields map[string]interface{})
}

type IMetrics interface {
    RecordRequest(ctx context.Context, method, path string, status int, duration float64)
    RecordError(ctx context.Context, errorType string)
    GinMiddleware() gin.HandlerFunc
}

type ITracer interface {
    StartSpan(ctx context.Context, operationName string) (context.Context, trace.Span)
    GinMiddleware() gin.HandlerFunc
}

type ICostTracker interface {
    RecordResourceUsage(ctx context.Context, resourceType string, usage float64, unit string)
    RecordCost(ctx context.Context, costType string, amount float64, currency string)
    GinMiddleware() gin.HandlerFunc
}
```

### Implementation Strategy

**Default Implementations (Batteries Included):**
- **Logging**: Logrus with JSON formatting and tenant context injection
- **Metrics**: Prometheus with VictoriaMetrics backend
- **Tracing**: OpenTelemetry with Jaeger exporter
- **Cost Tracking**: Built-in cost attribution with Prometheus metrics

**Custom Implementations (Pluggable):**
- **Logging**: Datadog, New Relic, Splunk, ELK Stack
- **Metrics**: Datadog, New Relic, CloudWatch, InfluxDB
- **Tracing**: Jaeger, Zipkin, AWS X-Ray, Datadog APM
- **Cost Tracking**: Custom billing systems, cloud provider APIs

**Usage Examples:**
```go
// Option 1: Use all defaults (rapid development)
observability := defaultobservability.New()

// Option 2: Use all custom (enterprise requirements)
observability := &CustomObservability{
    logger: datadoglogging.New(datadogConfig),
    metrics: newrelicmetrics.New(newrelicConfig),
    tracer: jaegertracing.New(jaegerConfig),
    cost: customcost.New(billingConfig),
}

// Option 3: Mix and match (common scenario)
observability := &MixedObservability{
    logger: defaultlogging.New(),           // Use default
    metrics: datadogmetrics.New(config),    // Use Datadog
    tracer: defaulttracing.New(),           // Use default
    cost: enterprisecost.New(config),       // Use enterprise
}
```

### 1. Identity Token Manager (Default Implementation)

**Purpose:** Extract tenant context from JWT tokens and make it available to the application.

**Interface:**
```go
type IIdentityTokenManager interface {
    ValidateAndExtract(tokenString string) (*TenantClaims, error)
    GinMiddleware() gin.HandlerFunc
}
```

**Pattern from SBT-AWS:**
- Token Vending Machine validates JWT and extracts custom attributes (e.g., `custom:tenantId`)
- Maps JWT claims to session tags for ABAC policies
- Provides tenant-scoped credentials

**Default Implementation (Ory Stack):**

```go
package tenantcontext

import (
    "context"
    "errors"
    "github.com/golang-jwt/jwt/v5"
    "github.com/lestrrat-go/jwx/v2/jwk"
)

// TenantClaims represents the tenant-specific claims in a JWT
type TenantClaims struct {
    TenantID   string `json:"tenant_id"`
    TenantTier string `json:"tenant_tier"`
    UserID     string `json:"sub"`
    Email      string `json:"email"`
    jwt.RegisteredClaims
}

// IdentityTokenManager validates JWTs and extracts tenant context
type IdentityTokenManager struct {
    jwksURL  string
    issuer   string
    audience string
    keySet   jwk.Set
}

func NewIdentityTokenManager(jwksURL, issuer, audience string) (*IdentityTokenManager, error) {
    // Fetch JWKS from Ory Hydra
    keySet, err := jwk.Fetch(context.Background(), jwksURL)
    if err != nil {
        return nil, err
    }
    
    return &IdentityTokenManager{
        jwksURL:  jwksURL,
        issuer:   issuer,
        audience: audience,
        keySet:   keySet,
    }, nil
}
```

// ValidateAndExtract validates the JWT and extracts tenant context
func (m *IdentityTokenManager) ValidateAndExtract(tokenString string) (*TenantClaims, error) {
    token, err := jwt.ParseWithClaims(tokenString, &TenantClaims{}, func(token *jwt.Token) (interface{}, error) {
        // Get the key ID from token header
        kid, ok := token.Header["kid"].(string)
        if !ok {
            return nil, errors.New("kid not found in token header")
        }
        
        // Find the key in JWKS
        key, ok := m.keySet.LookupKeyID(kid)
        if !ok {
            return nil, errors.New("key not found in JWKS")
        }
        
        var rawKey interface{}
        if err := key.Raw(&rawKey); err != nil {
            return nil, err
        }
        
        return rawKey, nil
    })
    
    if err != nil {
        return nil, err
    }
    
    claims, ok := token.Claims.(*TenantClaims)
    if !ok || !token.Valid {
        return nil, errors.New("invalid token")
    }
    
    // Validate issuer and audience
    if claims.Issuer != m.issuer {
        return nil, errors.New("invalid issuer")
    }
    
    if !claims.VerifyAudience(m.audience, true) {
        return nil, errors.New("invalid audience")
    }
    
    return claims, nil
}

// Gin middleware for automatic tenant context extraction
func (m *IdentityTokenManager) GinMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        authHeader := c.GetHeader("Authorization")
        if authHeader == "" {
            c.JSON(401, gin.H{"error": "Authorization header required"})
            c.Abort()
            return
        }
        
        // Extract token from "Bearer <token>"
        tokenString := strings.TrimPrefix(authHeader, "Bearer ")
        
        claims, err := m.ValidateAndExtract(tokenString)
        if err != nil {
            c.JSON(401, gin.H{"error": "Invalid token"})
            c.Abort()
            return
        }
        
        // Add tenant context to Gin context
        c.Set("tenant_id", claims.TenantID)
        c.Set("tenant_tier", claims.TenantTier)
        c.Set("user_id", claims.UserID)
        c.Set("email", claims.Email)
        
        // Add to request context for downstream use
        ctx := context.WithValue(c.Request.Context(), "tenant_id", claims.TenantID)
        ctx = context.WithValue(ctx, "tenant_tier", claims.TenantTier)
        ctx = context.WithValue(ctx, "user_id", claims.UserID)
        c.Request = c.Request.WithContext(ctx)
        
        c.Next()
    }
}
```

### 2. Logging Manager

**Purpose:** Automatically inject tenant context into all log entries.

```go
package tenantlogging

import (
    "context"
    "github.com/sirupsen/logrus"
)

// TenantLogger wraps logrus with automatic tenant context injection
type TenantLogger struct {
    logger *logrus.Logger
}

func NewTenantLogger() *TenantLogger {
    logger := logrus.New()
    logger.SetFormatter(&logrus.JSONFormatter{})
    return &TenantLogger{logger: logger}
}

// WithContext creates a logger with tenant context from the context
func (l *TenantLogger) WithContext(ctx context.Context) *logrus.Entry {
    entry := l.logger.WithFields(logrus.Fields{})
    
    // Extract tenant context
    if tenantID, ok := ctx.Value("tenant_id").(string); ok {
        entry = entry.WithField("tenant_id", tenantID)
    }
    if tenantTier, ok := ctx.Value("tenant_tier").(string); ok {
        entry = entry.WithField("tenant_tier", tenantTier)
    }
    if userID, ok := ctx.Value("user_id").(string); ok {
        entry = entry.WithField("user_id", userID)
    }
    
    return entry
}

// Convenience methods
func (l *TenantLogger) Info(ctx context.Context, msg string, fields map[string]interface{}) {
    entry := l.WithContext(ctx)
    for k, v := range fields {
        entry = entry.WithField(k, v)
    }
    entry.Info(msg)
}

func (l *TenantLogger) Error(ctx context.Context, msg string, err error, fields map[string]interface{}) {
    entry := l.WithContext(ctx).WithError(err)
    for k, v := range fields {
        entry = entry.WithField(k, v)
    }
    entry.Error(msg)
}

func (l *TenantLogger) Warn(ctx context.Context, msg string, fields map[string]interface{}) {
    entry := l.WithContext(ctx)
    for k, v := range fields {
        entry = entry.WithField(k, v)
    }
    entry.Warn(msg)
}
```

**Usage:**
```go
logger := tenantlogging.NewTenantLogger()

// Automatically includes tenant_id, tenant_tier, user_id in logs
logger.Info(ctx, "User created", map[string]interface{}{
    "username": "john.doe",
    "action":   "create_user",
})
// Output: {"tenant_id":"tenant-123","tenant_tier":"premium","user_id":"user-456","username":"john.doe","action":"create_user","level":"info","msg":"User created","time":"2026-03-16T..."}
```

### 3. Metrics Manager

**Purpose:** Automatically tag metrics with tenant context for per-tenant observability.

```go
package tenantmetrics

import (
    "context"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

// TenantMetrics wraps Prometheus metrics with automatic tenant tagging
type TenantMetrics struct {
    requestDuration *prometheus.HistogramVec
    requestCount    *prometheus.CounterVec
    errorCount      *prometheus.CounterVec
}

func NewTenantMetrics(namespace string) *TenantMetrics {
    return &TenantMetrics{
        requestDuration: promauto.NewHistogramVec(
            prometheus.HistogramOpts{
                Namespace: namespace,
                Name:      "request_duration_seconds",
                Help:      "Duration of requests",
            },
            []string{"tenant_id", "tenant_tier", "method", "path", "status"},
        ),
        requestCount: promauto.NewCounterVec(
            prometheus.CounterOpts{
                Namespace: namespace,
                Name:      "request_total",
                Help:      "Total number of requests",
            },
            []string{"tenant_id", "tenant_tier", "method", "path", "status"},
        ),
        errorCount: promauto.NewCounterVec(
            prometheus.CounterOpts{
                Namespace: namespace,
                Name:      "error_total",
                Help:      "Total number of errors",
            },
            []string{"tenant_id", "tenant_tier", "error_type"},
        ),
    }
}

// RecordRequest records a request with tenant context
func (m *TenantMetrics) RecordRequest(ctx context.Context, method, path string, status int, duration float64) {
    tenantID := getTenantIDFromContext(ctx)
    tenantTier := getTenantTierFromContext(ctx)
    
    m.requestDuration.WithLabelValues(
        tenantID,
        tenantTier,
        method,
        path,
        fmt.Sprintf("%d", status),
    ).Observe(duration)
    
    m.requestCount.WithLabelValues(
        tenantID,
        tenantTier,
        method,
        path,
        fmt.Sprintf("%d", status),
    ).Inc()
}

// RecordError records an error with tenant context
func (m *TenantMetrics) RecordError(ctx context.Context, errorType string) {
    tenantID := getTenantIDFromContext(ctx)
    tenantTier := getTenantTierFromContext(ctx)
    
    m.errorCount.WithLabelValues(tenantID, tenantTier, errorType).Inc()
}

// Gin middleware for automatic request metrics
func (m *TenantMetrics) GinMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        
        c.Next()
        
        duration := time.Since(start).Seconds()
        m.RecordRequest(
            c.Request.Context(),
            c.Request.Method,
            c.FullPath(),
            c.Writer.Status(),
            duration,
        )
    }
}

func getTenantIDFromContext(ctx context.Context) string {
    if tenantID, ok := ctx.Value("tenant_id").(string); ok {
        return tenantID
    }
    return "unknown"
}

func getTenantTierFromContext(ctx context.Context) string {
    if tenantTier, ok := ctx.Value("tenant_tier").(string); ok {
        return tenantTier
    }
    return "unknown"
}
```

### 4. Token Vending Machine (Credential Manager)

**Purpose:** Provide tenant-scoped credentials for accessing cloud resources (inspired by SBT-AWS Token Vending Machine).

**SBT-AWS Pattern:**
- Validates JWT token
- Extracts tenant attributes from JWT claims
- Assumes IAM role with session tags (ABAC)
- Returns temporary AWS credentials scoped to tenant

**open-sbt Adaptation for Kubernetes:**

```go
package tokenvendingmachine

import (
    "context"
    "fmt"
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/kubernetes"
)

// TokenVendingMachine provides tenant-scoped credentials
type TokenVendingMachine struct {
    k8sClient       kubernetes.Interface
    tokenManager    *IdentityTokenManager
    credentialStore CredentialStore
}

// CredentialStore interface for storing tenant credentials
type CredentialStore interface {
    GetCredentials(ctx context.Context, tenantID, resourceType string) (*Credentials, error)
    StoreCredentials(ctx context.Context, tenantID, resourceType string, creds *Credentials) error
}

type Credentials struct {
    AccessKey    string `json:"access_key"`
    SecretKey    string `json:"secret_key"`
    SessionToken string `json:"session_token,omitempty"`
    Endpoint     string `json:"endpoint"`
    ExpiresAt    int64  `json:"expires_at"`
}

func NewTokenVendingMachine(k8sClient kubernetes.Interface, tokenManager *IdentityTokenManager, store CredentialStore) *TokenVendingMachine {
    return &TokenVendingMachine{
        k8sClient:       k8sClient,
        tokenManager:    tokenManager,
        credentialStore: store,
    }
}
```

// GetTenantCredentials returns tenant-scoped credentials for a resource type
func (tvm *TokenVendingMachine) GetTenantCredentials(ctx context.Context, jwtToken, resourceType string) (*Credentials, error) {
    // 1. Validate JWT and extract tenant context
    claims, err := tvm.tokenManager.ValidateAndExtract(jwtToken)
    if err != nil {
        return nil, fmt.Errorf("invalid token: %w", err)
    }
    
    tenantID := claims.TenantID
    
    // 2. Check if credentials already exist and are valid
    creds, err := tvm.credentialStore.GetCredentials(ctx, tenantID, resourceType)
    if err == nil && creds.ExpiresAt > time.Now().Unix() {
        return creds, nil
    }
    
    // 3. Generate new tenant-scoped credentials
    switch resourceType {
    case "s3":
        return tvm.generateS3Credentials(ctx, tenantID)
    case "database":
        return tvm.generateDatabaseCredentials(ctx, tenantID)
    default:
        return nil, fmt.Errorf("unsupported resource type: %s", resourceType)
    }
}

// generateS3Credentials creates tenant-scoped S3 credentials
func (tvm *TokenVendingMachine) generateS3Credentials(ctx context.Context, tenantID string) (*Credentials, error) {
    // Get tenant's S3 credentials from Kubernetes Secret
    secretName := fmt.Sprintf("tenant-%s-s3-creds", tenantID)
    namespace := fmt.Sprintf("tenant-%s", tenantID)
    
    secret, err := tvm.k8sClient.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
    if err != nil {
        return nil, fmt.Errorf("failed to get S3 credentials: %w", err)
    }
    
    creds := &Credentials{
        AccessKey: string(secret.Data["access_key"]),
        SecretKey: string(secret.Data["secret_key"]),
        Endpoint:  string(secret.Data["endpoint"]),
        ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
    }
    
    // Store credentials
    if err := tvm.credentialStore.StoreCredentials(ctx, tenantID, "s3", creds); err != nil {
        return nil, err
    }
    
    return creds, nil
}

// generateDatabaseCredentials creates tenant-scoped database credentials
func (tvm *TokenVendingMachine) generateDatabaseCredentials(ctx context.Context, tenantID string) (*Credentials, error) {
    // Similar pattern for database credentials
    secretName := fmt.Sprintf("tenant-%s-db-creds", tenantID)
    namespace := fmt.Sprintf("tenant-%s", tenantID)
    
    secret, err := tvm.k8sClient.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
    if err != nil {
        return nil, fmt.Errorf("failed to get database credentials: %w", err)
    }
    
    creds := &Credentials{
        AccessKey: string(secret.Data["username"]),
        SecretKey: string(secret.Data["password"]),
        Endpoint:  string(secret.Data["host"]),
        ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
    }
    
    if err := tvm.credentialStore.StoreCredentials(ctx, tenantID, "database", creds); err != nil {
        return nil, err
    }
    
    return creds, nil
}
```

**Usage Example:**
```go
// In a tenant microservice
tvm := tokenvendingmachine.NewTokenVendingMachine(k8sClient, tokenManager, credStore)

// Get tenant-scoped S3 credentials
creds, err := tvm.GetTenantCredentials(ctx, jwtToken, "s3")
if err != nil {
    return err
}

// Use credentials to access tenant's S3 bucket
s3Client := s3.New(s3.Config{
    AccessKey: creds.AccessKey,
    SecretKey: creds.SecretKey,
    Endpoint:  creds.Endpoint,
})
```

### 6. Cost Attribution Manager

**Purpose:** Track resource usage per tenant for billing and cost optimization (inspired by SBT-AWS KubeCost integration).

**SBT-AWS Pattern:**
- Uses KubeCost for cost monitoring with Prometheus backend
- Kubernetes labels for cost attribution: `saas/tenant: "true"`
- Optional Grafana dashboards (disabled by default)
- Manual cost analysis via KubeCost UI

**open-sbt Implementation:**

```go
package tenantcost

import (
    "context"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

// CostAttributionManager tracks resource usage per tenant
type CostAttributionManager struct {
    resourceUsage *prometheus.GaugeVec
    costMetrics   *prometheus.GaugeVec
    requestCost   *prometheus.CounterVec
}

func NewCostAttributionManager(namespace string) *CostAttributionManager {
    return &CostAttributionManager{
        resourceUsage: promauto.NewGaugeVec(
            prometheus.GaugeOpts{
                Namespace: namespace,
                Name:      "tenant_resource_usage",
                Help:      "Resource usage per tenant",
            },
            []string{"tenant_id", "tenant_tier", "resource_type", "unit"},
        ),
        costMetrics: promauto.NewGaugeVec(
            prometheus.GaugeOpts{
                Namespace: namespace,
                Name:      "tenant_cost_attribution",
                Help:      "Cost attribution per tenant",
            },
            []string{"tenant_id", "tenant_tier", "cost_type", "currency"},
        ),
        requestCost: promauto.NewCounterVec(
            prometheus.CounterOpts{
                Namespace: namespace,
                Name:      "tenant_request_cost_total",
                Help:      "Total cost of requests per tenant",
            },
            []string{"tenant_id", "tenant_tier", "service", "operation"},
        ),
    }
}

// RecordResourceUsage records resource consumption
func (c *CostAttributionManager) RecordResourceUsage(ctx context.Context, resourceType string, usage float64, unit string) {
    tenantID := getTenantIDFromContext(ctx)
    tenantTier := getTenantTierFromContext(ctx)
    
    c.resourceUsage.WithLabelValues(tenantID, tenantTier, resourceType, unit).Set(usage)
}

// RecordCost records cost attribution
func (c *CostAttributionManager) RecordCost(ctx context.Context, costType string, amount float64, currency string) {
    tenantID := getTenantIDFromContext(ctx)
    tenantTier := getTenantTierFromContext(ctx)
    
    c.costMetrics.WithLabelValues(tenantID, tenantTier, costType, currency).Set(amount)
}

// RecordRequestCost records per-request cost
func (c *CostAttributionManager) RecordRequestCost(ctx context.Context, service, operation string, cost float64) {
    tenantID := getTenantIDFromContext(ctx)
    tenantTier := getTenantTierFromContext(ctx)
    
    c.requestCost.WithLabelValues(tenantID, tenantTier, service, operation).Add(cost)
}

// Gin middleware for automatic cost tracking
func (c *CostAttributionManager) GinMiddleware() gin.HandlerFunc {
    return func(ctx *gin.Context) {
        start := time.Now()
        
        ctx.Next()
        
        // Calculate request cost based on duration and tier
        duration := time.Since(start)
        cost := c.calculateRequestCost(ctx, duration)
        
        c.RecordRequestCost(
            ctx.Request.Context(),
            "api",
            ctx.Request.Method+" "+ctx.FullPath(),
            cost,
        )
    }
}

func (c *CostAttributionManager) calculateRequestCost(ctx *gin.Context, duration time.Duration) float64 {
    // Simple cost model - can be enhanced based on business logic
    tenantTier := ctx.GetString("tenant_tier")
    baseCost := 0.001 // $0.001 per request
    
    switch tenantTier {
    case "enterprise":
        return baseCost * 0.5 // 50% discount for enterprise
    case "premium":
        return baseCost * 0.8 // 20% discount for premium
    default:
        return baseCost
    }
}
```

**Usage:**
```go
costManager := tenantcost.NewCostAttributionManager("my_service")

// Record resource usage
costManager.RecordResourceUsage(ctx, "cpu", 0.5, "cores")
costManager.RecordResourceUsage(ctx, "memory", 1024, "MB")
costManager.RecordResourceUsage(ctx, "storage", 10, "GB")

// Record costs
costManager.RecordCost(ctx, "compute", 0.05, "USD")
costManager.RecordCost(ctx, "storage", 0.02, "USD")
```

### 7. Infrastructure Monitoring Integration

**Purpose:** Integrate with cloud-native monitoring tools (addresses SBT-AWS's external integration requirement).

**SBT-AWS Limitation:** Relies entirely on external monitoring tools with no built-in integration

**open-sbt Enhancement:**

```go
package tenantmonitoring

import (
    "context"
    "fmt"
)

// MonitoringIntegration provides built-in monitoring integrations
type MonitoringIntegration struct {
    victoriaMetrics *VictoriaMetricsClient
    openSearch      *OpenSearchClient
    grafanaAlloy    *GrafanaAlloyClient
    k8sGPT          *K8sGPTClient
}

func NewMonitoringIntegration(config MonitoringConfig) *MonitoringIntegration {
    return &MonitoringIntegration{
        victoriaMetrics: NewVictoriaMetricsClient(config.VictoriaMetrics),
        openSearch:      NewOpenSearchClient(config.OpenSearch),
        grafanaAlloy:    NewGrafanaAlloyClient(config.GrafanaAlloy),
        k8sGPT:          NewK8sGPTClient(config.K8sGPT),
    }
}

// SendMetrics sends tenant metrics to VictoriaMetrics
func (m *MonitoringIntegration) SendMetrics(ctx context.Context, metrics []TenantMetric) error {
    tenantID := getTenantIDFromContext(ctx)
    
    // Add tenant labels to all metrics
    for i := range metrics {
        if metrics[i].Labels == nil {
            metrics[i].Labels = make(map[string]string)
        }
        metrics[i].Labels["tenant_id"] = tenantID
        metrics[i].Labels["tenant_tier"] = getTenantTierFromContext(ctx)
    }
    
    return m.victoriaMetrics.Send(ctx, metrics)
}

// SendLogs sends tenant logs to OpenSearch with isolation
func (m *MonitoringIntegration) SendLogs(ctx context.Context, logs []TenantLog) error {
    tenantID := getTenantIDFromContext(ctx)
    
    // Add tenant context to all logs
    for i := range logs {
        logs[i].TenantID = tenantID
        logs[i].TenantTier = getTenantTierFromContext(ctx)
        logs[i].Index = fmt.Sprintf("tenant-%s-logs", tenantID) // Tenant-isolated index
    }
    
    return m.openSearch.Index(ctx, logs)
}

// RunDiagnostics runs K8sGPT diagnostics for tenant namespace
func (m *MonitoringIntegration) RunDiagnostics(ctx context.Context, namespace string) (*DiagnosticReport, error) {
    tenantID := getTenantIDFromContext(ctx)
    
    return m.k8sGPT.Analyze(ctx, K8sGPTRequest{
        Namespace: namespace,
        TenantID:  tenantID,
        Filters:   []string{"tenant-specific", "multi-tenant"},
    })
}
```

### 8. Distributed Tracing Manager

**Purpose:** Provide distributed tracing capabilities (missing from SBT-AWS).

**SBT-AWS Gap:** No distributed tracing support

**open-sbt Implementation:**

```go
package tenanttracing

import (
    "context"
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/trace"
)

// TenantTracer wraps OpenTelemetry with automatic tenant context
type TenantTracer struct {
    tracer trace.Tracer
}

func NewTenantTracer(serviceName string) *TenantTracer {
    return &TenantTracer{
        tracer: otel.Tracer(serviceName),
    }
}

// StartSpan starts a span with tenant context
func (t *TenantTracer) StartSpan(ctx context.Context, operationName string) (context.Context, trace.Span) {
    tenantID := getTenantIDFromContext(ctx)
    tenantTier := getTenantTierFromContext(ctx)
    userID := getUserIDFromContext(ctx)
    
    ctx, span := t.tracer.Start(ctx, operationName)
    
    // Add tenant attributes to span
    span.SetAttributes(
        attribute.String("tenant.id", tenantID),
        attribute.String("tenant.tier", tenantTier),
        attribute.String("user.id", userID),
    )
    
    return ctx, span
}

// Gin middleware for automatic tracing
func (t *TenantTracer) GinMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx, span := t.StartSpan(
            c.Request.Context(),
            fmt.Sprintf("%s %s", c.Request.Method, c.FullPath()),
        )
        defer span.End()
        
        c.Request = c.Request.WithContext(ctx)
        c.Next()
        
        // Add response attributes
        span.SetAttributes(
            attribute.Int("http.status_code", c.Writer.Status()),
            attribute.String("http.method", c.Request.Method),
            attribute.String("http.url", c.Request.URL.String()),
        )
    }
}
```

**Purpose:** Automatically set PostgreSQL RLS context for tenant isolation.

```go
package tenantdb

import (
    "context"
    "database/sql"
    "fmt"
)

// TenantDB wraps sql.DB with automatic RLS context setting
type TenantDB struct {
    db *sql.DB
}

func NewTenantDB(db *sql.DB) *TenantDB {
    return &TenantDB{db: db}
}

// BeginTx starts a transaction with tenant context
func (tdb *TenantDB) BeginTx(ctx context.Context) (*sql.Tx, error) {
    tenantID := getTenantIDFromContext(ctx)
    if tenantID == "" {
        return nil, fmt.Errorf("tenant_id not found in context")
    }
    
    tx, err := tdb.db.BeginTx(ctx, nil)
    if err != nil {
        return nil, err
    }
    
    // Set tenant context for RLS
    _, err = tx.ExecContext(ctx, "SET app.tenant_id = $1", tenantID)
    if err != nil {
        tx.Rollback()
        return nil, fmt.Errorf("failed to set tenant context: %w", err)
    }
    
    return tx, nil
}

// QueryContext executes a query with tenant context
func (tdb *TenantDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
    tx, err := tdb.BeginTx(ctx)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback() // Will be no-op if committed
    
    rows, err := tx.QueryContext(ctx, query, args...)
    if err != nil {
        return nil, err
    }
    
    // Commit to release the transaction
    if err := tx.Commit(); err != nil {
        return nil, err
    }
    
    return rows, nil
}

// ExecContext executes a statement with tenant context
func (tdb *TenantDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
    tx, err := tdb.BeginTx(ctx)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback()
    
    result, err := tx.ExecContext(ctx, query, args...)
    if err != nil {
        return nil, err
    }
    
    if err := tx.Commit(); err != nil {
        return nil, err
    }
    
    return result, nil
}
```

### 5. Database Isolation Helper

**Purpose:** Automatically set PostgreSQL RLS context for tenant isolation.

```go
package tenantdb

import (
    "context"
    "database/sql"
    "fmt"
)

// TenantDB wraps sql.DB with automatic RLS context setting
type TenantDB struct {
    db *sql.DB
}

func NewTenantDB(db *sql.DB) *TenantDB {
    return &TenantDB{db: db}
}

// BeginTx starts a transaction with tenant context
func (tdb *TenantDB) BeginTx(ctx context.Context) (*sql.Tx, error) {
    tenantID := getTenantIDFromContext(ctx)
    if tenantID == "" {
        return nil, fmt.Errorf("tenant_id not found in context")
    }
    
    tx, err := tdb.db.BeginTx(ctx, nil)
    if err != nil {
        return nil, err
    }
    
    // Set tenant context for RLS
    _, err = tx.ExecContext(ctx, "SET app.tenant_id = $1", tenantID)
    if err != nil {
        tx.Rollback()
        return nil, fmt.Errorf("failed to set tenant context: %w", err)
    }
    
    return tx, nil
}

// QueryContext executes a query with tenant context
func (tdb *TenantDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
    tx, err := tdb.BeginTx(ctx)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback() // Will be no-op if committed
    
    rows, err := tx.QueryContext(ctx, query, args...)
    if err != nil {
        return nil, err
    }
    
    // Commit to release the transaction
    if err := tx.Commit(); err != nil {
        return nil, err
    }
    
    return rows, nil
}

// ExecContext executes a statement with tenant context
func (tdb *TenantDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
    tx, err := tdb.BeginTx(ctx)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback()
    
    result, err := tx.ExecContext(ctx, query, args...)
    if err != nil {
        return nil, err
    }
    
    if err := tx.Commit(); err != nil {
        return nil, err
    }
    
    return result, nil
}
```

**Usage with sqlc:**
```go
// Generated by sqlc
type Queries struct {
    db DBTX
}

// Wrap with TenantDB
tenantDB := tenantdb.NewTenantDB(db)
queries := New(tenantDB)

// All queries automatically include tenant context
users, err := queries.ListUsers(ctx) // Only returns users for the tenant in context
```

## Complete Microservice Example

**Putting it all together:**

```go
package main

import (
    "github.com/gin-gonic/gin"
    "github.com/zero-ops/open-sbt/pkg/libraries/tenantcontext"
    "github.com/zero-ops/open-sbt/pkg/libraries/tenantlogging"
    "github.com/zero-ops/open-sbt/pkg/libraries/tenantmetrics"
    "github.com/zero-ops/open-sbt/pkg/libraries/tenantdb"
    "github.com/zero-ops/open-sbt/pkg/libraries/tenantcost"
    "github.com/zero-ops/open-sbt/pkg/libraries/tenanttracing"
)

func main() {
    // Initialize libraries
    tokenManager, _ := tenantcontext.NewIdentityTokenManager(
        "https://hydra.example.com/.well-known/jwks.json",
        "https://hydra.example.com/",
        "my-service",
    )
    
    logger := tenantlogging.NewTenantLogger()
    metrics := tenantmetrics.NewTenantMetrics("my_service")
    costManager := tenantcost.NewCostAttributionManager("my_service")
    tracer := tenanttracing.NewTenantTracer("my-service")
    tenantDB := tenantdb.NewTenantDB(db)
    
    // Setup Gin
    r := gin.Default()
    
    // Apply tenant middleware (order matters!)
    r.Use(tokenManager.GinMiddleware())  // 1. Extract tenant context
    r.Use(tracer.GinMiddleware())        // 2. Start distributed tracing
    r.Use(metrics.GinMiddleware())       // 3. Record metrics
    r.Use(costManager.GinMiddleware())   // 4. Track costs
    
    // Define routes
    r.GET("/users", func(c *gin.Context) {
        ctx := c.Request.Context()
        
        // Start a traced operation
        ctx, span := tracer.StartSpan(ctx, "list_users")
        defer span.End()
        
        // Logging automatically includes tenant context
        logger.Info(ctx, "Listing users", nil)
        
        // Database queries automatically filtered by tenant
        rows, err := tenantDB.QueryContext(ctx, "SELECT * FROM users")
        if err != nil {
            logger.Error(ctx, "Failed to list users", err, nil)
            c.JSON(500, gin.H{"error": "Internal server error"})
            return
        }
        defer rows.Close()
        
        // Record resource usage for cost attribution
        costManager.RecordResourceUsage(ctx, "database_queries", 1, "count")
        
        // Process rows...
        c.JSON(200, gin.H{"users": users})
    })
    
    r.Run(":8080")
}
```

**What the developer gets automatically:**
1. ✅ JWT validation and tenant context extraction
2. ✅ Tenant-aware logging (all logs tagged with tenant_id)
3. ✅ Tenant-aware metrics (all metrics tagged with tenant_id)
4. ✅ Database isolation via RLS (queries automatically filtered)
5. ✅ Tenant-scoped credentials (via Token Vending Machine)
6. ✅ Cost attribution and resource tracking
7. ✅ Distributed tracing with tenant context
8. ✅ Infrastructure monitoring integration

**What the developer writes:**
- Just business logic!

## SBT-AWS vs open-sbt Comparison

| Aspect | SBT-AWS | open-sbt |
|--------|---------|----------|
| **Logging** | Basic Log4j2 with manual tenant context | Automatic tenant context injection |
| **Metrics** | None (external tools required) | Built-in Prometheus metrics with tenant tags |
| **Tracing** | None | OpenTelemetry with tenant context |
| **Cost Attribution** | Optional KubeCost (manual setup) | Built-in cost tracking and attribution |
| **Monitoring Integration** | External tools only | VictoriaMetrics, OpenSearch, K8sGPT |
| **Alerting** | None | Built-in SLA/SLO monitoring |
| **Developer Experience** | Manual observability setup | Automatic multi-tenant observability |
| **Production Readiness** | Basic (requires external stack) | Comprehensive (batteries included) |

## Key Improvements over SBT-AWS

1. **Comprehensive Observability**: Unlike SBT-AWS's minimal approach, open-sbt provides complete observability out-of-the-box
2. **Automatic Tenant Context**: No manual tenant ID injection required in logs/metrics
3. **Built-in Cost Attribution**: Eliminates need for separate KubeCost setup
4. **Distributed Tracing**: Addresses SBT-AWS's complete lack of tracing
5. **Infrastructure Integration**: Native integration with cloud-native monitoring tools
6. **Developer Productivity**: Developers focus on business logic, not observability plumbing
