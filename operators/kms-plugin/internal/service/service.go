// Package service implements the Kubernetes KMS v2 provider interface.
//
// ADAPTED FROM GOOGLE'S PLUGIN, not invented here.
//
// Upstream is github.com/GoogleCloudPlatform/k8s-cloudkms-plugin (Apache-2.0), and the
// behaviour below follows its plugin/v2 package at commit
// a88bafe6cfcc8727e6a7243dfa16f978b1c9a468. Specifically taken from it: the cached
// key_id that keeps Status answerable while Cloud KMS is unreachable, the operator
// decryption being addressed to the parent key rather than a version. Upstream's
// --key-suffix was taken and then DELETED: see deriveKeyID.
//
// WHAT DIVERGES FROM UPSTREAM, and why, in one place so the fork stays reviewable:
//
//  1. Identity. Upstream obtains credentials from google.DefaultClient and the GCE
//     metadata server (plugin/http_client.go). These nodes are Hetzner; there is no
//     metadata server. See internal/keystore.
//
//  2. Exact-version encryption. Upstream encrypts against the parent CryptoKey and
//     reports whatever version came back. See Encrypt below.
//
//  3. Integrity. Upstream performs no CRC32C verification on any of the four paths.
//     See internal/keystore.
//
//  4. Status freshness. Upstream reports the key_id it cached BEFORE its probe and
//     updates the cache afterwards, so its first Status after a rotation disagrees with
//     Encrypt. See Status below.
//
//  5. Protocol version. Upstream advertises "v2beta1"; this advertises "v2". Both are
//     accepted (k8s.io/kms@v0.31.6 apis/v2/api.proto:37, and the validator at
//     k8s.io/apiserver encryptionconfig/config.go:496) and "v2" is the recommended one.
//
//  6. Generated types. Upstream vendors its own copy of the KMS v2 proto in
//     plugin/v2/api.pb.go. This module pins k8s.io/kms to the version the control plane
//     actually runs, which is the reason it is a separate Go module at all -- see
//     go.mod. Taking upstream's copy would mean two generated copies of one proto, one
//     of them not matched to the API server it talks to.
//
//  7. Image and runtime hardening. Upstream's Dockerfile is `FROM alpine:latest` with
//     no USER. See operators/kms-plugin/Dockerfile and deploy/static-pod.yaml.
package service

import (
	"context"
	"fmt"
	"log"
	"sync"

	kmsv2 "k8s.io/kms/apis/v2"

	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/keystore"
	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/metrics"
)

// annotationVersion carries the key version a data encryption key was wrapped under,
// stored beside the ciphertext by the API server and handed back on Decrypt.
//
// IT IS AN AUDIT TRAIL AND NOT A DEPENDENCY. Cloud KMS resolves the version from the
// ciphertext itself, so Decrypt does not need it; it is recorded because it is how a
// rotation can be SHOWN to have taken effect on data, and it is read back only to
// improve an error message. ADR-100's rotation evidence is this annotation.
//
// The key must be a fully qualified domain name per the interface's annotation rules.
const annotationVersion = "version.kms.soloz.io"

const (
	healthOK = "ok"
	// Upstream's wording, kept deliberately: an operator searching for the message in
	// one plugin's logs should find the other.
	keyNotReachable = "Cloud KMS key is not reachable"
)

// Service is the KMS v2 server.
//
// It holds the last observed key version and never consults the key authority on the
// Status path, which is what keeps Status and Encrypt from reading the primary
// independently. See Encrypt.
type Service struct {
	kmsv2.UnimplementedKeyManagementServiceServer

	store keystore.Store

	mu sync.RWMutex
	// observed is the last version read from the key authority, nil until the first
	// successful read.
	observed *keystore.KeyVersion
	// keyID is the identifier derived from observed. Derived once, when the observation
	// is installed, so the Status path and the Encrypt path cannot derive it
	// differently.
	keyID string
	// highestSeen supports a WARNING ONLY. A primary version that moves backwards is a
	// reactivation, which ADR-100 prohibits; this process cannot enforce that -- the
	// record does not survive a restart -- but when it does happen to hold the evidence
	// it says so rather than staying silent.
	highestSeen int
}

func New(store keystore.Store) *Service {
	return &Service{store: store}
}

// Refresh observes the key authority and installs the observation.
//
// Called on an interval by the process, NOT from Status or Encrypt. That is the point:
// both of those read ONE observation, so they cannot report different versions, and
// Encrypt names the observed version explicitly rather than letting Cloud KMS pick
// again. A failure here leaves the previous observation in place -- see Status.
func (s *Service) Refresh(ctx context.Context) error {
	kv, err := s.store.Active(ctx)
	if err != nil {
		return fmt.Errorf("reading the active key version: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.observed != nil && s.observed.Name == kv.Name {
		// Nothing changed. The identifier must stay stable while the key does not,
		// or the API server would read every poll as a rotation.
		return nil
	}
	if kv.Number < s.highestSeen {
		// NOT REFUSED. See the field comment: enforcement here would be enforcement
		// that silently lapses on restart, which is worse than an honest warning.
		log.Printf("WARNING: %s is now the primary version, and version %d has been primary "+
			"before in this process. ADR-100 prohibits reactivating a previous key version, "+
			"and the reported identifier for this material is one the API server has "+
			"ALREADY SEEN -- which the interface forbids. This process cannot prevent it; "+
			"the rotation authority is the only identity permitted to change the primary "+
			"version, and ADR-100 records the audited recovery workflow.",
			kv.Name, s.highestSeen)
	}
	if kv.Number > s.highestSeen {
		s.highestSeen = kv.Number
	}

	v := kv
	s.observed = &v
	s.keyID = s.deriveKeyID(kv.Name)
	// THE GAUGE IS THE IN-CLUSTER HALF OF THE NO-REACTIVATION SIGNAL. This process cannot
	// enforce the rule -- its memory does not survive a restart, which is why that guard
	// was removed -- but a gauge that DECREASES is an alert expression anybody can write,
	// and it is visible between the scheduled runs of the out-of-band check. Neither
	// signal is sufficient alone: this one is blind across a restart, the Git version
	// floor is blind between runs.
	metrics.ActiveKeyVersion.Set(float64(kv.Number))
	log.Printf("active key version is %s (key_id reported to the API server: %s)",
		kv.Name, s.keyID)
	return nil
}

// deriveKeyID builds the KMS v2 key_id.
//
// IT RETURNS THE CRYPTOKEYVERSION RESOURCE NAME, UNCHANGED. There is no second branch,
// and the absence of one is the decision.
//
// THREE DESIGNS ADDED SOMETHING TO THIS AND ALL THREE ARE DELETED. First a composite
// built from the cluster name, key name and version number. Then that resource name plus
// a cluster-name suffix. Then the suffix kept as a disaster-recovery escape hatch,
// defaulting to empty.
//
// The last one is the instructive deletion. It was defended as break-glass: if a primary
// version were ever regressed out of band, reinstated material would otherwise report an
// identifier the API server has already seen, and the suffix was the only way to make it
// fresh. That is true, and it is still not a reason to ship the flag -- because a
// break-glass path that is simply AVAILABLE is the manual identifier mutation this design
// removed, wearing a different name. Anything that can be reached by adding one argument
// gets reached.
//
// So there is no flag. If that recovery is ever needed it is a separately authorised,
// audited platform workflow, and ADR-100 records it as one -- not a thing a plugin
// accepts on its command line.
//
// The function survives, trivial, because it is the ONE place the identifier is produced
// and the preflight asserts it is a pure function of its argument. A derivation with
// nowhere to live is a derivation that grows a second one.
func (s *Service) deriveKeyID(versionName string) string {
	return versionName
}

// current returns the installed observation, or an error before one exists.
func (s *Service) current() (keystore.KeyVersion, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.observed == nil {
		return keystore.KeyVersion{}, "", fmt.Errorf("no active key version has been read from " +
			"Cloud KMS yet; the plugin cannot report an effective key it has not observed")
	}
	return *s.observed, s.keyID, nil
}

// Status reports the plugin's health and the identifier of the effective key.
//
// IT REPORTS THE LAST KNOWN KEY ID EVEN WHEN CLOUD KMS IS UNREACHABLE, and that is
// upstream's insight rather than ours. The API server validates the identifier
// (encryptionconfig/config.go:508) and treats a CHANGE in it as a key rotation: an
// empty or absent identifier during a transient Cloud KMS outage would therefore look
// like a new key and set the API server rotating DEKs over a blip. So an outage is
// reported as an outage -- unhealthy, with the identifier unchanged.
//
// Returning an error instead is reserved for the one case where there is nothing honest
// to say: the plugin has never successfully read the key, so it has no identifier at
// all and must not invent one.
func (s *Service) Status(ctx context.Context, _ *kmsv2.StatusRequest) (*kmsv2.StatusResponse, error) {
	// THE PROBE IS A REAL READ, ON THE REQUEST'S CONTEXT. `healthz: ok` has to mean the
	// key is reachable NOW; answering from a cached observation would report health for
	// a key authority that went away, and the API server would keep writing until a cold
	// read failed. Upstream probes on every Status for the same reason (it encrypts a
	// fixed "ping" plaintext); reading CryptoKey.Primary does it with a call the plugin
	// already needs and without writing anything.
	//
	// UPSTREAM REPORTS THE PRE-PROBE IDENTIFIER AND UPDATES ITS CACHE AFTERWARDS, which
	// means its first Status after a rotation returns the OLD key_id while Encrypt has
	// already moved to the new one. The API server gates DEK rotation on those two being
	// equal (encryptionconfig/config.go:424) and errors out when they are not, marking
	// the provider unhealthy for a poll interval on every rotation. Installing the
	// observation BEFORE answering removes that.
	healthz := healthOK
	if err := s.Refresh(ctx); err != nil {
		log.Printf("status probe: %v", err)
		healthz = keyNotReachable
		metrics.Healthy.Set(0)
	} else {
		metrics.Healthy.Set(1)
	}

	s.mu.RLock()
	observed, keyID := s.observed, s.keyID
	s.mu.RUnlock()

	if observed == nil {
		// Nothing honest to say: the plugin has never successfully read the key, so it
		// has no identifier and must not invent one. An empty key_id is rejected by the
		// API server's own validation (encryptionconfig/config.go:508).
		return nil, fmt.Errorf("no active key version has been read from Cloud KMS yet; the "+
			"plugin cannot report an effective key it has not observed: %s", healthz)
	}

	return &kmsv2.StatusResponse{
		Version: "v2",
		Healthz: healthz,
		KeyId:   keyID,
	}, nil
}

// Encrypt wraps a data encryption key under the version this plugin is reporting.
//
// ONE OBSERVATION SUPPLIES BOTH THE KEY ID AND THE VERSION THAT WRAPS, and the version
// is named explicitly in the request rather than left to Cloud KMS. That is what makes
// the two agree.
//
// The alternative -- encrypting against the parent CryptoKey, as upstream does -- has
// the status path and the encrypt path each read the primary version independently.
// Changing a key's primary version is eventually consistent, so those two reads can
// return different versions, and KMS v2 does not forgive that: the API server requires
// the key_id from Status to equal the key_id from Encrypt
// (encryptionconfig/config.go:424) and marks the provider unhealthy otherwise. Naming
// the observed version collapses the two reads into one.
func (s *Service) Encrypt(ctx context.Context, req *kmsv2.EncryptRequest) (*kmsv2.EncryptResponse, error) {
	observed, keyID, err := s.current()
	if err != nil {
		return nil, err
	}

	wrapped, usedName, err := s.store.Wrap(ctx, req.Plaintext, observed.Name)
	if err != nil {
		return nil, fmt.Errorf("wrapping the data encryption key under %s: %w", observed.Name, err)
	}

	// The store already refuses a response that names a different version; this is the
	// same invariant stated where the key_id is produced, because the cost of being
	// wrong here is a Secret labelled with material that did not encrypt it.
	if usedName != observed.Name {
		return nil, fmt.Errorf("encrypted under %s while reporting %s; nothing is stored under "+
			"an identifier that does not name the material that produced it",
			usedName, observed.Name)
	}

	return &kmsv2.EncryptResponse{
		Ciphertext: wrapped,
		KeyId:      keyID,
		Annotations: map[string][]byte{
			annotationVersion: []byte(observed.Name),
		},
	}, nil
}

// Decrypt unwraps a data encryption key, under the version it was WRAPPED with.
//
// Not the active version. An object written before the last rotation was wrapped under
// an earlier version, Cloud KMS retains non-destroyed versions, and unwrapping must
// keep working or a rotation would make every existing Secret unreadable -- the exact
// loss this design is built to avoid. The store addresses the parent key and Cloud KMS
// resolves the version from the ciphertext; see keystore.Unwrap.
func (s *Service) Decrypt(ctx context.Context, req *kmsv2.DecryptRequest) (*kmsv2.DecryptResponse, error) {
	dek, err := s.store.Unwrap(ctx, req.Ciphertext)
	if err == nil {
		return &kmsv2.DecryptResponse{Plaintext: dek}, nil
	}

	// THE ANNOTATION IS NOT REQUIRED, AND AN EARLIER DRAFT GOT THIS BACKWARDS.
	//
	// It refused to decrypt an object with no version annotation, on the reasoning that
	// guessing the version wrong on a Secret the cluster needs is unrecoverable. The
	// reasoning was sound and the premise was false: the ciphertext embeds its version
	// and rotation retains previous material, so there is nothing to guess.
	//
	// Requiring it would instead have been the dangerous choice. Every object written
	// before this plugin was deployed has no such annotation -- anything an earlier
	// plugin wrapped -- and a plugin that refuses those cannot be adopted on a cluster
	// that already has data, which is every cluster worth adopting it on.
	//
	// So it is read only here, to say which version the object claims. The most useful
	// thing it can say is that the object names a DIFFERENT KEY from the configured one,
	// which is a reconfiguration rather than a decrypt failure and sends the reader
	// somewhere else entirely.
	raw, ok := req.Annotations[annotationVersion]
	if !ok {
		return nil, fmt.Errorf("unwrapping a data encryption key (no %s annotation, so it "+
			"predates this plugin): %w", annotationVersion, err)
	}
	wrappedUnder := string(raw)
	observed, _, cerr := s.current()
	if cerr == nil {
		if a, b := keystore.ParentKeyOf(wrappedUnder), keystore.ParentKeyOf(observed.Name); a != b && a != "" {
			return nil, fmt.Errorf("this object was wrapped under key %s but the plugin is "+
				"configured for %s. That is a key reconfiguration, not a rotation, and the "+
				"original key is required to read it: %w", a, b, err)
		}
	}
	return nil, fmt.Errorf("unwrapping a data encryption key recorded as wrapped under %s: %w",
		wrappedUnder, err)
}
