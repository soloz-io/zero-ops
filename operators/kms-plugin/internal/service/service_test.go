package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	kmsv2 "k8s.io/kms/apis/v2"

	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/keystore"
)

const fakeKey = "projects/p/locations/global/keyRings/r/cryptoKeys/etcd-kek"

func vname(key string, n int) string {
	return fmt.Sprintf("%s/cryptoKeyVersions/%d", key, n)
}

// fakeStore behaves like Cloud KMS: a rotation is the SAME key with a higher version,
// previous versions keep unwrapping, and Encrypt uses the version it is ADDRESSED TO.
type fakeStore struct {
	key     string
	version int
	// wraps records the version name each Wrap was addressed to, which is how the
	// exact-version behaviour is observed from this side.
	wraps []string
	// unreachable makes Active fail, as a Cloud KMS outage or a revoked credential
	// would.
	unreachable bool
	// encryptUnder forces Wrap to report a version other than the one requested -- the
	// contract violation the real client refuses, repeated here because the service
	// must not depend on the client catching it.
	encryptUnder int
}

func (f *fakeStore) Active(context.Context) (keystore.KeyVersion, error) {
	if f.unreachable {
		return keystore.KeyVersion{}, fmt.Errorf("cloud kms is unreachable")
	}
	return keystore.KeyVersion{Name: vname(f.key, f.version), Number: f.version}, nil
}

func (f *fakeStore) Wrap(_ context.Context, dek []byte, versionName string) ([]byte, string, error) {
	f.wraps = append(f.wraps, versionName)
	used := versionName
	if f.encryptUnder != 0 {
		used = vname(f.key, f.encryptUnder)
	}
	return []byte(fmt.Sprintf("wrapped[%s]:%s", used, dek)), used, nil
}

func (f *fakeStore) Unwrap(_ context.Context, wrapped []byte) ([]byte, error) {
	str := string(wrapped)
	if !strings.HasPrefix(str, "wrapped[") {
		return nil, fmt.Errorf("not something this store produced")
	}
	i := strings.Index(str, "]:")
	if i < 0 {
		return nil, fmt.Errorf("malformed ciphertext")
	}
	return []byte(str[i+2:]), nil
}

func newSvc(t *testing.T, version int) (*Service, *fakeStore) {
	t.Helper()
	st := &fakeStore{key: fakeKey, version: version}
	// No suffix: that is the production configuration. See TestTheIdentifierIsExactly…
	s := New(st)
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return s, st
}

func TestTheIdentifierIsExactlyTheCryptoKeyVersionResourceName(t *testing.T) {
	// TWO EARLIER DESIGNS ADDED TO THIS, AND BOTH WERE DELETED FOR THE SAME REASON.
	//
	// First a composite built from the cluster name, key name and version number. Then
	// the resource name plus a cluster-name suffix. The resource name already carries
	// project, location, key ring, key and version, and GCP guarantees it is unique --
	// so anything appended restates what it says and adds something else that can be
	// wrong about an identifier the API server treats as the identity of the key.
	//
	// Cluster scoping is not this string's job: each cluster has its own key and its own
	// service account bound only to that key, which is enforced by IAM and proven
	// against real GCP rather than asserted here.
	s, _ := newSvc(t, 3)
	got, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	want := vname(fakeKey, 3)
	if got.KeyId != want {
		t.Fatalf("key_id is %q, want exactly the resource name %q", got.KeyId, want)
	}
	if strings.Contains(got.KeyId, ":") {
		t.Fatalf("key_id carries a synthetic component: %q", got.KeyId)
	}
	// The interface caps the identifier at 1 kB (envelope.go KeyIDMaxSize); a resource
	// name plus a cluster name is nowhere near it, and this says so rather than leaving
	// it to be discovered by a rejected Status.
	if len(got.KeyId) > 1024 {
		t.Fatalf("key_id is %d bytes, over the interface's 1 kB limit", len(got.KeyId))
	}
}

func TestTheProtocolVersionIsTheStableOne(t *testing.T) {
	// The upstream plugin advertises "v2beta1". Both are accepted
	// (k8s.io/kms apis/v2/api.proto:37, and the validator at encryptionconfig
	// config.go:496) but "v2" is the recommended one and is what the pinned k8s.io/kms
	// version documents. Pinned here so the fork cannot drift back to the beta string.
	s, _ := newSvc(t, 1)
	got, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "v2" {
		t.Fatalf("Status advertises %q, want \"v2\"", got.Version)
	}
	if got.Healthz != "ok" {
		t.Fatalf("Healthz is %q, want \"ok\"", got.Healthz)
	}
}

func TestARotationChangesTheReportedIdentifier(t *testing.T) {
	// THE VENDOR PLUGIN'S DEFECT, ASSERTED ABSENT. It returned the configured key id
	// verbatim and tracked no version, so a rotation never changed the reported
	// identifier: the API server keeps the data keys established under the old version
	// indefinitely and the rotation protects nothing while appearing to succeed.
	s, st := newSvc(t, 1)
	before, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}

	st.version = 2 // the key rotates
	after, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if before.KeyId == after.KeyId {
		t.Fatalf("the identifier did not change across a rotation (%q); the API server cannot "+
			"tell that anything happened and keeps the old data keys", before.KeyId)
	}
	if !strings.HasPrefix(before.KeyId, vname(fakeKey, 1)) ||
		!strings.HasPrefix(after.KeyId, vname(fakeKey, 2)) {
		t.Fatalf("the identifier does not carry the version: %q then %q", before.KeyId, after.KeyId)
	}
}

func TestOneStatusCallAfterARotationAlreadyReportsTheNewIdentifier(t *testing.T) {
	// THE UPSTREAM STALENESS DEFECT, ASSERTED ABSENT, and it is not cosmetic.
	//
	// Upstream reads its cached key_id, THEN probes, THEN updates the cache
	// (plugin/v2/plugin.go Status). So the first Status after a rotation returns the OLD
	// identifier while Encrypt has already moved. The API server responds to a changed
	// key_id by generating a DEK and requiring that Encrypt report the SAME key_id
	// Status did (encryptionconfig/config.go:424); when they differ it errors and marks
	// the provider unhealthy. Upstream therefore goes unhealthy for a poll interval on
	// every rotation.
	//
	// Installing the observation before answering makes the FIRST Status agree.
	s, st := newSvc(t, 1)
	st.version = 2

	status, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	enc, err := s.Encrypt(context.Background(), &kmsv2.EncryptRequest{Plaintext: []byte("dek")})
	if err != nil {
		t.Fatal(err)
	}
	if enc.KeyId != status.KeyId {
		t.Fatalf("the first Status after a rotation reported %q while Encrypt reported %q; the "+
			"API server treats that as an unhealthy provider", status.KeyId, enc.KeyId)
	}
	if !strings.HasPrefix(status.KeyId, vname(fakeKey, 2)) {
		t.Fatalf("Status still reports the pre-rotation version: %q", status.KeyId)
	}
}

func TestTheIdentifierIsStableWhileNothingRotates(t *testing.T) {
	// The other half: a changed identifier is read as a changed key, so re-observing the
	// same version must not produce a new one. Otherwise the API server establishes new
	// encryption state on every poll while nothing has happened.
	s, _ := newSvc(t, 7)
	first, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		got, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if got.KeyId != first.KeyId {
			t.Fatalf("the identifier changed from %q to %q without a rotation", first.KeyId, got.KeyId)
		}
	}
}

func TestEncryptNamesTheVersionItIsReportingAndNotTheParentKey(t *testing.T) {
	// THE EXACT-VERSION CHANGE, observed from the service side.
	//
	// Encrypting against the parent would have the status path and the encrypt path each
	// read the primary independently. Changing a primary version is eventually
	// consistent, so those reads can disagree, and the API server does not forgive that
	// (encryptionconfig/config.go:424). Naming the observed version collapses the two
	// reads into one.
	s, st := newSvc(t, 3)
	status, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	enc, err := s.Encrypt(context.Background(), &kmsv2.EncryptRequest{Plaintext: []byte("dek")})
	if err != nil {
		t.Fatal(err)
	}
	if enc.KeyId != status.KeyId {
		t.Fatalf("Encrypt reported %q but Status reported %q", enc.KeyId, status.KeyId)
	}
	if len(st.wraps) != 1 || st.wraps[0] != vname(fakeKey, 3) {
		t.Fatalf("Wrap was addressed to %v, want exactly [%s] -- the parent key would restore "+
			"the propagation race", st.wraps, vname(fakeKey, 3))
	}
	if got := string(enc.Annotations[annotationVersion]); got != vname(fakeKey, 3) {
		t.Fatalf("the stored annotation says %q, want the version resource name %q",
			got, vname(fakeKey, 3))
	}
}

func TestAWrapReportingADifferentVersionIsRefused(t *testing.T) {
	// The client refuses this too, and the duplication is deliberate: the key_id is
	// produced HERE, so the invariant is stated where being wrong writes a Secret
	// labelled with material that did not encrypt it.
	s, st := newSvc(t, 3)
	st.encryptUnder = 9
	if _, err := s.Encrypt(context.Background(), &kmsv2.EncryptRequest{Plaintext: []byte("dek")}); err == nil {
		t.Fatal("Encrypt accepted a wrap that reported a version other than the one requested")
	}
}

func TestDataWrappedUnderAnOlderVersionStillUnwraps(t *testing.T) {
	// A rotation must not make existing Secrets unreadable. Cloud KMS resolves the
	// version from the ciphertext and retains non-destroyed versions, so Decrypt is
	// addressed to the parent key and nothing has to be tracked per object.
	s, st := newSvc(t, 1)
	old, err := s.Encrypt(context.Background(), &kmsv2.EncryptRequest{Plaintext: []byte("secret-dek")})
	if err != nil {
		t.Fatal(err)
	}

	st.version = 5 // several rotations later
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := s.Decrypt(context.Background(), &kmsv2.DecryptRequest{
		Ciphertext:  old.Ciphertext,
		KeyId:       old.KeyId,
		Annotations: old.Annotations,
	})
	if err != nil {
		t.Fatalf("data wrapped under version 1 could not be unwrapped after rotating to 5: %v", err)
	}
	if string(got.Plaintext) != "secret-dek" {
		t.Fatalf("unwrapped %q", got.Plaintext)
	}
}

func TestDecryptWorksWithoutTheVersionAnnotation(t *testing.T) {
	// THE OPPOSITE OF WHAT AN EARLIER DRAFT DID. It refused an object with no version
	// annotation, reasoning that guessing the version wrong is unrecoverable. The
	// reasoning was sound; the premise was false -- the ciphertext embeds its own version
	// and rotation retains previous material, so there is nothing to guess.
	//
	// Requiring it would have been the dangerous choice: every object written before
	// this plugin was deployed has no such annotation, and a plugin that refuses those
	// cannot be adopted on a cluster that already holds data.
	s, _ := newSvc(t, 4)
	enc, err := s.Encrypt(context.Background(), &kmsv2.EncryptRequest{Plaintext: []byte("dek")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Decrypt(context.Background(), &kmsv2.DecryptRequest{
		Ciphertext:  enc.Ciphertext,
		KeyId:       enc.KeyId,
		Annotations: map[string][]byte{}, // as an object from before this plugin would arrive
	})
	if err != nil {
		t.Fatalf("Decrypt required the annotation, so this plugin could not be adopted on a "+
			"cluster that already has data: %v", err)
	}
	if string(got.Plaintext) != "dek" {
		t.Fatalf("unwrapped %q", got.Plaintext)
	}
}

func TestAFailureToUnwrapUnderADifferentKeyNamesTheReconfiguration(t *testing.T) {
	// A reconfigured key and a corrupted ciphertext produce the same symptom -- Decrypt
	// fails -- and send the reader to completely different places. The annotation is
	// read only for this.
	s, _ := newSvc(t, 1)
	otherKey := "projects/p/locations/global/keyRings/r/cryptoKeys/a-different-key"
	_, err := s.Decrypt(context.Background(), &kmsv2.DecryptRequest{
		Ciphertext:  []byte("not something this store produced"),
		Annotations: map[string][]byte{annotationVersion: []byte(vname(otherKey, 2))},
	})
	if err == nil {
		t.Fatal("expected a decrypt failure")
	}
	if !strings.Contains(err.Error(), "reconfiguration") || !strings.Contains(err.Error(), otherKey) {
		t.Fatalf("the error does not identify the key reconfiguration: %v", err)
	}
}

func TestAnUnreachableKeyAuthorityKeepsTheLastKnownIdentifier(t *testing.T) {
	// UPSTREAM'S INSIGHT, TAKEN. Its comment: "KeyId in StatusResponse cannot be empty
	// and shouldn't trigger key migration in case of transient remote service
	// unavailability."
	//
	// The API server validates the identifier and treats a CHANGE in it as a rotation
	// (encryptionconfig/config.go:508 and :514). Dropping or emptying it during a Cloud
	// KMS blip would therefore look like a new key and set the API server rotating DEKs
	// over an outage. So an outage is reported AS an outage: unhealthy, identifier
	// unchanged.
	s, st := newSvc(t, 6)
	healthy, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}

	st.unreachable = true
	got, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatalf("Status returned an error during an outage instead of reporting unhealthy; the "+
			"API server needs the response to keep the identifier stable: %v", err)
	}
	if got.Healthz == "ok" {
		t.Fatal("Status reported healthz=ok while the key authority was unreachable")
	}
	if got.KeyId != healthy.KeyId {
		t.Fatalf("the identifier changed to %q during an outage (was %q); the API server would "+
			"read that as a rotation", got.KeyId, healthy.KeyId)
	}
}

func TestBeforeTheKeyIsReadNothingIsReported(t *testing.T) {
	// The one case with nothing honest to say: no observation has ever succeeded, so
	// there is no identifier and the plugin must not invent one. An empty key_id is
	// rejected by the API server's own validation anyway
	// (encryptionconfig/config.go:508), but failing here says why.
	s := New(&fakeStore{key: fakeKey, version: 1, unreachable: true})
	if _, err := s.Status(context.Background(), &kmsv2.StatusRequest{}); err == nil {
		t.Fatal("Status succeeded before the key had ever been read")
	}
	if _, err := s.Encrypt(context.Background(), &kmsv2.EncryptRequest{Plaintext: []byte("d")}); err == nil {
		t.Fatal("Encrypt succeeded before the key had ever been read")
	}
}

func TestARegressedPrimaryVersionIsFollowedAndNotRefused(t *testing.T) {
	// THIS IS A DELIBERATE REVERSAL, and the reasoning matters more than the assertion.
	//
	// An earlier design refused to adopt a primary version lower than one already seen,
	// enforcing ADR-100's rule that a key version is never reactivated. The enforcement
	// was not sound: the record of which versions had been active lived in this
	// process's memory, so it vanished on every restart and on every control-plane node
	// replacement. v1 -> v2 -> restart -> v1 was accepted silently, which is the case
	// the refusal existed for.
	//
	// Deriving it from GCP instead was considered and is unsafe for a different reason:
	// a newer-but-not-yet-primary version is the normal middle of every rotation, and is
	// MANDATORY for HSM and external keys (PENDING_GENERATION). See
	// internal/keystore/gcpkms.go.
	//
	// So the invariant moved to the rotation authority -- the only identity permitted to
	// call UpdateCryptoKeyPrimaryVersion -- and this process reports what Cloud KMS
	// says. Refusing here would have produced a control plane that cannot encrypt, in
	// defence of a rule it cannot actually enforce.
	s, st := newSvc(t, 5)
	st.version = 3
	got, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatalf("a regressed primary version made Status fail: %v", err)
	}
	if !strings.HasPrefix(got.KeyId, vname(fakeKey, 3)) {
		t.Fatalf("Status reports %q; it must report what Cloud KMS says is primary", got.KeyId)
	}
	if _, err := s.Encrypt(context.Background(), &kmsv2.EncryptRequest{Plaintext: []byte("d")}); err != nil {
		t.Fatalf("a regressed primary version made Encrypt fail: %v", err)
	}
}

func TestTwoClustersReportDifferentIdentifiersBecauseTHEYHAVEDIFFERENTKEYS(t *testing.T) {
	// NOT BECAUSE OF A STRING APPENDED TO THE IDENTIFIER, which is what an earlier
	// design relied on and what the suffix was defended as doing.
	//
	// Each cluster gets its own CryptoKey -- kms.KeyIDFor puts the cluster name in the
	// KEY name -- and its own service account bound only to that key. So the resource
	// names differ at the key, not at a suffix, and a cluster that is misconfigured onto
	// another cluster's key is DENIED by IAM rather than merely reporting a confusable
	// identifier. That denial is proven against real GCP by `soloz kms prove`.
	spokeKey := "projects/p/locations/global/keyRings/r/cryptoKeys/etcd-kek-nutgraf-01"
	hubKey := "projects/p/locations/global/keyRings/r/cryptoKeys/etcd-kek-nutgraf-hub"

	spoke := New(&fakeStore{key: spokeKey, version: 1})
	hub := New(&fakeStore{key: hubKey, version: 1})
	for _, s := range []*Service{spoke, hub} {
		if err := s.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := spoke.Status(context.Background(), &kmsv2.StatusRequest{})
	b, _ := hub.Status(context.Background(), &kmsv2.StatusRequest{})
	if a.KeyId == b.KeyId {
		t.Fatalf("both clusters report %q", a.KeyId)
	}
	for _, id := range []string{a.KeyId, b.KeyId} {
		if strings.Contains(id, ":") {
			t.Errorf("identifier carries a synthetic component: %q", id)
		}
	}
}

func TestNothingCanBeAppendedToTheIdentifier(t *testing.T) {
	// THE ESCAPE HATCH IS GONE, AND ITS ABSENCE IS THE ASSERTION.
	//
	// A --key-suffix flag survived one revision as break-glass: if a primary version were
	// ever regressed out of band, reinstated material would otherwise report an identifier
	// the API server has already seen, and appending something was the only way to make it
	// fresh. That reasoning is sound and was still not a reason to ship the flag -- a
	// break-glass path that is merely AVAILABLE is the manual identifier mutation this
	// design removed, under a different name, and anything reachable by adding one
	// argument gets reached.
	//
	// So New() takes no suffix, deriveKeyID has no second branch, and there is no
	// configuration that can produce an identifier other than the resource name. If that
	// recovery is ever needed it is a separately authorised, audited platform workflow.
	s, _ := newSvc(t, 4)
	got, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyId != vname(fakeKey, 4) {
		t.Fatalf("key_id is %q, want exactly %q", got.KeyId, vname(fakeKey, 4))
	}
	if s.deriveKeyID("X") != "X" {
		t.Fatal("deriveKeyID is no longer the identity function, so something can be appended")
	}
}

func TestTheAnnotationKeyIsAQualifiedDomainName(t *testing.T) {
	// The interface requires it, and the failure is a rejected Encrypt rather than a
	// compile error.
	if !strings.Contains(annotationVersion, ".") {
		t.Fatalf("the annotation key %q is not a qualified domain name", annotationVersion)
	}
}
