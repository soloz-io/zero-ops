package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/soloz-io/zero-ops/internal/opensbt/api/handlers"
	"github.com/soloz-io/zero-ops/internal/opensbt/api/middleware"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/providers/openmeter"
	"github.com/soloz-io/zero-ops/internal/opensbt/providers/ory"
	"github.com/soloz-io/zero-ops/internal/opensbt/providers/zitadel"
)

func main() {
	// Initialize providers with graceful retry logic
	openMeterURL := getEnv("OPENMETER_URL", "http://openmeter-api.platform-billing.svc.cluster.local")

	meteringProviderRaw, err := retryWithBackoff(func() (interface{}, error) {
		return openmeter.NewMeteringProvider(openMeterURL)
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create metering provider after retries: %v", err))
	}
	meteringProvider := meteringProviderRaw.(interfaces.IMetering)

	billingProviderRaw, err := retryWithBackoff(func() (interface{}, error) {
		return openmeter.NewBillingProvider(openMeterURL)
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create billing provider after retries: %v", err))
	}
	billingProvider := billingProviderRaw.(interfaces.IBilling)

	authProviderRaw, err := retryWithBackoff(func() (interface{}, error) {
		return newAuthProvider()
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create auth provider after retries: %v", err))
	}
	authProvider := authProviderRaw.(interfaces.IAuth)

	// Initialize Gin router
	router := gin.New()

	// Global middleware
	router.Use(gin.Recovery())
	router.Use(middleware.RequestLogger())
	router.Use(middleware.CORS())
	router.Use(middleware.RateLimiter(100, 200)) // 100 req/sec burst, 200 max

	// Health check endpoint (no auth required)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	// API v1 routes with JWT validation
	v1 := router.Group("/api/v1")
	v1.Use(middleware.JWTValidator(authProvider))
	v1.Use(middleware.TenantContextInjector())
	v1.Use(middleware.RFC7807ErrorHandler())
	{
		// User management endpoints
		users := handlers.NewUserHandler(authProvider, meteringProvider)
		v1.POST("/tenants/:tenantID/users", users.CreateUser)
		v1.GET("/tenants/:tenantID/users/:userID", users.GetUser)
		v1.PUT("/tenants/:tenantID/users/:userID", users.UpdateUser)
		v1.DELETE("/tenants/:tenantID/users/:userID", users.DeleteUser)
		v1.GET("/tenants/:tenantID/users", users.ListUsers)

		// Usage query endpoints
		usage := handlers.NewUsageHandler(meteringProvider)
		v1.GET("/tenants/:tenantID/usage", usage.GetTenantUsage)
		v1.GET("/tenants/:tenantID/users/:userID/usage", usage.GetUserUsage)
		v1.GET("/tenants/:tenantID/entitlements", usage.CheckEntitlements)

		// Read-only catalog endpoints
		catalog := handlers.NewCatalogHandler(meteringProvider)
		v1.GET("/meters", catalog.ListMeters)
		v1.GET("/features", catalog.ListFeatures)
		v1.GET("/plans", catalog.ListPlans)

		// Subscription endpoints
		subscriptions := handlers.NewSubscriptionHandler(billingProvider)
		v1.POST("/tenants/:tenantID/subscriptions", subscriptions.CreateSubscription)
		v1.GET("/tenants/:tenantID/subscriptions/:subscriptionID", subscriptions.GetSubscription)
		v1.PUT("/tenants/:tenantID/subscriptions/:subscriptionID", subscriptions.UpdateSubscription)
		v1.DELETE("/tenants/:tenantID/subscriptions/:subscriptionID", subscriptions.CancelSubscription)
		v1.GET("/tenants/:tenantID/subscriptions", subscriptions.ListSubscriptions)

		// Invoice endpoints
		invoices := handlers.NewInvoiceHandler(billingProvider)
		v1.GET("/tenants/:tenantID/invoices/preview", invoices.PreviewInvoice)
		v1.GET("/tenants/:tenantID/invoices/:invoiceID", invoices.GetInvoice)
		v1.GET("/tenants/:tenantID/invoices", invoices.ListInvoices)
	}

	// Admin endpoints (platform_admin RBAC required)
	admin := router.Group("/api/v1/admin")
	admin.Use(middleware.JWTValidator(authProvider))
	admin.Use(middleware.RBACValidator("platform_admin"))
	admin.Use(middleware.RFC7807ErrorHandler())
	{
		dlq := handlers.NewDLQHandler()
		admin.POST("/dlq/replay", middleware.RateLimiter(50, 100), dlq.ReplayEvent)

	}

	// Tenant identity provisioning (ADR-041: this service owns the identity
	// lifecycle; the Hub Operator orchestrates and calls it).
	//
	// On an INTERNAL path, guarded by NetworkPolicy rather than by a bearer
	// token. The caller is a controller, not a person, and the admin group above
	// authenticates a USER — putting a reconcile loop behind it would mean
	// minting and rotating a user credential for a machine, which is a worse
	// thing to own than an ingress rule. This mirrors the auth-proxy's own
	// /internal/ surface.
	//
	// The guarantee is therefore the NetworkPolicy beside this deployment: it
	// admits the operator and nothing else. Widening that policy widens this.
	//
	// Registered only when the configured provider can provision tenants — the
	// capability is optional, and a route that always answered "not implemented"
	// would read as an outage rather than a configuration.
	if provisioner, ok := authProvider.(interfaces.ITenantIdentityProvisioner); ok {
		internal := router.Group("/internal")
		internal.Use(middleware.RFC7807ErrorHandler())
		ti := handlers.NewTenantIdentityHandler(provisioner, nil)
		internal.POST("/tenants/:tenantId/identity", ti.EnsureIdentity)
	}

	// Start HTTP server
	port := getEnv("PORT", "8080")
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		fmt.Printf("kube-sbt API server listening on :%s\n", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(fmt.Sprintf("failed to start server: %v", err))
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		panic(fmt.Sprintf("server forced to shutdown: %v", err))
	}

	fmt.Println("Server exited")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// retryWithBackoff executes a function with exponential backoff retry logic
// This implements the "stable-but-not-ready" pattern for graceful dependency handling
func retryWithBackoff(fn func() (interface{}, error)) (interface{}, error) {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second
	maxRetries := 30 // 5 minutes total with 10s intervals

	for i := 0; i < maxRetries; i++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}

		// Log the retry attempt
		fmt.Printf("Dependency connection failed (attempt %d/%d): %v, retrying in %v...\n", i+1, maxRetries, err, backoff)

		// Sleep with exponential backoff
		time.Sleep(backoff)

		// Increase backoff (exponential, capped at maxBackoff)
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}

	return nil, fmt.Errorf("max retries (%d) exceeded", maxRetries)
}

// newAuthProvider selects the identity provider from configuration.
//
// The interface was always pluggable; the WIRING was not — this call site named
// one product, so a second provider could not be selected without changing code
// and rebuilding. An abstraction that only a recompile can re-point is not one
// (ADR-059), so the choice is a setting.
//
// Defaulting is deliberate rather than lazy: an unset variable keeps an existing
// deployment on the provider it already runs, so this change cannot silently
// re-point a live environment at a different issuer.
func newAuthProvider() (interfaces.IAuth, error) {
	switch provider := getEnv("AUTH_PROVIDER", "ory"); provider {
	case "ory":
		return ory.NewAuthProvider(getEnv("ORY_KRATOS_URL",
			"http://kratos-public.platform-identity.svc.cluster.local"))

	case "zitadel":
		// The issuer is the public URL, not an in-cluster Service address. It is
		// what relying parties are configured with and what the token's iss claim
		// must equal, and this provider verifies the discovery document agrees
		// before trusting any key from it.
		return zitadel.NewAuth(zitadel.Config{
			Issuer:        getEnv("OIDC_ISSUER_URL", ""),
			ServiceToken:  os.Getenv("IDENTITY_SERVICE_TOKEN"),
			PlatformOrgID: getEnv("PLATFORM_ORG_ID", ""),
			ProjectName:   getEnv("IDENTITY_PROJECT_NAME", "platform"),
		})

	default:
		// Naming the value rather than falling back. A typo that silently
		// selected a default would start the service against the wrong issuer,
		// and every token would then fail validation for reasons that point
		// anywhere but here.
		return nil, fmt.Errorf("unknown AUTH_PROVIDER %q: expected \"ory\" or \"zitadel\"", provider)
	}
}
