package keystore

import (
	"context"
	"fmt"
	"hash/crc32"
	"net"
	"strings"
	"testing"

	"cloud.google.com/go/kms/apiv1/kmspb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// THE REAL CLIENT AGAINST THE REAL GENERATED CONTRACT.
//
// This server is deliberately NOT the Gate 1 stub, even though it does a similar job.
// Sharing code would mean a mistake in the shared part hides in both the test and the
// gate, and these exist to check each other. It implements
// kmspb.KeyManagementServiceServer, so a field either side forgets is a compile error.
type fakeKMS struct {
	kmspb.UnimplementedKeyManagementServiceServer

	key     string
	primary int
	// Fault injection, each simulating a specific real failure.
	corruptCiphertextCRC bool // the response's own checksum is wrong
	corruptPlaintextCRC  bool // the decrypted plaintext's checksum is wrong
	refuseToVerify       bool // the server did not check the request checksum
	answerWithVersion    int  // override: report a version other than the one requested
	noPrimary            bool // every version disabled or destroyed

	// gotEncryptName records what Encrypt was ADDRESSED TO, which is the whole subject
	// of the exact-version behaviour.
	gotEncryptName string
	gotDecryptName string
	// getCryptoKeyCalls must stay at zero: the plugin is not permitted that call.
	getCryptoKeyCalls int
}

func crcOf(b []byte) int64 {
	return int64(crc32.Checksum(b, crc32.MakeTable(crc32.Castagnoli)))
}

func (f *fakeKMS) versionName(n int) string {
	return fmt.Sprintf("%s/cryptoKeyVersions/%d", f.key, n)
}

// GetCryptoKey IS A TRAP, and answers the way a real project would.
//
// `cloudkms.cryptoKeys.get` is NOT carried by roles/cloudkms.cryptoKeyEncrypterDecrypter,
// so on a correctly scoped key this call is PERMISSION_DENIED. A fake that answered it
// helpfully would let the plugin depend on a permission the IAM policy does not grant,
// and the failure would wait for the first real project.
func (f *fakeKMS) GetCryptoKey(_ context.Context, _ *kmspb.GetCryptoKeyRequest) (*kmspb.CryptoKey, error) {
	f.getCryptoKeyCalls++
	return nil, status.Error(codes.PermissionDenied,
		"cloudkms.cryptoKeys.get denied: the EncrypterDecrypter role does not carry it")
}

func (f *fakeKMS) Encrypt(_ context.Context, req *kmspb.EncryptRequest) (*kmspb.EncryptResponse, error) {
	f.gotEncryptName = req.GetName()

	// WHICH VERSION ACTUALLY ENCRYPTED. Both request forms are honoured, because the
	// contract permits both and the plugin uses both for different purposes: the parent
	// key for Active's probe, where the server selects, and an exact version for a real
	// wrap. answerWithVersion forces the contract violation the client must refuse.
	var n int
	switch {
	case req.GetName() == f.key:
		if f.noPrimary {
			// What Cloud KMS does when every version is disabled or destroyed: the key
			// exists, and nothing can encrypt with it.
			return nil, status.Error(codes.FailedPrecondition,
				"key has no enabled primary version")
		}
		n = f.primary
	case strings.HasPrefix(req.GetName(), f.key+"/cryptoKeyVersions/"):
		v, err := parseVersion(req.GetName())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "unparseable version")
		}
		n = v
	default:
		return nil, status.Errorf(codes.NotFound, "this fake serves only %s and its versions", f.key)
	}
	if f.answerWithVersion != 0 {
		n = f.answerWithVersion
	}

	// Trivially reversible: this test is about the client's contract handling, not
	// about cryptography.
	ct := append([]byte(fmt.Sprintf("v%d:", n)), req.GetPlaintext()...)
	crc := crcOf(ct)
	if f.corruptCiphertextCRC {
		crc++
	}
	return &kmspb.EncryptResponse{
		Name:                    f.versionName(n),
		Ciphertext:              ct,
		CiphertextCrc32C:        wrapperspb.Int64(crc),
		VerifiedPlaintextCrc32C: !f.refuseToVerify,
	}, nil
}

func (f *fakeKMS) Decrypt(_ context.Context, req *kmspb.DecryptRequest) (*kmspb.DecryptResponse, error) {
	f.gotDecryptName = req.GetName()
	ct := string(req.GetCiphertext())
	i := strings.IndexByte(ct, ':')
	if i < 0 {
		return nil, status.Error(codes.InvalidArgument, "no version prefix")
	}
	plain := req.GetCiphertext()[i+1:]
	crc := crcOf(plain)
	if f.corruptPlaintextCRC {
		crc++
	}
	return &kmspb.DecryptResponse{
		Plaintext:       plain,
		PlaintextCrc32C: wrapperspb.Int64(crc),
	}, nil
}

const testKey = "projects/p/locations/global/keyRings/r/cryptoKeys/k"

func version(n int) string {
	return fmt.Sprintf("%s/cryptoKeyVersions/%d", testKey, n)
}

func serve(t *testing.T, f *fakeKMS) Store {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	kmspb.RegisterKeyManagementServiceServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	// The PRODUCTION constructor, reaching the fake through the documented test
	// endpoint. Nothing about the client is stubbed.
	t.Setenv("KMS_INSECURE_TEST_ENDPOINT", lis.Addr().String())
	st, err := NewGCPKMS(GCPKMSOptions{CryptoKey: testKey})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestADataEncryptionKeySurvivesTheRoundTripByteForByte(t *testing.T) {
	// A 32-byte DEK containing 0x00 and bytes above 0x7f, deliberately: a printable
	// plaintext can survive an encoding mistake and pass a careless test. This is the
	// same property the Infisical client had to be fixed for, kept because the risk is
	// in the contract rather than in the vendor.
	st := serve(t, &fakeKMS{key: testKey, primary: 3})
	dek := make([]byte, 32)
	for i := range dek {
		dek[i] = byte(i * 7)
	}

	ct, used, err := st.Wrap(context.Background(), dek, version(3))
	if err != nil {
		t.Fatal(err)
	}
	if used != version(3) {
		t.Fatalf("Wrap reported %q, want %q -- it must come from EncryptResponse.Name",
			used, version(3))
	}
	got, err := st.Unwrap(context.Background(), ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(dek) {
		t.Fatalf("the data encryption key did not survive.\n got %x\nwant %x", got, dek)
	}
}

func TestEncryptIsAddressedToTheExactVersionAndDecryptToTheParentKey(t *testing.T) {
	// THE CORE OF THE EXACT-VERSION CHANGE, and the two halves must differ.
	//
	// Encrypt names a CryptoKeyVersion so that the version which wraps is the version
	// the plugin is reporting -- changing a key's primary is eventually consistent, so
	// encrypting against the parent lets Status and Encrypt observe different versions,
	// which the API server treats as an unhealthy provider
	// (encryptionconfig/config.go:424).
	//
	// Decrypt must NOT name a version: Cloud KMS resolves it from the ciphertext, and
	// addressing a version would make every object written before the last rotation
	// unreadable.
	f := &fakeKMS{key: testKey, primary: 5}
	st := serve(t, f)
	ct, _, err := st.Wrap(context.Background(), []byte("dek"), version(5))
	if err != nil {
		t.Fatal(err)
	}
	if f.gotEncryptName != version(5) {
		t.Errorf("Encrypt was addressed to %q, want the exact version %q", f.gotEncryptName, version(5))
	}
	if _, err := st.Unwrap(context.Background(), ct); err != nil {
		t.Fatal(err)
	}
	if f.gotDecryptName != testKey {
		t.Errorf("Decrypt was addressed to %q, want the PARENT key %q; naming a version makes "+
			"pre-rotation data unreadable", f.gotDecryptName, testKey)
	}
}

func TestAResponseNamingADifferentVersionIsRefused(t *testing.T) {
	// With an exact version requested this cannot legitimately happen, so it is not a
	// race to absorb: the response does not describe the request, and the key_id about
	// to be reported would not name the material that was used.
	f := &fakeKMS{key: testKey, primary: 4, answerWithVersion: 9}
	st := serve(t, f)
	_, _, err := st.Wrap(context.Background(), []byte("dek"), version(4))
	if err == nil {
		t.Fatal("Wrap accepted a response naming a version other than the one requested")
	}
	if !strings.Contains(err.Error(), "does not describe the request") {
		t.Fatalf("the error does not explain the mismatch: %v", err)
	}
}

func TestWrapRefusesAParentKeyAsTheEncryptionTarget(t *testing.T) {
	// Passing the parent here would silently restore the behaviour this change removed:
	// Cloud KMS would select the primary itself and the propagation race would be back.
	st := serve(t, &fakeKMS{key: testKey, primary: 1})
	_, _, err := st.Wrap(context.Background(), []byte("dek"), testKey)
	if err == nil {
		t.Fatal("Wrap accepted the parent CryptoKey as the version to encrypt under")
	}
}

func TestACorruptedCiphertextChecksumIsRefused(t *testing.T) {
	// Storing a ciphertext that fails its own checksum produces a Secret that can never
	// be decrypted -- data loss with no remedy, which is why this is a hard refusal
	// after bounded retries rather than a warning.
	st := serve(t, &fakeKMS{key: testKey, primary: 1, corruptCiphertextCRC: true})
	if _, _, err := st.Wrap(context.Background(), []byte("dek"), version(1)); err == nil {
		t.Fatal("Wrap accepted a ciphertext that fails its own CRC32C")
	} else if !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("the error does not name the integrity failure: %v", err)
	}
}

func TestAnUnverifiedPlaintextChecksumIsRefused(t *testing.T) {
	// VerifiedPlaintextCrc32C false means the server did not check what we sent, so
	// corruption on the way IN would not have been caught and the ciphertext cannot be
	// trusted even though the call succeeded.
	st := serve(t, &fakeKMS{key: testKey, primary: 1, refuseToVerify: true})
	if _, _, err := st.Wrap(context.Background(), []byte("dek"), version(1)); err == nil {
		t.Fatal("Wrap accepted a response where the server did not verify our checksum")
	}
}

func TestACorruptedPlaintextChecksumOnUnwrapIsRefused(t *testing.T) {
	// A wrong DEK decrypts the Secret to garbage. Refusing is the only safe answer.
	f := &fakeKMS{key: testKey, primary: 1}
	st := serve(t, f)
	ct, _, err := st.Wrap(context.Background(), []byte("dek"), version(1))
	if err != nil {
		t.Fatal(err)
	}
	f.corruptPlaintextCRC = true
	if _, err := st.Unwrap(context.Background(), ct); err == nil {
		t.Fatal("Unwrap accepted a data encryption key that fails its own CRC32C")
	}
}

func TestTheActiveVersionIsDiscoveredByEncryptingAndNotByReadingMetadata(t *testing.T) {
	// THE LEAST-PRIVILEGE PROPERTY, ASSERTED RATHER THAN DOCUMENTED.
	//
	// An earlier version read CryptoKey.Primary with GetCryptoKey, which needs
	// `cloudkms.cryptoKeys.get` -- a permission roles/cloudkms.cryptoKeyEncrypterDecrypter
	// does not carry. The ADR claimed that one role covered all of it, which was an IAM
	// claim nobody had written a policy for. The fake now denies GetCryptoKey the way a
	// correctly scoped key would, so a regression here is a failing test rather than a
	// surprise on the first real project.
	f := &fakeKMS{key: testKey, primary: 11}
	st := serve(t, f)
	got, err := st.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != version(11) || got.Number != 11 {
		t.Fatalf("got %+v, want name=%s number=11", got, version(11))
	}
	if f.getCryptoKeyCalls != 0 {
		t.Fatalf("GetCryptoKey was called %d time(s); the plugin's identity is not permitted "+
			"cloudkms.cryptoKeys.get", f.getCryptoKeyCalls)
	}
	// The probe addresses the PARENT, which is the one place that is correct: the server
	// selects, and its answer therefore comes from the crypto path rather than from a
	// metadata view that can be ahead of it.
	if f.gotEncryptName != testKey {
		t.Fatalf("the probe was addressed to %q, want the parent key %q", f.gotEncryptName, testKey)
	}
}

func TestAKeyWithNoEnabledPrimaryVersionIsRefused(t *testing.T) {
	// Every version disabled or destroyed. Cloud KMS answers FAILED_PRECONDITION, and
	// the plugin must surface that rather than reporting some version it cannot use.
	st := serve(t, &fakeKMS{key: testKey, primary: 1, noPrimary: true})
	_, err := st.Active(context.Background())
	if err == nil {
		t.Fatal("a key with no enabled primary version was accepted")
	}
	if !strings.Contains(err.Error(), "primary") {
		t.Fatalf("the error does not explain the missing primary: %v", err)
	}
}

func TestTheProbeDiscardsItsCiphertextAndNeverCarriesAKey(t *testing.T) {
	// The probe encrypts a fixed, meaningless constant. If it ever encrypted something
	// derived from a DEK or from configuration, the ciphertext it throws away would be a
	// real one and the log line above it would be describing a write.
	f := &fakeKMS{key: testKey, primary: 2}
	st := serve(t, f)
	if _, err := st.Active(context.Background()); err != nil {
		t.Fatal(err)
	}
	if string(probePlaintext) == "" {
		t.Fatal("the probe plaintext is empty")
	}
	for _, forbidden := range []string{testKey, "cryptoKeyVersions"} {
		if strings.Contains(string(probePlaintext), forbidden) {
			t.Fatalf("the probe plaintext carries %q; it must be a meaningless constant", forbidden)
		}
	}
}

func TestAVersionResourceNameIsRequired(t *testing.T) {
	if _, err := parseVersion(testKey); err == nil {
		t.Fatal("a CryptoKey name was accepted where a CryptoKeyVersion was required")
	}
	v, err := parseVersion(version(42))
	if err != nil || v != 42 {
		t.Fatalf("got %d, %v; want 42", v, err)
	}
}

func TestAVersionPathIsRefusedAsTheConfiguredKey(t *testing.T) {
	// STILL CORRECT, FOR A REASON THAT CHANGED. The original reasoning was that naming
	// a version anywhere would pin writes to it and make a rotation invisible, and that
	// was wrong -- Wrap now names a version on every call. What remains true is that the
	// CONFIGURED value must be the parent, because reading CryptoKey.Primary is the only
	// way this plugin learns that a rotation happened at all. Configure a version and it
	// would encrypt under that version forever and report it as current.
	_, err := NewGCPKMS(GCPKMSOptions{CryptoKey: version(3)})
	if err == nil {
		t.Fatal("a cryptoKeyVersions path was accepted as KMS_CRYPTO_KEY")
	}
	if !strings.Contains(err.Error(), "PARENT key") {
		t.Fatalf("the error does not explain why: %v", err)
	}
}

func TestTheKeyIsTheOnlyRequiredSetting(t *testing.T) {
	// CLUSTER_NAME used to be required here, because the plugin composed its own
	// identifier from the cluster, key name and version. The CryptoKeyVersion resource
	// name is already globally unique, so that composition was deleted; the cluster name
	// survives only as the key_id suffix, which is the service's concern and not the key
	// authority's.
	_, err := NewGCPKMS(GCPKMSOptions{})
	if err == nil {
		t.Fatal("an empty configuration was accepted")
	}
	if !strings.Contains(err.Error(), "KMS_CRYPTO_KEY") {
		t.Errorf("the error does not name KMS_CRYPTO_KEY: %v", err)
	}
}

func TestErrorsDoNotCarryKeyMaterial(t *testing.T) {
	// A request body here is a DEK and a decrypt response IS a DEK. Anything in an
	// error reaches the API server's logs.
	f := &fakeKMS{key: testKey, primary: 1, corruptCiphertextCRC: true}
	st := serve(t, f)
	dek := []byte("THEDEKITSELF")
	_, _, err := st.Wrap(context.Background(), dek, version(1))
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), string(dek)) {
		t.Fatalf("the error leaks the data encryption key: %v", err)
	}
}

func TestTheParentKeyIsRecoverableFromAVersionName(t *testing.T) {
	// Used to tell a key RECONFIGURATION apart from a decrypt failure; see
	// service.Decrypt.
	if got := ParentKeyOf(version(7)); got != testKey {
		t.Fatalf("ParentKeyOf(%q) = %q, want %q", version(7), got, testKey)
	}
	if got := ParentKeyOf(version(7) + ":nutgraf-01"); got != testKey {
		t.Fatalf("a suffixed key_id did not yield the parent: %q", got)
	}
}
