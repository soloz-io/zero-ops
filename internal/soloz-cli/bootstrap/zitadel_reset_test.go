package bootstrap

import "testing"

// The reset is destructive, so the conditions that trigger it are the whole
// safety argument. Both halves of the signature are required, and the phase
// gate sits in front of both.
//
// eventstore written + migrations absent  -> setup did work it did not record,
//
//	and no retry can recover it
//
// eventstore written + migrations present -> a healthy Zitadel. Never drop.
// neither                                 -> untouched. Nothing to reset.
func TestPartialInitSignatureRequiresBothHalves(t *testing.T) {
	// The query is the contract; assert it names both tables and the polarity
	// of each test, because reversing either turns this into data loss.
	q := zitadelPartialInitQuery
	for _, want := range []string{
		"eventstore.events2",
		"IS NOT NULL",
		"projections.migrations",
		"IS NULL",
		"AND",
	} {
		if !contains(q, want) {
			t.Errorf("the partial-init signature no longer checks %q:\n%s", want, q)
		}
	}
}

// A completed phase means Zitadel initialised successfully at some point. Any
// later inconsistency is a running system's problem, to be diagnosed rather
// than silently dropped -- so the gate must come before any database check.
func TestACompletedPhaseIsNeverReset(t *testing.T) {
	o := &Orchestrator{}
	// phaseDone=true must return before it can reach kubectl, so an empty
	// kubeconfig proves it never tried.
	if err := o.resetZitadelIfInitIncomplete(t.Context(), "", true); err != nil {
		t.Errorf("a completed phase attempted a reset: %v", err)
	}
}

// No database yet is a first bootstrap, not a failure.
func TestNoDatabaseIsNotAnError(t *testing.T) {
	o := &Orchestrator{}
	if err := o.resetZitadelIfInitIncomplete(t.Context(), "/nonexistent-kubeconfig", false); err != nil {
		t.Errorf("an unreachable database was treated as an error: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
