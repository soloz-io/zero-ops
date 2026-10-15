package controller

import (
	"context"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	computev1alpha1 "github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/api/v1alpha1"
)

// jobStorageEnv gives a Job-mode workload that writes a result the fleet's
// object storage, BY REFERENCE.
//
// spec.output names where the result goes, never the credentials, and a pod has
// no other way to learn them. The submitter used to fill the gap by copying its
// own S3 values into spec.env, which stored the secret key in plaintext in this
// CR -- in etcd, and readable by anything that can read EphemeralJobs. The
// operator already knows where the fleet keeps them: the same Secret and
// ConfigMap the workspace-sync sidecars read ({fleetId}-sdk-secrets,
// {fleetId}-config). So the workload gets the same references, and no value is
// ever written into the request.
//
// Required, unlike workspace-sync's: a sandbox without storage still serves and
// only its checkpoints no-op, but a job that cannot write its result has
// failed. A missing Secret or key holds the pod in CreateContainerConfigError
// naming it, instead of a worker that runs to the end and then cannot upload.
// S3_REGION alone stays optional -- the fleet declares none, and S3-compatible
// endpoints that ignore region need none.
//
// A key the request states itself is left as stated, so a job can be pointed at
// a different bucket on purpose.
func jobStorageEnv(ej *computev1alpha1.EphemeralJob, fleetID string) []corev1.EnvVar {
	if ej.Spec.Output == nil {
		return nil
	}
	secretName := resolveWorkspaceSyncSecret(ej.Namespace, fleetID)
	configName := resolveWorkspaceSyncConfig(ej.Namespace, fleetID)
	refs := []struct {
		name     string
		secret   bool
		optional bool
	}{
		{name: "S3_ENDPOINT_URL"},
		{name: "S3_BUCKET_NAME"},
		{name: "S3_ACCESS_KEY_ID", secret: true},
		{name: "S3_SECRET_ACCESS_KEY", secret: true},
		{name: "S3_REGION", optional: true},
	}
	env := make([]corev1.EnvVar, 0, len(refs))
	for _, ref := range refs {
		if _, stated := ej.Spec.Env[ref.name]; stated {
			continue
		}
		src := &corev1.EnvVarSource{}
		if ref.secret {
			src.SecretKeyRef = &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  ref.name,
				Optional:             ptr(ref.optional),
			}
		} else {
			src.ConfigMapKeyRef = &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: configName},
				Key:                  ref.name,
				Optional:             ptr(ref.optional),
			}
		}
		env = append(env, corev1.EnvVar{Name: ref.name, ValueFrom: src})
	}
	return env
}

// jobRefusedAtAdmission reports whether creating the Job was refused by the API
// server's admission chain rather than failing transiently.
//
// A validating webhook's denial (Kyverno) arrives as 400 -- the API server
// raises any denial below 400 to 400 -- Pod Security and RBAC as 403, schema
// validation as 422. None of them changes on retry.
func jobRefusedAtAdmission(err error) bool {
	return apierrors.IsBadRequest(err) || apierrors.IsForbidden(err) || apierrors.IsInvalid(err)
}

// deleteJob removes the request's Job, if it has one, with its pods.
func (r *EphemeralJobReconciler) deleteJob(ctx context.Context, ej *computev1alpha1.EphemeralJob) error {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: ej.Namespace, Name: jobNameFor(ej)}}
	policy := metav1.DeletePropagationBackground
	return client.IgnoreNotFound(r.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &policy}))
}
