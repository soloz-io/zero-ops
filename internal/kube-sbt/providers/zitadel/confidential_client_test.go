package zitadel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The confidential client is the one credential the platform cannot mint itself.
//
// Zitadel generates a client secret and discloses it EXACTLY ONCE, at creation,
// so every branch below is about which call produced the value and whether the
// caller is allowed to destroy the live one. There is no cluster in these tests
// on purpose: the risk being covered is the shape of the issuer's API — request
// paths, the organisation header, and the fields the response is read from —
// which a cluster would exercise only after a tenant had already been onboarded
// against it.

// recorder is a stand-in Zitadel that records what it was asked for.
type recorder struct {
	// calls is every "METHOD path" in order.
	calls []string
	// orgHeaders is the x-zitadel-orgid sent with each call, same order.
	orgHeaders []string
	// existingApps is returned by the app search.
	existingApps []map[string]any
	// createResponse is returned by the app-create endpoint.
	createResponse map[string]any
	// regenerateResponse is returned by the secret-regenerate endpoint.
	regenerateResponse map[string]any
	// createBody captures the body the app-create endpoint received.
	createBody map[string]any
}

func (r *recorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.calls = append(r.calls, req.Method+" "+req.URL.Path)
		r.orgHeaders = append(r.orgHeaders, req.Header.Get(orgHeader))
		w.Header().Set("Content-Type", "application/json")

		path := req.URL.Path
		switch {
		case path == "/admin/v1/orgs/_search":
			json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{"id": "org-1", "name": "acme"}},
			})
		case path == "/management/v1/projects/_search":
			json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{"id": "proj-1", "name": "platform"}},
			})
		case strings.HasSuffix(path, "/apps/_search"):
			json.NewEncoder(w).Encode(map[string]any{"result": r.existingApps})
		case strings.HasSuffix(path, "/apps/oidc"):
			_ = json.NewDecoder(req.Body).Decode(&r.createBody)
			json.NewEncoder(w).Encode(r.createResponse)
		case strings.HasSuffix(path, "/_generate_client_secret"):
			json.NewEncoder(w).Encode(r.regenerateResponse)
		default:
			json.NewEncoder(w).Encode(map[string]any{})
		}
	})
}

func (r *recorder) called(method, path string) bool {
	for _, c := range r.calls {
		if c == method+" "+path {
			return true
		}
	}
	return false
}

func newTestAuth(t *testing.T, rec *recorder) (*Auth, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(rec.handler())
	a, err := NewAuth(Config{
		Issuer:       srv.URL,
		ServiceToken: "test-token",
		ProjectName:  "platform",
	})
	if err != nil {
		srv.Close()
		t.Fatalf("NewAuth: %v", err)
	}
	return a, srv
}

// A confidential client must be created with BASIC auth, not the public client's
// NONE. Creating it as public succeeds and yields no secret, so the BFF would
// then be handed an empty credential and fail only at token exchange.
func TestEnsureConfidentialClient_CreatesWithBasicAuthAndReturnsGeneratedSecret(t *testing.T) {
	rec := &recorder{
		existingApps: nil, // nothing exists yet
		createResponse: map[string]any{
			"appId":        "app-1",
			"clientId":     "client-1",
			"clientSecret": "generated-secret",
		},
	}
	a, srv := newTestAuth(t, rec)
	defer srv.Close()

	clientID, secret, err := a.EnsureConfidentialClient(context.Background(), "acme", "acme-bff", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clientID != "client-1" || secret != "generated-secret" {
		t.Errorf("got (%q, %q), want (client-1, generated-secret)", clientID, secret)
	}
	if got := rec.createBody["authMethodType"]; got != authMethodBasic {
		t.Errorf("authMethodType = %v, want %s — a public client has no secret to hand the BFF", got, authMethodBasic)
	}
	// The secret came from creation, so destroying a live credential must not
	// have been attempted.
	if rec.called("POST", "/management/v1/projects/proj-1/apps/app-1/oidc_config/_generate_client_secret") {
		t.Error("regenerated a secret on first creation — the creation response already carried one")
	}
}

// The issuer discloses a generated secret once. An app that already exists
// therefore yields no secret, and recovering one means destroying the live
// credential -- which only the caller may decide.
func TestEnsureConfidentialClient_ExistingAppIsNotRegeneratedUnlessAsked(t *testing.T) {
	rec := &recorder{
		existingApps: []map[string]any{
			{"id": "app-1", "name": "acme-bff", "oidcConfig": map[string]any{"clientId": "client-1"}},
		},
	}
	a, srv := newTestAuth(t, rec)
	defer srv.Close()

	clientID, secret, err := a.EnsureConfidentialClient(context.Background(), "acme", "acme-bff", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clientID != "client-1" {
		t.Errorf("clientID = %q, want client-1", clientID)
	}
	if secret != "" {
		t.Errorf("secret = %q, want empty — the issuer cannot disclose an existing secret", secret)
	}
	if rec.called("POST", "/management/v1/projects/proj-1/apps/app-1/oidc_config/_generate_client_secret") {
		t.Error("regenerated without being asked — this invalidates the credential the workload is holding")
	}
	if rec.called("POST", "/management/v1/projects/proj-1/apps/oidc") {
		t.Error("created a second application for a name that already exists")
	}
}

// When the caller has nothing stored, regenerating is the only way back to a
// usable credential. This pins the endpoint, which was the least verified part
// of the provider.
func TestEnsureConfidentialClient_RegeneratesExistingAppOnRequest(t *testing.T) {
	rec := &recorder{
		existingApps: []map[string]any{
			{"id": "app-1", "name": "acme-bff", "oidcConfig": map[string]any{"clientId": "client-1"}},
		},
		regenerateResponse: map[string]any{"clientSecret": "rotated-secret"},
	}
	a, srv := newTestAuth(t, rec)
	defer srv.Close()

	clientID, secret, err := a.EnsureConfidentialClient(context.Background(), "acme", "acme-bff", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clientID != "client-1" || secret != "rotated-secret" {
		t.Errorf("got (%q, %q), want (client-1, rotated-secret)", clientID, secret)
	}
	want := "POST /management/v1/projects/proj-1/apps/app-1/oidc_config/_generate_client_secret"
	if !rec.called("POST", "/management/v1/projects/proj-1/apps/app-1/oidc_config/_generate_client_secret") {
		t.Errorf("regenerate endpoint not called as %q; calls were %v", want, rec.calls)
	}
}

// An application created without a secret is unusable, and the caller would
// publish an empty credential that fails only at the first token exchange.
func TestEnsureConfidentialClient_RejectsCreationWithoutASecret(t *testing.T) {
	rec := &recorder{
		createResponse: map[string]any{"appId": "app-1", "clientId": "client-1"}, // no clientSecret
	}
	a, srv := newTestAuth(t, rec)
	defer srv.Close()

	_, _, err := a.EnsureConfidentialClient(context.Background(), "acme", "acme-bff", true)
	if err == nil {
		t.Fatal("expected an error when the issuer returns no client secret")
	}
	if !strings.Contains(err.Error(), "without a client secret") {
		t.Errorf("error does not name the cause: %v", err)
	}
}

// The organisation header is a security boundary, not a detail: omitted, Zitadel
// resolves against the default organisation and the client is created in — or
// read from — the WRONG tenant.
func TestEnsureConfidentialClient_ScopesEveryProjectCallToTheTenantOrg(t *testing.T) {
	rec := &recorder{
		createResponse: map[string]any{"appId": "app-1", "clientId": "client-1", "clientSecret": "s"},
	}
	a, srv := newTestAuth(t, rec)
	defer srv.Close()

	if _, _, err := a.EnsureConfidentialClient(context.Background(), "acme", "acme-bff", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, call := range rec.calls {
		if !strings.Contains(call, "/management/v1/projects") {
			continue // org search is deliberately unscoped
		}
		if rec.orgHeaders[i] != "org-1" {
			t.Errorf("%s sent %s=%q, want org-1", call, orgHeader, rec.orgHeaders[i])
		}
	}
}

func TestEnsureConfidentialClient_RequiresTenantAndAppName(t *testing.T) {
	rec := &recorder{}
	a, srv := newTestAuth(t, rec)
	defer srv.Close()

	if _, _, err := a.EnsureConfidentialClient(context.Background(), "", "acme-bff", true); err == nil {
		t.Error("expected an error for an empty tenantID")
	}
	if _, _, err := a.EnsureConfidentialClient(context.Background(), "acme", "", true); err == nil {
		t.Error("expected an error for an empty appName")
	}
	if len(rec.calls) != 0 {
		t.Errorf("made %d API calls before validating input: %v", len(rec.calls), rec.calls)
	}
}

// The public browser client must stay public: PKCE carries the proof, and a
// secret shipped to a browser is a published secret.
func TestEnsureOIDCApp_CreatesPublicClientWithNoSecret(t *testing.T) {
	rec := &recorder{
		createResponse: map[string]any{"appId": "app-2", "clientId": "public-1"},
	}
	a, srv := newTestAuth(t, rec)
	defer srv.Close()

	clientID, err := a.ensureOIDCApp(context.Background(), "org-1", "proj-1", "acme-public-client", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clientID != "public-1" {
		t.Errorf("clientID = %q, want public-1", clientID)
	}
	if got := rec.createBody["authMethodType"]; got != authMethodNone {
		t.Errorf("authMethodType = %v, want %s", got, authMethodNone)
	}
}
