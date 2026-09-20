package controller

import (
	"testing"

	computev1alpha1 "github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func envMap(c corev1.Container) map[string]string {
	m := map[string]string{}
	for _, v := range c.Env {
		m[v.Name] = v.Value
	}
	return m
}

// TestSharedWorkspaceIsTheSameMechanismPointedElsewhere pins the properties that
// make a second sync instance safe to run in the same pod.
//
// Each assertion here stands for a failure that is silent rather than loud:
// a shared readiness marker makes one instance's probe pass on the other's
// restore; the wrong mount propagation writes beneath an overlay where nothing
// reading /workspace can see it; and a privileged container copied for a
// capability it never uses widens the blast radius §19.6 exists to bound.
func TestSharedWorkspaceIsTheSameMechanismPointedElsewhere(t *testing.T) {
	ws := &computev1alpha1.WorkspacePersistenceSpec{
		WorkspaceID:       "playground-abc",
		AppID:             "app123",
		SharedWorkspaceID: "globals",
	}

	session := workspaceSyncContainer(ws, 5, "default")
	shared := workspaceSyncContainerFor(ws, 5, "default", workspaceSyncTarget{
		name:        "workspace-sync-shared",
		workspaceID: ws.SharedWorkspaceID,
		root:        WorkspaceMountPath + "/" + sharedWorkspaceDirName,
		staging:     workspaceStagingPath + "/" + sharedWorkspaceDirName,
		noArchive:   true,
	})

	se, sh := envMap(session), envMap(shared)

	// Same app root: both trees belong to one app, which is what allows an app
	// to be deleted with a single prefix operation (§14.4).
	if se["APP_ID"] != sh["APP_ID"] || sh["APP_ID"] != "app123" {
		t.Errorf("app root must match: session=%q shared=%q", se["APP_ID"], sh["APP_ID"])
	}

	// Different workspace ids, which is the whole point: a distinct prefix,
	// manifest chain and LATEST pointer, shared by every session of the app.
	if se["WORKSPACE_ID"] == sh["WORKSPACE_ID"] {
		t.Fatalf("both instances claim workspace %q; the shared tree would be a per-session copy", se["WORKSPACE_ID"])
	}
	if sh["WORKSPACE_ID"] != "globals" {
		t.Errorf("shared workspace id = %q, want the value the fleet asked for", sh["WORKSPACE_ID"])
	}

	// Roots: the shared tree lives inside the session tree, at the directory
	// workspace-sync's skipDir excludes from the session's own snapshot.
	if se["WORKSPACE_ROOT"] != WorkspaceMountPath {
		t.Errorf("session root = %q", se["WORKSPACE_ROOT"])
	}
	if want := WorkspaceMountPath + "/" + sharedWorkspaceDirName; sh["WORKSPACE_ROOT"] != want {
		t.Errorf("shared root = %q, want %q", sh["WORKSPACE_ROOT"], want)
	}

	// Loopback port must not be shared. Containers in a pod share a network
	// namespace, so the second binder exits with "address already in use" and
	// crashloops the pod before the workload starts — observed on a live pod.
	if se["WORKSPACE_SYNC_PORT"] == sh["WORKSPACE_SYNC_PORT"] {
		t.Fatalf("both instances bind port %q; the second cannot listen and the pod never starts",
			se["WORKSPACE_SYNC_PORT"])
	}
	// The session instance keeps the default, because that is the port
	// harness-runtime proxies /workspace/checkpoint to.
	if se["WORKSPACE_SYNC_PORT"] != "7070" {
		t.Errorf("session instance port = %q, want 7070 — harness-runtime's checkpoint proxy targets it",
			se["WORKSPACE_SYNC_PORT"])
	}

	// Staging must not be shared: the readiness marker is named from it.
	if se["STAGING_ROOT"] == sh["STAGING_ROOT"] {
		t.Fatalf("both instances stage in %q; one's .ready marker would satisfy the other's startup probe "+
			"and the workload could start against a tree that was never restored", se["STAGING_ROOT"])
	}
	sessionProbe := session.StartupProbe.ProbeHandler.Exec.Command
	sharedProbe := shared.StartupProbe.ProbeHandler.Exec.Command
	if sessionProbe[len(sessionProbe)-1] == sharedProbe[len(sharedProbe)-1] {
		t.Errorf("startup probes watch the same marker: %v", sessionProbe)
	}

	// The squashfs/FUSE fast path belongs to the session instance only.
	if se["WORKSPACE_NO_ARCHIVE"] != "" {
		t.Error("session instance must keep the squashfs fast path; it is what makes restore O(1)")
	}
	if sh["WORKSPACE_NO_ARCHIVE"] != "true" {
		t.Error("shared instance must skip squashfs: its root is inside a mount it does not own")
	}

	// Privilege is bought for FUSE, so only the instance that uses FUSE pays.
	if session.SecurityContext.Privileged == nil || !*session.SecurityContext.Privileged {
		t.Error("session instance mounts FUSE and must be privileged")
	}
	if shared.SecurityContext.Privileged == nil || *shared.SecurityContext.Privileged {
		t.Error("shared instance mounts nothing and must not be privileged (§19.6)")
	}

	// Root, though. Dropping privilege and dropping root are different things,
	// and conflating them crashlooped the pod: the restore chowns the tree to
	// the workload's uid, and a non-root process cannot chown a file it does
	// not own ("lchown /workspace/.global: operation not permitted").
	if shared.SecurityContext.RunAsUser == nil || *shared.SecurityContext.RunAsUser != 0 {
		t.Errorf("shared instance RunAsUser = %v, want 0 — it must chown the restored tree to the workload",
			shared.SecurityContext.RunAsUser)
	}
	// The escalation paths privilege would have brought stay closed.
	if shared.SecurityContext.AllowPrivilegeEscalation == nil || *shared.SecurityContext.AllowPrivilegeEscalation {
		t.Error("shared instance must not allow privilege escalation")
	}

	// Propagation: the session pushes its mount out to the host; the shared
	// instance and the workload both observe it from there.
	if session.VolumeMounts[0].MountPropagation == nil ||
		*session.VolumeMounts[0].MountPropagation != corev1.MountPropagationBidirectional {
		t.Error("session instance needs Bidirectional or its restore is invisible to the workload")
	}
	if shared.VolumeMounts[0].MountPropagation == nil ||
		*shared.VolumeMounts[0].MountPropagation != corev1.MountPropagationHostToContainer {
		t.Error("shared instance needs HostToContainer, or it writes beneath the session's overlay")
	}

	// Both carry the same credential channel and retention, by construction.
	for _, k := range []string{"KEEP_CHECKPOINTS", "WORKSPACE_UID"} {
		if se[k] != sh[k] {
			t.Errorf("%s differs: session=%q shared=%q", k, se[k], sh[k])
		}
	}
}

// TestSharedWorkspaceIsOptional: a fleet that does not ask for one gets exactly
// today's pod — one sync container, nothing mounted inside the workspace.
func TestSharedWorkspaceIsOptional(t *testing.T) {
	ws := &computev1alpha1.WorkspacePersistenceSpec{WorkspaceID: "ws1", AppID: "app1"}
	if ws.SharedWorkspaceID != "" {
		t.Fatal("unset SharedWorkspaceID must be empty")
	}
	c := workspaceSyncContainer(ws, 5, "default")
	if got := envMap(c)["WORKSPACE_ROOT"]; got != WorkspaceMountPath {
		t.Errorf("WORKSPACE_ROOT = %q, want %q", got, WorkspaceMountPath)
	}
	if c.Name != "workspace-sync" {
		t.Errorf("container name changed to %q; the session container's name is a stable identifier", c.Name)
	}
}

// TestCheckpointIntervalIsOptInAndReachesBothInstances covers the gap between an
// orderly shutdown and every other way a pod dies.
//
// The teardown checkpoint handles SIGTERM — idle reap, CR delete, drain — and is
// the only application-consistent one in the design. It cannot help with
// SIGKILL, an OOM kill or node loss, where nothing runs at all. For an
// unattended fleet that is the difference between losing one interval and losing
// an entire pipeline run, because there is no person present to save.
func TestCheckpointIntervalIsOptInAndReachesBothInstances(t *testing.T) {
	// Unset: no periodic upload. This is what an interactive fleet wants — a
	// timer checkpointing a tree mid-edit mostly records half-finished states.
	off := &computev1alpha1.WorkspacePersistenceSpec{WorkspaceID: "ws1", AppID: "app1"}
	if v := envMap(workspaceSyncContainer(off, 5, "default"))["WORKSPACE_SYNC_INTERVAL_SECONDS"]; v != "" {
		t.Errorf("interval = %q with none requested; the periodic backstop must be opt-in", v)
	}

	every := int32(120)
	on := &computev1alpha1.WorkspacePersistenceSpec{
		WorkspaceID: "ws1", AppID: "app1", SharedWorkspaceID: "globals",
		CheckpointIntervalSeconds: &every,
	}
	session := envMap(workspaceSyncContainer(on, 5, "default"))
	shared := envMap(workspaceSyncContainerFor(on, 5, "default", workspaceSyncTarget{
		name: "workspace-sync-shared", workspaceID: "globals",
		root:    WorkspaceMountPath + "/" + sharedWorkspaceDirName,
		staging: workspaceStagingPath + "/" + sharedWorkspaceDirName,
		port:    sharedWorkspaceSyncPort, noArchive: true,
	}))

	if session["WORKSPACE_SYNC_INTERVAL_SECONDS"] != "120" {
		t.Errorf("session interval = %q, want 120", session["WORKSPACE_SYNC_INTERVAL_SECONDS"])
	}
	// The app-scoped tree needs it just as much: a brand brief written once at
	// the start of a run is exactly what a crash an hour later would take.
	if shared["WORKSPACE_SYNC_INTERVAL_SECONDS"] != "120" {
		t.Errorf("shared interval = %q, want 120 — app-scoped artifacts are lost the same way",
			shared["WORKSPACE_SYNC_INTERVAL_SECONDS"])
	}
}
