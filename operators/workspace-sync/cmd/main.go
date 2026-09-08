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
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
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

// runRestore is a one-shot restore, kept for debugging and ops use only.
//
// The pod does NOT run it. Under §14.2 the FUSE mount belongs to the native
// sidecar and nothing else: a mount made by an init container dies with that
// container's mount namespace, so an init container can prepare a workspace
// the sidecar then hides, or download an archive the sidecar downloads again.
// One owner of restore, and it is `serve`.
func runRestore(root string) error {
	cfg, err := store.FromEnv()
	if errors.Is(err, store.ErrNotConfigured) {
		log.Print("object storage not configured — nothing to restore")
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
	_, err = restoreWorkspace(ctx, root, s)
	return err
}

// restoreWorkspace prepares root before the workload starts, and reports
// whether the result is a FUSE overlay (true) or a plain directory (false).
//
// Three outcomes, and telling them apart is the whole correctness of this
// function:
//
//   - an archive exists          → mount it, O(1) (§14.2);
//   - no archive, no manifest    → a genuinely new workspace, start empty;
//   - no archive, manifests exist → file-by-file restore (pre-§14.2 workspace,
//     or one whose archive creation failed).
//
// Anything else is an ERROR, and the caller must let it be fatal.
//
// That last rule is not defensive style, it is a data-loss guard. If S3 is
// unreachable and this returns "empty workspace" instead of failing, the
// workload starts against an empty tree, the periodic backstop uploads that
// emptiness as a new checkpoint, and retention (§14.2, keep 5) then evicts the
// real checkpoints behind it. A few backstop intervals of an unreachable
// bucket would destroy the workspace it exists to protect. Refusing to start
// is always recoverable; silently starting empty is not.
func restoreWorkspace(ctx context.Context, root string, s *store.Store) (bool, error) {
	if err := os.MkdirAll(root, 0o777); err != nil {
		return false, err
	}
	staging := s.StagingDir()
	archivePath := filepath.Join(staging, "archive.sqsh")

	// A pinned checkpoint takes a different route entirely (§14.3): the
	// workspace-level archive.sqsh is whatever was checkpointed LAST, so using
	// it for a pin would restore the wrong revision while reporting success.
	if pin := s.Pinned(); pin != "" {
		id, rErr := s.Resolve(ctx)
		if rErr != nil {
			return false, rErr
		}
		aErr := s.DownloadCheckpointArchive(ctx, id, archivePath)
		switch {
		case aErr == nil:
			if mErr := mountFuse(ctx, root, staging, archivePath); mErr != nil {
				log.Printf("pinned archive present but could not be mounted, falling back: %v", mErr)
				return false, restoreFromCheckpoint(ctx, root, s, id)
			}
			log.Printf("restored pinned checkpoint %s via squashfs overlay", id)
			return true, nil
		case errors.Is(aErr, store.ErrNoArchive):
			// The checkpoint exists — Resolve proved it — but predates
			// archives or had archive creation fail. Its content-addressed
			// objects still describe it exactly.
			return false, restoreFromCheckpoint(ctx, root, s, id)
		default:
			return false, fmt.Errorf("cannot fetch pinned checkpoint %s: %w", id, aErr)
		}
	}

	err := s.DownloadArchive(ctx, archivePath)
	switch {
	case err == nil:
		if mErr := mountFuse(ctx, root, staging, archivePath); mErr != nil {
			// A present-but-unmountable archive is NOT a reason to start empty
			// — the data exists. Fall back to the slow path, which reads the
			// same content from the content-addressed objects.
			log.Printf("archive present but could not be mounted, falling back to file-by-file: %v", mErr)
			return false, restoreFromManifest(ctx, root, s)
		}
		log.Printf("restored via squashfs overlay at %s", root)
		return true, nil

	case errors.Is(err, store.ErrNoArchive):
		return false, restoreFromManifest(ctx, root, s)

	default:
		return false, fmt.Errorf("cannot reach object storage to restore workspace: %w", err)
	}
}

// mountFuse mounts the archive read-only and lays a writable overlay over it.
//
// Both mounts live in THIS process's mount namespace. They are visible to the
// workload container only because the operator sets mountPropagation
// (Bidirectional here, HostToContainer there) — without it the workload sees
// the bare emptyDir and the restore is invisible to the thing it was for.
func mountFuse(ctx context.Context, workspaceRoot, staging, archivePath string) error {
	lowerDir := filepath.Join(staging, "lower")
	upperDir := filepath.Join(staging, "upper")
	workDir := filepath.Join(staging, "work")

	for _, d := range []string{lowerDir, upperDir, workDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create staging dir %s: %w", d, err)
		}
	}

	// upperDir backs every NEW entry the merged mount will ever show — not just
	// its contents once something exists, but the top-level directory itself.
	// fuse-overlayfs reports the merge point's own permission bits from upperDir
	// (upperDir already contains the root, so it is never "copied up" the way a
	// child directory is); those bits are otherwise whatever MkdirAll left them
	// as a moment ago: root:root, 0755. A workload running as uid 1000 then gets
	// EACCES on anything it tries to create directly under /workspace — `mkdir
	// node_modules`, in particular — while files that already exist (restored
	// from the read-only squashfs lower layer, correctly owned from when that
	// layer was built) remain readable and writable, because THEIR ownership
	// comes from the lower layer, not upperDir's.
	//
	// Confirmed live: `ls -lad /workspace` showed `root root`, while every entry
	// inside it — including subdirectories the agent had already been writing
	// into — showed `1000 root`. Only the mount's own top-level entry was wrong.
	//
	// This is also why the symptom outlives any one restore. The next snapshot
	// walks the live (merged) tree with mksquashfs, which preserves ownership by
	// default — so an unfixed root directory gets baked into the NEXT archive
	// too, and the corruption survives every subsequent restore of this
	// workspace until something explicitly re-chowns the mount root.
	if err := os.Chown(upperDir, workspaceUID(), workspaceUID()); err != nil {
		return fmt.Errorf("chown upper dir %s to workload uid: %w", upperDir, err)
	}

	// allow_other on BOTH mounts, and it is not optional.
	//
	// A FUSE mount is accessible only to the uid that created it. This process
	// runs as root (privileged, for the mount itself); the workload runs as
	// 1000. Without allow_other the agent gets EACCES on its own /workspace —
	// a restore that succeeded, a mount that exists, and a workload that cannot
	// read a single byte of it.
	//
	// Permitted here because the mounting user is root; unprivileged callers
	// would additionally need user_allow_other in /etc/fuse.conf.
	cmd := exec.CommandContext(ctx, "squashfuse", "-o", "allow_other", archivePath, lowerDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("squashfuse: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	cmd = exec.CommandContext(ctx, "fuse-overlayfs",
		"-o", fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s,allow_other", lowerDir, upperDir, workDir),
		workspaceRoot)
	if out, err := cmd.CombinedOutput(); err != nil {
		exec.Command("fusermount3", "-u", lowerDir).Run()
		return fmt.Errorf("fuse-overlayfs: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// workspaceUID is the uid the workload container runs as, and therefore the
// uid every restored file — and every directory the restore itself creates —
// must belong to.
//
// This process runs as root because FUSE mounting requires it. Anything it
// writes directly is therefore root-owned, and the agent — uid 1000 — cannot
// modify it. On a git tree that surfaces as a permission error deep inside an
// unrelated operation rather than as anything about ownership, which is the
// same failure that once took down every resumed session.
//
// The FUSE path is NOT exempt from this, despite this function previously
// claiming it was. Existing content is fine — its ownership comes from the
// squashfs image, which recorded the workload's uid when the archive was
// built — but upperDir, the writable layer mountFuse creates fresh on every
// restore, is written by this process directly and needs the same chown as
// the slow path's chownTree below. See mountFuse for what shipped without it.
func workspaceUID() int {
	if v := os.Getenv("WORKSPACE_UID"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return 1000
}

// chownTree gives a file-by-file restore back to the workload.
func chownTree(root string, uid int) error {
	return filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, uid)
	})
}

// restoreFromManifest is the content-addressed slow path: correct everywhere,
// O(number of files), and the only path that works without FUSE at all.
//
// No checkpoint is SUCCESS, not failure. It means a workspace that has never
// been checkpointed, which is every workspace's first run. Returning an error
// here made the process exit non-zero and the pod fail to start — so enabling
// object storage, which is meant to add durability, made every new sandbox
// unstartable.
func restoreFromManifest(ctx context.Context, root string, s *store.Store) error {
	id, err := s.Latest(ctx)
	if err != nil {
		return fmt.Errorf("reading LATEST pointer: %w", err)
	}
	if id == "" {
		log.Print("no checkpoint for this workspace yet — starting with an empty workspace")
		return nil
	}
	return restoreFromCheckpoint(ctx, root, s, id)
}

// restoreFromCheckpoint materialises one named checkpoint file-by-file.
//
// Unlike restoreFromManifest there is no "nothing to restore" case: the caller
// named a checkpoint, so failing to produce it is an error rather than an empty
// workspace.
func restoreFromCheckpoint(ctx context.Context, root string, s *store.Store, id string) error {
	m, err := s.GetManifest(ctx, id)
	if err != nil {
		return fmt.Errorf("reading checkpoint %s: %w", id, err)
	}
	if err := s.Restore(ctx, root, m); err != nil {
		return fmt.Errorf("restoring checkpoint %s: %w", id, err)
	}
	// Hand the tree to the workload — see workspaceUID.
	uid := workspaceUID()
	if err := chownTree(root, uid); err != nil {
		return fmt.Errorf("chown restored workspace to uid %d: %w", uid, err)
	}
	log.Printf("restored checkpoint %s file-by-file (%d files, taken %s), owned by uid %d",
		m.ID, len(m.Entries), m.CreatedAt.Format(time.RFC3339), uid)
	return nil
}

// unmountFuse tears down any active FUSE mounts at workspaceRoot and in stagingRoot.
func unmountFuse(workspaceRoot, stagingRoot string) {
	// Unmount overlay first (the merged view).
	exec.Command("fusermount3", "-u", workspaceRoot).Run()
	// Then unmount squashfs lower.
	exec.Command("fusermount3", "-u", stagingRoot+"/lower").Run()
	// Clean staging dirs.
	os.RemoveAll(stagingRoot + "/lower")
	os.RemoveAll(stagingRoot + "/upper")
	os.RemoveAll(stagingRoot + "/work")
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

	stagingRoot := "/ws-staging"
	if s != nil {
		stagingRoot = s.StagingDir()
	} else if v := os.Getenv("STAGING_ROOT"); v != "" {
		stagingRoot = v
	}

	// Read-only: mounted, serving, and incapable of writing (§14.3).
	//
	// Declared here because every path below consults it — the restore, the
	// HTTP handlers, the backstop and the shutdown snapshot. In this mode the
	// backstop and teardown snapshot are never STARTED, rather than started and
	// then refused by the store's own guard: a build has no business generating
	// a checkpoint attempt every five minutes, and the log noise of refusing
	// them would bury a real failure.
	readOnly := s != nil && s.ReadOnly()
	if readOnly {
		log.Print("workspace is READ-ONLY — no periodic backstop, no teardown checkpoint, no retention")
	}

	// Phase 1: restore, once, before anything else (§14.2).
	//
	// This is the ONLY restore in the pod. It used to run here AND in an init
	// container, which downloaded the same archive twice and mounted an overlay
	// that was destroyed the moment the init container exited.
	//
	// A failure returns, and returning kills the process. That is deliberate:
	// this is a native sidecar with restartPolicy Always, so it will
	// CrashLoopBackOff with the reason in its logs, the readiness marker below
	// is never written, the startup probe never passes, and the WORKLOAD NEVER
	// STARTS. A sandbox that refuses to run is the correct outcome when its
	// workspace could not be restored — the alternative is an agent editing an
	// empty tree that will be checkpointed over the real one.
	if !notConfigured {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		fused, rErr := restoreWorkspace(ctx, root, s)
		cancel()
		if rErr != nil {
			return fmt.Errorf("restore on startup: %w", rErr)
		}
		if fused {
			defer unmountFuse(root, stagingRoot)
		}
	}

	// Phase 2: announce readiness.
	//
	// A marker file, not an HTTP endpoint, because the kubelet probes from the
	// node against the pod IP and this process binds loopback — an httpGet
	// probe here can never succeed. The operator gates the workload's start on
	// an exec probe testing for this file, which is what guarantees the agent
	// never sees a half-restored workspace.
	//
	// It lives in the staging emptyDir, which is fresh on every pod, so a stale
	// marker from a previous life cannot make an unrestored workspace look
	// ready.
	readyMarker := filepath.Join(stagingRoot, ".ready")
	if err := os.WriteFile(readyMarker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write readiness marker: %w", err)
	}
	defer os.Remove(readyMarker)
	log.Printf("workspace ready at %s", root)

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

		if readOnly {
			// 409, not 500: the request is well-formed and the caller is simply
			// asking something this workspace cannot do. A build image that
			// checkpoints out of habit should see a clear refusal, not a
			// failure that looks like object storage being broken.
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "workspace is read-only; checkpoints are disabled for this job",
			})
			return
		}
		m, err := snapshot(req.Name, req.Description, "on-demand")
		if errors.Is(err, store.ErrNothingToSave) {
			// 200 with no id, like the not-configured case: the request was
			// correct and there is simply nothing here yet. A 4xx would read as
			// "you did something wrong".
			writeJSON(w, http.StatusOK, map[string]any{"checkpointId": "", "skipped": "nothing-to-save"})
			return
		}
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
		if readOnly {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "workspace is read-only; checkpoints are disabled for this job",
			})
			return
		}
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

	// POST /restore {checkpointId?} — undo-to-checkpoint (§14.2).
	//
	// Tears the overlay down and rebuilds it from the requested checkpoint's
	// archive, or from the latest if none is named.
	//
	// DESTRUCTIVE, and the caller has to understand that: everything written
	// since the target checkpoint lives in the overlay's upper layer, which
	// this discards. The tree the workload sees changes underneath it, so any
	// file descriptor the agent holds open across this call is invalidated —
	// the agent must be quiesced first. This endpoint does not and cannot
	// enforce that; it is reachable only from inside the pod.
	//
	// Serialised against snapshots with the same mutex. A checkpoint racing a
	// restore would otherwise capture a half-swapped tree and upload it as the
	// new latest, turning an undo into corruption.
	mux.HandleFunc("/restore", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if notConfigured {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "object storage not configured"})
			return
		}
		if readOnly {
			// A read-only job was given one exact checkpoint to work from
			// (§14.3). Letting it swap to another mid-run would mean a build
			// producing an artifact labelled with a revision it did not build.
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "workspace is pinned and read-only; restore is disabled for this job",
			})
			return
		}

		var req struct {
			CheckpointID string `json:"checkpointId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		mu.Lock()
		defer mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		// Resolve the target first, so a bad id is a 404 BEFORE anything is
		// unmounted. Rejecting after teardown would leave the workspace in
		// neither the old state nor the new one.
		target := req.CheckpointID
		if target == "" {
			id, err := s.Latest(ctx)
			if err != nil || id == "" {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "no checkpoint to restore"})
				return
			}
			target = id
		}
		m, err := s.GetManifest(ctx, target)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("checkpoint %s not found: %v", target, err)})
			return
		}

		archivePath := filepath.Join(stagingRoot, "archive.sqsh")
		archiveErr := s.DownloadCheckpointArchive(ctx, target, archivePath)
		if archiveErr != nil && !errors.Is(archiveErr, store.ErrNoArchive) {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": archiveErr.Error()})
			return
		}

		// Only now tear down the old view.
		unmountFuse(root, stagingRoot)

		if archiveErr == nil {
			if err := mountFuse(ctx, root, stagingRoot, archivePath); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
		} else {
			// No archive for this checkpoint — rebuild it file-by-file from the
			// manifest instead. unmountFuse already cleared the staging dirs, so
			// this writes into a bare workspace rather than over stale content.
			if err := s.Restore(ctx, root, m); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
		}

		// Lineage: work resumed after an undo descends from the checkpoint it
		// was restored to, not from whatever was checkpointed last.
		last = target
		log.Printf("restored to checkpoint %s (%d files)", target, len(m.Entries))
		writeJSON(w, http.StatusOK, map[string]any{"restored": true, "checkpoint": target})
	})

	// GET /list-checkpoints — returns checkpoint IDs in reverse chronological
	// order, for the undo-to-checkpoint UI.
	mux.HandleFunc("/list-checkpoints", func(w http.ResponseWriter, r *http.Request) {
		if notConfigured {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "object storage not configured"})
			return
		}
		ids, err := s.ListCheckpoints(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, ids)
	})

	port := os.Getenv("WORKSPACE_SYNC_PORT")
	if port == "" {
		port = "7070"
	}
	srv := &http.Server{Addr: "127.0.0.1:" + port, Handler: mux}

	// Periodic backstop — OFF by default (ADR-052 §14.6).
	//
	// A checkpoint is taken when something meaningful happens: the user asks
	// for one, or the pod is shutting down. Not on a clock.
	//
	// This matched the Cloudflare sandbox-sdk reference
	// (reference-projects/sandbox/sandbox-sdk) only after being turned off:
	// that design has NO periodic snapshot anywhere — its container saves on
	// suspend, and the platform deliberately refrains from destroying the
	// container so that save can finish (devin/src/index.ts's `stop`).
	//
	// The ticker ran unconditionally every 5 minutes, and the cost was not
	// theoretical. Each run writes a full compressed squashfs image twice (the
	// per-checkpoint archive and the workspace `archive.sqsh`) whether or not a
	// byte changed, so an idle sandbox shipped a whole workspace to object
	// storage twelve times an hour. Worse, at 12 checkpoints/hour against a
	// retention bound of 5, the checkpoints a user actually cared about were
	// evicted by identical idle ones within about twenty-five minutes.
	//
	// Set WORKSPACE_SYNC_INTERVAL_SECONDS to re-enable it where an unattended
	// workload has no one to press Save — a long batch job, say. The teardown
	// checkpoint still covers orderly shutdown either way; what no design can
	// cover is SIGKILL or node loss, and that is the exposure a fleet accepts
	// by leaving this off.
	interval := time.Duration(0)
	if v := os.Getenv("WORKSPACE_SYNC_INTERVAL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = time.Duration(n) * time.Second
		}
	}
	stop := make(chan struct{})
	go func() {
		if readOnly || interval <= 0 {
			return
		}
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

		// 2. Unmount FUSE overlays before teardown checkpoint (§14.2).
		//    This ensures the final snapshot captures a clean tree, not a
		//    FUSE-mounted overlay that may be stale.
		unmountFuse(root, stagingRoot)

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

		if readOnly {
			log.Print("read-only workspace — no teardown checkpoint")
			return
		}

		m, err := snapshotCtx(ctx, "", "", "teardown")
		switch {
		case errors.Is(err, store.ErrNotConfigured):
			log.Print("teardown checkpoint skipped — object storage not configured")
		case errors.Is(err, store.ErrNothingToSave):
			log.Print("teardown checkpoint skipped — workspace has no files to save")
		case err != nil:
			// Loud, and still a clean exit. Failing the container here would
			// mark the pod as failed for a durability miss the backstop
			// already bounds, and would make an orderly shutdown look like a
			// crashed workload.
			// Says what was actually lost. With the backstop off, everything
			// since the last user-requested checkpoint is gone — quoting an
			// interval that is not running would understate it.
			lost := "all work since the last saved checkpoint"
			if interval > 0 {
				lost = fmt.Sprintf("up to %s of work", interval)
			}
			log.Printf("TEARDOWN CHECKPOINT FAILED (%s not in object storage): %v", lost, err)
		default:
			log.Printf("teardown checkpoint %s (%d files)", m.ID, len(m.Entries))
		}
	}()

	backstop := "off (checkpoints on request and at shutdown)"
	if interval > 0 {
		backstop = fmt.Sprintf("every %s", interval)
	}
	log.Printf("workspace-sync serving on 127.0.0.1:%s (periodic backstop: %s)", port, backstop)
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
