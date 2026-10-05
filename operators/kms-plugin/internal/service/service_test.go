package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	kmsv2 "k8s.io/kms/apis/v2"

	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/active"
	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/keystore"
)

// fakeStore behaves like a key store: a rotation is the SAME key with a higher
// version, and previous versions keep unwrapping.
type fakeStore struct {
	key     string
	version int
	wraps   []int // the version each Wrap was asked to use
}

func (f *fakeStore) Active(context.Context) (keystore.KeyState, error) {
	return keystore.KeyState{Key: f.key, Version: f.version}, nil
}

func (f *fakeStore) Wrap(_ context.Context, version int, dek []byte) ([]byte, error) {
	f.wraps = append(f.wraps, version)
	return []byte(fmt.Sprintf("wrapped-v%d:%s", version, dek)), nil
}

func (f *fakeStore) Unwrap(_ context.Context, version int, wrapped []byte) ([]byte, error) {
	want := fmt.Sprintf("wrapped-v%d:", version)
	if !strings.HasPrefix(string(wrapped), want) {
		return nil, fmt.Errorf("version %d cannot unwrap this material", version)
	}
	return []byte(strings.TrimPrefix(string(wrapped), want)), nil
}

func newSvc(t *testing.T, version int) (*Service, *fakeStore) {
	t.Helper()
	st := &fakeStore{key: "etcd-kek", version: version}
	s := New("nutgraf-01", st, active.NewHolder())
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return s, st
}

func TestARotationChangesTheReportedIdentifier(t *testing.T) {
	// THE VENDOR PLUGIN'S DEFECT, ASSERTED ABSENT. It returns the configured key id
	// verbatim and tracks no version, so a rotation in the store never changes the
	// reported identifier: the API server keeps the data keys established under the old
	// version indefinitely and the rotation protects nothing while appearing to succeed.
	s, st := newSvc(t, 1)
	before, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}

	st.version = 2 // the store rotates
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := s.Status(context.Background(), &kmsv2.StatusRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if before.KeyId == after.KeyId {
		t.Fatalf("the identifier did not change across a rotation (%q); the API server cannot "+
			"tell that anything happened and keeps the old data keys", before.KeyId)
	}
	if !strings.HasSuffix(before.KeyId, "/v1") || !strings.HasSuffix(after.KeyId, "/v2") {
		t.Fatalf("the identifier does not carry the version: %q then %q", before.KeyId, after.KeyId)
	}
}

func TestTheIdentifierIsStableWhileNothingRotates(t *testing.T) {
	// The other half: a changed identifier is read as a changed key, so re-observing the
	// same version must not produce a new one. Otherwise the API server establishes new
	// encryption state on every poll while nothing has happened.
	s, _ := newSvc(t, 7)
	first, _ := s.Status(context.Background(), &kmsv2.StatusRequest{})
	for i := 0; i < 10; i++ {
		if err := s.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Status(context.Background(), &kmsv2.StatusRequest{})
		if got.KeyId != first.KeyId {
			t.Fatalf("the identifier changed from %q to %q without a rotation", first.KeyId, got.KeyId)
		}
	}
}

func TestStatusAndEncryptNeverReportDifferentKeys(t *testing.T) {
	// ADR-100 acceptance criterion 2. The interface treats a disagreement between the
	// reported key and the one used to wrap as an unhealthy plugin.
	s, st := newSvc(t, 3)
	status, _ := s.Status(context.Background(), &kmsv2.StatusRequest{})
	enc, err := s.Encrypt(context.Background(), &kmsv2.EncryptRequest{Plaintext: []byte("dek")})
	if err != nil {
		t.Fatal(err)
	}
	if enc.KeyId != status.KeyId {
		t.Fatalf("Encrypt reported %q but Status reported %q", enc.KeyId, status.KeyId)
	}
	// And the version actually used to wrap is the one in that identifier, not a second
	// observation of the store.
	if len(st.wraps) != 1 || st.wraps[0] != 3 {
		t.Fatalf("wrapped under versions %v, want [3]", st.wraps)
	}
	if got := string(enc.Annotations[annotationVersion]); got != "3" {
		t.Fatalf("the stored annotation says version %q, want \"3\"", got)
	}
}

func TestDataWrappedUnderAnOlderVersionStillUnwraps(t *testing.T) {
	// A rotation must not make existing Secrets unreadable. The version comes from the
	// object's annotation, not from the active snapshot.
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

func TestDecryptRefusesToGuessTheVersion(t *testing.T) {
	// Guessing wrong on a Secret the cluster needs to start is not a recoverable error,
	// so a missing annotation is an explicit failure rather than a fallback to "active".
	s, _ := newSvc(t, 2)
	_, err := s.Decrypt(context.Background(), &kmsv2.DecryptRequest{
		Ciphertext:  []byte("wrapped-v1:x"),
		Annotations: map[string][]byte{},
	})
	if err == nil {
		t.Fatal("Decrypt accepted an object with no version annotation")
	}
	if !strings.Contains(err.Error(), "refusing to guess") {
		t.Fatalf("the error does not say it is refusing to guess: %v", err)
	}
}

func TestBeforeTheStoreIsReadTheStatusIsNotHealthy(t *testing.T) {
	// Claiming healthz=ok with an empty key id tells the API server everything is fine
	// while the identifier means nothing.
	s := New("nutgraf-01", &fakeStore{key: "k", version: 1}, active.NewHolder())
	if _, err := s.Status(context.Background(), &kmsv2.StatusRequest{}); err == nil {
		t.Fatal("Status succeeded before the key store had been read")
	}
	if _, err := s.Encrypt(context.Background(), &kmsv2.EncryptRequest{Plaintext: []byte("d")}); err == nil {
		t.Fatal("Encrypt succeeded before the key store had been read")
	}
}

func TestTheReportedVersionAlwaysParsesBackToAnInteger(t *testing.T) {
	// The annotation is what Decrypt depends on for the life of the object, so its
	// encoding is pinned rather than assumed.
	s, _ := newSvc(t, 42)
	enc, err := s.Encrypt(context.Background(), &kmsv2.EncryptRequest{Plaintext: []byte("d")})
	if err != nil {
		t.Fatal(err)
	}
	v, err := strconv.Atoi(string(enc.Annotations[annotationVersion]))
	if err != nil || v != 42 {
		t.Fatalf("annotation %q does not parse back to 42", enc.Annotations[annotationVersion])
	}
	if !strings.Contains(annotationVersion, ".") {
		t.Fatalf("the annotation key %q is not a qualified domain name, which the interface "+
			"requires", annotationVersion)
	}
}
