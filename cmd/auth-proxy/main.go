package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	authproxy "github.com/soloz-io/zero-ops/internal/auth-proxy"
)

func main() {
	cfg, err := authproxy.LoadConfig()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	hydraClient := authproxy.NewHydraClient(cfg.HydraAdminURL)
	if err := hydraClient.RegisterClient(); err != nil {
		log.Fatalf("failed to register OAuth client: %v", err)
	}

	handler := authproxy.NewHandler(
		cfg.HydraPublicURL,
		cfg.HydraAdminURL,
		cfg.KratosPublicURL,
		cfg.KratosAdminURL,
		cfg.JWKSFetchTimeout,
		cfg.TrustedClientIDs,
		cfg.ExpectedJWTAudience,
		cfg.AuthPublicBaseURL,
		cfg.MCPGatewayBaseURL,
	)

	// Perform initial JWKS fetch to verify connectivity
	log.Println("Performing initial JWKS fetch...")
	validator := authproxy.NewJWTValidator(
		cfg.HydraPublicURL+"/.well-known/jwks.json",
		cfg.ExpectedJWTAudience,
		cfg.JWKSCacheTTL,
		cfg.JWKSFetchTimeout,
	)
	// Trigger initial fetch by attempting to get a non-existent key
	_, err = validator.Validate("eyJhbGciOiJSUzI1NiIsImtpZCI6InRlc3QifQ.e30.test")
	if err == nil || !strings.Contains(err.Error(), "failed to fetch JWKS") {
		// JWKS fetch succeeded (error is expected for invalid token, but fetch worked)
		log.Println("Initial JWKS fetch successful")
		handler.SetReady()
	} else {
		log.Fatalf("Initial JWKS fetch failed: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", handler.ServeAuthServerMetadata)
	mux.HandleFunc("/.well-known/oauth-authorization-server/mcp", handler.ServeAuthServerMetadata)
	mux.HandleFunc("/.well-known/jwks.json", handler.ProxyJWKS)
	mux.HandleFunc("/health/ready", handler.HealthReady)
	mux.HandleFunc("/login", handler.LoginHandler)
	mux.HandleFunc("/consent", handler.ConsentHandler)
	mux.HandleFunc("/internal/validate", handler.ValidateHandler)
	mux.HandleFunc("/oauth2/", handler.ProxyOAuth2)

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
	}

	go func() {
		log.Printf("auth-proxy listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("server forced to shutdown: %v", err)
	}

	log.Println("server exited")
}
