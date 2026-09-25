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

// A restore running as the workload's own uid must not try to chown.
//
// It cannot succeed: a process may chown a file to its own uid only where it
// already owns it, and the one path it never owns is the mount point the
// kubelet created. The shared instance crashlooped on exactly that —
//
//	serve failed: restore on startup: chown restored workspace to uid 1000:
//	lchown /workspace/.global: operation not permitted
//
// — a directory it did not need to own, since mode 0777 on the emptyDir already
// let it write there.
func TestChownIsSkippedWhenAlreadyTheTargetUID(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Our own uid: the walk must not run at all, so an unreadable directory
	// inside the tree cannot fail it either.
	if err := chownTree(root, os.Getuid()); err != nil {
		t.Errorf("chownTree to our own uid returned %v; it must be a no-op", err)
	}
	if err := chownRootIfNeeded(root, os.Getuid()); err != nil {
		t.Errorf("chownRootIfNeeded to our own uid returned %v; it must be a no-op", err)
	}
}

// The behaviour is not simply deleted: running as a DIFFERENT uid still walks,
// which is what the root path needs when this ever runs as root again.
func TestChownStillWalksForADifferentUID(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: a chown to another uid would succeed and prove nothing")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// uid 0 is not us and we are not root, so the walk must be attempted and
	// must fail — proving it was attempted.
	if err := chownTree(root, 0); err == nil {
		t.Error("chownTree to uid 0 as a non-root user returned nil; the walk was skipped")
	}
}
