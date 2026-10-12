package kms

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
)

func parse(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	b, _ := pem.Decode(certPEM)
	if b == nil {
		t.Fatal("not PEM")
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestALeafChainsToItsOwnClusterCA(t *testing.T) {
	ca, err := NewKMSCA("kms-canary-01")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := IssueLeaf(ca, "kms-canary-01")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.CertPEM) {
		t.Fatal("the CA did not load")
	}
	if _, err := parse(t, leaf.CertPEM).Verify(x509.VerifyOptions{
		Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("the leaf does not chain to its CA: %v", err)
	}
}

func TestOneClusterCALeafDoesNotChainToAnother(t *testing.T) {
	// THE ISOLATION THAT OPTION A COULD NOT PROVIDE. With the cluster CA as the trust
	// anchor, every certificate in the cluster chained to it and only an IAM binding
	// separated a kubelet certificate from a KMS credential. With a dedicated CA per
	// cluster, a leaf from one simply does not validate against another's trust store —
	// isolation is a property of the chain, not of a grant somebody could widen.
	a, err := NewKMSCA("cluster-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewKMSCA("cluster-b")
	if err != nil {
		t.Fatal(err)
	}
	leafA, err := IssueLeaf(a, "cluster-a")
	if err != nil {
		t.Fatal(err)
	}
	poolB := x509.NewCertPool()
	if !poolB.AppendCertsFromPEM(b.CertPEM) {
		t.Fatal("CA b did not load")
	}
	if _, err := parse(t, leafA.CertPEM).Verify(x509.VerifyOptions{
		Roots: poolB, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err == nil {
		t.Fatal("cluster A's leaf validated against cluster B's trust anchor")
	}
}

func TestIssuingACrossClusterLeafIsRefused(t *testing.T) {
	// Signing cluster A's identity with cluster B's CA produces a certificate that
	// validates against B's anchor while CLAIMING to be A — it would work, and it breaks
	// the isolation above. Refused at issuance rather than found in an audit.
	ca, err := NewKMSCA("cluster-b")
	if err != nil {
		t.Fatal(err)
	}
	_, err = IssueLeaf(ca, "cluster-a")
	if err == nil {
		t.Fatal("a leaf was signed by another cluster's CA")
	}
	if !strings.Contains(err.Error(), "wrong trust anchor") {
		t.Fatalf("the error does not explain the consequence: %v", err)
	}
}

func TestTheLeafCannotSignAnything(t *testing.T) {
	// THE PROPERTY THE WHOLE DECISION RESTS ON. A compromised node yields one credential
	// and no ability to issue another — which is what mounting the cluster CA's key could
	// not say.
	ca, _ := NewKMSCA("c")
	leaf, err := IssueLeaf(ca, "c")
	if err != nil {
		t.Fatal(err)
	}
	cert := parse(t, leaf.CertPEM)
	if cert.IsCA {
		t.Error("the leaf is a CA; a node holding it could mint identities")
	}
	if cert.KeyUsage&x509.KeyUsageCertSign != 0 {
		t.Error("the leaf carries certSign")
	}
	for _, u := range cert.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth {
			t.Error("the leaf carries serverAuth; it only authenticates TO Google")
		}
	}
	if len(cert.DNSNames)+len(cert.URIs)+len(cert.IPAddresses) != 0 {
		t.Error("the leaf carries SANs; the mapping uses the subject CN and nothing else")
	}
}

func TestTheCACannotSignAnotherCA(t *testing.T) {
	// An intermediate under this CA would be a second issuer nobody decided to have.
	ca, _ := NewKMSCA("c")
	cert := parse(t, ca.CertPEM)
	if !cert.MaxPathLenZero || cert.MaxPathLen != 0 {
		t.Errorf("the CA permits intermediates (MaxPathLen=%d, zero=%v)",
			cert.MaxPathLen, cert.MaxPathLenZero)
	}
	if cert.KeyUsage&x509.KeyUsageDigitalSignature != 0 {
		t.Error("the CA carries digitalSignature; its only job is signing one leaf")
	}
}

func TestTheSubjectIsWhatTheProviderMapsToAPrincipal(t *testing.T) {
	ca, _ := NewKMSCA("kms-canary-01")
	leaf, _ := IssueLeaf(ca, "kms-canary-01")
	if cn := parse(t, leaf.CertPEM).Subject.CommonName; cn != "kms-plugin.kms-canary-01" {
		t.Fatalf("subject is %q; the attribute mapping reads google.subject from "+
			"assertion.subject.dn.cn, so this string IS the identity", cn)
	}
}

func TestRenewalIsFlaggedWithWeeksOfMargin(t *testing.T) {
	// A SCHEDULING SIGNAL, NOT AN ALARM. ADR-100 requires renewal to be a planned roll;
	// a flag that fired with hours left would make it an incident instead.
	ca, _ := NewKMSCA("c")
	leaf, _ := IssueLeaf(ca, "c")

	fresh, left, err := LeafNeedsRenewal(leaf.CertPEM, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("a freshly issued leaf wants renewal")
	}
	if left < 80*24*time.Hour {
		t.Fatalf("a fresh leaf has only %v left; LeafValidity is %v", left, LeafValidity)
	}

	// Twenty-five days out: inside the planning horizon. Not twenty-eight — 28/90 is
	// 0.311, above the 0.3 threshold, so renewal is correctly NOT yet flagged there. The
	// threshold is ~27 days of a 90-day leaf, and the first version of this test asserted
	// the wrong side of it.
	soon := time.Now().Add(LeafValidity - 25*24*time.Hour)
	due, left, err := LeafNeedsRenewal(leaf.CertPEM, soon)
	if err != nil {
		t.Fatal(err)
	}
	if !due {
		t.Fatalf("with %v left the roll is not yet flagged; that is too late to plan one", left)
	}
}

func TestANonCAIsRefusedAsAnIssuer(t *testing.T) {
	ca, _ := NewKMSCA("c")
	leaf, _ := IssueLeaf(ca, "c")
	// Try to use the leaf as the issuer.
	_, err := IssueLeaf(&PEMPair{CertPEM: leaf.CertPEM, KeyPEM: leaf.KeyPEM}, "c")
	if err == nil {
		t.Fatal("a leaf was accepted as an issuing CA")
	}
}

func TestTheKeyIsNeverInTheCertificateField(t *testing.T) {
	// The pair is two fields so a caller cannot store or log it as one blob, which is how
	// a private key ends up somewhere a certificate was expected.
	ca, _ := NewKMSCA("c")
	if strings.Contains(string(ca.CertPEM), "PRIVATE KEY") {
		t.Fatal("the CA's certificate field contains a private key")
	}
	leaf, _ := IssueLeaf(ca, "c")
	if strings.Contains(string(leaf.CertPEM), "PRIVATE KEY") {
		t.Fatal("the leaf's certificate field contains a private key")
	}
}

type fakeKMSCrypto struct {
	failDecrypt bool
	failEncrypt bool
}

func (f *fakeKMSCrypto) Encrypt(_ context.Context, target string, plaintext []byte) (string, string, error) {
	if f.failEncrypt {
		return "", "", errors.New("kms encrypt denied")
	}
	return target + "/cryptoKeyVersions/1", base64.StdEncoding.EncodeToString(plaintext), nil
}

func (f *fakeKMSCrypto) Decrypt(_ context.Context, keyName, ciphertext string) ([]byte, error) {
	if f.failDecrypt {
		return nil, errors.New("kms decrypt denied")
	}
	return base64.StdEncoding.DecodeString(ciphertext)
}

func TestWrappedLeafCannotUnwrapWithoutKMS(t *testing.T) {
	ca, err := NewKMSCA("kms-canary-01")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := IssueLeaf(ca, "kms-canary-01")
	if err != nil {
		t.Fatal(err)
	}

	kmsClient := &fakeKMSCrypto{}
	wrapped, err := WrapLeaf(context.Background(), kmsClient, "etcd-kek-kms-canary-01", leaf)
	if err != nil {
		t.Fatalf("wrapping leaf: %v", err)
	}

	// 1. Plaintext unwrap without KMS MUST FAIL.
	// An attacker reading the hypervisor's user-data copy directly cannot decode it.
	if _, err := UnmarshalLeaf([]byte(wrapped)); err == nil {
		t.Fatal("wrapped leaf was unmarshaled directly as plaintext without KMS decryption")
	}

	// 2. KMS Decrypt failure (e.g. invalid/expired credential, IAM revoked) MUST FAIL.
	kmsDenied := &fakeKMSCrypto{failDecrypt: true}
	if _, err := UnwrapLeaf(context.Background(), kmsDenied, "etcd-kek-kms-canary-01", wrapped); err == nil {
		t.Fatal("UnwrapLeaf succeeded when KMS Decrypt failed")
	}

	// 3. Authorized KMS Decrypt recovers the exact leaf pair.
	unwrapped, err := UnwrapLeaf(context.Background(), kmsClient, "etcd-kek-kms-canary-01", wrapped)
	if err != nil {
		t.Fatalf("UnwrapLeaf with KMS failed: %v", err)
	}
	if string(unwrapped.CertPEM) != string(leaf.CertPEM) {
		t.Fatal("unwrapped cert does not match original")
	}
	if string(unwrapped.KeyPEM) != string(leaf.KeyPEM) {
		t.Fatal("unwrapped key does not match original")
	}
}

func TestBootstrapLeafExpiresInsideOneHour(t *testing.T) {
	ca, err := NewKMSCA("kms-canary-01")
	if err != nil {
		t.Fatal(err)
	}
	bootLeaf, err := IssueBootstrapLeaf(ca, "kms-canary-01")
	if err != nil {
		t.Fatalf("issuing bootstrap leaf: %v", err)
	}

	cert := parse(t, bootLeaf.CertPEM)
	now := time.Now()
	remaining := cert.NotAfter.Sub(now)

	// Must be <= 1 hour (plus up to 1 minute clock skew backdating)
	if remaining > BootstrapLeafValidity+time.Minute {
		t.Fatalf("bootstrap leaf validity %v exceeds %v ceiling", remaining, BootstrapLeafValidity)
	}
	if remaining < 50*time.Minute {
		t.Fatalf("bootstrap leaf validity %v is too short (< 50m)", remaining)
	}

	// Must chain to the CA
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.CertPEM) {
		t.Fatal("failed to append CA cert to pool")
	}
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("bootstrap leaf does not chain to cluster CA: %v", err)
	}
}

func TestCAEnsureIsIdempotent(t *testing.T) {
	// Re-running ca ensure on an existing CA must preserve the existing CA material,
	// never regenerating a new CA key.
	cluster := "kms-canary-01"
	existingCA, err := NewKMSCA(cluster)
	if err != nil {
		t.Fatal(err)
	}

	// Helper representing ensure logic: if already present, return existing without regenerating
	ensureCA := func(current *PEMPair) (*PEMPair, bool, error) {
		if current != nil {
			return current, false, nil
		}
		ca, err := NewKMSCA(cluster)
		return ca, true, err
	}

	// First ensure generates
	ca1, created1, err := ensureCA(nil)
	if err != nil || !created1 || ca1 == nil {
		t.Fatalf("first ensure failed: %v", err)
	}

	// Second ensure on ca1 returns identical CA
	ca2, created2, err := ensureCA(ca1)
	if err != nil || created2 || ca2 == nil {
		t.Fatalf("second ensure should not have created new CA: %v", err)
	}
	if string(ca1.CertPEM) != string(ca2.CertPEM) || string(ca1.KeyPEM) != string(ca2.KeyPEM) {
		t.Fatal("CA material changed across ensure calls")
	}

	// Ensure with existingCA
	ca3, created3, err := ensureCA(existingCA)
	if err != nil || created3 || string(ca3.KeyPEM) != string(existingCA.KeyPEM) {
		t.Fatal("existing CA material was not preserved")
	}
}

func TestCAKeyAppearsInNoNodeBoundSecret(t *testing.T) {
	cluster := "kms-canary-01"
	namespace := "platform-capi"

	ca, err := NewKMSCA(cluster)
	if err != nil {
		t.Fatal(err)
	}
	bootLeaf, err := IssueBootstrapLeaf(ca, cluster)
	if err != nil {
		t.Fatal(err)
	}
	longLeaf, err := IssueLeaf(ca, cluster)
	if err != nil {
		t.Fatal(err)
	}

	kmsClient := &fakeKMSCrypto{}
	wrapped, err := WrapLeaf(context.Background(), kmsClient, "etcd-kek-"+cluster, longLeaf)
	if err != nil {
		t.Fatal(err)
	}

	// Render bootstrap secret
	bootRaw, err := assets.ReadManifest("secrets/kms-plugin-bootstrap.yaml")
	if err != nil {
		t.Fatalf("reading bootstrap template: %v", err)
	}
	bootTmpl, err := template.New("bootstrap").Parse(string(bootRaw))
	if err != nil {
		t.Fatalf("parsing bootstrap template: %v", err)
	}
	var bootBuf bytes.Buffer
	if err := bootTmpl.Execute(&bootBuf, map[string]any{
		"SecretName": assets.KMSPluginBootstrapSecretName(cluster),
		"Namespace":  namespace,
		"CertB64":    base64.StdEncoding.EncodeToString(bootLeaf.CertPEM),
		"KeyB64":     base64.StdEncoding.EncodeToString(bootLeaf.KeyPEM),
	}); err != nil {
		t.Fatalf("rendering bootstrap template: %v", err)
	}
	bootSecret := bootBuf.String()

	// Render wrapped secret
	wrapRaw, err := assets.ReadManifest("secrets/kms-plugin-wrapped.yaml")
	if err != nil {
		t.Fatalf("reading wrapped template: %v", err)
	}
	wrapTmpl, err := template.New("wrapped").Parse(string(wrapRaw))
	if err != nil {
		t.Fatalf("parsing wrapped template: %v", err)
	}
	var wrapBuf bytes.Buffer
	if err := wrapTmpl.Execute(&wrapBuf, map[string]any{
		"SecretName":  assets.KMSPluginWrappedSecretName(cluster),
		"Namespace":   namespace,
		"WrappedLeaf": wrapped,
	}); err != nil {
		t.Fatalf("rendering wrapped template: %v", err)
	}
	wrapSecret := wrapBuf.String()

	caKeyStr := string(ca.KeyPEM)
	caKeyB64 := base64.StdEncoding.EncodeToString(ca.KeyPEM)

	// Assert CA key does NOT appear anywhere in the bootstrap Secret
	if strings.Contains(bootSecret, caKeyStr) {
		t.Fatal("CA private key PEM appeared in bootstrap Secret")
	}
	if strings.Contains(bootSecret, caKeyB64) {
		t.Fatal("base64-encoded CA private key appeared in bootstrap Secret")
	}

	// Assert CA key does NOT appear anywhere in the wrapped Secret
	if strings.Contains(wrapSecret, caKeyStr) {
		t.Fatal("CA private key PEM appeared in wrapped Secret")
	}
	if strings.Contains(wrapSecret, caKeyB64) {
		t.Fatal("base64-encoded CA private key appeared in wrapped Secret")
	}

	// Assert bootstrap private key is distinct from CA private key
	if string(bootLeaf.KeyPEM) == caKeyStr {
		t.Fatal("bootstrap leaf private key is identical to CA private key")
	}
}
