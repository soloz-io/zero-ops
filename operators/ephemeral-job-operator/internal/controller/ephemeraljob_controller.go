package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	computev1alpha1 "github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/api/v1alpha1"
)

// EphemeralJobReconciler owns the job lifecycle and nothing else (ADR-052 §7).
//
// What it is forbidden to do, per the ADR-041 matrix entry, is worth stating
// where the code is rather than only in the ADR: it provisions no
// infrastructure, never creates or patches MachineDeployment replicas, holds no
// provider credential, and authors no placement *policy*. It writes placement
// fields into a Job it owns, which is a different thing — the values come from
// placement.go, which is platform code in this repo, not from the request.
type EphemeralJobReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// ProvisioningBudget is the p95 cold-start budget for the placement class
	// (ADR-052 §11). A job that has been waiting for capacity longer than this
	// is reported as such — but it is NOT deleted, because deletion during
	// provisioning is indistinguishable to the autoscaler from deletion of a
	// healthy workload and causes the scale-up/scale-down thrash §11 describes.
	ProvisioningBudget time.Duration
}

const (
	jobOwnerKey    = ".metadata.controller"
	finalizerName  = "compute.nutgraf.in/ephemeraljob"
	labelJobUID    = "compute.nutgraf.in/ephemeraljob-uid"
	defaultRequeue = 10 * time.Second

	// Bounded so a slow or hanging receiver cannot stall the work queue for
	// every other job. The callback is a notification, not a transaction.
	callbackTimeout = 10 * time.Second
)

// callbackHTTP is shared so connections are reused across reconciles rather
// than a new transport being built per callback.
var callbackHTTP = &http.Client{Timeout: callbackTimeout}

func (r *EphemeralJobReconciler) callbackClient() *http.Client { return callbackHTTP }

// +kubebuilder:rbac:groups=compute.nutgraf.in,resources=ephemeraljobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compute.nutgraf.in,resources=ephemeraljobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compute.nutgraf.in,resources=ephemeraljobs/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;patch

func (r *EphemeralJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	var ej computev1alpha1.EphemeralJob
	if err := r.Get(ctx, req.NamespacedName, &ej); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !ej.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if isTerminal(ej.Status.Phase) {
		return r.reconcileTTL(ctx, &ej)
	}

	job, err := r.ensureJob(ctx, &ej)
	if err != nil {
		return ctrl.Result{}, err
	}

	// A Job whose pod cannot be admitted never produces a pod. The Job's own
	// failure condition carries the reason, and a ResourceQuota rejection is
	// the expected one at volume (ADR-052 §Consequences). It is terminal: the
	// fleet's burst quota is full, and retrying cannot change that.
	if reason, msg, rejected := jobAdmissionRejected(job); rejected {
		return ctrl.Result{}, r.markTerminal(ctx, &ej, computev1alpha1.PhaseFailed,
			computev1alpha1.ReasonQuotaRejected, fmt.Sprintf("%s: %s", reason, msg))
	}

	if done, phase, exit := jobFinished(job); done {
		return ctrl.Result{}, r.markFinished(ctx, &ej, phase, exit)
	}

	cap, err := AssessCapacity(ctx, r.Client, ej.Namespace, job.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	switch {
	case cap.Ready:
		// The workload clock starts when the pod runs, not when the request was
		// created: time spent waiting for a node is not the workload's time
		// (ADR-052 §11).
		if ej.Status.StartTime == nil {
			now := metav1.Now()
			ej.Status.StartTime = &now
			if err := r.Status().Update(ctx, &ej); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
		// Workload timeout, measured from first execution. Deleting the Job is
		// what actually stops the workload; unlike a deletion during
		// provisioning (§11 rule 3) this one is correct, because the pod is
		// running and the budget it was given is spent.
		if ran := time.Since(ej.Status.StartTime.Time); ran > time.Duration(ej.Spec.TimeoutSeconds)*time.Second {
			l.Info("workload exceeded its execution budget", "ephemeralJob", req.NamespacedName, "ran", ran)
			policy := metav1.DeletePropagationBackground
			if err := r.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &policy}); err != nil &&
				!apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			ej.Status.Message = fmt.Sprintf("workload ran %s, exceeding its %ds execution budget",
				ran.Truncate(time.Second), ej.Spec.TimeoutSeconds)
			return ctrl.Result{}, r.markFinished(ctx, &ej, computev1alpha1.PhaseTimedOut, nil)
		}
		r.setPhase(&ej, computev1alpha1.PhaseRunning, cap)

	case cap.Terminal:
		r.setPhase(&ej, computev1alpha1.PhaseProvisioning, cap)
		l.Info("burst capacity unavailable", "ephemeralJob", req.NamespacedName, "message", cap.Message)

	default:
		r.setPhase(&ej, computev1alpha1.PhaseProvisioning, cap)
		if waited := time.Since(ej.CreationTimestamp.Time); waited > r.ProvisioningBudget {
			// Over budget is reported, never acted on by deletion. See §11.
			ej.Status.Message = fmt.Sprintf(
				"waiting %s for burst capacity, over the %s budget for this placement class: %s",
				waited.Truncate(time.Second), r.ProvisioningBudget, cap.Message)
		}
	}

	if err := r.Status().Update(ctx, &ej); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{RequeueAfter: defaultRequeue}, nil
}

// ensureJob creates the batch/v1 Job for this request if it does not exist.
//
// The Job is named for and owned by the EphemeralJob, so garbage collection is
// structural. The predecessor implementation swept for orphaned compute on a
// timer because it provisioned VMs that nothing owned; owner references remove
// the need for that entirely.
func (r *EphemeralJobReconciler) ensureJob(ctx context.Context, ej *computev1alpha1.EphemeralJob) (*batchv1.Job, error) {
	name := jobNameFor(ej)

	var existing batchv1.Job
	err := r.Get(ctx, client.ObjectKey{Namespace: ej.Namespace, Name: name}, &existing)
	if err == nil {
		return &existing, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	placement, ok := ResolvePlacement(ej.Spec.PlacementClass)
	if !ok {
		return nil, fmt.Errorf("unknown placement class %q", ej.Spec.PlacementClass)
	}

	job := r.buildJob(ej, name, placement)
	if err := ctrl.SetControllerReference(ej, job, r.Scheme); err != nil {
		return nil, err
	}
	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return &existing, r.Get(ctx, client.ObjectKey{Namespace: ej.Namespace, Name: name}, &existing)
		}
		return nil, err
	}

	ej.Status.JobName = name
	return job, nil
}

func (r *EphemeralJobReconciler) buildJob(
	ej *computev1alpha1.EphemeralJob, name string, p Placement,
) *batchv1.Job {
	backoff := int32(0) // no retry: a burst node join per attempt is too costly to spend blindly
	ttl := ej.Spec.TTLSecondsAfterFinished

	env := make([]corev1.EnvVar, 0, len(ej.Spec.Env))
	for k, v := range ej.Spec.Env {
		env = append(env, corev1.EnvVar{Name: k, Value: v})
	}

	container := corev1.Container{
		Name:    "workload",
		Image:   ej.Spec.Image,
		Command: ej.Spec.Command,
		Args:    ej.Spec.Args,
		Env:     env,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr(false),
			RunAsNonRoot:             ptr(true),
			ReadOnlyRootFilesystem:   ptr(true),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
			{Name: "result", MountPath: "/result"},
		},
	}
	if ej.Spec.Resources != nil {
		container.Resources = *ej.Spec.Resources
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ej.Namespace,
			Labels: map[string]string{
				labelJobUID: string(ej.UID),
				"tenant-id": tenantFromNamespace(ej.Namespace),
				// enforce-tenant-abi/require-cost-labels matches every Job in a
				// tenant-* namespace and demands BOTH labels. tenant-id alone
				// meant the Job was rejected at admission — and rejected inside
				// this operator's reconcile, where the tenant sees only a CR
				// that never produces a pod.
				//
				// "platform" for the same reason the tenant gateway carries it:
				// this operator authors the pod, and its placement class,
				// priority and resource envelope are platform decisions with no
				// fleet-supplied input. Charging burst compute back to the
				// submitting tenant needs a real per-tenant source, which no
				// tenant namespace carries today; when one exists it belongs
				// here, read from the namespace rather than from the CR, so a
				// tenant cannot label its own spend.
				"cost-center": "platform",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff,
			// ActiveDeadlineSeconds is DELIBERATELY NOT SET.
			//
			// batch/v1 measures it from the Job's own start, which includes the
			// entire wait for burst capacity. Setting it to the workload's
			// timeout therefore spends the workload's budget on the node join:
			// on a pool whose cold start exceeds the timeout, every job fails
			// before it runs, and reports TimedOut — blaming the workload for
			// an infrastructure wait. ADR-052 §11 requires the two clocks to be
			// separate, so the workload timeout is enforced by this operator
			// from status.startTime, which is set when the pod actually runs.
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						labelJobUID: string(ej.UID),
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,

					// ── ADR-052 §4: placement, written by the component that
					// authors the pod. There is no fleet-supplied input to any
					// of these three fields.
					NodeSelector:      p.NodeSelector,
					Tolerations:       p.Tolerations,
					PriorityClassName: p.PriorityClassName,

					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr(true),
						RunAsUser:      ptr(int64(1000)),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{container},
					Volumes: []corev1.Volume{
						{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "result", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
}

// reconcileTTL removes the EphemeralJob once its retention window has passed.
// The Job itself is reclaimed by ttlSecondsAfterFinished, and its pods with it.
func (r *EphemeralJobReconciler) reconcileTTL(ctx context.Context, ej *computev1alpha1.EphemeralJob) (ctrl.Result, error) {
	if ej.Status.CompletionTime == nil {
		return ctrl.Result{}, nil
	}
	ttl := time.Duration(ej.Spec.TTLSecondsAfterFinished) * time.Second
	elapsed := time.Since(ej.Status.CompletionTime.Time)
	if elapsed < ttl {
		return ctrl.Result{RequeueAfter: ttl - elapsed}, nil
	}
	return ctrl.Result{}, client.IgnoreNotFound(r.Delete(ctx, ej))
}

func (r *EphemeralJobReconciler) setPhase(ej *computev1alpha1.EphemeralJob, phase computev1alpha1.Phase, c CapacityState) {
	ej.Status.Phase = phase
	ej.Status.Message = c.Message
	ej.Status.ObservedGeneration = ej.Generation
	status := metav1.ConditionFalse
	if c.Ready {
		status = metav1.ConditionTrue
	}
	meta_SetStatusCondition(&ej.Status.Conditions, metav1.Condition{
		Type:               computev1alpha1.ConditionCapacity,
		Status:             status,
		Reason:             c.Reason,
		Message:            c.Message,
		ObservedGeneration: ej.Generation,
	})
}

func (r *EphemeralJobReconciler) markFinished(
	ctx context.Context, ej *computev1alpha1.EphemeralJob, phase computev1alpha1.Phase, exit *int32,
) error {
	now := metav1.Now()
	ej.Status.Phase = phase
	ej.Status.CompletionTime = &now
	ej.Status.ExitCode = exit
	ej.Status.ObservedGeneration = ej.Generation
	status := metav1.ConditionTrue
	if phase != computev1alpha1.PhaseSucceeded {
		status = metav1.ConditionFalse
	}
	meta_SetStatusCondition(&ej.Status.Conditions, metav1.Condition{
		Type:               computev1alpha1.ConditionComplete,
		Status:             status,
		Reason:             string(phase),
		Message:            ej.Status.Message,
		ObservedGeneration: ej.Generation,
	})
	if err := client.IgnoreNotFound(r.Status().Update(ctx, ej)); err != nil {
		return err
	}
	// Terminal state is recorded; now tell whoever is waiting for it. Ordered
	// after the status write so the CR is the source of truth even if the
	// callback fails and this is retried.
	return r.fireCallback(ctx, ej, phase, exit)
}

func (r *EphemeralJobReconciler) markTerminal(
	ctx context.Context, ej *computev1alpha1.EphemeralJob, phase computev1alpha1.Phase, reason, msg string,
) error {
	now := metav1.Now()
	ej.Status.Phase = phase
	ej.Status.CompletionTime = &now
	ej.Status.Message = msg
	ej.Status.ObservedGeneration = ej.Generation
	meta_SetStatusCondition(&ej.Status.Conditions, metav1.Condition{
		Type:               computev1alpha1.ConditionCapacity,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: ej.Generation,
	})
	return client.IgnoreNotFound(r.Status().Update(ctx, ej))
}

func (r *EphemeralJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&computev1alpha1.EphemeralJob{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// fireCallback notifies Spec.CallbackURL that the job reached a terminal state.
//
// This is the completion signal the workflow is suspended on. The SDK's step
// creates a single-use hook, puts its URL here, and returns pending_hitl; until
// something POSTs, the run waits indefinitely. Rendering takes tens of minutes,
// so polling for this would be wasteful and slow — the Job's own status change
// already wakes this controller (Owns(&batchv1.Job{})), and this turns that
// event into the message the workflow is waiting for.
//
// It is deliberately the OPERATOR that reports, not the workload. A container
// can only report outcomes it survives: an OOM kill, an image that will not
// pull, a pod evicted, a deadline exceeded — in each case the workload posts
// nothing and the run hangs. jobFinished already classifies those, so reporting
// from here covers the failures the job itself cannot.
//
// Delivery is at-least-once and marked with ConditionCallbackDelivered so a
// later reconcile does not repeat it. The receiver claims a single-use token,
// so a duplicate that races the marker is refused rather than double-resuming.
func (r *EphemeralJobReconciler) fireCallback(
	ctx context.Context, ej *computev1alpha1.EphemeralJob, phase computev1alpha1.Phase, exit *int32,
) error {
	l := log.FromContext(ctx)

	// Optional by design: a job nobody is waiting on needs no callback.
	if ej.Spec.CallbackURL == "" {
		return nil
	}
	if meta_IsStatusConditionTrue(ej.Status.Conditions, computev1alpha1.ConditionCallbackDelivered) {
		return nil
	}

	body, err := json.Marshal(map[string]any{
		"jobId":    ej.Name,
		"status":   string(phase),
		"exitCode": exit,
		"output":   ej.Spec.Output,
		"message":  ej.Status.Message,
	})
	if err != nil {
		// Unmarshallable payload is a programming error, not a transient one:
		// retrying cannot fix it, and blocking the TTL on it would leak the CR.
		l.Error(err, "callback payload could not be marshalled", "ephemeralJob", ej.Name)
		return nil
	}

	// Bounded: this runs inside Reconcile, and a hanging callback would stall
	// the work queue for every other job.
	cctx, cancel := context.WithTimeout(ctx, callbackTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodPost, ej.Spec.CallbackURL, bytes.NewReader(body))
	if err != nil {
		l.Error(err, "callback request could not be built", "ephemeralJob", ej.Name)
		return nil
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.callbackClient().Do(req)
	if err != nil {
		// Requeue rather than swallow: a dropped callback strands the workflow
		// forever, which is a worse failure than a late one.
		l.Info("callback failed, will retry", "ephemeralJob", ej.Name, "err", err.Error())
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		l.Info("callback returned a server error, will retry",
			"ephemeralJob", ej.Name, "status", resp.StatusCode)
		return fmt.Errorf("callback to %s returned %d", ej.Spec.CallbackURL, resp.StatusCode)
	}

	// A 4xx is not retried. The token is single-use, so 404/409 is the expected
	// answer to a duplicate that beat the marker, and every other 4xx means the
	// receiver rejected this payload — neither improves by repeating.
	if resp.StatusCode >= 400 {
		l.Info("callback rejected, not retrying",
			"ephemeralJob", ej.Name, "status", resp.StatusCode)
	}

	meta_SetStatusCondition(&ej.Status.Conditions, metav1.Condition{
		Type:               computev1alpha1.ConditionCallbackDelivered,
		Status:             metav1.ConditionTrue,
		Reason:             string(phase),
		Message:            fmt.Sprintf("callback responded %d", resp.StatusCode),
		ObservedGeneration: ej.Generation,
	})
	return client.IgnoreNotFound(r.Status().Update(ctx, ej))
}
