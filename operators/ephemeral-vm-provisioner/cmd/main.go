package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/soloz-io/ephemeral-vm-provisioner/internal/hetzner"
	"github.com/soloz-io/ephemeral-vm-provisioner/internal/provision"
	"github.com/soloz-io/ephemeral-vm-provisioner/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// No fallback DSN. This carried a hardcoded tenant database — tenant
	// credentials, in the default namespace — which ADR-047 forbids in platform
	// code and which fails in the worst way: a misconfigured deployment silently
	// connects somewhere plausible instead of reporting that it was not
	// configured. Absent DATABASE_URL is a configuration error, so say so.
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	// Same contract as DATABASE_URL. Without an image a VM boots, pulls nothing
	// and the job never returns a result — a render that hangs rather than one
	// that fails. Refuse the job at startup, where the reason is visible.
	if os.Getenv("EPHEMERAL_WORKER_IMAGE") == "" {
		log.Error("EPHEMERAL_WORKER_IMAGE is required",
			"hint", "set it in the ephemeral-vm-provisioner-config ConfigMap; pin a digest, not a tag")
		os.Exit(1)
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
