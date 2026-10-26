package zitadel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The machine client is the credential ADR-097 makes cross-application calling
// out of, so every branch below is about a failure that is SILENT at provisioning
// time and surfaces somewhere else: an opaque token the receiver cannot validate
// locally, a secret regenerated under a running workload, or a client id taken
// from the wrong tenant's service identity.

// machineRecorder is a stand-in Zitadel for the machine-user endpoints.
type machineRecorder struct {
	calls      []string
	orgHeaders []string
	// searchResult is what POST /v2/users returns.
	searchResult []map[string]any
	// createBody and updateBody capture what the write endpoints received.
	createBody map[string]any
	updateBody map[string]any
	// secret is what PUT /users/{id}/secret returns.
	secret map[string]any
}

func (r *machineRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.calls = append(r.calls, req.Method+" "+req.URL.Path)
		r.orgHeaders = append(r.orgHeaders, req.Header.Get(orgHeader))
		w.Header().Set("Content-Type", "application/json")

		path := req.URL.Path
		switch {
		case path == "/v2/users":
			json.NewEncoder(w).Encode(map[string]any{"result": r.searchResult})
		case path == "/management/v1/users/machine":
			_ = json.NewDecoder(req.Body).Decode(&r.createBody)
			json.NewEncoder(w).Encode(map[string]any{"userId": "machine-1"})
		case strings.HasSuffix(path, "/machine"):
			_ = json.NewDecoder(req.Body).Decode(&r.updateBody)
			json.NewEncoder(w).Encode(map[string]any{})
		case strings.HasSuffix(path, "/secret"):
			json.NewEncoder(w).Encode(r.secret)
		default:
			json.NewEncoder(w).Encode(map[string]any{})
		}
	})
}

func (r *machineRecorder) called(method, path string) bool {
	for _, c := range r.calls {
		if c == method+" "+path {
			return true
		}
	}
	return false
}

func newMachineAuth(t *testing.T, rec *machineRecorder) (*Auth, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(rec.handler())
	a, err := NewAuth(Config{Issuer: srv.URL, ServiceToken: "test-token", ProjectName: "platform"})
	if err != nil {
		srv.Close()
		t.Fatalf("NewAuth: %v", err)
	}
	return a, srv
}

// The whole point of a machine identity here is a token the RECEIVER can validate
// without calling the issuer. Zitadel's default is opaque, so creating without
// naming the type produces a credential that works, mints tokens, and cannot be
// validated locally -- an authorization failure at the far end of every call.
func TestEnsureMachineClient_CreatesWithJWTAccessTokens(t *testing.T) {
	rec := &machineRecorder{secret: map[string]any{"clientId": "cid-1", "clientSecret": "sec-1"}}
	a, srv := newMachineAuth(t, rec)
	defer srv.Close()

	got, err := a.EnsureMachineClient(context.Background(), "org-1", "oranger-svc", "oranger service", false)
	if err != nil {
		t.Fatalf("EnsureMachineClient: %v", err)
	}
	if rec.createBody["accessTokenType"] != accessTokenTypeJWT {
		t.Fatalf("created with accessTokenType %v, want %s", rec.createBody["accessTokenType"], accessTokenTypeJWT)
	}
	if got.ClientID != "cid-1" || got.ClientSecret != "sec-1" {
		t.Fatalf("got %+v, want the generated client id and secret", got)
	}
	if !rec.called(http.MethodPut, "/management/v1/users/machine-1/secret") {
		t.Fatalf("no secret generated; calls were %v", rec.calls)
	}
}

// An identity created before this code existed, or by hand in the console, carries
// Zitadel's bearer default. Reading it back and trusting it would leave exactly
// the opaque-token failure above in place on the applications that matter most --
// the ones already running.
func TestEnsureMachineClient_CorrectsAnExistingBearerUserToJWT(t *testing.T) {
	rec := &machineRecorder{
		searchResult: []map[string]any{{
			"userId":  "machine-9",
			"machine": map[string]any{"accessTokenType": accessTokenTypeBearer, "hasSecret": true},
		}},
	}
	a, srv := newMachineAuth(t, rec)
	defer srv.Close()

	if _, err := a.EnsureMachineClient(context.Background(), "org-1", "oranger-svc", "", false); err != nil {
		t.Fatalf("EnsureMachineClient: %v", err)
	}
	if rec.updateBody["accessTokenType"] != accessTokenTypeJWT {
		t.Fatalf("existing user not corrected; update body was %v", rec.updateBody)
	}
	// Correcting the token type must not destroy the credential the workload holds.
	if rec.called(http.MethodPut, "/management/v1/users/machine-9/secret") {
		t.Fatalf("regenerated the secret while only the token type was wrong")
	}
}

// Regeneration invalidates the secret a running workload is holding, and the
// failure lands on its next call rather than on the reconcile that caused it. So
// it happens only when the caller asks.
func TestEnsureMachineClient_DoesNotRegenerateUnlessAsked(t *testing.T) {
	rec := &machineRecorder{
		searchResult: []map[string]any{{
			"userId":  "machine-9",
			"machine": map[string]any{"accessTokenType": accessTokenTypeJWT, "hasSecret": true},
		}},
		secret: map[string]any{"clientId": "cid-2", "clientSecret": "sec-2"},
	}
	a, srv := newMachineAuth(t, rec)
	defer srv.Close()

	got, err := a.EnsureMachineClient(context.Background(), "org-1", "oranger-svc", "", false)
	if err != nil {
		t.Fatalf("EnsureMachineClient: %v", err)
	}
	if got.ClientSecret != "" {
		t.Fatalf("disclosed a secret for an existing user: %+v", got)
	}
	if rec.called(http.MethodPut, "/management/v1/users/machine-9/secret") {
		t.Fatalf("regenerated without being asked; calls were %v", rec.calls)
	}

	rec.calls = nil
	got, err = a.EnsureMachineClient(context.Background(), "org-1", "oranger-svc", "", true)
	if err != nil {
		t.Fatalf("EnsureMachineClient(regenerate): %v", err)
	}
	if got.ClientSecret != "sec-2" {
		t.Fatalf("regeneration returned %+v, want the new secret", got)
	}
}

// A machine user with no secret cannot authenticate at all, so there is nothing to
// invalidate and nothing to protect. Gating this on regenerateIfExists would leave
// a service identity that exists, looks provisioned, and cannot get a token.
func TestEnsureMachineClient_GeneratesASecretWhenTheUserHasNone(t *testing.T) {
	rec := &machineRecorder{
		searchResult: []map[string]any{{
			"userId":  "machine-9",
			"machine": map[string]any{"accessTokenType": accessTokenTypeJWT, "hasSecret": false},
		}},
		secret: map[string]any{"clientId": "cid-3", "clientSecret": "sec-3"},
	}
	a, srv := newMachineAuth(t, rec)
	defer srv.Close()

	got, err := a.EnsureMachineClient(context.Background(), "org-1", "oranger-svc", "", false)
	if err != nil {
		t.Fatalf("EnsureMachineClient: %v", err)
	}
	if got.ClientSecret != "sec-3" {
		t.Fatalf("got %+v, want a freshly generated secret", got)
	}
}

// Login names are unique per organisation, not per instance. An unscoped search on
// a multi-tenant box can return another tenant's service identity -- and the
// caller would then publish ITS client id as an allowed caller on this tenant.
func TestEnsureMachineClient_ScopesEverySearchToTheOrganisation(t *testing.T) {
	rec := &machineRecorder{secret: map[string]any{"clientId": "cid-1", "clientSecret": "sec-1"}}
	a, srv := newMachineAuth(t, rec)
	defer srv.Close()

	if _, err := a.EnsureMachineClient(context.Background(), "org-7", "oranger-svc", "", false); err != nil {
		t.Fatalf("EnsureMachineClient: %v", err)
	}
	for i, h := range rec.orgHeaders {
		if h != "org-7" {
			t.Fatalf("call %q sent org header %q, want org-7", rec.calls[i], h)
		}
	}
}

// Two machine users answering one login name means the name is not the identity it
// is being used as. Picking one publishes an allowlist entry for a credential the
// caller may not hold -- admitting nobody, or worse, admitting the wrong service.
func TestEnsureMachineClient_RefusesAnAmbiguousLoginName(t *testing.T) {
	rec := &machineRecorder{
		searchResult: []map[string]any{
			{"userId": "machine-1", "machine": map[string]any{"accessTokenType": accessTokenTypeJWT, "hasSecret": true}},
			{"userId": "machine-2", "machine": map[string]any{"accessTokenType": accessTokenTypeJWT, "hasSecret": true}},
		},
	}
	a, srv := newMachineAuth(t, rec)
	defer srv.Close()

	_, err := a.EnsureMachineClient(context.Background(), "org-1", "oranger-svc", "", false)
	if err == nil || !strings.Contains(err.Error(), "exactly one service identity") {
		t.Fatalf("got %v, want a refusal naming the ambiguity", err)
	}
}

// A person's account occupying the name the service needs must not be silently
// adopted as a service identity: its subject is a human, and ADR-097 invariant 8
// forbids anything mapping a machine token's subject to a person.
func TestEnsureMachineClient_RefusesAHumanAccountUnderThatName(t *testing.T) {
	rec := &machineRecorder{searchResult: []map[string]any{{"userId": "human-1"}}}
	a, srv := newMachineAuth(t, rec)
	defer srv.Close()

	_, err := a.EnsureMachineClient(context.Background(), "org-1", "oranger-svc", "", false)
	if err == nil || !strings.Contains(err.Error(), "not a machine user") {
		t.Fatalf("got %v, want a refusal naming the user type", err)
	}
}

// The organisation is resolved by the caller (EnsureTenantIdentity). Provisioning a
// service identity without it would create it in the issuer's default org, where
// the tenant cannot see it and nothing revokes it with the tenant.
func TestEnsureMachineClient_RefusesWithoutAnOrganisation(t *testing.T) {
	rec := &machineRecorder{}
	a, srv := newMachineAuth(t, rec)
	defer srv.Close()

	if _, err := a.EnsureMachineClient(context.Background(), "", "oranger-svc", "", false); err == nil {
		t.Fatal("provisioned a machine client with no organisation")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("reached the issuer before validating: %v", rec.calls)
	}
}

// A half-credential is published and then fails at the token endpoint, far from
// the call that produced it.
func TestEnsureMachineClient_RefusesAnEmptyCredential(t *testing.T) {
	rec := &machineRecorder{secret: map[string]any{"clientId": "cid-1"}}
	a, srv := newMachineAuth(t, rec)
	defer srv.Close()

	_, err := a.EnsureMachineClient(context.Background(), "org-1", "oranger-svc", "", false)
	if err == nil || !strings.Contains(err.Error(), "without a client id or secret") {
		t.Fatalf("got %v, want a refusal naming the empty credential", err)
	}
}
