// Package active holds the one observation of the key store's active version that
// both the status path and the wrap path read.
//
// ADR-100's second acceptance criterion: there MUST be no window in which the status
// reports one version while the wrap path uses another. The interface treats that
// disagreement as an unhealthy plugin, and it is right to -- an API server told the
// effective key is one thing while its data is wrapped with another cannot reason
// about what a rotation has achieved.
//
// So the two paths do not each ask the key store. They read ONE snapshot, and moving
// to the next version is a controlled transition of that snapshot rather than two
// independent observations. This matters most during a rotation on a multi-node
// control plane, which is exactly when two observations would land at different
// moments.
package active

import (
	"fmt"
	"sync"

	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/keyid"
)

// Snapshot is an immutable observation of the active key version.
//
// Immutable on purpose: a caller that holds one cannot be overtaken mid-request by a
// rotation, so a single Encrypt reports and uses the same version even if the store
// moves underneath it. The next request sees the new one.
type Snapshot struct {
	Ref keyid.Ref
	// ID is derived once, when the snapshot is taken, so the status path and the wrap
	// path cannot derive it differently.
	ID string
}

// Holder owns the current snapshot and the rules for replacing it.
type Holder struct {
	mu   sync.RWMutex
	snap *Snapshot
	// seen records every version ever reported as active, which is what makes the
	// no-reactivation rule enforceable rather than aspirational.
	seen map[int]bool
}

func NewHolder() *Holder {
	return &Holder{seen: map[int]bool{}}
}

// Current returns the snapshot both paths read, or an error before one exists.
//
// An error rather than a zero value. A plugin that has not yet read the key store does
// not know the effective key, and answering Status with an empty identifier would tell
// the API server the plugin is healthy and its key is "".
func (h *Holder) Current() (Snapshot, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.snap == nil {
		return Snapshot{}, fmt.Errorf("no active key version has been read from the key store yet; " +
			"the plugin cannot report an effective key it has not observed")
	}
	return *h.snap, nil
}

// Advance installs a new active version, and is the only way the snapshot changes.
//
// THREE RULES, each of which exists because breaking it is silent:
//
//  1. The Ref must be derivable into a sound identifier. Checked here so a bad key
//     name fails at the transition rather than on the next wrap.
//
//  2. The version must not go BACKWARDS or repeat. ADR-100 prohibits reactivating a
//     previous version: the interface forbids reusing an identifier, so a reinstated
//     version would have to be reported under a name it has already used -- or under a
//     new name for old material, which makes the identifier a lie. Recovering from a
//     bad rotation is rolling FORWARD to a new version, never back to an old one.
//     Unwrapping under previous versions continues, because the store retains them and
//     data written earlier depends on them; only the ACTIVE version is constrained.
//
//  3. The cluster and key must not change once set. A plugin whose key changed
//     identity underneath it is misconfigured, not rotated, and continuing would
//     report an identifier for a key the earlier data was never wrapped with.
func (h *Holder) Advance(ref keyid.Ref) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.snap != nil {
		prev := h.snap.Ref
		if ref.Cluster != prev.Cluster || ref.Key != prev.Key {
			return fmt.Errorf("the key changed identity from %s/%s to %s/%s; that is a "+
				"reconfiguration, not a rotation, and data already wrapped under the old key "+
				"would be unwrappable under an identifier that claims to be the same lineage",
				prev.Cluster, prev.Key, ref.Cluster, ref.Key)
		}
		if ref.Version == prev.Version {
			// Not an error: the store was re-read and nothing had changed. The snapshot
			// stays as it is, which keeps the identifier stable while the key does not
			// change -- the other half of the interface's requirement.
			return nil
		}
		if ref.Version < prev.Version {
			return fmt.Errorf("refusing to make version %d active after %d: ADR-100 prohibits "+
				"reactivating a previous version, because the interface forbids reusing an "+
				"identifier. Recover by rolling FORWARD to a new version",
				ref.Version, prev.Version)
		}
	}
	if h.seen[ref.Version] {
		return fmt.Errorf("refusing to make version %d active again: it has been the active "+
			"version before, and an identifier may never be reused", ref.Version)
	}

	h.snap = &Snapshot{Ref: ref, ID: ref.String()}
	h.seen[ref.Version] = true
	return nil
}
