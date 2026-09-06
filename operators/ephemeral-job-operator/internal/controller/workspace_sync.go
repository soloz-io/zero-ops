package controller

import (
	"os"

	corev1 "k8s.io/api/core/v1"
)

// The platform's workspace persistence agent (ADR-052 §14). Image and secret
// are operator configuration, never fleet input — a fleet that could name
// either could substitute its own agent or point it at its own bucket.
var (
	workspaceSyncImage = envOr("WORKSPACE_SYNC_IMAGE", "workspace-sync:dev")
	// The object-store credential, by reference (§19.6).
	//
	// `hetzner-credentials` is not a new object invented for this: it is the
	// Secret the SDK already consumes for its own S3 access, rendered into the
	// tenant namespace by the platform through ExternalSecrets/Infisical
	// (ADR-003, ADR-047 Tier 2). Nothing hand-creates it, in any environment.
	//
	// Reusing it rather than defining a parallel Secret means a credential
	// rotation has one place to land, not two — and that the sidecar and the
	// SDK can never disagree about which bucket a workspace lives in.
	//
	// Optional, so a cluster without it still runs: workspace-sync reports
	// "not configured" and checkpoints no-op, which §14 names as the one
	// legitimate no-op. A local Kind cluster has no Infisical and therefore no
	// such Secret — the SDK's own reference to it is optional for the same
	// reason — so hard-failing here would make every sandbox unstartable on a
	// dev cluster to enforce a backup nobody asked for.
	workspaceSyncSecret = envOr("WORKSPACE_SYNC_SECRET", "hetzner-credentials")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// workspaceSyncContainers builds the restore init container and the sync
// sidecar for a persisted workspace. BOTH belong in `.spec.initContainers`.
//
// The sidecar is a NATIVE sidecar — an init container carrying
// `restartPolicy: Always` — and that is load-bearing in three separate ways
// (ADR-052 §14):
//
//  1. Job completion. As an ordinary entry in `.spec.containers`, this
//     process would never exit, and a pod reaches Succeeded only when every
//     one of its containers has. Every `mode: Job` workload asking for a
//     persisted workspace would hang until a deadline killed it. Nothing
//     caught this because every sandbox so far is `mode: Service`.
//
//  2. Termination ordering. The kubelet stops native sidecars only AFTER the
//     regular containers have exited, which is exactly the ordering a final
//     checkpoint needs: the workload stops mutating the tree, and only then
//     is the tree read. An ordinary sidecar gets SIGTERM alongside the
//     workload and would snapshot a workspace still being written.
//
//  3. Startup ordering. Init containers run in order, and a native sidecar
//     starts only after the plain init containers before it have completed.
//     So `workspace-restore` finishes populating the volume before this
//     process starts, and this process is serving before the workload runs.
//
// The ordering in 3 is why the caller must append these two in this order and
// keep the sidecar last.
func workspaceSyncContainers(workspaceID string) (initC, sideC corev1.Container) {
	uid := int64(1000)
	// Both run as uid 1000, matching the workload rather than the image's own
	// nonroot uid. The two processes write the same volume, and a uid mismatch
	// produces files the other cannot modify — which on a git tree surfaces as
	// a permission error deep inside an unrelated operation rather than as
	// anything about ownership.
	sec := &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr(false),
		RunAsNonRoot:             ptr(true),
		RunAsUser:                &uid,
		ReadOnlyRootFilesystem:   ptr(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
	// Mapped key by key, not `envFrom`.
	//
	// The Secret's keys are kebab-case (`s3-endpoint-url`), because that is how
	// the platform's ExternalSecret renders them and how the SDK already
	// consumes them. `envFrom` would inject those names verbatim — which are
	// not valid environment variable names and are not what this process reads
	// — and would do it silently, leaving the sidecar reporting "not
	// configured" beside a Secret that was mounted correctly.
	//
	// Every reference is optional for the same reason the SDK's are: a cluster
	// without Infisical (any local Kind cluster) has no such Secret, and a
	// sandbox must still start there.
	secretEnv := func(name, key string) corev1.EnvVar {
		return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: workspaceSyncSecret},
				Key:                  key,
				Optional:             ptr(true),
			},
		}}
	}
	env := []corev1.EnvVar{
		{Name: "WORKSPACE_ID", Value: workspaceID},
		secretEnv("S3_ENDPOINT_URL", "s3-endpoint-url"),
		secretEnv("S3_BUCKET_NAME", "s3-bucket-name"),
		secretEnv("S3_ACCESS_KEY_ID", "s3-access-key"),
		secretEnv("S3_SECRET_ACCESS_KEY", "s3-secret-key"),
		secretEnv("S3_REGION", "s3-region"),
	}
	mounts := []corev1.VolumeMount{{Name: WorkspaceVolumeName, MountPath: WorkspaceMountPath}}

	initC = corev1.Container{
		Name:            "workspace-restore",
		Image:           workspaceSyncImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            []string{"restore"},
		Env:             env,
		VolumeMounts:    mounts,
		SecurityContext: sec,
	}

	always := corev1.ContainerRestartPolicyAlways
	sideC = corev1.Container{
		Name:            "workspace-sync",
		Image:           workspaceSyncImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            []string{"serve"},
		Env:             env,
		VolumeMounts:    mounts,
		SecurityContext: sec,
		// What makes it a native sidecar rather than a plain init container
		// that would block the pod from ever starting.
		RestartPolicy: &always,

		// No probes, deliberately — and this is a consequence of the loopback
		// bind, not an oversight.
		//
		// The kubelet runs an httpGet probe from the NODE against the pod IP,
		// so it cannot reach a listener bound to 127.0.0.1 inside the pod: an
		// HTTP liveness probe here would fail permanently and restart a
		// perfectly healthy sidecar forever. An exec probe is the usual way
		// out, but this image is distroless — no shell, no curl — so there is
		// nothing to exec.
		//
		// Readiness would be actively harmful besides. On a native sidecar it
		// gates the workload's start, and this process must never be able to
		// keep a sandbox from running: an unreachable object store is
		// degraded durability, not a reason to deny the user their workspace.
		//
		// What is lost is small and already covered. A wedged sidecar shows up
		// as checkpoints that stop appearing, which the restore path reports
		// on the next session ("no checkpoint for this workspace yet"), and
		// the PVC — not this process — is what holds the live tree.
		// `/healthz` stays in the server for in-pod debugging.
	}
	return initC, sideC
}
