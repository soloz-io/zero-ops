package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPlanRetentionNeverOverDeletes covers the one function in this package
// that destroys data.
//
// Under §14.2 there is no PVC and no second copy: what retention removes is
// gone. So the cases that matter most here are the degenerate ones, where a
// slicing error or a misread config would take everything.
func TestPlanRetentionNeverOverDeletes(t *testing.T) {
	ids := []string{"e", "d", "c", "b", "a"} // newest first

	tests := []struct {
		name      string
		ids       []string
		keep      int
		survivors []string
		doomed    []string
	}{
		{"fewer than the bound", []string{"b", "a"}, 5, []string{"b", "a"}, nil},
		{"exactly the bound", ids, 5, ids, nil},
		{"over the bound keeps the newest", ids, 3, []string{"e", "d", "c"}, []string{"b", "a"}},
		{"empty store", nil, 5, nil, nil},

		// keep <= 0 must not mean "keep none". A KEEP_CHECKPOINTS of 0 or a
		// negative from a bad parse has to degrade to unbounded storage — the
		// recoverable failure — rather than to an empty bucket.
		{"keep zero deletes nothing", ids, 0, ids, nil},
		{"keep negative deletes nothing", ids, -1, ids, nil},
		{"keep one", ids, 1, []string{"e"}, []string{"d", "c", "b", "a"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			survivors, doomed := planRetention(tc.ids, tc.keep, nil)
			if strings.Join(survivors, ",") != strings.Join(tc.survivors, ",") {
				t.Errorf("survivors = %v, want %v", survivors, tc.survivors)
			}
			if strings.Join(doomed, ",") != strings.Join(tc.doomed, ",") {
				t.Errorf("doomed = %v, want %v", doomed, tc.doomed)
			}
			// Whatever the inputs, nothing may be both kept and deleted, and
			// nothing may vanish from the accounting entirely.
			if len(survivors)+len(doomed) != len(tc.ids) {
				t.Errorf("%d survivors + %d doomed != %d inputs — a checkpoint was lost or duplicated",
					len(survivors), len(doomed), len(tc.ids))
			}
		})
	}
}

// TestNewIDSortsChronologically pins a coupling ListCheckpoints depends on.
//
// ListCheckpoints orders by plain string sort instead of reading CreatedAt out
// of every manifest, which is only correct because newID() prefixes the id with
// the creation time in big-endian nanoseconds. If that encoding ever changes,
// ordering silently inverts — and retention deletes the NEWEST checkpoints
// while the undo list shows the oldest.
func TestNewIDSortsChronologically(t *testing.T) {
	var ids []string
	for i := 0; i < 8; i++ {
		ids = append(ids, newID())
		time.Sleep(time.Millisecond)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("ids are not lexically increasing with time: %q >= %q.\n"+
				"ListCheckpoints sorts strings to get chronological order, so this breaks "+
				"both the undo list and retention.", ids[i-1], ids[i])
		}
	}
}

// TestFromEnvRequiresAllFourS3Values guards the partial-credential case.
//
// Three of four values must read as "not configured", not as configured — a
// Store built from a half-filled config would fail every operation at the
// network layer instead of no-opping cleanly.
func TestFromEnvRequiresAllFourS3Values(t *testing.T) {
	t.Setenv("WORKSPACE_ID", "ws-1")
	t.Setenv("APP_ID", "app-1")
	t.Setenv("S3_ENDPOINT_URL", "https://hel1.example.com")
	t.Setenv("S3_BUCKET_NAME", "bucket")
	t.Setenv("S3_ACCESS_KEY_ID", "key")
	t.Setenv("S3_SECRET_ACCESS_KEY", "")

	if _, err := FromEnv(); err != ErrNotConfigured {
		t.Errorf("FromEnv with a missing secret key = %v, want ErrNotConfigured", err)
	}

	t.Setenv("S3_SECRET_ACCESS_KEY", "secret")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv with all four set = %v, want nil", err)
	}
	if cfg.KeepCheckpoints != DefaultKeepCheckpoints {
		t.Errorf("KeepCheckpoints = %d, want the %d default", cfg.KeepCheckpoints, DefaultKeepCheckpoints)
	}
	// Staging must never default to os.TempDir(): the container's root
	// filesystem is read-only, so an archive written to /tmp silently fails.
	if cfg.Staging != "/ws-staging" {
		t.Errorf("Staging = %q, want /ws-staging", cfg.Staging)
	}
}

// TestKeyLayoutIsAppRooted pins the object layout (§14.4).
//
// It is a test rather than a comment because the prefix is unverifiable at
// runtime: writing to the wrong one does not error, it silently addresses a
// workspace nobody else can see. The symptom is an empty restore much later,
// with nothing in any log pointing at the cause.
func TestKeyLayoutIsAppRooted(t *testing.T) {
	// Built through New(), not by hand. The previous version of this test
	// assembled the prefix itself, so it asserted its own arithmetic and would
	// have passed unchanged while New() produced something else entirely.
	st, err := New(Config{
		Endpoint: "http://localhost:9000", Bucket: "b",
		AccessKey: "k", SecretKey: "s",
		AppID: "app-123", WorkspaceID: "ws-42",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if st.prefix != "app-123/ws-42" {
		t.Fatalf("prefix = %q, want %q — the workspace root, with no segment between it and the names below",
			st.prefix, "app-123/ws-42")
	}

	cases := map[string]string{
		st.objectKey("abc"):            "app-123/ws-42/objects/abc",
		st.manifestKey("cp1"):          "app-123/ws-42/checkpoints/cp1.json",
		st.checkpointArchiveKey("cp1"): "app-123/ws-42/checkpoints/cp1.sqsh",
		st.archiveKey():                "app-123/ws-42/archive.sqsh",
		st.prefix + latestPointer:      "app-123/ws-42/checkpoints/LATEST",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("key = %q, want %q", got, want)
		}
	}

	// The real safety property, which `code/` was mistakenly credited with.
	//
	// Retention lists <prefix>/objects/ and <prefix>/checkpoints/ and deletes
	// within them. What keeps it away from an app's other data is that both
	// sweeps are scoped to those names — not that a segment sits above them. So
	// what must hold is that nothing this package sweeps shares a prefix with a
	// sibling namespace the SDK owns (storage/keys.ts).
	siblings := []string{
		"app-123/ws-42/chat-attachments/",
		"app-123/ws-42/artifacts/",
		"app-123/ws-42/builds/",
	}
	for _, swept := range []string{st.prefix + "/objects/", st.prefix + "/checkpoints/"} {
		for _, sib := range siblings {
			if strings.HasPrefix(sib, swept) {
				t.Errorf("retention sweeps %q, which contains the sibling namespace %q", swept, sib)
			}
			if strings.HasPrefix(swept, sib) {
				t.Errorf("swept prefix %q sits inside sibling namespace %q", swept, sib)
			}
		}
	}
}

// TestAppIDIsRequired guards the half of the key that has no default.
//
// Missing WORKSPACE_ID already failed. APP_ID must fail the same way and for
// the same reason: there is no app-less location in this layout, so continuing
// would write a workspace to a prefix nothing else addresses.
func TestAppIDIsRequired(t *testing.T) {
	t.Setenv("WORKSPACE_ID", "ws-42")
	t.Setenv("S3_ENDPOINT_URL", "https://hel1.example.com")
	t.Setenv("S3_BUCKET_NAME", "bucket")
	t.Setenv("S3_ACCESS_KEY_ID", "key")
	t.Setenv("S3_SECRET_ACCESS_KEY", "secret")
	t.Setenv("APP_ID", "")

	_, err := FromEnv()
	if err == nil {
		t.Fatal("FromEnv with no APP_ID succeeded; keys would be rooted at an empty segment")
	}
	if errors.Is(err, ErrNotConfigured) {
		t.Errorf("FromEnv reported ErrNotConfigured for a missing APP_ID (%v); a misconfigured "+
			"deployment must not look like one that has no object storage", err)
	}

	t.Setenv("APP_ID", "app-123")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv with APP_ID set = %v, want nil", err)
	}
	if cfg.AppID != "app-123" {
		t.Errorf("AppID = %q, want app-123", cfg.AppID)
	}
}

// TestPinnedCheckpointsSurviveRetention covers §14.3's deployment pin.
//
// A checkpoint some live deployment was built from must outlive the retention
// window. Without this the app keeps serving while its source is gone —
// unreproducible, undiffable, un-rollback-able — and nothing about serving the
// build reveals the loss.
func TestPinnedCheckpointsSurviveRetention(t *testing.T) {
	ids := []string{"e", "d", "c", "b", "a"} // newest first

	// "a" is the oldest and well outside a keep-of-2, but it is deployed.
	survivors, doomed := planRetention(ids, 2, map[string]struct{}{"a": {}})

	if strings.Join(survivors, ",") != "e,d,a" {
		t.Errorf("survivors = %v, want [e d a] — the deployed checkpoint must not be evicted", survivors)
	}
	if strings.Join(doomed, ",") != "c,b" {
		t.Errorf("doomed = %v, want [c b]", doomed)
	}

	// A pin must not CONSUME a retention slot: keeping N recent checkpoints and
	// keeping the deployed one are separate promises, and letting them compete
	// would let one old deployment quietly shrink usable history.
	if len(survivors) != 3 {
		t.Errorf("pin consumed a retention slot: %d survivors, want 3 (2 recent + 1 pinned)", len(survivors))
	}

	// A pin already inside the window changes nothing.
	survivors, doomed = planRetention(ids, 2, map[string]struct{}{"e": {}})
	if strings.Join(survivors, ",") != "e,d" || strings.Join(doomed, ",") != "c,b,a" {
		t.Errorf("pin inside the window altered the split: survivors=%v doomed=%v", survivors, doomed)
	}

	// A pin naming a checkpoint that no longer exists is inert, not an error.
	survivors, _ = planRetention(ids, 2, map[string]struct{}{"gone": {}})
	if strings.Join(survivors, ",") != "e,d" {
		t.Errorf("unknown pin altered the split: %v", survivors)
	}
}

func TestSplitListIgnoresBlanks(t *testing.T) {
	// A trailing comma or an unset variable must yield no ids rather than one
	// empty id — an empty pin would match nothing and merely look like a bug.
	for _, in := range []string{"", "   ", ",", " , , "} {
		if got := splitList(in); len(got) != 0 {
			t.Errorf("splitList(%q) = %v, want empty", in, got)
		}
	}
	got := splitList(" a , b ,, c ")
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("splitList = %v, want [a b c]", got)
	}
}

// TestSkipDirCoversTheCostlyTrees pins the exclusion list that makes a save
// finish at all.
//
// Measured on a real scaffolded Expo app: 21,412 files / 369 MB in the
// workspace, of which 21,388 files / 368 MB were node_modules — the source was
// 24 files. Snapshot hashes and PutIfAbsent-checks each file individually, so
// including node_modules meant ~21k round trips to object storage per save and
// a 504 at every layer.
func TestSkipDirCoversTheCostlyTrees(t *testing.T) {
	for _, d := range []string{"node_modules", ".expo", "dist", "build", "web-build", ".cache"} {
		if !skipDir[d] {
			t.Errorf("%q is not excluded; it is derived output and re-adds the cost this list exists to avoid", d)
		}
	}

	// `.git` must NEVER be excluded. It is the user's history, §14 names it
	// explicitly as an ordinary subdirectory, and it is small next to
	// node_modules. Excluding it would silently drop everything a restore needs
	// to show what changed.
	if skipDir[".git"] {
		t.Error(".git is excluded; it is the user's history, not derived output (§14)")
	}
	// Nor the platform's own workspace scaffolding.
	if skipDir[".builder"] {
		t.Error(".builder is excluded; it is workspace state, not derived output")
	}
}

// TestSkipDirIsNotAppliedToTheWalkRoot pins the guard that makes the globals
// instance work at all.
//
// `.global` is in skipDir so the SESSION snapshot leaves app-scoped artifacts
// to their own prefix. But the globals instance walks with WORKSPACE_ROOT set
// to that same directory, and filepath.Walk calls its callback on the root
// first — so a name-only check skips the root and the walk yields nothing.
//
// The failure that guard prevents is silent, which is why it is tested: an
// empty walk produces ErrNothingToSave, which reads exactly like a workspace
// that legitimately has no files yet. Globals would simply never be uploaded.
func TestSkipDirIsNotAppliedToTheWalkRoot(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".global", "skills", "brand-brief", "SKILL.md"), "brand")
	mustWrite(t, filepath.Join(root, "output", "scenes", "scene001", "motion.tsx"), "scene")

	// Session walk, rooted at the workspace: .global is excluded.
	if got := walkKept(t, root); len(got) != 1 || got[0] != filepath.Join("output", "scenes", "scene001", "motion.tsx") {
		t.Errorf("session walk should keep only the session tree, got %v", got)
	}

	// Globals walk, rooted AT .global: the root must not skip itself.
	globalsRoot := filepath.Join(root, ".global")
	got := walkKept(t, globalsRoot)
	if len(got) == 0 {
		t.Fatal("globals walk returned nothing: skipDir was applied to its own root, " +
			"so app-scoped artifacts would never be uploaded and the error would look like an empty workspace")
	}
	if got[0] != filepath.Join("skills", "brand-brief", "SKILL.md") {
		t.Errorf("unexpected globals entry %v", got)
	}
}

// walkKept mirrors Snapshot's traversal rules — the skipDir check with its
// `p != root` guard, and the regular-file filter — without needing S3.
func walkKept(t *testing.T, root string) []string {
	t.Helper()
	var kept []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipDir[info.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		kept = append(kept, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return kept
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEmptyWorkspaceIsNotCheckpointed guards against writing a snapshot of
// nothing.
//
// An empty checkpoint becomes LATEST, appears in the deploy picker as something
// selectable that would build nothing, and consumes a retention slot. Observed:
// `checkpoint 18d31e0c… (0 files)` written before the agent had scaffolded
// anything.
func TestEmptyWorkspaceIsNotCheckpointed(t *testing.T) {
	// The sentinel is what callers branch on to say "nothing to save yet"
	// rather than reporting either a success or a fault.
	if ErrNothingToSave == nil {
		t.Fatal("ErrNothingToSave must exist for callers to distinguish this case")
	}
	if errors.Is(ErrNothingToSave, ErrNotConfigured) || errors.Is(ErrNothingToSave, ErrReadOnly) {
		t.Error("ErrNothingToSave must be distinguishable from the other no-op sentinels")
	}
}

// TestStagingDirIsPerInstance pins the reason two instances cannot share one
// staging directory, and therefore why the directory has to be created.
//
// The readiness marker is named from StagingDir(). The operator gates the
// workload's start on an exec probe testing for that file, so two instances
// sharing a directory would let whichever restored first satisfy both probes —
// and the workload would start against a tree the other had not finished
// restoring. Giving the second instance a subdirectory is what keeps the two
// probes independent, and a subdirectory of an emptyDir is not created by the
// kubelet: the process must create it or die writing its own marker.
func TestStagingDirIsPerInstance(t *testing.T) {
	session, err := New(Config{
		Endpoint: "http://localhost:9000", Bucket: "b", AccessKey: "k", SecretKey: "s",
		AppID: "app1", WorkspaceID: "ws1", Staging: "/ws-staging",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	shared, err := New(Config{
		Endpoint: "http://localhost:9000", Bucket: "b", AccessKey: "k", SecretKey: "s",
		AppID: "app1", WorkspaceID: "globals", Staging: "/ws-staging/.global",
		NoArchive: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if session.StagingDir() == shared.StagingDir() {
		t.Fatalf("both instances stage in %q; one's readiness marker would satisfy the other's probe",
			session.StagingDir())
	}
	if !shared.NoArchive() {
		t.Error("the shared instance must skip squashfs: its root is inside a mount it does not own")
	}
	if session.NoArchive() {
		t.Error("the session instance must keep the squashfs fast path")
	}
}
