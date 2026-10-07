package kms

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newCA mints a self-signed CA, standing in for a cluster's own.
func newCA(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

type wifFake struct {
	anchors  [][]byte
	disabled bool
	noX509   bool
	status   int
}

func (f *wifFake) client(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if f.status != 0 {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(`{"error":{"code":404,"status":"NOT_FOUND","message":"no"}}`))
			return
		}
		out := map[string]any{"name": "p", "disabled": f.disabled}
		if !f.noX509 {
			var anchors []map[string]string
			for _, a := range f.anchors {
				anchors = append(anchors, map[string]string{"pemCertificate": string(a)})
			}
			out["x509"] = map[string]any{"trustStore": map[string]any{"trustAnchors": anchors}}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return &Client{http: srv.Client(), projectID: "nutgraf-510805",
		kmsHost: srv.URL, iamHost: srv.URL, serviceUsageHost: srv.URL}
}

func TestTheCorrectCAIsAccepted(t *testing.T) {
	ca := newCA(t, "nutgraf-hub")
	f := &wifFake{anchors: [][]byte{ca}}
	if err := f.client(t).VerifyTrustAnchor(context.Background(), "pool", "prov", ca); err != nil {
		t.Fatalf("the CA the provider trusts was rejected: %v", err)
	}
}

func TestAReformattedCertificateStillMatches(t *testing.T) {
	// COMPARED BY FINGERPRINT, NOT BY STRING. The same certificate round-tripped through
	// different tools differs in line wrapping and trailing newlines, and a string
	// comparison would report a mismatch that is not one -- the kind of false alarm that
	// gets a check switched off.
	ca := newCA(t, "nutgraf-hub")
	mangled := strings.ReplaceAll(string(ca), "\n", "\r\n") + "\n\n"
	f := &wifFake{anchors: [][]byte{[]byte(mangled)}}
	if err := f.client(t).VerifyTrustAnchor(context.Background(), "pool", "prov", ca); err != nil {
		t.Fatalf("the same certificate was rejected after reformatting: %v", err)
	}
}

func TestADIFFERENTCAIsRefusedAndTheErrorNamesTheRebuildTrap(t *testing.T) {
	// THE REBUILD CASE. A rebuilt cluster has a new CA; the provider still holds the old
	// one. Every certificate the new control plane presents fails validation, so the
	// plugin cannot authenticate and the API server cannot decrypt -- and the first
	// symptom is a KMS provider reporting unhealthy, which reads as a plugin bug.
	previous := newCA(t, "nutgraf-hub-old")
	rebuilt := newCA(t, "nutgraf-hub")
	f := &wifFake{anchors: [][]byte{previous}}

	err := f.client(t).VerifyTrustAnchor(context.Background(), "pool", "prov", rebuilt)
	if err == nil {
		t.Fatal("a CA the provider does not trust was accepted")
	}
	for _, want := range []string{"does not trust", "rebuild", "sha256:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

func TestAMissingProviderIsRefusedWithTheTrapExplained(t *testing.T) {
	ca := newCA(t, "nutgraf-hub")
	f := &wifFake{status: http.StatusNotFound}
	err := f.client(t).VerifyTrustAnchor(context.Background(), "pool", "prov", ca)
	if err == nil {
		t.Fatal("a missing provider was accepted")
	}
	if !strings.Contains(err.Error(), "does not exist") ||
		!strings.Contains(err.Error(), "before configuring") {
		t.Fatalf("the error does not say what to do: %v", err)
	}
}

func TestADisabledProviderIsRefusedEvenWithTheRightAnchor(t *testing.T) {
	// A disabled provider validates nothing, so trusting the right CA is irrelevant --
	// and a check that passed here would be the worst kind: correct about the anchor and
	// wrong about the outcome.
	ca := newCA(t, "nutgraf-hub")
	f := &wifFake{anchors: [][]byte{ca}, disabled: true}
	err := f.client(t).VerifyTrustAnchor(context.Background(), "pool", "prov", ca)
	if err == nil {
		t.Fatal("a disabled provider was accepted")
	}
	if !strings.Contains(err.Error(), "DISABLED") {
		t.Fatalf("the error does not name the disabled provider: %v", err)
	}
}

func TestAProviderWithNoX509TrustStoreIsRefused(t *testing.T) {
	// An OIDC or AWS provider cannot validate a control-plane certificate at all.
	ca := newCA(t, "nutgraf-hub")
	f := &wifFake{noX509: true}
	err := f.client(t).VerifyTrustAnchor(context.Background(), "pool", "prov", ca)
	if err == nil {
		t.Fatal("a provider with no x509 trust store was accepted")
	}
	if !strings.Contains(err.Error(), "no x509 trust store") {
		t.Fatalf("the error does not name the missing trust store: %v", err)
	}
}

func TestSomethingThatIsNotACertificateIsRefused(t *testing.T) {
	// Hashing raw PEM bytes would produce a confident fingerprint of whatever this is.
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("nope")})
	if _, err := fingerprint(key); err == nil {
		t.Error("a PRIVATE KEY block was fingerprinted as a certificate")
	}
	if _, err := fingerprint([]byte("not pem at all")); err == nil {
		t.Error("non-PEM input was fingerprinted")
	}
	garbage := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not der")})
	if _, err := fingerprint(garbage); err == nil {
		t.Error("a CERTIFICATE block holding non-DER was fingerprinted")
	}
}

func TestVerifyingMutatesNothing(t *testing.T) {
	ca := newCA(t, "nutgraf-hub")
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"p","x509":{"trustStore":{"trustAnchors":[{"pemCertificate":` +
			mustJSON(string(ca)) + `}]}}}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{http: srv.Client(), projectID: "p",
		kmsHost: srv.URL, iamHost: srv.URL, serviceUsageHost: srv.URL}
	if err := c.VerifyTrustAnchor(context.Background(), "pool", "prov", ca); err != nil {
		t.Fatal(err)
	}
	for _, m := range calls {
		if m != http.MethodGet {
			t.Errorf("verification made a %s request; it must only read", m)
		}
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
