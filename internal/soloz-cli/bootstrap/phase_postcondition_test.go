package bootstrap

import (
	"errors"
	"testing"

	"github.com/soloz-io/zero-ops/internal/soloz-cli/state"
)

// The defect this mechanism exists for: a phase is recorded complete, the
// assertions that define "complete" are strengthened afterwards, and every later
// run skips the phase on the strength of a record written before the assertion
// existed. Boundary 04 sat in exactly that state -- recorded done twenty-four
// phases deep, while the identity provider had never once served.
func TestACompletedPhaseWithAFailingPostconditionRunsAgain(t *testing.T) {
	o := &Orchestrator{}
	mgr := state.NewTenantStateManager(t.TempDir(), "acme-hub")
	bs := &state.BootstrapState{
		ClusterName:     "acme-hub",
		CompletedPhases: []state.BootstrapPhase{state.PhaseBoundary04},
	}

	ran := false
	err := o.runPhase(t.Context(), mgr, bs, state.PhaseBoundary04, "boundary04", "",
		func() error { ran = true; return nil },
		nil,
		withPostcondition(func() error { return errors.New("no ready replica") }),
	)
	if err != nil {
		t.Fatalf("re-running the phase failed: %v", err)
	}
	if !ran {
		t.Error("a completed phase whose postcondition does not hold was skipped; " +
			"the completion record was trusted over the state of the cluster")
	}
}

// The complement, and the reason the postcondition must be a cheap probe: when
// it holds, resume stays a no-op. A postcondition that re-did the work would
// make every resume pay for every phase.
func TestACompletedPhaseWithAHoldingPostconditionIsSkipped(t *testing.T) {
	o := &Orchestrator{}
	mgr := state.NewTenantStateManager(t.TempDir(), "acme-hub")
	bs := &state.BootstrapState{
		ClusterName:     "acme-hub",
		CompletedPhases: []state.BootstrapPhase{state.PhaseBoundary04},
	}

	ran := false
	err := o.runPhase(t.Context(), mgr, bs, state.PhaseBoundary04, "boundary04", "",
		func() error { ran = true; return nil },
		nil,
		withPostcondition(func() error { return nil }),
	)
	if err != nil {
		t.Fatalf("skipping the phase failed: %v", err)
	}
	if ran {
		t.Error("a phase whose postcondition holds was run again")
	}
}

// Adoption is per phase. Every other phase passes no postcondition and must keep
// its existing behaviour exactly, or this change is a rewrite of the state
// machine rather than an addition to it.
func TestAPhaseWithoutAPostconditionIsStillSkippedWhenComplete(t *testing.T) {
	o := &Orchestrator{}
	mgr := state.NewTenantStateManager(t.TempDir(), "acme-hub")
	bs := &state.BootstrapState{
		ClusterName:     "acme-hub",
		CompletedPhases: []state.BootstrapPhase{state.PhaseBoundary04},
	}

	ran := false
	if err := o.runPhase(t.Context(), mgr, bs, state.PhaseBoundary04, "boundary04", "",
		func() error { ran = true; return nil }, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ran {
		t.Error("a completed phase with no declared postcondition was re-run")
	}
}

// The outermost claim is the one that shut the door: `complete` was accepted
// without being checked, so the box could not be repaired by the tool that
// built it. Withdrawing `complete` is what lets the ordinary resume path reach
// the phase at all.
func TestWithdrawCompletionClearsCompleteAndTheFailingPhase(t *testing.T) {
	bs := &state.BootstrapState{
		CompletedPhases: []state.BootstrapPhase{
			state.PhaseBoundary03,
			state.PhaseBoundary04,
			state.PhaseBoundary05,
			state.PhaseComplete,
		},
		CurrentPhase: state.PhaseComplete,
	}
	withdrawCompletion(bs, map[state.BootstrapPhase]error{
		state.PhaseBoundary04: errors.New("no ready replica"),
	})

	for _, gone := range []state.BootstrapPhase{state.PhaseComplete, state.PhaseBoundary04} {
		for _, p := range bs.CompletedPhases {
			if p == gone {
				t.Errorf("%s is still recorded complete", gone)
			}
		}
	}
	// Phases that DID hold keep their records. Withdrawing them would re-run
	// hours of work to repair one phase.
	var kept int
	for _, p := range bs.CompletedPhases {
		if p == state.PhaseBoundary03 || p == state.PhaseBoundary05 {
			kept++
		}
	}
	if kept != 2 {
		t.Errorf("phases whose postconditions were never in question were withdrawn: %v",
			bs.CompletedPhases)
	}
}

// A phase that was never completed is not a phase whose postcondition failed.
// Reporting one would make a first bootstrap look like a broken one.
func TestUnmetPostconditionsIgnoresPhasesThatNeverRan(t *testing.T) {
	o := &Orchestrator{}
	bs := &state.BootstrapState{CompletedPhases: []state.BootstrapPhase{}}
	if got := o.unmetPostconditions(t.Context(), bs, "/nonexistent-kubeconfig"); len(got) != 0 {
		t.Errorf("a phase that never ran was reported as unmet: %v", got)
	}
}
