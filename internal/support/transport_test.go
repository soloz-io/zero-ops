package support

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// issue writes a self-signed client certificate and key, returning their paths.
func issue(t *testing.T, cn string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath = filepath.Join(dir, "tls.crt")
	keyPath = filepath.Join(dir, "tls.key")
	writePEM(t, certPath, "CERTIFICATE", der)
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	writePEM(t, keyPath, "EC PRIVATE KEY", kb)
	return certPath, keyPath
}

func writePEM(t *testing.T, path, typ string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ADR-077 ships the agent inert to every box, and ADR-077 addendum 1 makes the
// certificate the enrolment. "Inert" therefore has to mean exactly "no readable
// certificate" -- and it has to be re-evaluated, not decided once at start-up,
// or a tenant who revokes by deleting the Secret keeps being reported until
// somebody restarts a pod.
func TestEnrolmentIsTheCertificateAndIsReEvaluated(t *testing.T) {
	certPath, keyPath := issue(t, "dev.acme.example")
	e := Enrolment{CertFile: certPath, KeyFile: keyPath, Endpoint: "https://support.example"}

	if !e.Enrolled() {
		t.Fatal("a box with a certificate and an endpoint is enrolled")
	}
	// The tenant revokes.
	if err := os.Remove(certPath); err != nil {
		t.Fatal(err)
	}
	if e.Enrolled() {
		t.Error("the certificate was deleted and the box still reports as enrolled; " +
			"revocation would not take effect until a restart")
	}
}

func TestNoEndpointIsNotEnrolled(t *testing.T) {
	certPath, keyPath := issue(t, "dev.acme.example")
	e := Enrolment{CertFile: certPath, KeyFile: keyPath}
	if e.Enrolled() {
		t.Error("a box with nowhere to report is not enrolled, whatever it holds")
	}
}

// The identity the platform sees comes from the certificate, never from a field
// the agent chose. A box cannot report under a name it was not issued.
func TestIdentityComesFromTheCertificate(t *testing.T) {
	certPath, keyPath := issue(t, "dev.acme.example")
	got, err := Enrolment{CertFile: certPath, KeyFile: keyPath}.Identity()
	if err != nil {
		t.Fatal(err)
	}
	if got != "dev.acme.example" {
		t.Errorf("Identity() = %q, want the certificate's common name", got)
	}
}

// A transport that quietly degraded to an unauthenticated connection when the
// client certificate would not load would keep reporting and look healthy.
func TestTransportRefusesToStartWithoutAUsableCertificate(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "tls.crt")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTransport(Enrolment{CertFile: bad, KeyFile: bad, Endpoint: "https://x"}); err == nil {
		t.Error("a malformed certificate was accepted; the agent would report unauthenticated")
	}
}

func TestTransportRefusesACABundleWithNoCertificateInIt(t *testing.T) {
	certPath, keyPath := issue(t, "dev.acme.example")
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(ca, []byte("# no PEM here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewTransport(Enrolment{CertFile: certPath, KeyFile: keyPath, CAFile: ca,
		Endpoint: "https://x"})
	if err == nil {
		t.Fatal("an empty CA bundle was accepted; the agent would fall back to " +
			"the host trust store and any public CA could terminate this connection")
	}
	if !strings.Contains(err.Error(), "no certificate") {
		t.Errorf("the refusal should say the bundle is empty, got: %v", err)
	}
}

// The payload goes out; nothing the platform returns is acted on. A response
// that could change collection would make the allowlist advisory -- the platform
// could widen its own scope without publishing a version the tenant merges.
func TestSendIgnoresWhateverTheEndpointReturns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"collect":["everything"],"allowlist":null}`))
	}))
	defer srv.Close()

	certPath, keyPath := issue(t, "dev.acme.example")
	tr, err := NewTransport(Enrolment{CertFile: certPath, KeyFile: keyPath, Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	// httptest serves plain HTTP; the point here is the request/response
	// handling, and the mTLS configuration is asserted by the tests above.
	tr.client = srv.Client()
	if err := tr.Send(context.Background(), Payload{Cluster: "dev.acme.example"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSendReportsANonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	certPath, keyPath := issue(t, "dev.acme.example")
	tr, err := NewTransport(Enrolment{CertFile: certPath, KeyFile: keyPath, Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	tr.client = srv.Client()
	// 403 is what a blocklisted fingerprint looks like from this end. It must
	// surface, not be swallowed: a revoked agent that logged success would leave
	// the platform and the tenant disagreeing about whether support is active.
	if err := tr.Send(context.Background(), Payload{}); err == nil {
		t.Error("a rejected payload reported success")
	}
}

// ADR-077 addendum 3, decision 6: revocation is stated by the platform, never
// inferred by the agent. This is the test that stops the convenient shortcut --
// treating any 401/403 as revocation -- from being reintroduced, because that
// would stop a healthy tenant reporting the first time someone misconfigures a
// proxy in front of the endpoint.
func TestOnlyAnExplicitMarkerMeansRevoked(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		revoked bool
	}{
		{"explicit marker", 403, `{"reason":"ENROLMENT_REVOKED"}`, true},
		{"marker with other fields", 403, `{"reason":"ENROLMENT_REVOKED","at":"now"}`, true},
		// Everything below is a fault to retry, not a reason to stop.
		{"bare 403", 403, ``, false},
		{"bare 401", 401, ``, false},
		{"401 with an unrelated reason", 401, `{"reason":"BAD_TOKEN"}`, false},
		{"proxy html", 403, `<html>403 Forbidden</html>`, false},
		{"server error", 500, ``, false},
		{"gateway down", 502, `{"reason":"upstream unavailable"}`, false},
		// A message that merely quotes the marker must not revoke: the field is
		// the contract, not the presence of the string.
		{"marker quoted in prose", 403, `{"reason":"unknown value ENROLMENT_REVOKED"}`, false},
	}

	certPath, keyPath := issue(t, "dev.acme.example")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()

			tr, err := NewTransport(Enrolment{CertFile: certPath, KeyFile: keyPath, Endpoint: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			tr.client = srv.Client()

			err = tr.Send(context.Background(), Payload{})
			if err == nil {
				t.Fatal("a non-2xx response reported success")
			}
			got := errors.Is(err, ErrEnrolmentRevoked)
			if got != c.revoked {
				t.Errorf("revoked = %v, want %v (status %d, body %q)",
					got, c.revoked, c.status, c.body)
			}
		})
	}
}

// A plane that cannot answer cannot revoke. Failure to communicate is never
// revocation -- the correct default for the one direction a tenant cannot undo.
func TestAnUnreachablePlaneCannotRevoke(t *testing.T) {
	certPath, keyPath := issue(t, "dev.acme.example")
	tr, err := NewTransport(Enrolment{CertFile: certPath, KeyFile: keyPath,
		Endpoint: "https://127.0.0.1:1/nothing-listens-here"})
	if err != nil {
		t.Fatal(err)
	}
	err = tr.Send(context.Background(), Payload{})
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if errors.Is(err, ErrEnrolmentRevoked) {
		t.Error("an unreachable endpoint was read as revocation")
	}
}
