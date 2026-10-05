package active

import (
	"strings"
	"sync"
	"testing"

	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/keyid"
)

func ref(v int) keyid.Ref {
	return keyid.Ref{Cluster: "nutgraf-01", Key: "etcd-kek", Version: v}
}

// ADR-100 acceptance criterion 2: status and wrap never disagree.

func TestStatusAndWrapReadOneSnapshot(t *testing.T) {
	// The criterion: there must be no window in which the status reports one version
	// while the wrap path uses another. Both call Current(), and a Snapshot is a value --
	// so a rotation landing between the two calls cannot make them disagree about the
	// request they are serving.
	h := NewHolder()
	if err := h.Advance(ref(1)); err != nil {
		t.Fatal(err)
	}
	statusView, _ := h.Current()

	// A rotation arrives between the two reads.
	if err := h.Advance(ref(2)); err != nil {
		t.Fatal(err)
	}
	wrapView, _ := h.Current()

	if statusView.ID == wrapView.ID {
		t.Fatal("the snapshot did not advance, so a rotation is invisible")
	}
	// The held snapshot must not have mutated under the holder of it.
	if statusView.Ref.Version != 1 || !strings.HasSuffix(statusView.ID, "/v1") {
		t.Fatalf("a held snapshot changed underneath its holder: %+v -- a single request "+
			"could then report one version and wrap with another", statusView)
	}
}

func TestBeforeTheStoreIsReadNothingIsReported(t *testing.T) {
	// Answering with an empty identifier would tell the API server the plugin is healthy
	// and its effective key is "".
	h := NewHolder()
	if _, err := h.Current(); err == nil {
		t.Fatal("Current() succeeded before any version was read")
	}
}

func TestReReadingAnUnchangedVersionKeepsTheIdentifierStable(t *testing.T) {
	// The other half of the interface's requirement: the identifier must stay stable
	// while the key does not change. A poll that re-observes the same version must not
	// be treated as a rotation.
	h := NewHolder()
	if err := h.Advance(ref(1)); err != nil {
		t.Fatal(err)
	}
	before, _ := h.Current()
	for i := 0; i < 5; i++ {
		if err := h.Advance(ref(1)); err != nil {
			t.Fatalf("re-observing the same version was rejected: %v", err)
		}
	}
	after, _ := h.Current()
	if before.ID != after.ID {
		t.Fatalf("the identifier changed from %q to %q without a rotation", before.ID, after.ID)
	}
}

// ADR-100: a version is never reactivated.

func TestRollingBackToAPreviousVersionIsRefused(t *testing.T) {
	// The interface forbids reusing an identifier, so a reinstated version would be
	// reported under a name it has already used. Recovery is forward, never back.
	h := NewHolder()
	for _, v := range []int{1, 2, 3} {
		if err := h.Advance(ref(v)); err != nil {
			t.Fatal(err)
		}
	}
	err := h.Advance(ref(2))
	if err == nil {
		t.Fatal("rolling back to version 2 was accepted")
	}
	if !strings.Contains(err.Error(), "rolling FORWARD") {
		t.Fatalf("the error does not say what to do instead: %v", err)
	}
	// And the active version is untouched by the refusal.
	cur, _ := h.Current()
	if cur.Ref.Version != 3 {
		t.Fatalf("a refused rollback changed the active version to %d", cur.Ref.Version)
	}
}

func TestAVersionIsNeverReusedEvenAfterMovingPastIt(t *testing.T) {
	// Monotonic is not sufficient on its own: 1 -> 2 -> 1 is caught by the ordering
	// rule, but the `seen` set is what forbids an identifier from EVER being reused,
	// which is what the interface actually requires.
	h := NewHolder()
	if err := h.Advance(ref(5)); err != nil {
		t.Fatal(err)
	}
	if err := h.Advance(ref(5)); err != nil {
		t.Fatalf("re-observing the current version must be a no-op, not an error: %v", err)
	}
	// Advancing to 6 and then back to 5 is refused by ordering; a hypothetical store
	// that reported 5 as new again after 6 is refused by `seen`.
	if err := h.Advance(ref(6)); err != nil {
		t.Fatal(err)
	}
	if err := h.Advance(ref(5)); err == nil {
		t.Fatal("version 5 was made active a second time")
	}
}

func TestTheKeyItselfCannotChangeUnderneathThePlugin(t *testing.T) {
	// A different key is a reconfiguration, not a rotation. Continuing would report an
	// identifier in a lineage the earlier data was never wrapped with.
	h := NewHolder()
	if err := h.Advance(ref(1)); err != nil {
		t.Fatal(err)
	}
	err := h.Advance(keyid.Ref{Cluster: "nutgraf-01", Key: "a-different-kek", Version: 2})
	if err == nil {
		t.Fatal("the key was allowed to change identity")
	}
	if !strings.Contains(err.Error(), "not a rotation") {
		t.Fatalf("the error does not distinguish reconfiguration from rotation: %v", err)
	}
}

func TestABadRefIsRefusedAtTheTransition(t *testing.T) {
	h := NewHolder()
	if err := h.Advance(keyid.Ref{Cluster: "c", Key: "k", Version: 0}); err == nil {
		t.Fatal("a Ref with no observed version was installed")
	}
	if _, err := h.Current(); err == nil {
		t.Fatal("a refused Advance left a snapshot behind")
	}
}

func TestConcurrentReadersNeverSeeAPartialTransition(t *testing.T) {
	// The wrap path runs on every Secret write. Under -race this is what catches a
	// snapshot being assembled in place rather than replaced.
	h := NewHolder()
	if err := h.Advance(ref(1)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				s, err := h.Current()
				if err != nil {
					t.Error(err)
					return
				}
				// The invariant: the ID is always the one derived from the Ref beside it.
				if s.ID != s.Ref.String() {
					t.Errorf("snapshot is internally inconsistent: ID %q but Ref renders %q",
						s.ID, s.Ref.String())
					return
				}
			}
		}()
	}
	for v := 2; v < 40; v++ {
		if err := h.Advance(ref(v)); err != nil {
			t.Error(err)
			break
		}
	}
	wg.Wait()
}
