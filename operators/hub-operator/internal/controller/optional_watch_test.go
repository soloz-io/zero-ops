package controller

import (
	"os"
	"strings"
	"testing"
)

// An optional watch must never gate startup.
//
// Registering a watch on a kind whose CRD is absent makes controller-runtime
// wait for a cache that can never sync, and the manager exits with
// "timed out waiting for cache to be synced". That turned an optional capability
// into a hard prerequisite and deadlocked two boundaries: the SpokeMachineIdentity
// CRD ships in boundary 04, this operator runs in boundary 03, and boundary 04
// does not open until this operator has provisioned the database roles it never
// reached — surfacing two boundaries later as Zitadel unable to authenticate.
func TestTheOptionalWatchIsGuardedByCRDPresence(t *testing.T) {
	src, err := os.ReadFile("spokepool_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func (r *SpokePoolReconciler) SetupWithManager(")
	if start < 0 {
		t.Fatal("SetupWithManager is gone")
	}
	fn := body[start:]
	fn = fn[:strings.Index(fn, "\n}\n")]

	if !strings.Contains(fn, "kindIsInstalled(mgr, smiGVK)") {
		t.Error("SpokeMachineIdentity is watched unconditionally; its CRD ships in a " +
			"later boundary, so its absence stops this controller starting at all")
	}
	// Certificate must stay unconditional: cert-manager is boundary 01
	// infrastructure, and marking a genuinely required kind optional hides a
	// broken cluster.
	if strings.Contains(fn, "kindIsInstalled(mgr, certGVK)") {
		t.Error("Certificate has been made optional; cert-manager is a hard " +
			"prerequisite and its absence should be loud")
	}
}

// Presence is asked of the RESTMapper, not inferred from a failed list: a list
// can fail for reasons unrelated to the CRD, and reading those as "not
// installed" silently drops a watch that should exist.
func TestCRDPresenceIsAskedOfTheRESTMapper(t *testing.T) {
	src, err := os.ReadFile("spokepool_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func kindIsInstalled(")
	if start < 0 {
		t.Fatal("kindIsInstalled is gone")
	}
	fn := body[start:]
	fn = fn[:strings.Index(fn, "\n}\n")]
	if !strings.Contains(fn, "RESTMapping") {
		t.Error("CRD presence is no longer determined from the RESTMapper")
	}
}

// A CRD that appears later must be picked up. controller-runtime fixes a
// controller's watches when it is built, so the only honest mechanism is to end
// the manager and let Kubernetes restart it — which requires returning an ERROR,
// because a runnable returning nil just ends its own goroutine and leaves the
// controller permanently blind to the new kind.
func TestALaterCRDCausesARestartRatherThanASilentNoop(t *testing.T) {
	src, err := os.ReadFile("spokepool_controller.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func awaitOptionalCRD(")
	if start < 0 {
		t.Fatal("awaitOptionalCRD is gone; a CRD installed later would never be watched")
	}
	fn := body[start:]
	fn = fn[:strings.Index(fn, "\n}\n")]
	if !strings.Contains(fn, "return fmt.Errorf(") {
		t.Error("the watcher does not end the manager when the CRD appears; returning " +
			"nil leaves the controller running without the watch, for ever")
	}
}
