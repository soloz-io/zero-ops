// Package keyid derives the identifier the KMS v2 plugin reports for the key it is
// wrapping data encryption keys with.
//
// THIS PACKAGE IS THE WHOLE REASON THE PLATFORM SHIPS A PLUGIN (ADR-100).
//
// The key store and the Kubernetes provider interface disagree about what identity
// means. The store treats a rotation as the SAME key with an incremented version,
// retaining previous versions so older data still unwraps. The interface treats the
// reported identifier as the identity of the EFFECTIVE key: it must change when the
// key changes, stay stable while it does not, and never be reused.
//
// The vendor plugin returns the configured key id verbatim from both its status and
// its encrypt path and tracks no version at all, so a rotation in the store is
// invisible to the API server -- the identifier never changes, the data keys
// established under the old version are kept indefinitely, and the rotation appears
// to succeed while protecting nothing.
//
// So the identifier reported here is COMPOSITE: the key, qualified by its active
// version. Version 2 of a key is a different identifier from version 1, and one that
// has never been seen before.
package keyid

import (
	"fmt"
	"regexp"
	"strings"
)

// Ref is the material an identifier is derived from, and nothing else.
//
// ADR-100's first acceptance criterion: the same key version MUST produce the same
// identifier on every control-plane node and across every restart. It is derived from
// values that do not change -- the cluster, the key, the version -- and from nothing
// else.
//
// It MUST NOT incorporate a timestamp, process state, a random value, or a counter
// held locally. Each of those produces a different identifier for the same key on a
// different node or after a restart, and the interface reads a changed identifier as a
// changed key: the API server would establish new encryption state on every node
// restart, repeatedly, while nothing had rotated. That does not lose data -- material
// written under the previous identifier still unwraps -- it makes the identifier
// meaningless, and with it any ability to tell whether a rotation has happened.
//
// There is deliberately no clock, no hostname, no pid and no counter in this struct.
// A field cannot be read from if it does not exist.
type Ref struct {
	// Cluster scopes the identifier, because ADR-100 gives each cluster its own key
	// and two clusters' version 1 are different keys.
	Cluster string
	// Key is the key store's identifier for the key itself, stable across rotations.
	Key string
	// Version is the ACTIVE version. A rotation increments it.
	Version int
}

// Component characters are constrained so the identifier cannot be ambiguous: a key
// containing the separator would make two different Refs produce the same string.
var safe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// sep separates the components. Chosen because it is excluded from every component by
// the pattern above, so the encoding is injective.
const sep = "/"

// String is the identifier reported to the API server.
//
// Deterministic by construction: same Ref, same string, on any node, at any time.
func (r Ref) String() string {
	return fmt.Sprintf("%s%s%s%sv%d", r.Cluster, sep, r.Key, sep, r.Version)
}

// Validate refuses a Ref that cannot produce a sound identifier.
//
// Refused rather than sanitised. Silently rewriting a key name would make the
// identifier disagree with the key store for the life of the cluster, and the failure
// would surface as data that cannot be unwrapped rather than as a bad name.
func (r Ref) Validate() error {
	var problems []string
	if !safe.MatchString(r.Cluster) {
		problems = append(problems, fmt.Sprintf("cluster %q is empty or contains a character "+
			"outside [A-Za-z0-9._-]", r.Cluster))
	}
	if !safe.MatchString(r.Key) {
		problems = append(problems, fmt.Sprintf("key %q is empty or contains a character "+
			"outside [A-Za-z0-9._-]", r.Key))
	}
	if strings.Contains(r.Cluster, sep) || strings.Contains(r.Key, sep) {
		problems = append(problems, fmt.Sprintf("a component contains the separator %q, which "+
			"would let two different keys produce the same identifier", sep))
	}
	// Version 0 is refused rather than treated as "unset". A key store numbers versions
	// from 1, so 0 means nobody looked -- and reporting an identifier for a version
	// that was never read is the silent failure this package exists to prevent.
	if r.Version < 1 {
		problems = append(problems, fmt.Sprintf("version %d is not a key version; the store "+
			"numbers from 1 and 0 means the active version was never read", r.Version))
	}
	if len(problems) > 0 {
		return fmt.Errorf("refusing to derive a key identifier: %s", strings.Join(problems, "; "))
	}
	return nil
}
