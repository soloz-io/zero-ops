package controller

import (
	"reflect"
	"strings"
	"testing"

	computev1alpha1 "github.com/soloz-io/zero-ops/operators/ephemeral-job-operator/api/v1alpha1"
)

// TestSpecCannotExpressPlacement is the falsifiable form of the ADR-052 §4
// guarantee on the job path: a fleet cannot escape burst placement onto
// home-lab capacity because the request type has no field carrying placement.
//
// If someone later adds a NodeSelector, Tolerations, PriorityClassName or
// Affinity field to EphemeralJobSpec, this test fails and the guarantee is gone
// — which is the point. It is checked here rather than left to review because
// the failure mode is silent: tenant workloads would simply start landing on
// home-lab nodes, which is the outcome the whole ADR exists to prevent.
func TestSpecCannotExpressPlacement(t *testing.T) {
	forbidden := []string{"nodeselector", "toleration", "priorityclass", "affinity", "nodename", "runtimeclass"}

	st := reflect.TypeOf(computev1alpha1.EphemeralJobSpec{})
	for i := 0; i < st.NumField(); i++ {
		name := strings.ToLower(st.Field(i).Name)
		tag := strings.ToLower(st.Field(i).Tag.Get("json"))
		for _, f := range forbidden {
			if strings.Contains(name, f) || strings.Contains(tag, f) {
				t.Errorf("EphemeralJobSpec.%s exposes placement to the fleet; placement must be platform-supplied (ADR-052 §4)",
					st.Field(i).Name)
			}
		}
	}
}

// TestBurstPlacementIsComplete guards the ADR-046 §11 rule that
// workload-location is never sufficient on its own: without the worker role
// label a burst pod can land on a control-plane node that happens to carry the
// location label.
func TestBurstPlacementIsComplete(t *testing.T) {
	p, ok := ResolvePlacement("burst")
	if !ok {
		t.Fatal("burst placement class must resolve")
	}
	if p.NodeSelector[WorkloadLocationKey] != WorkloadLocationValue {
		t.Errorf("burst placement must select workload-location=%s, got %q",
			WorkloadLocationValue, p.NodeSelector[WorkloadLocationKey])
	}
	if _, ok := p.NodeSelector[NodeRoleWorkerKey]; !ok {
		t.Errorf("burst placement must also select %s (ADR-046 §11: workload-location is never sufficient alone)",
			NodeRoleWorkerKey)
	}
	if len(p.Tolerations) == 0 {
		t.Error("burst placement must tolerate the burst pool taint, or its pods never schedule")
	}
	if p.PriorityClassName != BurstPriorityClass {
		t.Errorf("burst placement must carry the %s PriorityClass: it is both the preemption order and the ResourceQuota scope (ADR-052 §5), got %q",
			BurstPriorityClass, p.PriorityClassName)
	}
}

// TestUnknownPlacementClassIsRejected: an unresolvable class must be an error,
// never a pass-through. Silently emitting a Job with no placement would put
// tenant demand on home-lab capacity.
func TestUnknownPlacementClassIsRejected(t *testing.T) {
	if _, ok := ResolvePlacement("home"); ok {
		t.Error(`"home" must not be a selectable placement class for tenant jobs (ADR-052 §10)`)
	}
	if _, ok := ResolvePlacement("nonsense"); ok {
		t.Error("unknown placement class must be rejected, not passed through")
	}
}

// TestEmptyPlacementClassDefaultsToBurst: an EphemeralJob created before the
// CRD default applied, or by a client that omits the field, must still be
// placed on burst capacity rather than left unplaced.
func TestEmptyPlacementClassDefaultsToBurst(t *testing.T) {
	p, ok := ResolvePlacement("")
	if !ok {
		t.Fatal("empty placement class must default to burst, not fail open")
	}
	if p.NodeSelector[WorkloadLocationKey] != WorkloadLocationValue {
		t.Error("default placement must be burst")
	}
}
