package kms

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func keySet(kids ...string) []byte {
	var keys []map[string]string
	for _, k := range kids {
		keys = append(keys, map[string]string{
			"kid": k, "kty": "RSA", "alg": "RS256", "use": "sig", "n": "abc", "e": "AQAB",
		})
	}
	b, _ := json.Marshal(map[string]any{"keys": keys})
	return b
}

type jwksFake struct {
	existing string
	notOIDC  bool
	status   int
	calls    []string
	patched  string
}

func (f *jwksFake) client(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls = append(f.calls, r.Method+" "+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		if f.status != 0 {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(`{"error":{"code":404,"status":"NOT_FOUND","message":"no"}}`))
			return
		}
		if r.Method == http.MethodPatch {
			var in struct {
				Oidc struct {
					JwksJson string `json:"jwksJson"`
				} `json:"oidc"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.patched = in.Oidc.JwksJson
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if f.notOIDC {
			_, _ = w.Write([]byte(`{"name":"p","x509":{}}`))
			return
		}
		out := map[string]any{"name": "p", "oidc": map[string]any{
			"issuerUri": "https://kubernetes.default.svc", "jwksJson": f.existing}}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return &Client{http: srv.Client(), projectID: "nutgraf-510805",
		kmsHost: srv.URL, iamHost: srv.URL, serviceUsageHost: srv.URL}
}

func TestAnUnchangedKeySetWritesNothing(t *testing.T) {
	// IDEMPOTENT BY CONTENT, not by string. The same key set re-serialised by a different
	// tool differs in field order and whitespace; comparing strings would PATCH on every
	// run and turn a scheduled control into a write loop against the identity provider.
	existing := `{"keys":[{"use":"sig","kid":"b","kty":"RSA","e":"AQAB","n":"abc","alg":"RS256"},
	             {"kid":"a","kty":"RSA","alg":"RS256","use":"sig","n":"abc","e":"AQAB"}]}`
	f := &jwksFake{existing: existing}
	got, err := f.client(t).SyncJWKS(context.Background(), "pool", "prov", keySet("a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Changed {
		t.Fatal("an unchanged key set was uploaded again")
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "PATCH") {
			t.Fatalf("a write was made for an unchanged key set: %v", f.calls)
		}
	}
}

func TestARotatedSigningKeyIsUploaded(t *testing.T) {
	// The case this control exists for. Cluster signing keys rotate; the uploaded copy
	// goes stale; every token then fails validation with nothing in the cluster having
	// changed.
	f := &jwksFake{existing: string(keySet("old"))}
	got, err := f.client(t).SyncJWKS(context.Background(), "pool", "prov", keySet("old", "new"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Changed {
		t.Fatal("a rotated key set was not uploaded")
	}
	if len(got.Before) != 1 || len(got.After) != 2 {
		t.Fatalf("before %v after %v", got.Before, got.After)
	}
	if !strings.Contains(f.patched, `"new"`) {
		t.Errorf("the uploaded set does not contain the new key: %s", f.patched)
	}
}

func TestThePatchTouchesOnlyTheJWKS(t *testing.T) {
	// A full-resource write would silently revert an attributeMapping or an audience
	// somebody changed for a reason.
	f := &jwksFake{existing: string(keySet("old"))}
	if _, err := f.client(t).SyncJWKS(context.Background(), "pool", "prov", keySet("new")); err != nil {
		t.Fatal(err)
	}
	var sawMask bool
	for _, c := range f.calls {
		if strings.HasPrefix(c, "PATCH") {
			sawMask = strings.Contains(c, "updateMask=oidc.jwks_json")
		}
	}
	if !sawMask {
		t.Fatalf("the PATCH carried no updateMask scoped to the JWKS: %v", f.calls)
	}
}

func TestAnEmptyKeySetIsRefused(t *testing.T) {
	// Accepted by the API, and means nothing validates — a change that looks like a
	// successful sync and removes the identity it was meant to establish.
	f := &jwksFake{}
	_, err := f.client(t).SyncJWKS(context.Background(), "pool", "prov", []byte(`{"keys":[]}`))
	if err == nil {
		t.Fatal("an empty key set was accepted")
	}
	if !strings.Contains(err.Error(), "looks like a successful sync") {
		t.Fatalf("the error does not explain the danger: %v", err)
	}
}

func TestTooManyKeysIsRefusedRatherThanTruncated(t *testing.T) {
	// A cluster mid-rotation briefly publishes more keys than usual, and a silent
	// truncation could drop the one signing current tokens.
	var kids []string
	for i := 0; i < MaxUploadedJWKs+1; i++ {
		kids = append(kids, fmt.Sprintf("k%d", i))
	}
	f := &jwksFake{}
	_, err := f.client(t).SyncJWKS(context.Background(), "pool", "prov", keySet(kids...))
	if err == nil {
		t.Fatal("an oversized key set was accepted")
	}
	if !strings.Contains(err.Error(), "Truncating") {
		t.Fatalf("the error does not say why it refuses rather than trims: %v", err)
	}
}

func TestAKeyWithNoKidIsRefused(t *testing.T) {
	// Without kids, a rotation cannot be distinguished from a reserialisation, and the
	// idempotency comparison becomes meaningless.
	f := &jwksFake{}
	_, err := f.client(t).SyncJWKS(context.Background(), "pool", "prov",
		[]byte(`{"keys":[{"kty":"RSA","alg":"RS256","use":"sig","n":"a","e":"AQAB"}]}`))
	if err == nil {
		t.Fatal("a key with no kid was accepted")
	}
}

func TestAnX509ProviderIsRefusedWithTheRightExplanation(t *testing.T) {
	// The KMS plugin's provider is X.509 and is a different object. Pointing the JWKS
	// sync at it would otherwise fail with something unhelpful.
	f := &jwksFake{notOIDC: true}
	_, err := f.client(t).SyncJWKS(context.Background(), "pool", "prov", keySet("a"))
	if err == nil {
		t.Fatal("an X.509 provider was accepted for a JWKS sync")
	}
	if !strings.Contains(err.Error(), "X.509 and is a different object") {
		t.Fatalf("the error does not explain the mix-up: %v", err)
	}
}

func TestAMissingProviderSaysWhatIsMissing(t *testing.T) {
	f := &jwksFake{status: http.StatusNotFound}
	_, err := f.client(t).SyncJWKS(context.Background(), "pool", "prov", keySet("a"))
	if err == nil {
		t.Fatal("a missing provider was accepted")
	}
	if !strings.Contains(err.Error(), "has to be established before") {
		t.Fatalf("the error does not say what to do: %v", err)
	}
}
