package cluster

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/internal/platform/kms"
)

func makeTestCert(t *testing.T, validity time.Duration, isCA bool) ([]byte, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(validity),
		IsCA:                  isCA,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(priv)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func setupValidPreconditions(t *testing.T, cluster, namespace string, bootLeafValidity time.Duration) *KMSPreconditions {
	caCert, caKey := makeTestCert(t, kms.CAValidity, true)
	bootCert, bootKey := makeTestCert(t, bootLeafValidity, false)

	secrets := map[string]map[string]any{
		assets.KMSCASecretName(cluster): {
			"tls.crt": base64.StdEncoding.EncodeToString(caCert),
			"tls.key": base64.StdEncoding.EncodeToString(caKey),
		},
		assets.KMSPluginBootstrapSecretName(cluster): {
			"tls.crt": base64.StdEncoding.EncodeToString(bootCert),
			"tls.key": base64.StdEncoding.EncodeToString(bootKey),
		},
		assets.KMSPluginWrappedSecretName(cluster): {
			"leaf.enc": "valid-ciphertext-payload",
		},
	}

	return &KMSPreconditions{
		ClusterName: cluster,
		Namespace:   namespace,
		KMSConfig: KMSSettings{
			ProjectID: "test-proj",
			Location:  "europe-west3",
			KeyRing:   "soloz-etcd",
		},
		SecretGetter: func(ctx context.Context, name string) (map[string]any, error) {
			s, ok := secrets[name]
			if !ok {
				return nil, fmt.Errorf("secret %s not found", name)
			}
			return s, nil
		},
		TrustVerifier: func(ctx context.Context, pool, provider string, caPEM []byte) error {
			return nil
		},
		KeyVerifier: func(ctx context.Context, keyName string) error {
			return nil
		},
		IAMVerifier: func(ctx context.Context, keyName, expectedMember string) error {
			return nil
		},
	}
}

func TestKMSPreconditionsAllPass(t *testing.T) {
	pre := setupValidPreconditions(t, "kms-canary-01", "platform-capi", 55*time.Minute)
	if err := pre.Assert(context.Background()); err != nil {
		t.Fatalf("expected preconditions to pass, got: %v", err)
	}
}

func TestKMSPreconditionsFailsOnMissingCASecret(t *testing.T) {
	pre := setupValidPreconditions(t, "kms-canary-01", "platform-capi", 55*time.Minute)
	origGetter := pre.SecretGetter
	pre.SecretGetter = func(ctx context.Context, name string) (map[string]any, error) {
		if name == assets.KMSCASecretName("kms-canary-01") {
			return nil, errors.New("not found")
		}
		return origGetter(ctx, name)
	}

	err := pre.Assert(context.Background())
	if err == nil || !strings.Contains(err.Error(), "1/6 failed") {
		t.Fatalf("expected 1/6 failure for missing CA secret, got: %v", err)
	}
}

func TestKMSPreconditionsFailsOnWIFTrustMismatch(t *testing.T) {
	pre := setupValidPreconditions(t, "kms-canary-01", "platform-capi", 55*time.Minute)
	pre.TrustVerifier = func(ctx context.Context, pool, provider string, caPEM []byte) error {
		return errors.New("trust anchor mismatch")
	}

	err := pre.Assert(context.Background())
	if err == nil || !strings.Contains(err.Error(), "2/6 failed") {
		t.Fatalf("expected 2/6 failure for trust mismatch, got: %v", err)
	}
}

func TestKMSPreconditionsFailsOnMissingCryptoKey(t *testing.T) {
	pre := setupValidPreconditions(t, "kms-canary-01", "platform-capi", 55*time.Minute)
	pre.KeyVerifier = func(ctx context.Context, keyName string) error {
		return errors.New("key does not exist")
	}

	err := pre.Assert(context.Background())
	if err == nil || !strings.Contains(err.Error(), "3/6 failed") {
		t.Fatalf("expected 3/6 failure for missing key, got: %v", err)
	}
}

func TestKMSPreconditionsFailsOnMissingIAMBinding(t *testing.T) {
	pre := setupValidPreconditions(t, "kms-canary-01", "platform-capi", 55*time.Minute)
	pre.IAMVerifier = func(ctx context.Context, keyName, expectedMember string) error {
		return errors.New("binding missing")
	}

	err := pre.Assert(context.Background())
	if err == nil || !strings.Contains(err.Error(), "4/6 failed") {
		t.Fatalf("expected 4/6 failure for missing IAM binding, got: %v", err)
	}
}

func TestKMSPreconditionsFailsOnMissingNodeBoundSecret(t *testing.T) {
	pre := setupValidPreconditions(t, "kms-canary-01", "platform-capi", 55*time.Minute)
	origGetter := pre.SecretGetter
	pre.SecretGetter = func(ctx context.Context, name string) (map[string]any, error) {
		if name == assets.KMSPluginWrappedSecretName("kms-canary-01") {
			return nil, errors.New("not found")
		}
		return origGetter(ctx, name)
	}

	err := pre.Assert(context.Background())
	if err == nil || !strings.Contains(err.Error(), "5/6 failed") {
		t.Fatalf("expected 5/6 failure for missing wrapped secret, got: %v", err)
	}
}

func TestKMSPreconditionsFailsOnStaleBootstrapLeaf(t *testing.T) {
	// Bootstrap leaf with only 20 minutes left (< 30m / 50% validity remaining)
	pre := setupValidPreconditions(t, "kms-canary-01", "platform-capi", 20*time.Minute)

	err := pre.Assert(context.Background())
	if err == nil || !strings.Contains(err.Error(), "6/6 failed") {
		t.Fatalf("expected 6/6 failure for expired/stale bootstrap leaf, got: %v", err)
	}
}
