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

	// Resources is the workload's requested envelope. It is bounded by the
	// fleet's priority-scoped ResourceQuota (ADR-052 §5), which the API server
	// enforces at admission of the Pod.
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
	// +kubebuilder:validation:Enum=burst
	// +kubebuilder:default=burst
	// +optional
	PlacementClass string `json:"placementClass,omitempty"`
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

	// JobName is the batch/v1 Job this request owns.
	// +optional
	JobName string `json:"jobName,omitempty"`

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
