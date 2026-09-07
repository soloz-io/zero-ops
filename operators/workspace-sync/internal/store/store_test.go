package store

import (
	"errors"
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
			survivors, doomed := planRetention(tc.ids, tc.keep)
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
	s := &Store{prefix: "app-123" + "/" + "ws-42" + "/code"}

	cases := map[string]string{
		s.objectKey("abc"):            "app-123/ws-42/code/objects/abc",
		s.manifestKey("cp1"):          "app-123/ws-42/code/checkpoints/cp1.json",
		s.checkpointArchiveKey("cp1"): "app-123/ws-42/code/checkpoints/cp1.sqsh",
		s.archiveKey():                "app-123/ws-42/code/archive.sqsh",
		s.prefix + latestPointer:      "app-123/ws-42/code/checkpoints/LATEST",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("key = %q, want %q", got, want)
		}
	}

	// The `code/` segment is what makes an app-rooted layout safe to share.
	// Retention deletes everything under <prefix>/objects/ that no surviving
	// manifest references; without this segment that sweep would sit directly
	// above the app's chat attachments and build artifacts.
	for k := range cases {
		if !strings.HasPrefix(k, "app-123/ws-42/code/") {
			t.Errorf("key %q escapes the code/ subtree — retention could reach sibling data", k)
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
