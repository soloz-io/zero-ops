package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/soloz-io/zero-ops/internal/zero-ops-api/api"
	"github.com/soloz-io/zero-ops/internal/zero-ops-api/config"
	"github.com/soloz-io/zero-ops/internal/zero-ops-api/db"
	"github.com/soloz-io/zero-ops/internal/opensbt/providers/nats"
	"go.uber.org/zap"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	dbPool, err := db.NewPool(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}
	defer dbPool.Close()

	eventBus, err := nats.NewEventBus(nats.Config{
		URLs: getEnv("NATS_URLS", "nats://nats:4222"),
	})
	if err != nil {
		logger.Fatal("failed to connect to NATS", zap.Error(err))
	}
	defer eventBus.Close()

	srv := api.NewServer(cfg, dbPool, logger, eventBus)

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server failed", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("server forced to shutdown", zap.Error(err))
	}

	logger.Info("server exited")
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
