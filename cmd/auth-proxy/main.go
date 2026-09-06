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

	// No client registration on start.
	//
	// This used to register an OAuth client with Hydra's admin API before serving
	// anything, and exit if that failed. Zitadel has no dynamic client
	// registration (RFC 7591): clients are provisioned by the Tenant Identity
	// Service (ADR-041), so there is nothing for this process to register and
	// nothing whose absence should stop it starting.
	handler := authproxy.NewHandler(
		cfg.ZitadelIssuerURL,
		cfg.ZitadelInternalURL,
		cfg.JWKSFetchTimeout,
		cfg.ExpectedJWTAudience,
		cfg.AuthPublicBaseURL,
		cfg.MCPGatewayBaseURL,
	)

	// Verify JWKS connectivity before reporting ready.
	log.Println("Performing initial JWKS fetch...")
	validator := authproxy.NewJWTValidator(
		handler.JWKSURL(),
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

	// Only the machine surfaces the Gateway routes here are served.
	//
	// /login, /consent, /userinfo and /oauth2/* are gone: they implemented
	// Hydra's login/consent challenge flow, and auth.<zone> now redirects
	// everything outside /.well-known/ and /internal/ to Zitadel, which hosts its
	// own login. Keeping them would have left endpoints that no request reaches
	// and that no longer have a backend.
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", handler.ProxyOpenIDConfiguration)
	mux.HandleFunc("/.well-known/oauth-authorization-server", handler.ServeAuthServerMetadata)
	mux.HandleFunc("/.well-known/oauth-authorization-server/mcp", handler.ServeAuthServerMetadata)
	mux.HandleFunc("/.well-known/jwks.json", handler.ProxyJWKS)
	mux.HandleFunc("/health/ready", handler.HealthReady)
	mux.HandleFunc("/internal/validate", handler.ValidateHandler)

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
