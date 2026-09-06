package controller

import (
	"reflect"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	computev1alpha1 "github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/api/v1alpha1"
)

// TestWorkspaceSpecCannotExpressStorage is the storage analogue of
// TestSpecCannotExpressPlacement, and it exists for the same reason: ADR-052
// §14 keeps StorageClass, size and PVC naming with the platform, and the only
// enforcement that survives a refactor is the fleet-facing type being incapable
// of saying them.
//
// If someone later adds StorageClass or Size here, a fleet can place its data
// on storage it was not granted, and — because a PVC bound to the wrong class
// is unreachable from the node the pod is placed on — do it in a way that
// presents as a scheduling fault rather than a policy breach.
func TestWorkspaceSpecCannotExpressStorage(t *testing.T) {
	forbidden := []string{"storageclass", "size", "capacity", "accessmode", "volumename", "provisioner"}

	st := reflect.TypeOf(computev1alpha1.WorkspacePersistenceSpec{})
	for i := 0; i < st.NumField(); i++ {
		name := strings.ToLower(st.Field(i).Name)
		tag := strings.ToLower(st.Field(i).Tag.Get("json"))
		for _, f := range forbidden {
			if strings.Contains(name, f) || strings.Contains(tag, f) {
				t.Errorf("WorkspacePersistenceSpec.%s lets a fleet name its own storage; that is platform-supplied (ADR-052 §14)",
					st.Field(i).Name)
			}
		}
	}
}

// TestPersistedWorkspaceIsAnEmptyDir is the behaviour the whole feature rests
// on (§14.2): workspace identity lives in S3, not in a PVC. Every pod starts
// with an emptyDir and the sidecar restores via FUSE mount.
func TestPersistedWorkspaceIsAnEmptyDir(t *testing.T) {
	r := &EphemeralJobReconciler{}
	p, _ := ResolvePlacement("home")

	ej := &computev1alpha1.EphemeralJob{
		Spec: computev1alpha1.EphemeralJobSpec{
			Image:                "example.com/img@sha256:" + strings.Repeat("a", 64),
			WorkspacePersistence: &computev1alpha1.WorkspacePersistenceSpec{WorkspaceID: "app-1"},
		},
	}

	spec := r.buildPodSpec(ej, p, corev1.Container{Name: "workload"})

	// 1. Workspace volume must be emptyDir, not PVC.
	var ws *corev1.Volume
	for i := range spec.Volumes {
		if spec.Volumes[i].Name == WorkspaceVolumeName {
			ws = &spec.Volumes[i]
		}
	}
	if ws == nil {
		t.Fatalf("no %q volume in the pod spec", WorkspaceVolumeName)
	}
	if ws.PersistentVolumeClaim != nil {
		t.Fatalf("%q volume is PVC-backed (got %+v) — §14.2 says workspace identity lives in S3, not a PVC",
			WorkspaceVolumeName, ws.VolumeSource)
	}
	if ws.EmptyDir == nil {
		t.Fatalf("%q volume is not emptyDir (got %+v)", WorkspaceVolumeName, ws.VolumeSource)
	}

	// 2. Exactly one workspace volume.
	n := 0
	for _, v := range spec.Volumes {
		if v.Name == WorkspaceVolumeName {
			n++
		}
	}
	if n != 1 {
		t.Errorf("found %d %q volumes, want exactly 1", n, WorkspaceVolumeName)
	}

	// 3. Workload mounts it.
	mounted := false
	for _, m := range spec.Containers[0].VolumeMounts {
		if m.Name == WorkspaceVolumeName && m.MountPath == WorkspaceMountPath {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("workload does not mount %q at %s", WorkspaceVolumeName, WorkspaceMountPath)
	}

	// 4. Staging emptyDir present for FUSE mounts (§14.2).
	var staging *corev1.Volume
	for i := range spec.Volumes {
		if spec.Volumes[i].Name == "ws-staging" {
			staging = &spec.Volumes[i]
		}
	}
	if staging == nil {
		t.Error("no ws-staging volume — squashfs archive needs a staging dir for FUSE mounts (§14.2)")
	} else if staging.EmptyDir == nil {
		t.Error("ws-staging volume is not emptyDir")
	}

	// 5. Sidecar has SYS_ADMIN capability for FUSE mounts.
	for i := range spec.InitContainers {
		c := &spec.InitContainers[i]
		if c.Name == "workspace-sync" && c.SecurityContext != nil {
			if c.SecurityContext.Capabilities == nil {
				t.Error("workspace-sync has no capabilities — FUSE mounts need SYS_ADMIN")
			} else {
				found := false
				for _, cap := range c.SecurityContext.Capabilities.Add {
					if cap == "SYS_ADMIN" {
						found = true
					}
				}
				if !found {
					t.Error("workspace-sync missing SYS_ADMIN capability — needed for squashfuse + fuse-overlayfs (§14.2)")
				}
			}
		}
	}
}

// TestWithoutPersistenceWorkspaceStaysEphemeral guards the other direction: a
// job that asked for nothing must be byte-for-byte unaffected by this feature
// existing. Renders and every batch workload rely on that.
func TestWithoutPersistenceWorkspaceStaysEphemeral(t *testing.T) {
	r := &EphemeralJobReconciler{}
	p, _ := ResolvePlacement("burst")

	ej := &computev1alpha1.EphemeralJob{
		Spec: computev1alpha1.EphemeralJobSpec{
			Image: "example.com/img@sha256:" + strings.Repeat("a", 64),
		},
	}

	spec := r.buildPodSpec(ej, p, corev1.Container{Name: "workload"})

	for _, v := range spec.Volumes {
		if v.Name == WorkspaceVolumeName {
			if v.PersistentVolumeClaim != nil {
				t.Errorf("workspace is PVC-backed without workspacePersistence being requested")
			}
			if v.EmptyDir == nil {
				t.Errorf("workspace lost its emptyDir source")
			}
		}
	}
	// No staging volume without persistence.
	for _, v := range spec.Volumes {
		if v.Name == "ws-staging" {
			t.Error("ws-staging volume present without workspacePersistence — should only be added when persistence is requested")
		}
	}
}

// TestNoServiceAccountTokenInTenantPods guards ADR-052 §19.6.
//
// AutomountServiceAccountToken was UNSET here, which defaults to true, so every
// sandbox pod carried a projected API credential that tenant-authored code
// could read. The threat model is untrusted code with a shell, and the failure
// is silent — nothing reports a token that is merely present.
func TestNoServiceAccountTokenInTenantPods(t *testing.T) {
	r := &EphemeralJobReconciler{}
	p, _ := ResolvePlacement("home")
	ej := &computev1alpha1.EphemeralJob{
		Spec: computev1alpha1.EphemeralJobSpec{
			Image: "example.com/img@sha256:" + strings.Repeat("a", 64),
		},
	}

	spec := r.buildPodSpec(ej, p, corev1.Container{Name: "workload"})

	if spec.AutomountServiceAccountToken == nil {
		t.Fatal("AutomountServiceAccountToken is unset, which defaults to TRUE — tenant code can read an API credential (ADR-052 §19.6)")
	}
	if *spec.AutomountServiceAccountToken {
		t.Error("AutomountServiceAccountToken is true; agent-vault must be the only credential channel (ADR-052 §19.6)")
	}
}

// TestTerminalEventIDIsStablePerOutcome is the property a callback receiver
// deduplicates on (ADR-052 §16.2).
//
// Delivery is at-least-once: the window between a receiver committing its side
// effect and this operator persisting ConditionCallbackDelivered cannot be
// closed from this side. So the key must be identical across redeliveries of
// ONE outcome and different across outcomes — otherwise a receiver either
// double-applies or silently drops a real second event.
func TestTerminalEventIDIsStablePerOutcome(t *testing.T) {
	ej := &computev1alpha1.EphemeralJob{
		Spec: computev1alpha1.EphemeralJobSpec{RequestID: "req-1"},
	}

	a := terminalEventID(ej, computev1alpha1.PhaseSucceeded)
	b := terminalEventID(ej, computev1alpha1.PhaseSucceeded)
	if a != b {
		t.Errorf("same outcome produced different ids: %q then %q — a redelivery would be treated as new work", a, b)
	}

	if f := terminalEventID(ej, computev1alpha1.PhaseFailed); f == a {
		t.Error("Succeeded and Failed share a terminal event id; a receiver would drop the second as a duplicate")
	}

	other := &computev1alpha1.EphemeralJob{Spec: computev1alpha1.EphemeralJobSpec{RequestID: "req-2"}}
	if terminalEventID(other, computev1alpha1.PhaseSucceeded) == a {
		t.Error("different requests share a terminal event id")
	}

	// No RequestID: must still produce a usable key rather than collapsing every
	// unnamed request onto one id.
	u1 := &computev1alpha1.EphemeralJob{}
	u1.UID = "uid-1"
	u2 := &computev1alpha1.EphemeralJob{}
	u2.UID = "uid-2"
	if terminalEventID(u1, computev1alpha1.PhaseSucceeded) == terminalEventID(u2, computev1alpha1.PhaseSucceeded) {
		t.Error("requests without a RequestID collide on UID fallback")
	}
}

// TestJobIgnoresDisruptionFailures covers the half of §16.3 with a real cost on
// burst capacity: preemption and node drain are infrastructure-transient, and
// counting them against the retry budget fails a workload for something it did
// not do. Expressed as podFailurePolicy so the API server makes the judgement
// rather than this operator re-deriving it from pod status.
func TestJobIgnoresDisruptionFailures(t *testing.T) {
	r := &EphemeralJobReconciler{}
	p, _ := ResolvePlacement("burst")
	ej := &computev1alpha1.EphemeralJob{
		Spec: computev1alpha1.EphemeralJobSpec{
			Image: "example.com/img@sha256:" + strings.Repeat("a", 64),
		},
	}

	job := r.buildJob(ej, "j", p)
	if job.Spec.PodFailurePolicy == nil {
		t.Fatal("no podFailurePolicy: disruption would consume the retry budget (ADR-052 §16.3)")
	}
	found := false
	for _, rule := range job.Spec.PodFailurePolicy.Rules {
		for _, c := range rule.OnPodConditions {
			if c.Type == corev1.DisruptionTarget && rule.Action == batchv1.PodFailurePolicyActionIgnore {
				found = true
			}
		}
	}
	if !found {
		t.Error("podFailurePolicy does not Ignore DisruptionTarget; preemption on burst capacity would fail the job")
	}
}

// TestWorkspaceSyncIsANativeSidecar pins the three lifecycle properties the
// teardown checkpoint depends on (ADR-052 §14). Each one was wrong before, and
// each failed silently rather than loudly.
//
// The predecessor of this design put workspace-sync in .spec.containers and had
// the operator POST /flush at the pod IP. That could never work: the sidecar
// binds loopback, and the flush fired on CR deletion — long after the pod was
// reaped. Nothing caught it because the path never ran.
func TestWorkspaceSyncIsANativeSidecar(t *testing.T) {
	r := &EphemeralJobReconciler{}
	p, _ := ResolvePlacement("home")

	ej := &computev1alpha1.EphemeralJob{
		Spec: computev1alpha1.EphemeralJobSpec{
			Image:                "example.com/img@sha256:" + strings.Repeat("a", 64),
			WorkspacePersistence: &computev1alpha1.WorkspacePersistenceSpec{WorkspaceID: "app-1"},
		},
	}
	spec := r.buildPodSpec(ej, p, corev1.Container{Name: "workload"})

	// 1. It must NOT be an ordinary container. A never-exiting process in
	//    .spec.containers means a mode: Job pod can never reach Succeeded,
	//    because a pod succeeds only when every container has exited.
	for _, c := range spec.Containers {
		if c.Name == "workspace-sync" {
			t.Fatal("workspace-sync is in .spec.containers; a mode: Job pod with a persisted " +
				"workspace could never complete, because this process never exits")
		}
	}

	var sync, restore *corev1.Container
	syncIdx, restoreIdx := -1, -1
	for i := range spec.InitContainers {
		switch spec.InitContainers[i].Name {
		case "workspace-sync":
			sync, syncIdx = &spec.InitContainers[i], i
		case "workspace-restore":
			restore, restoreIdx = &spec.InitContainers[i], i
		}
	}
	if sync == nil || restore == nil {
		t.Fatalf("expected both workspace containers in .spec.initContainers, got sync=%v restore=%v",
			sync != nil, restore != nil)
	}

	// 2. restartPolicy: Always is the whole difference between a native sidecar
	//    and a plain init container that would block the pod from ever starting.
	if sync.RestartPolicy == nil || *sync.RestartPolicy != corev1.ContainerRestartPolicyAlways {
		t.Errorf("workspace-sync restartPolicy = %v, want Always — without it this is a plain "+
			"init container and the pod hangs before the workload ever starts", sync.RestartPolicy)
	}
	// The restore step is the opposite: it must run to completion.
	if restore.RestartPolicy != nil {
		t.Errorf("workspace-restore restartPolicy = %v, want nil — it must terminate before the "+
			"sidecar starts", *restore.RestartPolicy)
	}

	// 3. Ordering. Init containers run in sequence, and a native sidecar starts
	//    only once the plain ones before it have finished — so restore must come
	//    first, or the sidecar would begin snapshotting a workspace still being
	//    populated.
	if restoreIdx > syncIdx {
		t.Errorf("workspace-restore at index %d comes after workspace-sync at %d; the sidecar "+
			"would start against an unrestored workspace", restoreIdx, syncIdx)
	}

	// Probes would be actively harmful here: the kubelet dials the pod IP and
	// the sidecar binds 127.0.0.1, so an httpGet liveness probe fails forever
	// and restarts a healthy sidecar; a readiness probe on a native sidecar
	// gates the workload's start, letting an unreachable bucket deny the user
	// their workspace.
	if sync.LivenessProbe != nil || sync.ReadinessProbe != nil || sync.StartupProbe != nil {
		t.Error("workspace-sync declares a probe; the kubelet probes the pod IP and this " +
			"process binds loopback, so it can only ever fail")
	}
}

// TestPersistedWorkspaceGetsAGracePeriodFloor guards the window the teardown
// checkpoint runs in. The kubelet SIGKILLs whatever is still running when the
// grace period expires, so the default 30s is a ceiling on the final snapshot
// that nobody chose.
func TestPersistedWorkspaceGetsAGracePeriodFloor(t *testing.T) {
	r := &EphemeralJobReconciler{}
	p, _ := ResolvePlacement("home")
	img := "example.com/img@sha256:" + strings.Repeat("a", 64)

	// Unset: the floor applies.
	spec := r.buildPodSpec(&computev1alpha1.EphemeralJob{
		Spec: computev1alpha1.EphemeralJobSpec{
			Image:                img,
			WorkspacePersistence: &computev1alpha1.WorkspacePersistenceSpec{WorkspaceID: "app-1"},
		},
	}, p, corev1.Container{Name: "workload"})
	if spec.TerminationGracePeriodSeconds == nil || *spec.TerminationGracePeriodSeconds != minWorkspaceGraceSeconds {
		t.Errorf("grace period = %v, want the %ds floor — the teardown checkpoint would be "+
			"SIGKILLed at the 30s default", spec.TerminationGracePeriodSeconds, minWorkspaceGraceSeconds)
	}

	// A fleet asking for MORE keeps it: this is a floor, not an override.
	longer := int64(minWorkspaceGraceSeconds + 300)
	spec = r.buildPodSpec(&computev1alpha1.EphemeralJob{
		Spec: computev1alpha1.EphemeralJobSpec{
			Image:                         img,
			WorkspacePersistence:          &computev1alpha1.WorkspacePersistenceSpec{WorkspaceID: "app-1"},
			TerminationGracePeriodSeconds: &longer,
		},
	}, p, corev1.Container{Name: "workload"})
	if spec.TerminationGracePeriodSeconds == nil || *spec.TerminationGracePeriodSeconds != longer {
		t.Errorf("grace period = %v, want the fleet's %d preserved", spec.TerminationGracePeriodSeconds, longer)
	}

	// And a job without persistence is untouched by any of this.
	shorter := int64(5)
	spec = r.buildPodSpec(&computev1alpha1.EphemeralJob{
		Spec: computev1alpha1.EphemeralJobSpec{
			Image:                         img,
			TerminationGracePeriodSeconds: &shorter,
		},
	}, p, corev1.Container{Name: "workload"})
	if spec.TerminationGracePeriodSeconds == nil || *spec.TerminationGracePeriodSeconds != shorter {
		t.Errorf("grace period = %v on a job with no persisted workspace, want the fleet's %d untouched",
			spec.TerminationGracePeriodSeconds, shorter)
	}
}
