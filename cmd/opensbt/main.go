package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/soloz-io/zero-ops/internal/opensbt/controlplane"
	"github.com/soloz-io/zero-ops/internal/opensbt/providers/nats"
	"github.com/soloz-io/zero-ops/internal/opensbt/providers/ory"
	"github.com/soloz-io/zero-ops/internal/opensbt/providers/postgres"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize providers
	auth := ory.NewAuth(ory.Config{
		KratosPublicURL: getEnv("KRATOS_PUBLIC_URL", "http://kratos-public:4433"),
		KratosAdminURL:  getEnv("KRATOS_ADMIN_URL", "http://kratos-admin:4434"),
		HydraPublicURL:  getEnv("HYDRA_PUBLIC_URL", "http://hydra-public:4444"),
		HydraAdminURL:   getEnv("HYDRA_ADMIN_URL", "http://hydra-admin:4445"),
		KetoReadURL:     getEnv("KETO_READ_URL", "http://keto-read:4466"),
		KetoWriteURL:    getEnv("KETO_WRITE_URL", "http://keto-write:4467"),
		JWTAudience:     getEnv("JWT_AUDIENCE", "opensbt"),
	})

	eventBus, err := nats.NewEventBus(nats.Config{
		URLs: getEnv("NATS_URLS", "nats://nats:4222"),
	})
	if err != nil {
		log.Fatalf("failed to create event bus: %v", err)
	}

	storage, err := postgres.NewStorage(ctx, postgres.Config{
		DSN: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/opensbt?sslmode=disable"),
	})
	if err != nil {
		log.Fatalf("failed to create storage: %v", err)
	}
	defer storage.Close()

	// Create Control Plane
	cp, err := controlplane.NewControlPlane(controlplane.Config{
		Auth:             auth,
		EventBus:         eventBus,
		Storage:          storage,
		SystemAdminEmail: getEnv("ADMIN_EMAIL", "admin@example.com"),
		APIPort:          8080,
	})
	if err != nil {
		log.Fatalf("failed to create control plane: %v", err)
	}

	// Handle shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("shutting down opensbt...")
		cancel()
	}()

	// Start Control Plane (blocks)
	if err := cp.Start(ctx); err != nil {
		log.Fatalf("control plane failed: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
