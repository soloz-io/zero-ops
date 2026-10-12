package credential

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/soloz-io/zero-ops/operators/kms-plugin/internal/keystore"
)

type mockStore struct {
	unwrapCalls int
	unwrapFunc  func(ctx context.Context, wrapped []byte) ([]byte, error)
}

func (m *mockStore) Active(ctx context.Context) (keystore.KeyVersion, error) {
	return keystore.KeyVersion{
		Name:   "projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1",
		Number: 1,
	}, nil
}

func (m *mockStore) Wrap(ctx context.Context, dek []byte, versionName string) ([]byte, string, error) {
	return nil, "", nil
}

func (m *mockStore) Unwrap(ctx context.Context, wrapped []byte) ([]byte, error) {
	m.unwrapCalls++
	if m.unwrapFunc != nil {
		return m.unwrapFunc(ctx, wrapped)
	}
	return nil, fmt.Errorf("unexpected Unwrap call")
}

func generateCertAndKey(t *testing.T, notBefore, notAfter time.Time) ([]byte, []byte, *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating private key: %v", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generating serial number: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "kms-plugin-test",
			Organization: []string{"soloz.io"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshaling private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, priv
}

// Test 1: tls.* present and valid -> no KMS call at all for credential setup (the reboot path).
func TestSetup_RebootPath_SteadyStateValid(t *testing.T) {
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "tls.crt")
	keyPath := filepath.Join(tmpDir, "tls.key")
	leafEncPath := filepath.Join(tmpDir, "leaf.enc")
	steadyCred := filepath.Join(tmpDir, "steady-cred.json")
	bootCred := filepath.Join(tmpDir, "boot-cred.json")

	now := time.Now()
	certPEM, keyPEM, _ := generateCertAndKey(t, now.Add(-1*time.Hour), now.Add(90*24*time.Hour))
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("writing cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("writing key: %v", err)
	}
	// Even if leaf.enc also exists on disk, the presence of valid tls.* means reboot path!
	if err := os.WriteFile(leafEncPath, []byte("should-not-be-read"), 0o600); err != nil {
		t.Fatalf("writing leaf.enc: %v", err)
	}
	if err := os.WriteFile(steadyCred, []byte("{}"), 0o600); err != nil {
		t.Fatalf("writing steady cred: %v", err)
	}

	mock := &mockStore{}
	storeBuilderCalled := false

	opts := SetupOptions{
		CertPath:            certPath,
		KeyPath:             keyPath,
		WrappedLeafPath:     leafEncPath,
		SteadyStateCredFile: steadyCred,
		BootstrapCredFile:   bootCred,
		Now:                 func() time.Time { return now },
		StoreBuilder: func() (keystore.Store, error) {
			storeBuilderCalled = true
			return mock, nil
		},
	}

	err := Setup(context.Background(), opts)
	if err != nil {
		t.Fatalf("expected Setup to succeed on reboot path, got: %v", err)
	}

	if storeBuilderCalled {
		t.Errorf("StoreBuilder was called during steady-state reboot path; expected NO KMS client creation")
	}
	if mock.unwrapCalls > 0 {
		t.Errorf("mockStore.Unwrap was called %d times; expected 0 KMS calls during steady-state reboot", mock.unwrapCalls)
	}
	if got := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); got != steadyCred {
		t.Errorf("GOOGLE_APPLICATION_CREDENTIALS = %q, want %q", got, steadyCred)
	}
}

// Test 2: leaf.enc only -> unwraps, writes tls.* at 0600, proceeds.
func TestSetup_FirstBoot_UnwrapsAndWrites(t *testing.T) {
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "tls.crt")
	keyPath := filepath.Join(tmpDir, "tls.key")
	leafEncPath := filepath.Join(tmpDir, "leaf.enc")
	steadyCred := filepath.Join(tmpDir, "steady-cred.json")
	bootCred := filepath.Join(tmpDir, "boot-cred.json")

	now := time.Now()
	certPEM, keyPEM, _ := generateCertAndKey(t, now.Add(-10*time.Minute), now.Add(90*24*time.Hour))

	payloadJSON, err := json.Marshal(map[string]string{
		"cert": string(certPEM),
		"key":  string(keyPEM),
	})
	if err != nil {
		t.Fatalf("marshaling payload: %v", err)
	}

	wrappedCiphertext := []byte("ciphertext-encrypted-under-cluster-kek")
	// Test base64 encoding as delivered via Kubernetes Secret stringData
	wrappedB64 := base64.StdEncoding.EncodeToString(wrappedCiphertext)
	if err := os.WriteFile(leafEncPath, []byte(wrappedB64), 0o600); err != nil {
		t.Fatalf("writing leaf.enc: %v", err)
	}
	if err := os.WriteFile(bootCred, []byte(`{"type":"bootstrap"}`), 0o600); err != nil {
		t.Fatalf("writing boot cred: %v", err)
	}
	if err := os.WriteFile(steadyCred, []byte(`{"type":"steady"}`), 0o600); err != nil {
		t.Fatalf("writing steady cred: %v", err)
	}

	mock := &mockStore{
		unwrapFunc: func(ctx context.Context, wrapped []byte) ([]byte, error) {
			if string(wrapped) != string(wrappedCiphertext) {
				return nil, fmt.Errorf("unexpected ciphertext: %q", string(wrapped))
			}
			// Verify bootstrap credentials were set in env when Store was constructed / unwrapped
			if env := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); env != bootCred {
				return nil, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS during unwrap was %q, want %q", env, bootCred)
			}
			return payloadJSON, nil
		},
	}

	opts := SetupOptions{
		CertPath:            certPath,
		KeyPath:             keyPath,
		WrappedLeafPath:     leafEncPath,
		SteadyStateCredFile: steadyCred,
		BootstrapCredFile:   bootCred,
		Now:                 func() time.Time { return now },
		StoreBuilder: func() (keystore.Store, error) {
			return mock, nil
		},
	}

	err = Setup(context.Background(), opts)
	if err != nil {
		t.Fatalf("expected Setup to succeed on first boot, got: %v", err)
	}

	if mock.unwrapCalls != 1 {
		t.Errorf("mockStore.Unwrap was called %d times, want 1", mock.unwrapCalls)
	}

	// Verify tls.crt and tls.key exist and are 0600
	certFI, err := os.Stat(certPath)
	if err != nil {
		t.Fatalf("stating written cert %s: %v", certPath, err)
	}
	if certFI.Mode().Perm() != 0o600 {
		t.Errorf("cert file mode is %o, want 0600", certFI.Mode().Perm())
	}
	keyFI, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stating written key %s: %v", keyPath, err)
	}
	if keyFI.Mode().Perm() != 0o600 {
		t.Errorf("key file mode is %o, want 0600", keyFI.Mode().Perm())
	}

	// Verify certificate parses and matches key
	cert, err := ValidateCertificateFile(certPath, keyPath)
	if err != nil {
		t.Fatalf("validating written certificate files: %v", err)
	}
	if cert.Subject.CommonName != "kms-plugin-test" {
		t.Errorf("subject CN = %q, want 'kms-plugin-test'", cert.Subject.CommonName)
	}

	// Verify switched to steady-state credentials
	if env := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); env != steadyCred {
		t.Errorf("final GOOGLE_APPLICATION_CREDENTIALS = %q, want steady cred %q", env, steadyCred)
	}
}

// Test 3: Neither present -> refuses to start, error names the missing file.
func TestSetup_NeitherPresent_RefusesToStart(t *testing.T) {
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "tls.crt")
	keyPath := filepath.Join(tmpDir, "tls.key")
	leafEncPath := filepath.Join(tmpDir, "leaf.enc")

	mock := &mockStore{}
	storeBuilderCalled := false

	opts := SetupOptions{
		CertPath:        certPath,
		KeyPath:         keyPath,
		WrappedLeafPath: leafEncPath,
		StoreBuilder: func() (keystore.Store, error) {
			storeBuilderCalled = true
			return mock, nil
		},
	}

	err := Setup(context.Background(), opts)
	if err == nil {
		t.Fatalf("expected Setup to fail when neither tls.* nor leaf.enc exists")
	}

	if !strings.Contains(err.Error(), "refusing to start") {
		t.Errorf("error %q does not contain 'refusing to start'", err.Error())
	}
	if !strings.Contains(err.Error(), certPath) {
		t.Errorf("error %q does not name missing cert %s", err.Error(), certPath)
	}
	if !strings.Contains(err.Error(), leafEncPath) {
		t.Errorf("error %q does not name missing leaf.enc %s", err.Error(), leafEncPath)
	}

	if storeBuilderCalled {
		t.Errorf("StoreBuilder was called when neither file was present; expected no client creation")
	}
	if mock.unwrapCalls > 0 {
		t.Errorf("mockStore.Unwrap was called %d times; expected 0", mock.unwrapCalls)
	}
}

// Test 4: tls.* present but expired, leaf.enc also present -> refuses, does NOT fall back to bootstrap.
func TestSetup_TLSPresentButExpired_RefusesNoFallback(t *testing.T) {
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "tls.crt")
	keyPath := filepath.Join(tmpDir, "tls.key")
	leafEncPath := filepath.Join(tmpDir, "leaf.enc")
	bootCred := filepath.Join(tmpDir, "boot-cred.json")

	now := time.Now()
	// Cert expired 5 minutes ago
	certPEM, keyPEM, _ := generateCertAndKey(t, now.Add(-90*24*time.Hour), now.Add(-5*time.Minute))
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("writing expired cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("writing key: %v", err)
	}

	// leaf.enc is valid and present on disk
	freshCertPEM, freshKeyPEM, _ := generateCertAndKey(t, now.Add(-10*time.Minute), now.Add(90*24*time.Hour))
	freshPayload, _ := json.Marshal(map[string]string{
		"cert": string(freshCertPEM),
		"key":  string(freshKeyPEM),
	})
	if err := os.WriteFile(leafEncPath, []byte("wrapped-blob"), 0o600); err != nil {
		t.Fatalf("writing leaf.enc: %v", err)
	}
	if err := os.WriteFile(bootCred, []byte("{}"), 0o600); err != nil {
		t.Fatalf("writing boot cred: %v", err)
	}

	mock := &mockStore{
		unwrapFunc: func(ctx context.Context, wrapped []byte) ([]byte, error) {
			t.Errorf("CRITICAL VIOLATION: Unwrap was called despite tls.* being present!")
			return freshPayload, nil
		},
	}
	storeBuilderCalled := false

	opts := SetupOptions{
		CertPath:          certPath,
		KeyPath:           keyPath,
		WrappedLeafPath:   leafEncPath,
		BootstrapCredFile: bootCred,
		Now:               func() time.Time { return now },
		StoreBuilder: func() (keystore.Store, error) {
			storeBuilderCalled = true
			return mock, nil
		},
	}

	err := Setup(context.Background(), opts)
	if err == nil {
		t.Fatalf("expected Setup to refuse when tls certificate is expired")
	}

	if !strings.Contains(err.Error(), "refusing to start") {
		t.Errorf("error %q does not contain 'refusing to start'", err.Error())
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error %q does not mention 'expired'", err.Error())
	}
	if !strings.Contains(err.Error(), "will not fall back to bootstrap credential") {
		t.Errorf("error %q does not state 'will not fall back to bootstrap credential'", err.Error())
	}

	if storeBuilderCalled {
		t.Errorf("StoreBuilder was called despite expired steady-state cert; expected ZERO fallback")
	}
	if mock.unwrapCalls > 0 {
		t.Errorf("Unwrap was called %d times; expected ZERO fallback", mock.unwrapCalls)
	}

	// Verify the original expired cert was NOT modified or replaced
	remainingCert, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("reading certPath: %v", err)
	}
	if string(remainingCert) != string(certPEM) {
		t.Errorf("certPath was modified despite refusal!")
	}
}

// Edge Case: tls.crt present but tls.key missing, leaf.enc present -> hard refusal, no fallback.
func TestSetup_CertOnly_RefusesNoFallback(t *testing.T) {
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "tls.crt")
	keyPath := filepath.Join(tmpDir, "tls.key")
	leafEncPath := filepath.Join(tmpDir, "leaf.enc")

	now := time.Now()
	certPEM, _, _ := generateCertAndKey(t, now.Add(-1*time.Hour), now.Add(90*24*time.Hour))
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("writing cert: %v", err)
	}
	if err := os.WriteFile(leafEncPath, []byte("wrapped-blob"), 0o600); err != nil {
		t.Fatalf("writing leaf.enc: %v", err)
	}

	storeBuilderCalled := false
	opts := SetupOptions{
		CertPath:        certPath,
		KeyPath:         keyPath,
		WrappedLeafPath: leafEncPath,
		Now:             func() time.Time { return now },
		StoreBuilder: func() (keystore.Store, error) {
			storeBuilderCalled = true
			return &mockStore{}, nil
		},
	}

	err := Setup(context.Background(), opts)
	if err == nil {
		t.Fatalf("expected refusal when key is missing")
	}
	if !strings.Contains(err.Error(), "will not fall back to bootstrap credential") {
		t.Errorf("expected refusal to mention no fallback, got: %v", err)
	}
	if storeBuilderCalled {
		t.Errorf("StoreBuilder was called; expected no fallback")
	}
}

// Edge Case: tls.key present but tls.crt missing, leaf.enc present -> hard refusal, no fallback.
func TestSetup_KeyOnly_RefusesNoFallback(t *testing.T) {
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "tls.crt")
	keyPath := filepath.Join(tmpDir, "tls.key")
	leafEncPath := filepath.Join(tmpDir, "leaf.enc")

	now := time.Now()
	_, keyPEM, _ := generateCertAndKey(t, now.Add(-1*time.Hour), now.Add(90*24*time.Hour))
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("writing key: %v", err)
	}
	if err := os.WriteFile(leafEncPath, []byte("wrapped-blob"), 0o600); err != nil {
		t.Fatalf("writing leaf.enc: %v", err)
	}

	storeBuilderCalled := false
	opts := SetupOptions{
		CertPath:        certPath,
		KeyPath:         keyPath,
		WrappedLeafPath: leafEncPath,
		Now:             func() time.Time { return now },
		StoreBuilder: func() (keystore.Store, error) {
			storeBuilderCalled = true
			return &mockStore{}, nil
		},
	}

	err := Setup(context.Background(), opts)
	if err == nil {
		t.Fatalf("expected refusal when cert is missing")
	}
	if !strings.Contains(err.Error(), "will not fall back to bootstrap credential") {
		t.Errorf("expected refusal to mention no fallback, got: %v", err)
	}
	if storeBuilderCalled {
		t.Errorf("StoreBuilder was called; expected no fallback")
	}
}

// Edge Case: tls.crt and tls.key have mismatched keys, leaf.enc present -> hard refusal, no fallback.
func TestSetup_MismatchedKeyAndCert_RefusesNoFallback(t *testing.T) {
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "tls.crt")
	keyPath := filepath.Join(tmpDir, "tls.key")
	leafEncPath := filepath.Join(tmpDir, "leaf.enc")

	now := time.Now()
	certPEM, _, _ := generateCertAndKey(t, now.Add(-1*time.Hour), now.Add(90*24*time.Hour))
	_, otherKeyPEM, _ := generateCertAndKey(t, now.Add(-1*time.Hour), now.Add(90*24*time.Hour))

	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("writing cert: %v", err)
	}
	if err := os.WriteFile(keyPath, otherKeyPEM, 0o600); err != nil {
		t.Fatalf("writing mismatched key: %v", err)
	}
	if err := os.WriteFile(leafEncPath, []byte("wrapped-blob"), 0o600); err != nil {
		t.Fatalf("writing leaf.enc: %v", err)
	}

	storeBuilderCalled := false
	opts := SetupOptions{
		CertPath:        certPath,
		KeyPath:         keyPath,
		WrappedLeafPath: leafEncPath,
		Now:             func() time.Time { return now },
		StoreBuilder: func() (keystore.Store, error) {
			storeBuilderCalled = true
			return &mockStore{}, nil
		},
	}

	err := Setup(context.Background(), opts)
	if err == nil {
		t.Fatalf("expected refusal on mismatched cert/key")
	}
	if !strings.Contains(err.Error(), "will not fall back to bootstrap credential") {
		t.Errorf("expected refusal to mention no fallback, got: %v", err)
	}
	if storeBuilderCalled {
		t.Errorf("StoreBuilder was called; expected no fallback")
	}
}

// Round-trip test: payload shaped exactly as MarshalLeaf (internal/platform/kms/ca.go:220) produces it.
// Pins the accepted reader shape to the writer.
func TestParseLeafPayload_WriterShapeRoundTrip(t *testing.T) {
	now := time.Now()
	certPEM, keyPEM, _ := generateCertAndKey(t, now.Add(-1*time.Hour), now.Add(90*24*time.Hour))

	payload, err := json.Marshal(map[string]string{
		"cert": string(certPEM),
		"key":  string(keyPEM),
	})
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	pair, err := ParseLeafPayload(payload)
	if err != nil {
		t.Fatalf("ParseLeafPayload failed for writer-produced shape: %v", err)
	}
	if string(pair.CertPEM) != string(certPEM) {
		t.Errorf("cert PEM mismatch: got %q, want %q", string(pair.CertPEM), string(certPEM))
	}
	if string(pair.KeyPEM) != string(keyPEM) {
		t.Errorf("key PEM mismatch: got %q, want %q", string(pair.KeyPEM), string(keyPEM))
	}
}

// Rejection test: legacy aliases cert_pem and key_pem must be strictly rejected.
func TestParseLeafPayload_RejectsLegacyAliases(t *testing.T) {
	now := time.Now()
	certPEM, keyPEM, _ := generateCertAndKey(t, now.Add(-1*time.Hour), now.Add(90*24*time.Hour))

	payload, err := json.Marshal(map[string]string{
		"cert_pem": string(certPEM),
		"key_pem":  string(keyPEM),
	})
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	pair, err := ParseLeafPayload(payload)
	if err == nil {
		t.Fatalf("expected ParseLeafPayload to reject cert_pem/key_pem aliases, got pair: %+v", pair)
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "cert_pem") || !strings.Contains(errMsg, "key_pem") {
		t.Errorf("error %q should name the found keys cert_pem and key_pem", errMsg)
	}
	if !strings.Contains(errMsg, "expected exactly keys \"cert\" and \"key\"") {
		t.Errorf("error %q should state expected keys \"cert\" and \"key\"", errMsg)
	}
}

// Rejection test: concatenated PEM blocks must be strictly rejected.
func TestParseLeafPayload_RejectsConcatenatedPEM(t *testing.T) {
	now := time.Now()
	certPEM, keyPEM, _ := generateCertAndKey(t, now.Add(-1*time.Hour), now.Add(90*24*time.Hour))
	concat := append(append([]byte{}, certPEM...), keyPEM...)

	pair, err := ParseLeafPayload(concat)
	if err == nil {
		t.Fatalf("expected ParseLeafPayload to reject concatenated PEM, got pair: %+v", pair)
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "not a valid JSON object") {
		t.Errorf("error %q should indicate payload is not a valid JSON object", errMsg)
	}
	if !strings.Contains(errMsg, "expected JSON object with keys \"cert\" and \"key\"") {
		t.Errorf("error %q should name expected keys \"cert\" and \"key\"", errMsg)
	}
}

// Rejection test: extraneous keys or missing keys in JSON must be rejected.
func TestParseLeafPayload_RejectsMissingOrExtraKeys(t *testing.T) {
	now := time.Now()
	certPEM, keyPEM, _ := generateCertAndKey(t, now.Add(-1*time.Hour), now.Add(90*24*time.Hour))

	// Missing key: only cert
	payloadCertOnly, _ := json.Marshal(map[string]string{
		"cert": string(certPEM),
	})
	if _, err := ParseLeafPayload(payloadCertOnly); err == nil {
		t.Error("expected rejection when 'key' is missing")
	} else if !strings.Contains(err.Error(), "found JSON keys [cert]") {
		t.Errorf("error %q should report found JSON keys [cert]", err.Error())
	}

	// Missing cert: only key
	payloadKeyOnly, _ := json.Marshal(map[string]string{
		"key": string(keyPEM),
	})
	if _, err := ParseLeafPayload(payloadKeyOnly); err == nil {
		t.Error("expected rejection when 'cert' is missing")
	} else if !strings.Contains(err.Error(), "found JSON keys [key]") {
		t.Errorf("error %q should report found JSON keys [key]", err.Error())
	}

	// Extra key
	payloadExtra, _ := json.Marshal(map[string]string{
		"cert":  string(certPEM),
		"key":   string(keyPEM),
		"extra": "value",
	})
	if _, err := ParseLeafPayload(payloadExtra); err == nil {
		t.Error("expected rejection when extraneous key is present")
	} else if !strings.Contains(err.Error(), "found JSON keys [cert, extra, key]") {
		t.Errorf("error %q should report found JSON keys [cert, extra, key]", err.Error())
	}

	// Empty cert string
	payloadEmptyCert, _ := json.Marshal(map[string]string{
		"cert": "",
		"key":  string(keyPEM),
	})
	if _, err := ParseLeafPayload(payloadEmptyCert); err == nil {
		t.Error("expected rejection when 'cert' is empty string")
	} else if !strings.Contains(err.Error(), "\"cert\" field is empty") {
		t.Errorf("error %q should report cert field is empty", err.Error())
	}
}

// Rejection test: raw (non-base64) ciphertext in UnwrapLeafBlob must be rejected.
func TestUnwrapLeafBlob_RejectsRawCiphertext(t *testing.T) {
	rawBinaryCiphertext := []byte("\x00\x01\x02\x03\xff\xfe\xfd\x12\x34\x56\x78\x9a\xbc\xde\xf0")
	mock := &mockStore{
		unwrapFunc: func(ctx context.Context, wrapped []byte) ([]byte, error) {
			t.Errorf("CRITICAL VIOLATION: store.Unwrap was called with raw ciphertext!")
			return nil, fmt.Errorf("unexpected call")
		},
	}

	_, err := UnwrapLeafBlob(context.Background(), mock, rawBinaryCiphertext)
	if err == nil {
		t.Fatal("expected UnwrapLeafBlob to reject raw non-base64 ciphertext")
	}

	if mock.unwrapCalls != 0 {
		t.Errorf("mock.Unwrap called %d times; expected 0 calls on raw ciphertext", mock.unwrapCalls)
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "not valid standard base64") {
		t.Errorf("error %q should mention not valid standard base64", errMsg)
	}
	if !strings.Contains(errMsg, "raw binary 0x") {
		t.Errorf("error %q should name the raw binary byte shape", errMsg)
	}
}

// Rejection test: unencrypted text/JSON passed as leaf.enc in UnwrapLeafBlob must be rejected.
func TestUnwrapLeafBlob_RejectsUnencryptedNonBase64Text(t *testing.T) {
	nonB64Text := []byte("{\"cert\": \"-----BEGIN CERTIFICATE-----\"}")
	mock := &mockStore{}

	_, err := UnwrapLeafBlob(context.Background(), mock, nonB64Text)
	if err == nil {
		t.Fatal("expected UnwrapLeafBlob to reject non-base64 JSON text")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "not valid standard base64") {
		t.Errorf("error %q should mention not valid standard base64", errMsg)
	}
	if !strings.Contains(errMsg, "ASCII/text") {
		t.Errorf("error %q should name ASCII/text shape", errMsg)
	}
}

// Acceptance test: standard base64 ciphertext is decoded and passed to KMS Unwrap.
func TestUnwrapLeafBlob_AcceptsBase64(t *testing.T) {
	expectedPlaintext := []byte(`{"cert":"c","key":"k"}`)
	rawCiphertext := []byte("kms-wrapped-ciphertext-bytes-here")
	b64String := base64.StdEncoding.EncodeToString(rawCiphertext)

	mock := &mockStore{
		unwrapFunc: func(ctx context.Context, wrapped []byte) ([]byte, error) {
			if string(wrapped) != string(rawCiphertext) {
				return nil, fmt.Errorf("unexpected ciphertext to unwrap: got %q, want %q", string(wrapped), string(rawCiphertext))
			}
			return expectedPlaintext, nil
		},
	}

	// Also verify whitespace/newline trimming (standard for Kubernetes Secret file mounts)
	blobWithNewline := []byte(b64String + "\n")
	plain, err := UnwrapLeafBlob(context.Background(), mock, blobWithNewline)
	if err != nil {
		t.Fatalf("UnwrapLeafBlob failed: %v", err)
	}
	if string(plain) != string(expectedPlaintext) {
		t.Errorf("got plaintext %q, want %q", string(plain), string(expectedPlaintext))
	}
	if mock.unwrapCalls != 1 {
		t.Errorf("mock.Unwrap called %d times, want 1", mock.unwrapCalls)
	}
}

// Rejection test: empty leaf blob in UnwrapLeafBlob must be rejected.
func TestUnwrapLeafBlob_RejectsEmpty(t *testing.T) {
	mock := &mockStore{}
	if _, err := UnwrapLeafBlob(context.Background(), mock, nil); err == nil {
		t.Error("expected error on nil blob")
	}
	if _, err := UnwrapLeafBlob(context.Background(), mock, []byte("   \n\t")); err == nil {
		t.Error("expected error on whitespace-only blob")
	}
}

func TestPrepareCredentialDir_Success(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("skipping PrepareCredentialDir test: requires root (CAP_CHOWN)")
	}
	tmp := t.TempDir()
	credDir := filepath.Join(tmp, "kms")
	if err := os.Mkdir(credDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(credDir, "leaf.enc")
	if err := os.WriteFile(testFile, []byte("ciphertext"), 0600); err != nil {
		t.Fatal(err)
	}

	uid := os.Getuid()
	if err := PrepareCredentialDir(credDir, uid); err != nil {
		t.Fatalf("PrepareCredentialDir failed: %v", err)
	}

	fi, err := os.Stat(credDir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0700 {
		t.Errorf("expected dir mode 0700, got %o", fi.Mode().Perm())
	}

	// Calling again (idempotent / restart path) returns nil
	if err := PrepareCredentialDir(credDir, uid); err != nil {
		t.Fatalf("idempotent PrepareCredentialDir failed: %v", err)
	}
}

