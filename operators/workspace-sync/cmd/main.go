// Command workspace-sync is the platform's workspace persistence agent
// (ADR-052 §14).
//
// It runs in two shapes inside a sandbox pod, from one image:
//
//	workspace-sync restore   init container — runs once, before the workload
//	workspace-sync serve     sidecar — on-demand checkpoints + periodic backstop
//
// It is platform-owned and holds the object-store credential. The fleet's
// workload container never receives that credential and never speaks to S3
// (§19.6): it asks this process for a checkpoint over localhost, which is why
// the control endpoint below needs no authentication of its own — reaching it
// already requires being inside this pod.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/soloz-io/zero-ops/operators/workspace-sync/internal/store"
)

const defaultRoot = "/workspace"

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if len(os.Args) < 2 {
		log.Fatal("usage: workspace-sync <restore|serve>")
	}
	root := os.Getenv("WORKSPACE_ROOT")
	if root == "" {
		root = defaultRoot
	}

	switch os.Args[1] {
	case "restore":
		if err := runRestore(root); err != nil {
			log.Fatalf("restore failed: %v", err)
		}
	case "serve":
		if err := runServe(root); err != nil {
			log.Fatalf("serve failed: %v", err)
		}
	default:
		log.Fatalf("unknown command %q", os.Args[1])
	}
}

// runRestore populates a FRESH volume and does nothing to a reattached one.
//
// The empty check is the whole correctness of this command. A reattached PVC
// already holds the workspace — including edits made after the last checkpoint
// — and ADR-052 §18.2 makes it authoritative while the sandbox runs. Restoring
// over it would replace newer local state with an older remote copy, and would
// additionally fail on git's read-only loose objects.
func runRestore(root string) error {
	if err := os.MkdirAll(root, 0o777); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		log.Printf("volume already populated (%d entries) — reattached, leaving it untouched", len(entries))
		return nil
	}

	cfg, err := store.FromEnv()
	if errors.Is(err, store.ErrNotConfigured) {
		// The one legitimate no-op (§14): a deployment with no object store.
		// The workload still gets an empty, writable volume.
		log.Print("object storage not configured — starting with an empty workspace")
		return nil
	}
	if err != nil {
		return err
	}
	s, err := store.New(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	id, err := s.Latest(ctx)
	if err != nil || id == "" {
		// No checkpoint yet is a first run, not a failure.
		log.Printf("no checkpoint for this workspace yet — starting empty (%v)", err)
		return nil
	}
	m, err := s.GetManifest(ctx, id)
	if err != nil {
		return fmt.Errorf("reading checkpoint %s: %w", id, err)
	}
	if err := s.Restore(ctx, root, m); err != nil {
		return fmt.Errorf("restoring checkpoint %s: %w", id, err)
	}
	log.Printf("restored checkpoint %s (%d files, taken %s)", m.ID, len(m.Entries), m.CreatedAt.Format(time.RFC3339))
	return nil
}

// runServe is the sidecar: one snapshot engine behind two triggers.
//
// On-demand is the primary path — the agent knows when a unit of work is worth
// recording and calls POST /checkpoint at that boundary. The periodic backstop
// exists only for the case an on-demand call cannot cover: the agent crashed,
// or the node died, before it ever asked.
func runServe(root string) error {
	cfg, err := store.FromEnv()
	notConfigured := errors.Is(err, store.ErrNotConfigured)
	if err != nil && !notConfigured {
		return err
	}

	var s *store.Store
	if !notConfigured {
		if s, err = store.New(cfg); err != nil {
			return err
		}
	} else {
		log.Print("object storage not configured — checkpoints will be accepted and no-op")
	}

	// Parent pointer, for checkpoint lineage.
	//
	// Guarded because three goroutines reach it — the periodic ticker, the
	// /checkpoint handler, and the shutdown path — and the mutex also
	// serialises the snapshots themselves. That serialisation is the point as
	// much as the field is: two concurrent walks of the same tree would upload
	// interleaved views of it and race to claim the same parent.
	var (
		mu   sync.Mutex
		last string
	)

	snapshotCtx := func(ctx context.Context, name, desc, trigger string) (*store.Manifest, error) {
		if s == nil {
			return nil, store.ErrNotConfigured
		}
		mu.Lock()
		defer mu.Unlock()
		m, err := s.Snapshot(ctx, root, name, desc, trigger, last)
		if err != nil {
			return nil, err
		}
		last = m.ID
		return m, nil
	}

	// The ordinary path: a generous ceiling, since a first checkpoint of a
	// large workspace is genuinely slow and nothing is waiting on it. The
	// shutdown path passes its own, much shorter budget instead.
	snapshot := func(name, desc, trigger string) (*store.Manifest, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		return snapshotCtx(ctx, name, desc, trigger)
	}

	mux := http.NewServeMux()

	// POST /checkpoint — the agent's boundary call (§14).
	mux.HandleFunc("/checkpoint", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		m, err := snapshot(req.Name, req.Description, "on-demand")
		if errors.Is(err, store.ErrNotConfigured) {
			// 200 with no id: the caller asked correctly and this deployment
			// has nowhere to put it. Failing here would make an unconfigured
			// dev cluster look like a broken agent.
			writeJSON(w, http.StatusOK, map[string]any{"checkpointId": "", "skipped": "not-configured"})
			return
		}
		if err != nil {
			log.Printf("checkpoint failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("checkpoint %s (%d files, name=%q)", m.ID, len(m.Entries), m.Name)
		writeJSON(w, http.StatusOK, map[string]any{"checkpointId": m.ID, "files": len(m.Entries)})
	})

	// POST /flush — an on-demand snapshot, forced, from inside the pod.
	//
	// This was the operator's finalizer-gated teardown call. It is no longer:
	// teardown is handled by the SIGTERM path below, which needs no caller.
	// The endpoint stays because it is the only way to force a checkpoint
	// without waiting out the backstop interval, which makes it how this layer
	// gets tested — `kubectl exec` into the workload container and POST to
	// localhost, since the sidecar's own image is distroless and has no shell.
	mux.HandleFunc("/flush", func(w http.ResponseWriter, r *http.Request) {
		m, err := snapshot("", "", "teardown")
		if errors.Is(err, store.ErrNotConfigured) {
			writeJSON(w, http.StatusOK, map[string]any{"skipped": "not-configured"})
			return
		}
		if err != nil {
			log.Printf("flush failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"checkpointId": m.ID})
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	port := os.Getenv("WORKSPACE_SYNC_PORT")
	if port == "" {
		port = "7070"
	}
	srv := &http.Server{Addr: "127.0.0.1:" + port, Handler: mux}

	// Periodic backstop. Deliberately infrequent: it exists for the crash the
	// agent could not report, not as the main mechanism, and a short interval
	// would upload the same tree repeatedly for no benefit.
	interval := 5 * time.Minute
	if v := os.Getenv("WORKSPACE_SYNC_INTERVAL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = time.Duration(n) * time.Second
		}
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if _, err := snapshot("", "", "periodic"); err != nil && !errors.Is(err, store.ErrNotConfigured) {
					// Logged, not fatal: the backstop failing must not take the
					// sandbox down with it. The on-demand path is the primary
					// mechanism and is unaffected.
					log.Printf("periodic checkpoint failed: %v", err)
				}
			case <-stop:
				return
			}
		}
	}()

	// Teardown checkpoint, on SIGTERM (ADR-052 §14).
	//
	// This process is a NATIVE sidecar, so the kubelet signals it only after
	// the workload containers have exited. That ordering is what makes the
	// snapshot below meaningful: nothing is writing the tree any more, so this
	// is the one checkpoint in the whole design that is application-consistent
	// rather than merely crash-consistent.
	//
	// It replaces an operator-driven HTTP /flush that could never work — it
	// dialled the pod IP against a listener bound to loopback, and it fired on
	// CR deletion, by which time the pod was already gone. Doing it here needs
	// no network call, no authentication and no finalizer.
	//
	// It is an OPTIMISATION, not the durability mechanism. A node failure,
	// hard eviction or SIGKILL gives no graceful window at all, and no
	// signal-based design can promise one. The periodic backstop above is what
	// actually bounds loss; this narrows the window in the common case where
	// the shutdown is orderly.
	done := make(chan struct{})
	go func() {
		defer close(done)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		log.Print("shutdown signal received")

		// 1. Stop the backstop, so the ticker cannot start an upload that
		//    races the final one for the same tree.
		close(stop)

		// 2. Stop accepting new control requests, but let an in-flight
		//    /checkpoint finish — Shutdown waits for active handlers rather
		//    than cutting them off. Doing this BEFORE the final snapshot is
		//    what makes "no new sync work" true while it runs.
		shutCtx, cancelShut := context.WithTimeout(context.Background(), 30*time.Second)
		_ = srv.Shutdown(shutCtx)
		cancelShut()

		// 3. Take the final checkpoint, and only then exit.
		//
		// Bounded, because the kubelet SIGKILLs whatever is left when
		// terminationGracePeriodSeconds expires: better to give up a little
		// early and log it than to be killed mid-upload with no record of
		// having tried. The operator sets a floor on that grace period for
		// exactly this call (minWorkspaceGraceSeconds), and this budget is
		// deliberately shorter so the log line survives the deadline.
		budget := 90 * time.Second
		if v := os.Getenv("WORKSPACE_SYNC_SHUTDOWN_BUDGET_SECONDS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				budget = time.Duration(n) * time.Second
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()

		m, err := snapshotCtx(ctx, "", "", "teardown")
		switch {
		case errors.Is(err, store.ErrNotConfigured):
			log.Print("teardown checkpoint skipped — object storage not configured")
		case err != nil:
			// Loud, and still a clean exit. Failing the container here would
			// mark the pod as failed for a durability miss the backstop
			// already bounds, and would make an orderly shutdown look like a
			// crashed workload.
			log.Printf("TEARDOWN CHECKPOINT FAILED (up to %s of work not in object storage): %v", interval, err)
		default:
			log.Printf("teardown checkpoint %s (%d files)", m.ID, len(m.Entries))
		}
	}()

	log.Printf("workspace-sync serving on 127.0.0.1:%s (backstop every %s)", port, interval)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// ListenAndServe returns as soon as Shutdown is called, which is step 2 of
	// four. Returning here would exit before the checkpoint is written and
	// silently discard the very work this shutdown path exists to save.
	<-done
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
