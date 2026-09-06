// +kubebuilder:object:generate=true
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Phase is the job lifecycle state (ADR-052 §7).
//
// Provisioning is deliberately distinct from Pending: it is the interval in
// which the pod exists but is unschedulable because burst capacity is being
// created. A submitter that cannot tell the two apart cannot tell "the platform
// is working on it" from "nothing is happening", and the difference is minutes.
type Phase string

const (
	PhasePending      Phase = "Pending"
	PhaseProvisioning Phase = "Provisioning"
	PhaseRunning      Phase = "Running"
	PhaseSucceeded    Phase = "Succeeded"
	PhaseFailed       Phase = "Failed"
	PhaseTimedOut     Phase = "TimedOut"
)

// Condition types surfaced on status.
const (
	// ConditionCapacity reports whether capacity exists for this job's pod.
	// False with reason WaitingForCapacity is the normal provisioning state,
	// NOT an error — see ADR-052 §11.
	ConditionCapacity = "CapacityAvailable"
	// ConditionComplete reports terminal outcome.
	ConditionComplete = "Complete"
	// ConditionCallbackDelivered records that CallbackURL has been notified of
	// the terminal outcome.
	//
	// It exists to make delivery exactly-once from this side. Reconcile runs
	// again for reasons unrelated to the job — a resync, a status write, an
	// operator restart — and without a marker each pass would POST again. The
	// receiver claims a single-use hook token, so a duplicate is not corrupting,
	// but it is a spurious error in someone's logs for a workflow that already
	// resumed correctly.
	ConditionCallbackDelivered = "CallbackDelivered"
)

// Reasons for ConditionCapacity. These are the three outcomes ADR-052 §12
// requires a consumer to distinguish; they must not share a code path.
const (
	ReasonQuotaRejected       = "QuotaRejected"       // terminal — the fleet's burst quota is full
	ReasonWaitingForCapacity  = "WaitingForCapacity"  // wait — a node is being provisioned
	ReasonCapacityUnavailable = "CapacityUnavailable" // terminal for now — maxNodes reached
	ReasonScheduled           = "Scheduled"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ej;ejs
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Capacity",type=string,JSONPath=".status.conditions[?(@.type=='CapacityAvailable')].reason"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// EphemeralJob is a fleet-authored request to run a single batch workload on
// platform-provided burst capacity (ADR-052 §7).
//
// The fleet owns the request. The platform owns every layer the request depends
// on — placement, bounds, capacity — and none of those is expressible here.
// There is deliberately no field for nodeSelector, tolerations, priorityClassName
// or node affinity: placement is written by this operator into the Job it
// authors (ADR-052 §4), so a fleet can neither escape burst placement onto
// home-lab capacity nor place itself on burst capacity it was not granted.
type EphemeralJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EphemeralJobSpec   `json:"spec,omitempty"`
	Status EphemeralJobStatus `json:"status,omitempty"`
}

type EphemeralJobSpec struct {
	// Image is the workload container image. It MUST be an immutable digest
	// reference: the tenant ABI (kyverno-tenant-abi, rule 2) rejects mutable
	// tags on the resulting Pod, and rejecting here instead means the submitter
	// learns at submission rather than from a controller log in another
	// namespace.
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_.\-/]+@sha256:[a-fA-F0-9]{64}$`
	Image string `json:"image"`

	// +optional
	Command []string `json:"command,omitempty"`
	// +optional
	Args []string `json:"args,omitempty"`
	// +optional
	Env map[string]string `json:"env,omitempty"`

	// Resources is the workload's requested envelope, and it is what the
	// submitter should state: only the submitter knows what its workload needs,
	// and a render and a sandbox differ by an order of magnitude.
	//
	// Optional, because the pod cannot go without one. The burst-compute
	// ResourceQuota is scoped to the burst-tenant priority class and refuses any
	// pod omitting requests.cpu/memory, so the operator fills in a modest floor
	// when a request names none rather than letting the pod be refused
	// invisibly. A stated envelope is always used as-is.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Input is opaque job input, serialised into the workload environment.
	// +optional
	Input *runtime.RawExtension `json:"input,omitempty"`

	// Output declares where the workload writes its result.
	// +optional
	Output *OutputSpec `json:"output,omitempty"`

	// CallbackURL is called by the result sidecar on terminal state. It MUST be
	// in-cluster: a burst node is an ordinary node of this spoke (ADR-052 §0),
	// so cluster DNS resolves and no egress exception is required.
	// +optional
	CallbackURL string `json:"callbackUrl,omitempty"`

	// TimeoutSeconds bounds the workload's execution, not its wait for capacity.
	// The two clocks are deliberately separate: a job that waited four minutes
	// for a node has not consumed any of its own runtime. See ADR-052 §11.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=600
	// +optional
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`

	// TTLSecondsAfterFinished is how long the EphemeralJob and its Job are
	// retained after reaching a terminal phase.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=3600
	// +optional
	TTLSecondsAfterFinished int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// PlacementClass selects among the placement classes the platform offers
	// (ADR-046 §11). It names a class, never a node property: the operator
	// resolves it to concrete placement, and an unknown class is rejected rather
	// than passed through.
	// +kubebuilder:validation:Enum=burst;home
	// +kubebuilder:default=burst
	// +optional
	PlacementClass string `json:"placementClass,omitempty"`

	// Mode selects the lifecycle, and it is the ONE field that decides whether
	// this request has an end.
	//
	// Job   — batch. Runs to completion, reports a terminal phase, fires
	//         CallbackURL, and is reaped by TTLSecondsAfterFinished.
	// Service — long-lived. There is no completion to wait for: an agent
	//         sandbox serves requests until it goes idle, so it is reaped by
	//         IdleTimeoutSeconds instead and never reports Succeeded.
	//
	// Both modes are authored by this operator, which is the whole point of
	// having one CR. Placement is written here for every pod (ADR-052 §4), so a
	// sandbox can no longer be admitted unplaced onto home-lab capacity because
	// a mutating policy failed to match — the failure mode kyverno-burst-placement
	// documents and which was live on the dev spoke until this existed.
	// +kubebuilder:validation:Enum=Job;Service
	// +kubebuilder:default=Job
	// +optional
	Mode Mode `json:"mode,omitempty"`

	// Ports the workload container listens on. Required for Service mode to be
	// reachable; ignored by Job mode, which nothing connects to.
	// +optional
	Ports []corev1.ContainerPort `json:"ports,omitempty"`

	// WorkingDir for the workload container.
	// +optional
	WorkingDir string `json:"workingDir,omitempty"`

	// ReadinessProbe decides whether the workload is fit to receive traffic.
	//
	// It is what stops a Service routing to a workload that is present but not
	// working. Without one, a container that started and then died leaves a pod
	// that still looks alive — one healthy sidecar is enough for the pod to
	// report Running — and callers get a 502 from an endpoint the platform is
	// still advertising.
	// +optional
	ReadinessProbe *corev1.Probe `json:"readinessProbe,omitempty"`

	// LivenessProbe restarts a workload that is running but wedged.
	// +optional
	LivenessProbe *corev1.Probe `json:"livenessProbe,omitempty"`

	// ImagePullPolicy for the workload container. A sidecar can already state
	// its own, so withholding it from the primary was an asymmetry with no
	// reason behind it.
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// Sidecars run beside the workload in the same pod.
	//
	// corev1.Container is reused deliberately. The ADR-052 §4 guarantee is
	// about PLACEMENT, and placement is a pod-level property — nodeSelector,
	// tolerations, priorityClassName, affinity. None of them is expressible on
	// a container, so accepting a full container here widens what a workload
	// may describe about ITSELF without widening what it may say about WHERE it
	// runs. That distinction is the reason this CR has no podTemplate.
	// +optional
	Sidecars []corev1.Container `json:"sidecars,omitempty"`

	// Volumes available to the workload and its sidecars. When empty the
	// operator supplies its own default pair (workspace, result), so the batch
	// path is unchanged by this field existing.
	// +optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// VolumeMounts for the workload container. Empty means the operator's
	// defaults, matching Volumes above.
	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// ImagePullSecrets for private registries.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Service, when set, gives the workload a stable in-cluster address.
	// +optional
	Service *ServiceSpec `json:"service,omitempty"`

	// IdleTimeoutSeconds reaps a Service-mode workload that has gone quiet.
	//
	// Measured from Status.LastActivityTime, which a client refreshes to say "I
	// am still using this". It is the ONLY thing that stops a Service-mode pod
	// running forever, because unlike Job mode there is no completion — so an
	// unset value in Service mode is rejected rather than defaulted to
	// unlimited.
	// +kubebuilder:validation:Minimum=1
	// +optional
	IdleTimeoutSeconds int32 `json:"idleTimeoutSeconds,omitempty"`

	// ReadinessDeadlineSeconds bounds how long a Service-mode workload may run
	// without becoming ready before it is declared failed.
	//
	// It exists because idleness cannot detect a broken workload. The idle clock
	// is refreshed by ATTEMPTS — a client retrying against a dead sandbox
	// refreshes it on every try — so a workload that never serves is never idle
	// and never reaped. Observed on a sandbox whose harness had been dead for 37
	// minutes with an idle age of 30 seconds, holding a burst node the whole
	// time.
	//
	// Measured from StartTime, so waiting for a node is not counted against it.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=300
	// +optional
	ReadinessDeadlineSeconds int32 `json:"readinessDeadlineSeconds,omitempty"`

	// MaxLifetimeSeconds is the absolute bound on a Service-mode workload,
	// independent of idleness or readiness.
	//
	// The idle clock is refreshable by a client, so it bounds nothing a client
	// can keep touching. This is the bound nothing can extend: burst capacity
	// bills per node-hour, and a workload whose end depends entirely on a
	// cooperative client has no end at all.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=28800
	// +optional
	MaxLifetimeSeconds int32 `json:"maxLifetimeSeconds,omitempty"`

	// TerminationGracePeriodSeconds for the pod.
	// +optional
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`

	// WorkspacePersistence gives the workload a durable /workspace (ADR-052 §14).
	//
	// The fleet states WHICH workspace and nothing else. StorageClass, PVC
	// naming, size and reuse are the operator's, for the same reason placement
	// is (§4): a fleet that could name a StorageClass could place its data on
	// storage it was not granted, and the fleet-facing type is the only place
	// that can be made incapable of saying so.
	// +optional
	WorkspacePersistence *WorkspacePersistenceSpec `json:"workspacePersistence,omitempty"`
}

// WorkspacePersistenceSpec asks for a durable /workspace (ADR-052 §14).
//
// Deliberately one field. Everything else about the volume is a platform
// decision, and each additional knob here would be a way for a fleet to
// contradict one.
type WorkspacePersistenceSpec struct {
	// WorkspaceID identifies the WORKSPACE, not this request.
	//
	// Two EphemeralJobs carrying the same WorkspaceID resolve to the same PVC —
	// that is the point, not a collision to defend against. It is how a
	// workspace outlives the sandbox that created it, and how successive
	// sessions of one app see the same disk.
	//
	// The PVC name is derived from a hash of this rather than from the value
	// itself: a workspace id is caller-chosen and need not be an RFC 1123
	// subdomain, and a name the API server rejects would surface as a 422 on
	// pod creation rather than as anything about the id.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	WorkspaceID string `json:"workspaceId"`
}

// Mode is the workload lifecycle. See EphemeralJobSpec.Mode.
// +kubebuilder:validation:Enum=Job;Service
type Mode string

const (
	// ModeJob runs to completion and reports a terminal phase.
	ModeJob Mode = "Job"
	// ModeService runs until idle. It never reports Succeeded.
	ModeService Mode = "Service"
)

// ServiceSpec asks for a ClusterIP Service in front of the workload.
//
// The operator owns the Service and names it from the EphemeralJob, so a
// workload cannot claim an address belonging to another.
type ServiceSpec struct {
	// Ports exposed by the Service.
	// +kubebuilder:validation:MinItems=1
	Ports []corev1.ServicePort `json:"ports"`

	// Type is ClusterIP in the cluster. NodePort exists for local development,
	// where the client runs outside the cluster and cannot reach a ClusterIP —
	// it is not a production shape and nothing on a spoke should ask for it.
	// +kubebuilder:validation:Enum=ClusterIP;NodePort
	// +kubebuilder:default=ClusterIP
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`
}

// OutputSpec declares the result destination. Credentials are never carried
// here — they are delivered to the sidecar from a platform-rendered Secret
// (ADR-003), so this holds only the location.
type OutputSpec struct {
	// +optional
	ObjectPrefix string `json:"objectPrefix,omitempty"`
}

type EphemeralJobStatus struct {
	// +optional
	Phase Phase `json:"phase,omitempty"`

	// Conditions carries CapacityAvailable and Complete. CapacityAvailable is
	// the projection ADR-052 §7 requires: it reports why a job is not yet
	// running, derived from namespaced objects only (the Pod's PodScheduled
	// condition and the autoscaler's Events on that Pod), so a submitter sees
	// infrastructure progress without holding infrastructure permissions.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// JobName is the batch/v1 Job this request owns. Job mode only — a
	// Service-mode request owns a Pod directly, because a batch/v1 Job exists
	// to drive something to completion and a sandbox has none.
	// +optional
	JobName string `json:"jobName,omitempty"`

	// PodName is the Pod this request owns in Service mode.
	// +optional
	PodName string `json:"podName,omitempty"`

	// ServiceName is the Service fronting the workload, when one was asked for.
	// +optional
	ServiceName string `json:"serviceName,omitempty"`

	// LastActivityTime is the idle clock for Service mode. A client refreshes
	// it to keep the workload alive; IdleTimeoutSeconds is measured from here.
	//
	// It lives in status rather than spec because it is an observation about
	// use, not a declaration of intent — and because a client refreshing it
	// every few seconds must not be able to rewrite the request itself. The
	// SDK is therefore granted patch on the status subresource only.
	// +optional
	LastActivityTime *metav1.Time `json:"lastActivityTime,omitempty"`

	// StartTime is when the workload began executing — not when the request was
	// created. TimeoutSeconds is measured from here.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the workload reached a terminal phase.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// +optional
	ExitCode *int32 `json:"exitCode,omitempty"`

	// Message is a human-readable explanation of the current phase.
	// +optional
	Message string `json:"message,omitempty"`

	// ObservedGeneration is the .metadata.generation this status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true

type EphemeralJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EphemeralJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EphemeralJob{}, &EphemeralJobList{})
}
