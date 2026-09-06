package controller

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	computev1alpha1 "github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/api/v1alpha1"
)

// The platform's workspace persistence agent (ADR-052 §14). Image and secret
// are operator configuration, never fleet input — a fleet that could name
// either could substitute its own agent or point it at its own bucket.
var (
	workspaceSyncImage = envOr("WORKSPACE_SYNC_IMAGE", "workspace-sync:dev")
	// The object-store credential, delivered by reference rather than as
	// literal values (§19.6). Marked optional so a cluster without it still
	// runs: workspace-sync then reports "not configured" and checkpoints
	// no-op, which §14 names as the one legitimate no-op. Hard-failing pod
	// startup instead would make an unconfigured dev cluster look broken.
	workspaceSyncSecret = envOr("WORKSPACE_SYNC_SECRET", "workspace-sync-s3")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// workspaceSyncContainers builds the init container and sidecar for a
// persisted workspace.
//
// Both run as uid 1000, matching the workload rather than the image's own
// nonroot uid. The two processes write the same volume, and a uid mismatch
// produces files the other cannot modify — which on a git tree surfaces as a
// permission error deep inside an unrelated operation rather than as anything
// about ownership.
func workspaceSyncContainers(workspaceID string) (initC, sideC corev1.Container) {
	uid := int64(1000)
	sec := &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr(false),
		RunAsNonRoot:             ptr(true),
		RunAsUser:                &uid,
		ReadOnlyRootFilesystem:   ptr(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
	env := []corev1.EnvVar{{Name: "WORKSPACE_ID", Value: workspaceID}}
	envFrom := []corev1.EnvFromSource{{
		SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: workspaceSyncSecret},
			Optional:             ptr(true),
		},
	}}
	mounts := []corev1.VolumeMount{{Name: WorkspaceVolumeName, MountPath: WorkspaceMountPath}}

	initC = corev1.Container{
		Name:            "workspace-restore",
		Image:           workspaceSyncImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            []string{"restore"},
		Env:             env,
		EnvFrom:         envFrom,
		VolumeMounts:    mounts,
		SecurityContext: sec,
	}
	sideC = corev1.Container{
		Name:            "workspace-sync",
		Image:           workspaceSyncImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            []string{"serve"},
		Env:             env,
		EnvFrom:         envFrom,
		VolumeMounts:    mounts,
		SecurityContext: sec,
	}
	return initC, sideC
}

// workspaceFinalizer holds an EphemeralJob's deletion open until its workspace
// has been flushed (ADR-052 §14).
const workspaceFinalizer = "compute.nutgraf.in/workspace-flush"

func hasFinalizer(ej *computev1alpha1.EphemeralJob) bool {
	for _, f := range ej.Finalizers {
		if f == workspaceFinalizer {
			return true
		}
	}
	return false
}

func removeFinalizer(ej *computev1alpha1.EphemeralJob) {
	out := ej.Finalizers[:0]
	for _, f := range ej.Finalizers {
		if f != workspaceFinalizer {
			out = append(out, f)
		}
	}
	ej.Finalizers = out
}

// finalizeWorkspace flushes the workspace, then releases the deletion.
//
// Best-effort by design, and the reason is a hard trade: a flush that cannot
// succeed must not strand the object forever. A permanently-held finalizer is
// worse than a lost final checkpoint — it blocks the namespace from draining
// and leaves the operator reconciling a corpse. The periodic backstop already
// bounds what a failed flush costs (§14), so this logs loudly and lets go.
func (r *EphemeralJobReconciler) finalizeWorkspace(
	ctx context.Context, ej *computev1alpha1.EphemeralJob,
) (ctrl.Result, error) {
	l := log.FromContext(ctx)
	if !hasFinalizer(ej) {
		return ctrl.Result{}, nil
	}

	if ej.Status.PodName != "" {
		if err := r.flushWorkspace(ctx, ej); err != nil {
			l.Error(err, "workspace flush failed; releasing anyway",
				"name", ej.Name, "pod", ej.Status.PodName)
		} else {
			l.Info("workspace flushed before teardown", "name", ej.Name)
		}
	}

	removeFinalizer(ej)
	return ctrl.Result{}, client.IgnoreNotFound(r.Update(ctx, ej))
}

// flushWorkspace asks the pod's sidecar to snapshot now.
//
// Reached at the pod IP rather than through a Service: this is a specific
// pod's disk, and a Service could round-robin to a different one. The sidecar
// binds 127.0.0.1, so this only works from inside the pod network — which is
// also why it needs no authentication of its own.
func (r *EphemeralJobReconciler) flushWorkspace(
	ctx context.Context, ej *computev1alpha1.EphemeralJob,
) error {
	var pod corev1.Pod
	if err := r.Get(ctx, client.ObjectKey{Name: ej.Status.PodName, Namespace: ej.Namespace}, &pod); err != nil {
		return err
	}
	if pod.Status.PodIP == "" {
		return fmt.Errorf("pod %s has no IP; nothing to flush", pod.Name)
	}
	url := fmt.Sprintf("http://%s:%s/flush", pod.Status.PodIP, envOr("WORKSPACE_SYNC_PORT", "7070"))

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	resp, err := callbackHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("flush returned %s", resp.Status)
	}
	return nil
}
