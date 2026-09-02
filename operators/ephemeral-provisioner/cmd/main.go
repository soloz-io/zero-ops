package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/soloz-io/ephemeral-provisioner/internal/hetzner"
	"github.com/soloz-io/ephemeral-provisioner/internal/provision"
	"github.com/soloz-io/ephemeral-provisioner/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgresql://waypoint:waypoint@waypoint-postgres.default.svc.cluster.local:5432/waypoint_db"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	st, err := store.New(ctx, databaseURL)
	if err != nil {
		log.Error("failed to connect to database", "error", err.Error())
		os.Exit(1)
	}
	defer st.Close()
	log.Info("database connected")

	hcloud := hetzner.NewClient()
	reconciler := provision.NewReconciler(st, hcloud, log)
	server := provision.NewServer(reconciler, st, log)

	go reconciler.Run(ctx)

	addr := ":" + envOr("PORT", "8080")
	if err := server.Run(ctx, addr); err != nil {
		log.Error("http server failed", "error", err.Error())
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
