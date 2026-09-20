package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWorkspaceRootIsHandedToTheWorkload pins the ownership handover that the
// agent depends on and that no restore path guarantees on its own.
//
// The failure it guards against is silent and remote from its cause: restore
// succeeds, the pod starts, and the agent fails minutes later trying to write
// its own workspace — reported as "the folder is read-only" with nothing
// pointing back at a directory created empty at pod start.
//
// Two things conspire. MkdirAll's mode passes through umask, so 0777 lands as
// 0755; and the sync process runs as root (it must, to chown restored files),
// so anything it creates is root-owned. A workspace with no checkpoint yet
// never reaches the file-by-file chown, so nothing corrects either.
func TestWorkspaceRootIsHandedToTheWorkload(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(root, 0o777); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	// Demonstrates the half of the problem that holds on every platform: the
	// requested mode is not the mode you get.
	if perm := info.Mode().Perm(); perm == 0o777 {
		t.Logf("umask did not clear any bits here (perm=%#o); on a 022 umask this is 0755", perm)
	} else {
		t.Logf("MkdirAll(0777) produced %#o — the mode alone cannot be relied on", perm)
	}

	// The chown itself is only meaningful as root, which tests do not run as,
	// so assert the contract rather than the syscall: workspaceUID is what the
	// workload runs as, and it is what restoreWorkspace must hand the root to.
	if uid := workspaceUID(); uid != 1000 {
		t.Errorf("workspaceUID() = %d, want 1000 — the uid the workload container runs as", uid)
	}

	t.Setenv("WORKSPACE_UID", "1234")
	if uid := workspaceUID(); uid != 1234 {
		t.Errorf("workspaceUID() = %d, want 1234 — the operator must be able to override it", uid)
	}
}
