package store

import (
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
