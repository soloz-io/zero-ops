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
	"github.com/soloz-io/zero-ops/internal/opensbt/providers/openmeter"
	"github.com/soloz-io/zero-ops/internal/opensbt/providers/ory"
)

func main() {
	// Initialize providers
	openMeterURL := getEnv("OPENMETER_URL", "http://openmeter-api.platform-billing.svc.cluster.local")
	oryKratosURL := getEnv("ORY_KRATOS_URL", "http://kratos-public.platform-identity.svc.cluster.local")

	meteringProvider, err := openmeter.NewMeteringProvider(openMeterURL)
	if err != nil {
		panic(fmt.Sprintf("failed to create metering provider: %v", err))
	}

	billingProvider, err := openmeter.NewBillingProvider(openMeterURL)
	if err != nil {
		panic(fmt.Sprintf("failed to create billing provider: %v", err))
	}

	authProvider, err := ory.NewAuthProvider(oryKratosURL)
	if err != nil {
		panic(fmt.Sprintf("failed to create auth provider: %v", err))
	}

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
