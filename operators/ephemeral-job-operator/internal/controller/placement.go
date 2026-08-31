package controller

import (
	corev1 "k8s.io/api/core/v1"
)

// Placement is the concrete node placement for one placement class
// (ADR-046 §11, ADR-052 §4).
//
// These values are the platform's, not the fleet's. They are resolved from a
// class name the fleet may select among, never from fields the fleet supplies,
// and they are written by this operator into the Job it authors. That is the
// whole of the ADR-052 §4 guarantee on the job path: a fleet cannot express
// placement because EphemeralJobSpec has no field carrying it, so there is
// nothing to mutate afterwards and nothing to reject.
type Placement struct {
	NodeSelector      map[string]string
	Tolerations       []corev1.Toleration
	PriorityClassName string
}

// The burst node's identity. These constants MUST match, exactly, the node
// labels and taint the burst worker registers with in
// manifests/providers/hetzner/base/spokepool-clusterclass-v1.yaml, and the
// capacity.cluster-autoscaler.kubernetes.io/{labels,taints} annotations on the
// burst MachineDeployment.
//
// The coupling is a correctness requirement, not documentation. A taint present
// on the real node but absent from the autoscaler's annotations makes the
// autoscaler simulate a node the pending pod cannot tolerate; it concludes
// scale-up will not help, declines to scale, and logs no error. The pod pends
// forever. scripts/validate/preflight/86-burst-node-identity.sh asserts the
// three copies agree, because three hand-maintained strings for one taint is a
// silent-outage generator.
const (
	// Taint: what the burst node repels. Keyed on workload CLASS, not location:
	// the pure-hetzner spoke's general workers also carry
	// workload-location=hetzner and must NOT be tainted, or every workload on
	// that spoke becomes unschedulable.
	BurstTaintKey   = "workload-class"
	BurstTaintValue = "burst"

	// Labels: what the burst node advertises.
	WorkloadLocationKey   = "workload-location"
	WorkloadLocationValue = "hetzner"

	// ADR-046 §11: workload-location is never sufficient alone. Propagated from
	// the MachineDeployment by CAPI — kubelet cannot self-register a
	// node-role.kubernetes.io/* label, NodeRestriction rejects it.
	NodeRoleWorkerKey = "node-role.kubernetes.io/worker"

	BurstPriorityClass = "burst-tenant"
)

// placements maps the classes EphemeralJobSpec.PlacementClass may name.
//
// ADR-046 §11 is explicit that workload-location is never sufficient on its
// own — the worker role label must accompany it, or a workload can land on a
// control-plane node that happens to carry the location label.
var placements = map[string]Placement{
	"burst": {
		NodeSelector: map[string]string{
			WorkloadLocationKey: WorkloadLocationValue,
			NodeRoleWorkerKey:   "",
		},
		Tolerations: []corev1.Toleration{{
			Key:      BurstTaintKey,
			Operator: corev1.TolerationOpEqual,
			Value:    BurstTaintValue,
			Effect:   corev1.TaintEffectNoSchedule,
		}},
		// Below every platform workload (ADR-052 §5), so burst work can never
		// preempt infrastructure. This is also the scope the per-fleet
		// ResourceQuota selects on, so the bound and the preemption order come
		// from the same object.
		PriorityClassName: BurstPriorityClass,
	},
}

// ResolvePlacement returns the placement for a class name. An unknown class is
// an error rather than a pass-through: silently running an unplaced workload
// would put tenant demand on home-lab capacity, which is the outcome ADR-052
// exists to prevent.
func ResolvePlacement(class string) (Placement, bool) {
	if class == "" {
		class = "burst"
	}
	p, ok := placements[class]
	return p, ok
}
