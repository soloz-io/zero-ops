package nodecert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCA puts a kubeadm-shaped cluster CA on disk: RSA, PKCS#1, as kubeadm writes it.
func writeCA(t *testing.T, dir string) Paths {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "kubernetes"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := Paths{
		CACert: filepath.Join(dir, "pki", "ca.crt"),
		CAKey:  filepath.Join(dir, "pki", "ca.key"),
		Cert:   filepath.Join(dir, "kms", "tls.crt"),
		Key:    filepath.Join(dir, "kms", "tls.key"),
	}
	if err := os.MkdirAll(filepath.Dir(p.CACert), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.CACert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.CAKey, pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(raw)
	if b == nil {
		t.Fatal("not PEM")
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestACertificateIsIssuedAndChainsToTheClusterCA(t *testing.T) {
	// The whole point: the federation provider's trust anchor IS this CA, so a
	// certificate that does not chain to it authenticates nothing.
	dir := t.TempDir()
	p := writeCA(t, dir)

	wrote, err := Ensure(p, "nutgraf-01")
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("no certificate was issued on a node that had none")
	}

	leaf := readCert(t, p.Cert)
	pool := x509.NewCertPool()
	caRaw, _ := os.ReadFile(p.CACert)
	if !pool.AppendCertsFromPEM(caRaw) {
		t.Fatal("the CA did not load")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("the issued certificate does not chain to the cluster CA: %v", err)
	}
}

func TestTheSubjectIsPerClusterAndClientAuthOnly(t *testing.T) {
	dir := t.TempDir()
	p := writeCA(t, dir)
	if _, err := Ensure(p, "nutgraf-01"); err != nil {
		t.Fatal(err)
	}
	leaf := readCert(t, p.Cert)

	if leaf.Subject.CommonName != "kms-plugin.nutgraf-01" {
		t.Errorf("subject is %q; the provider maps this to a principal and it must name the "+
			"cluster", leaf.Subject.CommonName)
	}
	// CLIENT auth only. A certificate that could also serve is one that can be used for
	// something nobody intended.
	for _, u := range leaf.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth {
			t.Error("the certificate carries serverAuth; it only ever authenticates TO Google")
		}
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Errorf("extended key usage is %v, want clientAuth alone", leaf.ExtKeyUsage)
	}
}

func TestTheKeyIsNotWorldReadable(t *testing.T) {
	// WriteFile's mode is masked by umask, so this is checked rather than assumed: 0600
	// that became 0644 publishes the plugin's private key to every process on the node.
	dir := t.TempDir()
	p := writeCA(t, dir)
	if _, err := Ensure(p, "c"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p.Key)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("the private key is mode %o, want 0600", fi.Mode().Perm())
	}
}

func TestASecondRunDoesNotChurnTheCertificate(t *testing.T) {
	// kubelet restarts static pods on every node reboot and every control-plane roll.
	// Re-issuing each time would mean a new principal in the audit log for no reason.
	dir := t.TempDir()
	p := writeCA(t, dir)
	if _, err := Ensure(p, "c"); err != nil {
		t.Fatal(err)
	}
	first := readCert(t, p.Cert).SerialNumber.String()

	wrote, err := Ensure(p, "c")
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Fatal("a fresh certificate was re-issued on a restart")
	}
	if readCert(t, p.Cert).SerialNumber.String() != first {
		t.Fatal("the certificate changed without being re-issued")
	}
}

func TestACertificateForADifferentClusterIsReplaced(t *testing.T) {
	// A re-purposed node holding another cluster's certificate would authenticate as the
	// wrong principal, or fail against this cluster's provider — the better outcome, and
	// still wrong.
	dir := t.TempDir()
	p := writeCA(t, dir)
	if _, err := Ensure(p, "old-cluster"); err != nil {
		t.Fatal(err)
	}
	wrote, err := Ensure(p, "new-cluster")
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("another cluster's certificate was kept")
	}
	if cn := readCert(t, p.Cert).Subject.CommonName; cn != "kms-plugin.new-cluster" {
		t.Fatalf("subject is %q after re-issue", cn)
	}
}

func TestAnExpiringCertificateIsReplaced(t *testing.T) {
	// kubeadm renews only its own hardcoded list and will never touch this file, so a
	// certificate issued once expires silently about a year later — and the symptom is a
	// control plane that cannot cold-read Secrets. Renewal happens on pod start or not at
	// all.
	dir := t.TempDir()
	p := writeCA(t, dir)
	if _, err := Ensure(p, "c"); err != nil {
		t.Fatal(err)
	}
	leaf := readCert(t, p.Cert)

	// Just past the renewal threshold.
	nearlyGone := leaf.NotAfter.Add(-Lifetime/2 + time.Minute)
	if f := RemainingFraction(leaf, nearlyGone); f >= 0.5 {
		t.Fatalf("remaining fraction at the threshold is %v; the test's arithmetic is wrong", f)
	}
	if f := RemainingFraction(leaf, leaf.NotAfter.Add(time.Hour)); f != 0 {
		t.Errorf("an expired certificate reports %v remaining, want 0", f)
	}
}

func TestAMissingCAIsAnExplicitRetryableFailure(t *testing.T) {
	// THE DEADLOCK THIS PACKAGE EXISTS TO AVOID. The static pod manifest arrives as
	// cloud-init content before kubeadm runs, so on a fresh node the CA does not exist
	// yet. An initContainer that failed quietly, or succeeded without a certificate,
	// would start a plugin that cannot authenticate — and the API server would never
	// come up, so kubeadm would never create the CA.
	dir := t.TempDir()
	p := Paths{
		CACert: filepath.Join(dir, "nope", "ca.crt"),
		CAKey:  filepath.Join(dir, "nope", "ca.key"),
		Cert:   filepath.Join(dir, "kms", "tls.crt"),
		Key:    filepath.Join(dir, "kms", "tls.key"),
	}
	_, err := Ensure(p, "c")
	if err == nil {
		t.Fatal("a missing cluster CA was accepted; the plugin would start without a credential")
	}
	if _, statErr := os.Stat(p.Cert); statErr == nil {
		t.Fatal("a certificate was written without a CA to sign it")
	}
}

func TestANonCAIsRefused(t *testing.T) {
	// Something that is not a CA would sign a certificate that chains to nothing, and the
	// failure would surface as a trust error against Google rather than here.
	dir := t.TempDir()
	p := writeCA(t, dir)
	leafKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "not-a-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &leafKey.PublicKey, leafKey)
	if err := os.WriteFile(p.CACert, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.CAKey, pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)}), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Ensure(p, "c")
	if err == nil {
		t.Fatal("a non-CA certificate was accepted as the signer")
	}
}
