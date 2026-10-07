// Package keystore is the boundary between the plugin and the key authority.
//
// AN INTERFACE AND NOT A CLIENT, because what the plugin adds is operational
// behaviour -- exact-version encryption, integrity verification, identifier
// semantics -- and that must be testable without a cloud account.
//
// THE AUTHORITY IS GOOGLE CLOUD KMS, platform-owned (ADR-100, and ADR-065's
// Amendment 2 which permits it and states its cost). It was briefly Infisical's CMEK
// API; that is recorded in ADR-100's Context along with the two reasons it is the
// wrong authority for this key -- least privilege being licence-gated, and an encrypt
// endpoint that takes no version.
//
// Every field and behaviour this package depends on was read from the pinned client
// (cloud.google.com/go/kms@v1.35.0) rather than from documentation, which is ADR-097's
// rule:
//
//	EncryptRequest.Name                service.pb.go    -> accepts a CryptoKey OR a
//	                                                       CryptoKeyVersion. Both are
//	                                                       used: the parent to DISCOVER
//	                                                       the active version, an exact
//	                                                       version to wrap under it
//	EncryptResponse.Name               service.pb.go    -> the version that encrypted
//	EncryptRequest.PlaintextCrc32C     service.pb.go    -> integrity, request
//	EncryptResponse.CiphertextCrc32C   service.pb.go    -> integrity, response
//	EncryptResponse.VerifiedPlaintextCrc32C             -> did the server check ours
//	DecryptRequest.CiphertextCrc32C    service.pb.go
//	DecryptResponse.PlaintextCrc32C    service.pb.go
package keystore

import (
	"context"
	"os"
)

// KeyVersion is one observation of the version Cloud KMS reports as PRIMARY.
type KeyVersion struct {
	// Name is the full CryptoKeyVersion resource name:
	//
	//	projects/P/locations/L/keyRings/R/cryptoKeys/K/cryptoKeyVersions/N
	//
	// THE WHOLE NAME, not the trailing number, because this is what becomes the
	// KMS v2 key_id. It already carries project, location, key ring and key, so it
	// is globally unique without the plugin composing an identifier of its own --
	// which is why internal/keyid was deleted rather than kept.
	Name string
	// Number is the trailing version number.
	//
	// IT IS NOT PART OF THE IDENTIFIER and must not be used as one. It exists for a
	// single purpose: ordering, so a primary version that moves BACKWARDS can be
	// reported in the log. See ADR-100 on why that is a warning here and an invariant
	// of the rotation authority rather than a refusal in this process.
	Number int
}

// Store is the subset of a key store this plugin needs.
//
// Wrap and Unwrap take data encryption keys, not Secrets. The DEK never leaves the API
// server in plaintext except to be wrapped here, and the KEK never leaves the store at
// all -- which is the exposure `secretbox` cannot close and the reason ADR-100 exists.
type Store interface {
	// Active reports the version currently PRIMARY for encryption.
	//
	// HOW is the implementation's business, and the GCP implementation deliberately
	// does NOT read CryptoKey.Primary: that needs `cloudkms.cryptoKeys.get`, which the
	// one role the plugin's identity holds does not carry. It encrypts a fixed
	// constant against the parent key and reads EncryptResponse.Name, which also means
	// the answer comes from the crypto path rather than from a metadata view that can
	// be ahead of it. See gcpkms.go.
	//
	// It is also the health probe: if this call cannot complete, the plugin has
	// nothing honest to report as healthy.
	Active(ctx context.Context) (KeyVersion, error)

	// Wrap encrypts a data encryption key UNDER THE VERSION IT IS TOLD TO USE.
	//
	// THE VERSION IS AN ARGUMENT, AND THAT IS THE WHOLE POINT. An earlier version of
	// this interface encrypted against the parent CryptoKey and let Cloud KMS select
	// the primary, on the reasoning that naming a version would pin writes to it and
	// make a rotation invisible. That reasoning was wrong: the plugin reads the primary
	// itself, so naming THAT version still follows a rotation.
	//
	// What it removes is a race the Kubernetes contract does not forgive. Changing a
	// key's primary version is eventually consistent, so two independent calls can
	// disagree: the status path reads primary N while an encrypt against the parent
	// lands on N-1. KMS v2 requires the key_id reported by Status to equal the key_id
	// returned by Encrypt -- k8s.io/apiserver encryptionconfig/config.go:424 gates DEK
	// rotation on exactly that equality and errors out otherwise, marking the provider
	// unhealthy. Against the parent key that disagreement persists for the whole
	// propagation window. Naming the version collapses the two reads into one and the
	// window disappears.
	//
	// usedName is EncryptResponse.Name, returned rather than assumed. Cloud KMS's own
	// documentation on that field: "Check this field to verify that the intended
	// resource was used for encryption." With an exact version requested it must equal
	// versionName, and an implementation is expected to refuse if it does not.
	Wrap(ctx context.Context, dek []byte, versionName string) (ciphertext []byte, usedName string, err error)

	// Unwrap decrypts a wrapped data encryption key.
	//
	// NO VERSION ARGUMENT. Cloud KMS resolves the version from the ciphertext itself
	// and retains non-destroyed versions for decryption, so objects written before any
	// number of rotations keep unwrapping without the plugin tracking which version
	// each one used.
	Unwrap(ctx context.Context, wrapped []byte) ([]byte, error)
}

// FromEnv builds the configured Store from the environment.
//
// IT REFUSES RATHER THAN DEGRADING when configuration is missing. There is no
// in-memory fallback, and that is deliberate: an in-memory key would WORK -- the
// plugin would start, the API server would accept it, Secrets would encrypt and
// decrypt, and the key protecting the cluster would live in the plugin's heap.
// Strictly worse than secretbox and indistinguishable from success. A provider the
// API server depends on must not be startable in a state that only looks right.
func FromEnv() (Store, error) {
	return NewGCPKMS(GCPKMSOptions{CryptoKey: os.Getenv("KMS_CRYPTO_KEY")})
}
