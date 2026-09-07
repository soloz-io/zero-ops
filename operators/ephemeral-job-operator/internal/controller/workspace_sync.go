package controller

import (
	"os"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	computev1alpha1 "github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/api/v1alpha1"
)

// The bounds the CRD advertises, restated here because this operator clamps to
// them rather than trusting that admission enforced them.
const (
	minKeepCheckpoints int32 = 1
	maxKeepCheckpoints int32 = 50
)

// The platform's workspace persistence agent (ADR-052 §14, §14.2). Image and
// secret are operator configuration, never fleet input — a fleet that could name
// either could substitute its own agent or point it at its own bucket.
var (
	workspaceSyncImage = envOr("WORKSPACE_SYNC_IMAGE", "workspace-sync:dev")
	// The object-store credential, by reference (§19.6).
	//
	// A TEMPLATE, not a name. `{namespace}` expands to the EphemeralJob's own
	// namespace at reconcile time.
	//
	// This operator serves every fleet on the spoke, and each one keeps its S3
	// credential in its own Secret in its own namespace. A literal name here
	// would be a tenant identifier in platform code — which ADR-047's addendum
	// forbids, and which the preflight check rejects — and it would also be
	// simply wrong for the second fleet onboarded.
	//
	// The tenant namespace IS the fleet id (the registry renders workloads into
	// `namespace: {TENANT_ID}`), so `{namespace}-app-secrets` resolves to
	// exactly the Secret each fleet's registry already declares — for waypoint,
	// the one its own SDK binds S3_* from
	// (workloads/base/sdk/rollout-patch.yaml, rendered by the ExternalSecret in
	// values.yaml). Reusing the fleet's own Secret rather than rendering a
	// parallel platform-owned one means a credential rotation lands in one
	// place, and the sidecar and the SDK cannot disagree about which bucket a
	// workspace lives in.
	//
	// Two earlier values were wrong, and both failed SILENTLY, because every
	// reference the operator injects is optional (a cluster without Infisical
	// must still start a sandbox): the sidecar simply reported "object storage
	// not configured" and no-opped every checkpoint.
	//   - `hcloud-token` — a KEY, not a Secret name.
	//   - `hetzner-credentials` — a real Secret, but platform INFRASTRUCTURE
	//     (hub-operator's constant: "For CAPI/CCM/CSI"). It carries S3 keys only
	//     in waypoint's local Kind manifest; no registry binds it for an SDK, so
	//     it worked locally and in no deployed environment.
	workspaceSyncSecretTemplate = envOr("WORKSPACE_SYNC_SECRET", "{namespace}-app-secrets")
)

// resolveWorkspaceSyncSecret expands the template for one job's namespace.
//
// A deployment that sets WORKSPACE_SYNC_SECRET to a literal name still works —
// the expansion is a no-op on a string with no placeholder — which is what a
// spoke with a single fleet and a non-conventional Secret name needs.
func resolveWorkspaceSyncSecret(namespace string) string {
	return strings.ReplaceAll(workspaceSyncSecretTemplate, "{namespace}", namespace)
}

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
//
// §14.2 changes: both containers mount a staging emptyDir (`ws-staging`) for
// squashfs archive and FUSE working directories. The sidecar additionally
// requires FUSE device access (device 229) for squashfuse and fuse-overlayfs.
func workspaceSyncContainer(ws *computev1alpha1.WorkspacePersistenceSpec, keepCheckpoints int32, namespace string) corev1.Container {
	var sideC corev1.Container
	workspaceID := ws.WorkspaceID
	uid := int64(1000)
	// Both run as uid 1000, matching the workload rather than the image's own
	// nonroot uid. The two processes write the same volume, and a uid mismatch
	// produces files the other cannot modify — which on a git tree surfaces as
	// a permission error deep inside an unrelated operation rather than as
	// anything about ownership.
	//
	// The sidecar's SecurityContext is extended with FUSE device access (§14.2)
	// because squashfuse and fuse-overlayfs require /dev/fuse.
	// The FUSE sidecar is PRIVILEGED, and that is the honest cost of §14.2.
	//
	// An earlier version of this asked for `SYS_ADMIN` with
	// allowPrivilegeEscalation=false and runAsNonRoot=true, described in the
	// ADR as "narrower than SYS_ADMIN". That combination cannot mount anything,
	// for four independent reasons:
	//
	//  1. Bidirectional mount propagation — without which the workload cannot
	//     see the overlay at all — is rejected by the kubelet on a
	//     non-privileged container.
	//  2. /dev/fuse does not exist in the container. Kubernetes SecurityContext
	//     has no `devices` field; exposing a device needs a device plugin or
	//     privileged mode. squashfuse fails with "device not found".
	//  3. allowPrivilegeEscalation=false sets NoNewPrivs, which blocks setuid
	//     execution — and squashfuse and fuse-overlayfs both mount through
	//     fusermount3, which is setuid root.
	//  4. A capability added to a container that then setuids to a non-root
	//     user is dropped from the effective set unless the binary carries file
	//     capabilities. These do not.
	//
	// So the choice is privileged or no FUSE, and the ADR should say so rather
	// than describe a middle ground that does not exist. What keeps this within
	// §19.6 is that the container is platform-authored and platform-owned: a
	// fleet cannot supply it, name its image, or exec into it, and the tenant's
	// own workload container is unchanged — unprivileged, no added
	// capabilities, no device access, and still no ServiceAccount token.
	privileged := true
	sidecarSec := &corev1.SecurityContext{
		Privileged:             &privileged,
		RunAsUser:              ptr(int64(0)),
		RunAsNonRoot:           ptr(false),
		ReadOnlyRootFilesystem: ptr(true),
	}
	_ = uid
	// Mapped key by key, not `envFrom`.
	//
	// The key names happen to equal the env names here, so `envFrom` would
	// technically work — but it would also inject every OTHER key the fleet
	// keeps in this Secret (LLM keys, database URLs, provider tokens) into a
	// container that has no business holding them. §19.6 is about what a
	// process can reach, and an explicit map is the only form that bounds it.
	//
	// Every reference is optional for the same reason the SDK's are: a cluster
	// without Infisical (any local Kind cluster) may have no such Secret, and a
	// sandbox must still start there — checkpoints then no-op, which §14 names
	// as the one legitimate no-op.
	secretName := resolveWorkspaceSyncSecret(namespace)
	secretEnv := func(name, key string) corev1.EnvVar {
		return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  key,
				Optional:             ptr(true),
			},
		}}
	}
	env := []corev1.EnvVar{
		{Name: "WORKSPACE_ID", Value: workspaceID},
		// The key root (§14.4). Every object this sidecar reads or writes lives
		// under <appId>/<workspaceId>/code/, so getting this wrong does not
		// error — it silently addresses a workspace nobody else can see.
		{Name: "APP_ID", Value: ws.AppID},
		// Key names as the registry's ExternalSecret renders them
		// (fleet-registry/tenants/waypoint/dev/values.yaml): SCREAMING_SNAKE,
		// matching the env names the SDK binds them to.
		secretEnv("S3_ENDPOINT_URL", "S3_ENDPOINT_URL"),
		secretEnv("S3_BUCKET_NAME", "S3_BUCKET_NAME"),
		secretEnv("S3_ACCESS_KEY_ID", "S3_ACCESS_KEY_ID"),
		secretEnv("S3_SECRET_ACCESS_KEY", "S3_SECRET_ACCESS_KEY"),
		// Optional in a stronger sense than the rest: the registry declares no
		// S3_REGION at all, so this is absent in every deployed environment and
		// the store falls back to us-east-1. That is correct for an
		// S3-compatible endpoint that ignores region, and wrong for one that
		// signs against it — Hetzner does. Local setup derives it from the
		// endpoint host; deployed environments need the key added to the
		// ExternalSecret before a region-checking provider will authenticate.
		secretEnv("S3_REGION", "S3_REGION"),
	}
	// Bidirectional on the workspace mount is what makes the restore visible.
	//
	// Containers in a pod share a network namespace but NOT a mount namespace.
	// A fuse-overlayfs mount this container makes at /workspace is, by default,
	// invisible to the workload container — which would go on seeing the bare
	// emptyDir. The restore would log success and the agent would find nothing.
	//
	// Bidirectional propagates the mount back to the host and onward into the
	// workload's HostToContainer mount (set in buildPodSpec). Kubernetes allows
	// Bidirectional ONLY on a privileged container, which is why this one is
	// privileged — see sidecarSec.
	bidirectional := corev1.MountPropagationBidirectional
	volumeMounts := []corev1.VolumeMount{
		{
			Name:             WorkspaceVolumeName,
			MountPath:        WorkspaceMountPath,
			MountPropagation: &bidirectional,
		},
		{Name: "ws-staging", MountPath: "/ws-staging"},
	}

	always := corev1.ContainerRestartPolicyAlways
	sideC = corev1.Container{
		Name:            "workspace-sync",
		Image:           workspaceSyncImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            []string{"serve"},
		Env:             env,
		VolumeMounts:    volumeMounts,
		SecurityContext: sidecarSec,

		// Gates the workload's start on the workspace actually being ready
		// (§14.2). For a native sidecar the kubelet waits for the startup probe
		// before starting the next container, so this is what guarantees the
		// agent never opens a half-restored tree.
		//
		// exec, not httpGet: the kubelet probes from the node against the pod
		// IP, and this process binds 127.0.0.1, so an HTTP probe could never
		// succeed. The marker lives in the staging emptyDir, which is fresh on
		// every pod, so it cannot survive from a previous life.
		//
		// failureThreshold × periodSeconds = 10 minutes, matching the restore
		// timeout in the sidecar. A restore slower than that is a fault, and
		// the pod should fail rather than hang indefinitely.
		StartupProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"test", "-f", "/ws-staging/.ready"},
				},
			},
			InitialDelaySeconds: 1,
			PeriodSeconds:       2,
			FailureThreshold:    300,
		},
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
		// out, but this image is now debian-slim — curl is available, but
		// probes would gate the workload's start and a degraded object store
		// should not deny the user their workspace.
		//
		// Readiness would be actively harmful besides. On a native sidecar it
		// gates the workload's start, and this process must never be able to
		// keep a sandbox from running: an unreachable object store is
		// degraded durability, not a reason to deny the user their workspace.
		//
		// What is lost is small and already covered. A wedged sidecar shows up
		// as checkpoints that stop appearing, which the restore path reports
		// on the next session ("no checkpoint for this workspace yet"), and
		// the live tree is held in the overlay filesystem. `/healthz` stays
		// in the server for in-pod debugging.
	}
	sideC.Env = append(sideC.Env,
		corev1.EnvVar{
			// The uid the workload runs as, so a file-by-file restore hands the
			// tree back to it rather than leaving it root-owned (see the
			// sidecar's workspaceUID). Kept in sync with the pod
			// securityContext below.
			Name: "WORKSPACE_UID", Value: "1000",
		},
		corev1.EnvVar{
			// Retention, resolved by the caller from the CR (§14.2).
			//
			// Always set explicitly, even when it equals the default, so the
			// value that governs a running pod is visible in `kubectl describe`
			// rather than implied by the sidecar's own fallback. A retention
			// bound that has to be inferred is one nobody checks.
			Name: "KEEP_CHECKPOINTS", Value: strconv.Itoa(int(keepCheckpoints)),
		},
	)

	// §14.3. Both are set only when asked for, so `kubectl describe` on an
	// ordinary sandbox stays free of fields that describe the default.
	if ws.CheckpointID != "" {
		sideC.Env = append(sideC.Env, corev1.EnvVar{Name: "CHECKPOINT_ID", Value: ws.CheckpointID})
	}
	if ws.ReadOnly {
		sideC.Env = append(sideC.Env, corev1.EnvVar{Name: "WORKSPACE_READ_ONLY", Value: "true"})
	}
	if len(ws.PinnedCheckpoints) > 0 {
		// Comma-separated: a short list of ids, where JSON encoding would add a
		// parsing failure mode for no benefit.
		sideC.Env = append(sideC.Env, corev1.EnvVar{
			Name: "PINNED_CHECKPOINTS", Value: strings.Join(ws.PinnedCheckpoints, ","),
		})
	}
	return sideC
}

// resolveKeepCheckpoints turns the fleet's optional request into the value the
// sidecar runs with (§14.2).
//
// Clamped as well as defaulted. The CRD's Minimum/Maximum already reject an
// out-of-range value at admission, but this operator must not depend on that:
// a CR applied before the schema was updated, or through a path that skipped
// validation, would otherwise reach the sidecar — and `keep: 0` there means
// "prune everything but the newest", while a negative would be read as
// "unbounded". Neither is a value a fleet can be assumed to have meant.
func resolveKeepCheckpoints(ws *computev1alpha1.WorkspacePersistenceSpec) int32 {
	if ws == nil || ws.KeepCheckpoints == nil {
		return computev1alpha1.DefaultKeepCheckpoints
	}
	switch n := *ws.KeepCheckpoints; {
	case n < minKeepCheckpoints:
		return minKeepCheckpoints
	case n > maxKeepCheckpoints:
		return maxKeepCheckpoints
	default:
		return n
	}
}
