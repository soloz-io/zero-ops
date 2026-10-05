// Package keystore is the boundary between the plugin and whatever holds the key.
//
// AN INTERFACE AND NOT A CLIENT, because what the plugin adds (ADR-100) is the
// identifier semantics, and those must be testable without a key store. The vendor
// plugin's own behaviour -- returning the configured key id verbatim from both paths,
// tracking no version -- is exactly what this platform replaces, and a test that
// needed a live Infisical to demonstrate it would not be run.
package keystore

import (
	"context"
	"fmt"
)

// KeyState is one observation of the key store's view of a key.
type KeyState struct {
	// Key is the store's stable identifier for the key, unchanged by rotation.
	Key string
	// Version is the ACTIVE version. A rotation in the store increments it and the
	// store retains the previous ones so earlier data still unwraps.
	Version int
}

// Store is the subset of a key store this plugin needs: wrap, unwrap, and the ability
// to say which version is currently active.
//
// Wrap and Unwrap take data encryption keys, not Secrets. The DEK never leaves the API
// server in plaintext except to be wrapped here, and the KEK never leaves the store at
// all -- which is the exposure `secretbox` cannot close and the reason ADR-100 exists.
type Store interface {
	// Active reports the key and its active version. Called on an interval; both the
	// status path and the wrap path read a SNAPSHOT of the result rather than calling
	// this themselves, so they cannot observe different versions (ADR-100 acceptance
	// criterion 2).
	Active(ctx context.Context) (KeyState, error)

	// Wrap encrypts a data encryption key under the given version of the key.
	//
	// The version is passed explicitly rather than read from the store inside Wrap. The
	// caller holds a snapshot and wraps under the version it is reporting; letting Wrap
	// resolve "current" itself is precisely how the status and the wrap path come to
	// disagree during a rotation.
	Wrap(ctx context.Context, version int, dek []byte) ([]byte, error)

	// Unwrap decrypts a wrapped data encryption key.
	//
	// The version comes from the stored annotation, not from the active snapshot:
	// unwrapping under PREVIOUS versions must keep working, because the store retains
	// them and data written earlier depends on them. Only the active version is
	// constrained by the no-reactivation rule.
	Unwrap(ctx context.Context, version int, wrapped []byte) ([]byte, error)
}

// FromEnv builds the configured Store, or refuses.
//
// THERE IS NO IMPLEMENTATION YET, AND THIS REFUSES RATHER THAN DEGRADING.
//
// ADR-100's decision is the identifier semantics -- the composite key/version
// identifier, the single snapshot shared by the status and wrap paths, and the
// prohibition on reactivating a version. Those are what the platform adds to a vendor
// plugin that tracks no version at all, they are the parts whose failure is silent,
// and they are implemented and tested.
//
// What is not here is the client that talks to the key store. Wiring it is a separate
// change with its own verification against the deployed key store, and the reason this
// returns an error instead of, say, an in-memory key is that an in-memory key would
// WORK: the plugin would start, the API server would accept it, Secrets would encrypt
// and decrypt, and the key protecting the cluster would live in the plugin's heap --
// which is strictly worse than secretbox and indistinguishable from success.
//
// A plugin the API server depends on must not be startable in a state that only looks
// right, so the gap is a refusal at startup rather than a fallback.
func FromEnv() (Store, error) {
	return nil, fmt.Errorf("no key store is configured: keystore.Store has no implementation " +
		"yet, so this plugin cannot wrap or unwrap data encryption keys. Refusing to start " +
		"rather than serving a provider the API server would depend on (ADR-100)")
}
