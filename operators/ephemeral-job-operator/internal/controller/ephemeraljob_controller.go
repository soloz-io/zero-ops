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
	"k8s.io/apimachinery/pkg/api/resource"
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

	// APIReader reads straight from the API server, bypassing the manager's
	// cache. It exists for one job: reading a Job's Events. Events are numerous
	// and short-lived, and a cached read would make this controller maintain an
	// informer over every Event in the cluster to answer a question it asks only
	// when a job looks stuck.
	APIReader client.Reader

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

	// The envelope a request gets when it names none. See the comment at the
	// assignment for why an absent value cannot be left absent.
	defaultRequestCPU    = "2"
	defaultRequestMemory = "4Gi"

	// Sidecars get their own, much smaller floor. A sidecar is auxiliary by
	// definition — a credential broker, a log shipper — so giving it the
	// workload's envelope would multiply a sandbox's quota footprint several
	// times over for containers that idle.
	defaultSidecarRequestCPU    = "50m"
	defaultSidecarRequestMemory = "64Mi"
)

// callbackHTTP is shared so connections are reused across reconciles rather
// than a new transport being built per callback.
var callbackHTTP = &http.Client{Timeout: callbackTimeout}

func (r *EphemeralJobReconciler) callbackClient() *http.Client { return callbackHTTP }

// +kubebuilder:rbac:groups=compute.nutgraf.in,resources=ephemeraljobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compute.nutgraf.in,resources=ephemeraljobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compute.nutgraf.in,resources=ephemeraljobs/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;delete
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

	// Reject an unrunnable request once, here, instead of letting it fail later
	// somewhere that does not say why. Both checks below describe pods the API
	// server would refuse to create, and refusing them at submission is the
	// difference between a submitter seeing the reason and a CR that silently
	// never produces a pod.
	if reason, msg, invalid := validateSpec(&ej); invalid {
		return ctrl.Result{}, r.markTerminal(ctx, &ej, computev1alpha1.PhaseFailed, reason, msg)
	}

	// Service mode has no Job, no completion and no callback to fire. It is a
	// different lifecycle over the SAME pod description, so it branches here
	// rather than inside every step below.
	if ej.Spec.Mode == computev1alpha1.ModeService {
		return r.reconcileServiceMode(ctx, &ej)
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

	// The same outcome by the other route. The check above sees a Job the
	// controller gave up on; this sees one it will retry forever because each
	// pod is refused before it exists. Both mean no pod will run, and neither
	// resolves by waiting — but only this one is reached in practice, because a
	// refused create sets no condition for the check above to read.
	if reason, msg, blocked := jobPodCreationBlocked(ctx, r.APIReader, job); blocked {
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

	container := r.buildWorkloadContainer(ej)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ej.Namespace,
			Labels: authoredLabels(ej, map[string]string{
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
			}),
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
					Labels: authoredLabels(ej, map[string]string{
						labelJobUID: string(ej.UID),
					}),
				},
				Spec: r.buildPodSpec(ej, p, container),
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
	if r.APIReader == nil {
		r.APIReader = mgr.GetAPIReader()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&computev1alpha1.EphemeralJob{}).
		Owns(&batchv1.Job{}).
		// Service mode owns its Pod and Service directly rather than through a
		// Job, so both must wake this controller too — otherwise a sandbox pod
		// going Ready or dying would be noticed only on the next timed requeue.
		Owns(&corev1.Pod{}).
		Owns(&corev1.Service{}).
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


// buildWorkloadContainer builds the container the request describes. Shared by
// both modes: a sandbox and a render job differ in lifecycle, not in how the
// workload container itself is assembled.
func (r *EphemeralJobReconciler) buildWorkloadContainer(ej *computev1alpha1.EphemeralJob) corev1.Container {
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
	// The request's envelope wins; the platform supplies one only when the
	// request names none.
	//
	// Resources are optional on the CR but MANDATORY on the pod: every burst pod
	// runs under the burst-tenant priority class, and the burst-compute
	// ResourceQuota is scoped to it — a quota naming requests.cpu/memory refuses
	// any pod that omits them. Left absent, the Job's every pod creation is
	// forbidden, and invisibly: a refused create is not a failed pod, so the job
	// controller retries forever, status.failed stays 0, JobFailed is never set,
	// and the request reports WaitingForCapacity indefinitely.
	//
	// The floor is deliberately modest and a REQUEST, not a limit, so a workload
	// still bursts above it on an idle node while four fit concurrently in the
	// quota. A workload that needs a different envelope states it and this is
	// not consulted — which is the normal case: both callers now do.
	if ej.Spec.Resources != nil {
		container.Resources = *ej.Spec.Resources
	}
	withRequests(&container, defaultRequestCPU, defaultRequestMemory)
	if ej.Spec.ImagePullPolicy != "" {
		container.ImagePullPolicy = ej.Spec.ImagePullPolicy
	}
	container.Ports = ej.Spec.Ports
	container.WorkingDir = ej.Spec.WorkingDir
	if len(ej.Spec.VolumeMounts) > 0 {
		container.VolumeMounts = ej.Spec.VolumeMounts
	}
	return container
}

// buildPodSpec is the ONE place a pod belonging to this operator is described.
//
// Both modes go through it, which is the point of the merge: placement is
// written here, so there is no path — Job or Service — by which a pod of ours
// reaches a node without it. Previously the sandbox path had exactly such a
// path, because its pod was authored upstream and placement was bolted on by a
// mutating policy that could simply not match.
func (r *EphemeralJobReconciler) buildPodSpec(
	ej *computev1alpha1.EphemeralJob, p Placement, container corev1.Container,
) corev1.PodSpec {
	volumes := ej.Spec.Volumes
	if len(volumes) == 0 {
		volumes = []corev1.Volume{
			{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			{Name: "result", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		}
	}

	// Every container, not just the workload.
	//
	// The burst-compute ResourceQuota refuses a POD in which any container omits
	// requests.cpu/memory, so defaulting only the primary left the sidecar to
	// fail the whole pod: "must specify requests.cpu for: agent-vault". The
	// rejection names the container, but it is the pod that is never created —
	// and the request that named an envelope for its workload looks, from the
	// outside, like one that did not.
	sidecars := make([]corev1.Container, 0, len(ej.Spec.Sidecars))
	for _, c := range ej.Spec.Sidecars {
		withRequests(&c, defaultSidecarRequestCPU, defaultSidecarRequestMemory)
		sidecars = append(sidecars, c)
	}

	// Never for a job; Always for a service.
	//
	// A batch workload that exits non-zero has failed and the Job controller
	// decides whether to retry, so restarting the container underneath it would
	// hide the outcome. A Service-mode workload is the opposite: it is expected
	// to keep running, and a transient startup failure must not be terminal.
	//
	// This was Never for both, and the cost was immediate. A sandbox scheduled
	// onto a burst node 60 seconds old lost the DNS race — cluster DNS had not
	// converged for the new node — and the harness exited 3 on
	// "Temporary failure in name resolution". With Never it stayed dead, so the
	// pod existed, the CR read Running, and the sandbox was permanently broken.
	// Cold-start races are inherent to scale-from-zero, so recovery has to be.
	restart := corev1.RestartPolicyNever
	if ej.Spec.Mode == computev1alpha1.ModeService {
		restart = corev1.RestartPolicyAlways
	}

	spec := corev1.PodSpec{
		RestartPolicy: restart,

		// ── ADR-052 §4: placement, written by the component that authors the
		// pod. There is no fleet-supplied input to any of these three fields.
		NodeSelector:      p.NodeSelector,
		Tolerations:       p.Tolerations,
		PriorityClassName: p.PriorityClassName,

		// Resolve a service FQDN in one query, not five. The cluster default of
		// ndots:5 makes any name with fewer than 5 dots relative, so an ordinary
		// service address is tried against every search domain first — one of
		// which, on a hybrid spoke, is the node's tailnet domain that forwards
		// off-cluster. Under load that lookup fails outright.
		DNSConfig: &corev1.PodDNSConfig{
			Options: []corev1.PodDNSConfigOption{{Name: "ndots", Value: ptr("2")}},
		},

		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot:   ptr(true),
			RunAsUser:      ptr(int64(1000)),
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		Containers:                    append([]corev1.Container{container}, sidecars...),
		Volumes:                       volumes,
		ImagePullSecrets:              ej.Spec.ImagePullSecrets,
		TerminationGracePeriodSeconds: ej.Spec.TerminationGracePeriodSeconds,
	}
	return spec
}

// buildPod is the Service-mode workload: a bare Pod, not a Job.
//
// A batch/v1 Job exists to drive something to completion and reports failure
// when its pod exits non-zero. A sandbox has no completion — it serves until it
// goes idle — so wrapping it in a Job would either report a permanent failure
// or a success that never arrives. The Pod is owned by the EphemeralJob, so it
// is still garbage-collected structurally.
func (r *EphemeralJobReconciler) buildPod(
	ej *computev1alpha1.EphemeralJob, name string, p Placement,
) *corev1.Pod {
	container := r.buildWorkloadContainer(ej)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ej.Namespace,
			Labels: authoredLabels(ej, map[string]string{
				labelJobUID: string(ej.UID),
				"tenant-id": tenantFromNamespace(ej.Namespace),
				// enforce-tenant-abi/require-cost-labels does not match bare
				// Pods, but the label is carried anyway so Job-mode and
				// Service-mode workloads attribute identically.
				"cost-center": "platform",
			}),
		},
		Spec: r.buildPodSpec(ej, p, container),
	}
}

// buildService fronts a Service-mode workload with a stable in-cluster address.
//
// The selector is the EphemeralJob's UID, not its name: a name can be reused
// after deletion, and a Service that outlived its pod would then silently
// forward a new session's traffic to whatever claimed the old name.
func (r *EphemeralJobReconciler) buildService(
	ej *computev1alpha1.EphemeralJob, name string,
) *corev1.Service {
	svcType := ej.Spec.Service.Type
	if svcType == "" {
		svcType = corev1.ServiceTypeClusterIP
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ej.Namespace,
			Labels: authoredLabels(ej, map[string]string{
				labelJobUID: string(ej.UID),
				"tenant-id": tenantFromNamespace(ej.Namespace),
			}),
		},
		Spec: corev1.ServiceSpec{
			Type:     svcType,
			Selector: map[string]string{labelJobUID: string(ej.UID)},
			Ports:    ej.Spec.Service.Ports,
		},
	}
}


// reconcileServiceMode drives a long-lived workload: a Pod, an optional Service
// in front of it, and an idle clock instead of a completion.
//
// The idle clock is the only thing that ends it. A Job-mode request is bounded
// by its own exit; a sandbox serves until nobody is using it, so if
// LastActivityTime is never refreshed the pod is reaped at IdleTimeoutSeconds.
// That is deliberately fail-closed: a client that crashes without releasing its
// sandbox loses it on the timeout rather than holding burst capacity forever.
func (r *EphemeralJobReconciler) reconcileServiceMode(
	ctx context.Context, ej *computev1alpha1.EphemeralJob,
) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	placement, ok := ResolvePlacement(ej.Spec.PlacementClass)
	if !ok {
		return ctrl.Result{}, fmt.Errorf("unknown placement class %q", ej.Spec.PlacementClass)
	}

	name := jobNameFor(ej)

	// Service first: it selects on the EphemeralJob UID, so it is correct the
	// moment the pod appears and there is no window where a caller resolves the
	// address and reaches nothing.
	if ej.Spec.Service != nil {
		if err := r.ensureService(ctx, ej, serviceNameFor(ej)); err != nil {
			return ctrl.Result{}, err
		}
	}

	pod, err := r.ensurePod(ctx, ej, name, placement)
	if err != nil {
		return ctrl.Result{}, err
	}

	switch pod.Status.Phase {
	case corev1.PodFailed:
		ej.Status.Message = podTerminationMessage(pod)
		return ctrl.Result{}, r.markFinished(ctx, ej, computev1alpha1.PhaseFailed, podExitCode(pod))
	case corev1.PodSucceeded:
		// A service that exited on its own. Not an error, but it is over.
		return ctrl.Result{}, r.markFinished(ctx, ej, computev1alpha1.PhaseSucceeded, podExitCode(pod))
	}

	cap, err := AssessCapacityBySelector(ctx, r.Client, ej.Namespace,
		client.MatchingLabels{labelJobUID: string(ej.UID)})
	if err != nil {
		return ctrl.Result{}, err
	}

	if !cap.Ready {
		r.setPhase(ej, computev1alpha1.PhaseProvisioning, cap)
		if waited := time.Since(ej.CreationTimestamp.Time); waited > r.ProvisioningBudget {
			ej.Status.Message = fmt.Sprintf(
				"waiting %s for burst capacity, over the %s budget for this placement class: %s",
				waited.Truncate(time.Second), r.ProvisioningBudget, cap.Message)
		}
		if err := r.Status().Update(ctx, ej); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return ctrl.Result{RequeueAfter: defaultRequeue}, nil
	}

	now := metav1.Now()
	if ej.Status.StartTime == nil {
		ej.Status.StartTime = &now
	}
	// Seed the idle clock when the workload first runs, not when the request
	// was created: time spent waiting for a node is not idle time, and charging
	// it against the idle budget would reap a sandbox that never got to serve.
	if ej.Status.LastActivityTime == nil {
		ej.Status.LastActivityTime = &now
	}

	idle := time.Since(ej.Status.LastActivityTime.Time)
	budget := time.Duration(ej.Spec.IdleTimeoutSeconds) * time.Second
	if idle > budget {
		l.Info("reaping idle service workload", "ephemeralJob", ej.Name, "idle", idle.Truncate(time.Second))
		policy := metav1.DeletePropagationBackground
		if err := r.Delete(ctx, pod, &client.DeleteOptions{PropagationPolicy: &policy}); err != nil &&
			!apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		ej.Status.Message = fmt.Sprintf("idle for %s, exceeding the %ds idle budget",
			idle.Truncate(time.Second), ej.Spec.IdleTimeoutSeconds)
		return ctrl.Result{}, r.markFinished(ctx, ej, computev1alpha1.PhaseSucceeded, nil)
	}

	r.setPhase(ej, computev1alpha1.PhaseRunning, cap)
	ej.Status.PodName = pod.Name
	if ej.Spec.Service != nil {
		ej.Status.ServiceName = serviceNameFor(ej)
	}
	if err := r.Status().Update(ctx, ej); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Wake when the idle budget could next expire, so a quiet sandbox is reaped
	// promptly rather than at the next unrelated event.
	return ctrl.Result{RequeueAfter: minDuration(budget-idle, defaultRequeue)}, nil
}

func (r *EphemeralJobReconciler) ensurePod(
	ctx context.Context, ej *computev1alpha1.EphemeralJob, name string, p Placement,
) (*corev1.Pod, error) {
	var existing corev1.Pod
	err := r.Get(ctx, client.ObjectKey{Namespace: ej.Namespace, Name: name}, &existing)
	if err == nil {
		return &existing, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	pod := r.buildPod(ej, name, p)
	if err := ctrl.SetControllerReference(ej, pod, r.Scheme); err != nil {
		return nil, err
	}
	if err := r.Create(ctx, pod); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return &existing, r.Get(ctx, client.ObjectKey{Namespace: ej.Namespace, Name: name}, &existing)
		}
		return nil, err
	}
	return pod, nil
}

func (r *EphemeralJobReconciler) ensureService(
	ctx context.Context, ej *computev1alpha1.EphemeralJob, name string,
) error {
	var existing corev1.Service
	err := r.Get(ctx, client.ObjectKey{Namespace: ej.Namespace, Name: name}, &existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	svc := r.buildService(ej, name)
	if err := ctrl.SetControllerReference(ej, svc, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, svc); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func podExitCode(p *corev1.Pod) *int32 {
	for i := range p.Status.ContainerStatuses {
		if t := p.Status.ContainerStatuses[i].State.Terminated; t != nil {
			code := t.ExitCode
			return &code
		}
	}
	return nil
}

func podTerminationMessage(p *corev1.Pod) string {
	for i := range p.Status.ContainerStatuses {
		if t := p.Status.ContainerStatuses[i].State.Terminated; t != nil && t.Reason != "" {
			return fmt.Sprintf("container %s terminated: %s", p.Status.ContainerStatuses[i].Name, t.Reason)
		}
	}
	if p.Status.Reason != "" {
		return p.Status.Reason
	}
	return "pod failed"
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}


// authoredLabels merges the request's own labels into the ones this operator
// stamps on what it authors.
//
// The submitter's labels have to reach the POD, not just the CR, because that
// is where cluster policy selects: the platform's sandbox default-deny
// NetworkPolicy matches agents.x-k8s.io/sandbox on the pod. Under the previous
// design the upstream controller copied podTemplate labels through; now that
// this operator authors the pod, dropping them here would silently remove a
// workload from the policy that is supposed to contain it.
//
// The operator's own labels win on conflict. They are the ones other platform
// components key on — ownership, attribution — and a submitter must not be able
// to reassign its pod's tenant by relabelling its request.
func authoredLabels(ej *computev1alpha1.EphemeralJob, own map[string]string) map[string]string {
	merged := make(map[string]string, len(ej.Labels)+len(own))
	for k, v := range ej.Labels {
		merged[k] = v
	}
	for k, v := range own {
		merged[k] = v
	}
	return merged
}

// validateSpec rejects requests that describe a pod the API server would refuse,
// so the submitter learns at submission instead of watching a CR that never
// produces one.
func validateSpec(ej *computev1alpha1.EphemeralJob) (reason, message string, invalid bool) {
	// A Service-mode workload has no completion, so without an idle bound it
	// never terminates and holds burst capacity indefinitely.
	if ej.Spec.Mode == computev1alpha1.ModeService && ej.Spec.IdleTimeoutSeconds <= 0 {
		return "InvalidSpec", "mode=Service requires idleTimeoutSeconds: without it the workload would never be reaped", true
	}
	return "", "", false
}


// serviceNameFor is the address a client resolves.
//
// "<name>-svc" is the convention the SDK's serviceName() already used when it
// created this Service itself, and it is kept so that moving ownership into the
// operator does not also move every caller's address. status.serviceName is
// published alongside it for anything that would rather read than derive.
func serviceNameFor(ej *computev1alpha1.EphemeralJob) string {
	return ej.Name + "-svc"
}


// withRequests fills in any resource request a container leaves unset.
//
// It exists because the requirement is per-CONTAINER: the burst-compute
// ResourceQuota refuses a pod in which any container omits requests.cpu or
// requests.memory, so the workload and every sidecar must each carry them. A
// stated value is never overwritten.
func withRequests(c *corev1.Container, cpu, memory string) {
	if c.Resources.Requests == nil {
		c.Resources.Requests = corev1.ResourceList{}
	}
	if _, ok := c.Resources.Requests[corev1.ResourceCPU]; !ok {
		c.Resources.Requests[corev1.ResourceCPU] = resource.MustParse(cpu)
	}
	if _, ok := c.Resources.Requests[corev1.ResourceMemory]; !ok {
		c.Resources.Requests[corev1.ResourceMemory] = resource.MustParse(memory)
	}
}
