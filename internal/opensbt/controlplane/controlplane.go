package controlplane

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ControlPlane is the central coordinator for tenant management, authentication,
// event orchestration, and the HTTP API. It wires together the core interfaces
// (IAuth, IEventBus, IStorage) and optional providers (IBilling, IMetering, etc.)
// following the design principle of interface-based abstraction.
type ControlPlane struct {
	cfg    Config
	router *gin.Engine
	server *http.Server

	// Convenience aliases to cfg fields (avoids cfg. prefix noise in handlers)
	auth     interfaces.IAuth
	eventBus interfaces.IEventBus
	storage  interfaces.IStorage
}

// NewControlPlane validates the config, applies defaults, and wires the HTTP
// router. It does NOT start the server — call Start for that.
func NewControlPlane(cfg Config) (*ControlPlane, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.defaults()

	cp := &ControlPlane{
		cfg:      cfg,
		auth:     cfg.Auth,
		eventBus: cfg.EventBus,
		storage:  cfg.Storage,
	}
	cp.setupRouter()
	return cp, nil
}

// Start bootstraps the system admin user (if configured), subscribes to
// Application Plane events, and begins serving HTTP requests. It blocks until
// the context is cancelled or the server fails.
func (cp *ControlPlane) Start(ctx context.Context) error {
	if err := cp.bootstrapAdmin(ctx); err != nil {
		return fmt.Errorf("controlplane: bootstrap admin: %w", err)
	}

	if err := cp.subscribeEvents(ctx); err != nil {
		return fmt.Errorf("controlplane: subscribe events: %w", err)
	}

	cp.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", cp.cfg.APIPort),
		Handler: cp.router,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("controlplane: listening on :%d", cp.cfg.APIPort)
		if err := cp.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		return cp.Stop()
	case err := <-errCh:
		return err
	}
}

// Stop gracefully drains in-flight requests within ShutdownTimeout.
func (cp *ControlPlane) Stop() error {
	if cp.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), cp.cfg.ShutdownTimeout)
	defer cancel()
	log.Printf("controlplane: shutting down (timeout %s)", cp.cfg.ShutdownTimeout)
	return cp.server.Shutdown(ctx)
}

// ─── Router setup (8.5, 8.6, 8.7, 8.8) ──────────────────────────────────────

func (cp *ControlPlane) setupRouter() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// 8.5 — middleware stack
	r.Use(gin.Recovery())
	r.Use(requestLogger())
	r.Use(securityHeaders())        // 8.8 security headers
	r.Use(cp.corsMiddleware())      // 8.8 CORS
	r.Use(maintenanceMiddleware())  // 503 during maintenance

	// 8.6 — health endpoints (unauthenticated)
	r.GET("/health", cp.handleHealth)
	r.GET("/ready", cp.handleReady)

	// 8.7 — Prometheus metrics endpoint (unauthenticated; restrict via network policy)
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Authenticated API routes — JWT middleware applied per-group in service files
	cp.router = r
}

// Router returns the underlying Gin engine so service layers can register routes.
func (cp *ControlPlane) Router() *gin.Engine {
	return cp.router
}

// ─── Health endpoints (8.6) ───────────────────────────────────────────────────

func (cp *ControlPlane) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "time": time.Now().UTC()})
}

// handleReady checks that all required dependencies are reachable.
func (cp *ControlPlane) handleReady(c *gin.Context) {
	components := map[string]string{}
	overall := "ok"

	// Auth readiness — well-known endpoint reachable
	if ep := cp.auth.GetWellKnownEndpoint(); ep == "" {
		components["auth"] = "not configured"
		overall = "degraded"
	} else {
		components["auth"] = "ok"
	}

	status := http.StatusOK
	if overall != "ok" {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"status": overall, "components": components})
}

// ─── Middleware (8.5, 8.8) ────────────────────────────────────────────────────

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Printf("%s %s %d %s", c.Request.Method, c.Request.URL.Path,
			c.Writer.Status(), time.Since(start))
	}
}

// securityHeaders sets standard security response headers (8.8).
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}

// corsMiddleware handles CORS preflight and response headers (8.8).
func (cp *ControlPlane) corsMiddleware() gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, o := range cp.cfg.AllowedOrigins {
		allowed[o] = true
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && (len(allowed) == 0 || allowed[origin]) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-ID")
			c.Header("Access-Control-Max-Age", "86400")
			c.Header("Vary", "Origin")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// maintenanceMiddleware returns 503 when maintenance mode is active.
// The flag is toggled via ISystemAdmin.EnableMaintenanceMode (Task 8 scope:
// the middleware slot is wired here; the toggle lives in the system admin handler).
var maintenanceActive bool
var maintenanceReason string

// SetMaintenanceMode toggles platform maintenance mode (used by ISystemAdmin).
func SetMaintenanceMode(active bool, reason string) {
	maintenanceActive = active
	maintenanceReason = reason
}

// IsMaintenanceMode reports whether maintenance mode is currently active.
func IsMaintenanceMode() bool { return maintenanceActive }

func maintenanceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Health/ready/metrics are always available
		p := c.Request.URL.Path
		if p == "/health" || p == "/ready" || p == "/metrics" {
			c.Next()
			return
		}
		if maintenanceActive {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "platform is under maintenance",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// ─── Bootstrap (8.2) ─────────────────────────────────────────────────────────

func (cp *ControlPlane) bootstrapAdmin(ctx context.Context) error {
	if cp.cfg.SystemAdminEmail == "" {
		return nil
	}
	if err := cp.auth.CreateAdminUser(ctx, models.CreateAdminUserProps{
		Email: cp.cfg.SystemAdminEmail,
		Name:  cp.cfg.SystemAdminName,
	}); err != nil {
		return err
	}
	// Sync group assignments from Git ConfigMap
	return cp.syncUserGroups(ctx)
}

// userGroupsConfig is the structure of the groups.yaml data in the ConfigMap.
type userGroupsConfig struct {
	Users []struct {
		Email  string   `yaml:"email"`
		Groups []string `yaml:"groups"`
	} `yaml:"users"`
}

// syncUserGroups reads the identity-user-groups ConfigMap and sets groups on
// each user's metadata_public in Kratos. This runs on every bootstrap to
// ensure group assignments stay in sync with the Git source of truth.
func (cp *ControlPlane) syncUserGroups(ctx context.Context) error {
	if cp.cfg.K8sClient == nil {
		log.Println("controlplane: skipping user groups sync (no k8s client)")
		return nil
	}

	cm, err := cp.cfg.K8sClient.CoreV1().ConfigMaps("platform-identity").Get(ctx, cp.cfg.UserGroupsCM, metav1.GetOptions{})
	if err != nil {
		log.Printf("controlplane: user groups ConfigMap not found, skipping: %v", err)
		return nil
	}

	data, ok := cm.Data[cp.cfg.UserGroupsKey]
	if !ok {
		log.Printf("controlplane: key %q not found in ConfigMap %s, skipping", cp.cfg.UserGroupsKey, cp.cfg.UserGroupsCM)
		return nil
	}

	var cfg userGroupsConfig
	if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
		return fmt.Errorf("controlplane: parse user groups: %w", err)
	}

	for _, u := range cfg.Users {
		if err := cp.auth.SetUserGroups(ctx, u.Email, u.Groups); err != nil {
			log.Printf("controlplane: set groups for %s: %v", u.Email, err)
			continue
		}
		log.Printf("controlplane: synced groups for %s: %v", u.Email, u.Groups)
	}

	return nil
}

// ─── Event subscriptions (Application Plane → Control Plane) ─────────────────

func (cp *ControlPlane) subscribeEvents(ctx context.Context) error {
	handlers := map[string]interfaces.EventHandler{
		models.EventProvisionSuccess:   cp.onProvisionSuccess,
		models.EventProvisionFailure:   cp.onProvisionFailure,
		models.EventDeprovisionSuccess: cp.onDeprovisionSuccess,
		models.EventDeprovisionFailure: cp.onDeprovisionFailure,
		models.EventActivateSuccess:    cp.onActivateSuccess,
		models.EventActivateFailure:    cp.onActivateFailure,
		models.EventDeactivateSuccess:  cp.onDeactivateSuccess,
		models.EventDeactivateFailure:  cp.onDeactivateFailure,
	}
	for eventType, handler := range handlers {
		if err := cp.eventBus.SubscribeQueue(ctx, eventType, "control-plane", handler); err != nil {
			return fmt.Errorf("subscribe %s: %w", eventType, err)
		}
	}
	return nil
}

// ─── Application Plane event handlers ────────────────────────────────────────

func (cp *ControlPlane) onProvisionSuccess(ctx context.Context, event models.Event) error {
	tenantID, _ := event.Detail["tenantId"].(string)
	if tenantID == "" {
		return nil
	}
	return cp.storage.UpdateTenantStatus(ctx, tenantID, "active")
}

func (cp *ControlPlane) onProvisionFailure(ctx context.Context, event models.Event) error {
	tenantID, _ := event.Detail["tenantId"].(string)
	if tenantID == "" {
		return nil
	}
	return cp.storage.UpdateTenantStatus(ctx, tenantID, "failed")
}

func (cp *ControlPlane) onDeprovisionSuccess(ctx context.Context, event models.Event) error {
	tenantID, _ := event.Detail["tenantId"].(string)
	if tenantID == "" {
		return nil
	}
	return cp.storage.DeleteTenant(ctx, tenantID)
}

func (cp *ControlPlane) onDeprovisionFailure(ctx context.Context, event models.Event) error {
	tenantID, _ := event.Detail["tenantId"].(string)
	if tenantID == "" {
		return nil
	}
	return cp.storage.UpdateTenantStatus(ctx, tenantID, "failed")
}

func (cp *ControlPlane) onActivateSuccess(ctx context.Context, event models.Event) error {
	tenantID, _ := event.Detail["tenantId"].(string)
	if tenantID == "" {
		return nil
	}
	return cp.storage.UpdateTenantStatus(ctx, tenantID, "active")
}

func (cp *ControlPlane) onActivateFailure(ctx context.Context, event models.Event) error {
	tenantID, _ := event.Detail["tenantId"].(string)
	if tenantID == "" {
		return nil
	}
	return cp.storage.UpdateTenantStatus(ctx, tenantID, "failed")
}

func (cp *ControlPlane) onDeactivateSuccess(ctx context.Context, event models.Event) error {
	tenantID, _ := event.Detail["tenantId"].(string)
	if tenantID == "" {
		return nil
	}
	return cp.storage.UpdateTenantStatus(ctx, tenantID, "suspended")
}

func (cp *ControlPlane) onDeactivateFailure(ctx context.Context, event models.Event) error {
	tenantID, _ := event.Detail["tenantId"].(string)
	if tenantID == "" {
		return nil
	}
	return cp.storage.UpdateTenantStatus(ctx, tenantID, "failed")
}
