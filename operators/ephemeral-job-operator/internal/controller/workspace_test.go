package controller

import (
	"reflect"
	"strings"
	"testing"

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

// TestWorkspacePVCNameIsStableAndValid covers the two properties the name has
// to have at once, which pull in opposite directions.
//
// Stable: the same workspace id must always resolve to the same claim, or a
// second session would silently get a fresh empty disk instead of reattaching —
// the failure would look like data loss with nothing in error.
//
// Valid: workspace ids are caller-chosen and need not be RFC 1123 subdomains,
// so the name is a hash. Passing the id through would turn "this id has an
// underscore" into a 422 on pod creation that names the volume, not the id.
func TestWorkspacePVCNameIsStableAndValid(t *testing.T) {
	ids := []string{
		"app_123",                      // underscore: illegal in a k8s name
		"App/With/Slashes",             // slashes and capitals
		"a" + strings.Repeat("b", 400), // longer than the 253 limit
		"01JQ8XN0X4V4Z0RTP0J0X2M9AB",   // a ULID, the common real case
		"",                             // degenerate, must still produce a legal name
	}

	valid := func(s string) bool {
		if s == "" || len(s) > 253 {
			return false
		}
		for _, r := range s {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '.' {
				return false
			}
		}
		return !strings.HasPrefix(s, "-") && !strings.HasSuffix(s, "-")
	}

	seen := map[string]string{}
	for _, id := range ids {
		got := workspacePVCName(id)

		if !valid(got) {
			t.Errorf("workspacePVCName(%q) = %q, which is not a legal RFC 1123 name", id, got)
		}
		if again := workspacePVCName(id); again != got {
			t.Errorf("workspacePVCName(%q) is not deterministic: %q then %q", id, got, again)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("workspacePVCName collided: %q and %q both produced %q", prev, id, got)
		}
		seen[got] = id
	}
}

// TestPersistedWorkspaceReplacesTheEmptyDir is the behaviour the whole feature
// rests on, and the one with a silent failure mode.
//
// The workload mounts `workspace` at /workspace either way. If the PVC were
// APPENDED as a second volume rather than replacing the emptyDir's source, the
// container would go on mounting the emptyDir while the PVC sat bound and empty
// — every check would pass, the claim would look healthy, and none of the data
// would be on it.
func TestPersistedWorkspaceReplacesTheEmptyDir(t *testing.T) {
	r := &EphemeralJobReconciler{}
	p, _ := ResolvePlacement("home")

	ej := &computev1alpha1.EphemeralJob{
		Spec: computev1alpha1.EphemeralJobSpec{
			Image:                "example.com/img@sha256:" + strings.Repeat("a", 64),
			WorkspacePersistence: &computev1alpha1.WorkspacePersistenceSpec{WorkspaceID: "app-1"},
		},
	}

	spec := r.buildPodSpec(ej, p, corev1.Container{Name: "workload"})

	var ws *corev1.Volume
	for i := range spec.Volumes {
		if spec.Volumes[i].Name == WorkspaceVolumeName {
			ws = &spec.Volumes[i]
		}
	}
	if ws == nil {
		t.Fatalf("no %q volume in the pod spec", WorkspaceVolumeName)
	}
	if ws.PersistentVolumeClaim == nil {
		t.Fatalf("%q volume is not PVC-backed (got %+v) — the workload would write to an emptyDir while the claim sat empty",
			WorkspaceVolumeName, ws.VolumeSource)
	}
	if got, want := ws.PersistentVolumeClaim.ClaimName, workspacePVCName("app-1"); got != want {
		t.Errorf("claim name = %q, want %q", got, want)
	}
	if ws.EmptyDir != nil {
		t.Errorf("%q volume still carries an emptyDir source alongside the claim", WorkspaceVolumeName)
	}

	// Exactly one workspace volume: two would be rejected by the API server, and
	// the message names the pod rather than the cause.
	n := 0
	for _, v := range spec.Volumes {
		if v.Name == WorkspaceVolumeName {
			n++
		}
	}
	if n != 1 {
		t.Errorf("found %d %q volumes, want exactly 1", n, WorkspaceVolumeName)
	}

	// And the workload actually mounts it.
	mounted := false
	for _, m := range spec.Containers[0].VolumeMounts {
		if m.Name == WorkspaceVolumeName && m.MountPath == WorkspaceMountPath {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("workload does not mount %q at %s", WorkspaceVolumeName, WorkspaceMountPath)
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
}

// TestEveryPlacementClassHasAStorageClass stops a class being added without
// one. The failure it prevents is not a compile error but a runtime refusal:
// ensureWorkspacePVC would reject the request for a class that resolves to an
// empty StorageClass, which reads as "persistence is broken" rather than "this
// class was never given storage".
func TestEveryPlacementClassHasAStorageClass(t *testing.T) {
	for class, p := range placements {
		if p.StorageClass == "" {
			t.Errorf("placement class %q defines no StorageClass; workspacePersistence cannot be satisfied for it (ADR-052 §14)", class)
		}
	}
}
