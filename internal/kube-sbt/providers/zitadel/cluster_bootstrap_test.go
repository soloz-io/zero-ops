package zitadel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeIDP struct {
	mu         sync.Mutex
	revoked    bool
	created    bool
	clientID   string
	failCreate bool
	failRevoke bool
	lastApp    map[string]any
	authSeen   string
}

func (f *fakeIDP) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(createApplication, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.authSeen = r.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.lastApp = body
		if f.failCreate {
			http.Error(w, `{"message":"permission denied"}`, http.StatusForbidden)
			return
		}
		f.created = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"applicationId":     "app-1",
			"oidcConfiguration": map[string]any{"clientId": f.clientID},
		})
	})
	mux.HandleFunc("/v2/users/u-1/pats/t-1", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method != http.MethodDelete {
			http.Error(w, "wrong method", http.StatusBadRequest)
			return
		}
		if f.failRevoke {
			http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
			return
		}
		f.revoked = true
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newBootstrap(url string) *Bootstrap {
	return &Bootstrap{
		Issuer: url, Token: "pat-secret",
		UserID: "u-1", TokenID: "t-1", ProjectID: "p-1",
	}
}

func TestRegistersAPublicPKCEClientAndRevokesTheToken(t *testing.T) {
	f := &fakeIDP{clientID: "123@proj"}
	b := newBootstrap(f.server(t).URL)

	res, err := b.Run(context.Background(), "kubectl", []string{"http://localhost:8000"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ClientID != "123@proj" {
		t.Errorf("ClientID = %q", res.ClientID)
	}
	if !f.revoked {
		t.Error("the bootstrap token was not revoked; the box keeps a standing management credential")
	}

	// Asserted against the LITERALS the identity provider defines, not against
	// this package's own constants. Comparing a constant to itself passes however
	// the constant is changed, so it would have accepted a switch to an auth
	// method that mints a client secret -- which is the single value here worth
	// protecting.
	oidc, _ := f.lastApp["oidcRequest"].(map[string]any)
	if oidc["appType"] != "OIDC_APP_TYPE_NATIVE" {
		t.Errorf("appType = %v, want OIDC_APP_TYPE_NATIVE", oidc["appType"])
	}
	// Any other auth method mints a client secret, which would then have to be
	// distributed to every operator's laptop and could not be revoked for one
	// person.
	if oidc["authMethodType"] != "OIDC_AUTH_METHOD_TYPE_NONE" {
		t.Errorf("authMethodType = %v, want OIDC_AUTH_METHOD_TYPE_NONE (public client, PKCE)", oidc["authMethodType"])
	}
	if oidc["grantTypes"] == nil || oidc["responseTypes"] == nil {
		t.Error("the authorization-code flow was not requested")
	}
	if f.authSeen != "Bearer pat-secret" {
		t.Errorf("the borrowed token was not presented: %q", f.authSeen)
	}
}

// The case the design exists for. A registration that fails must not leave the
// management credential alive -- that is the path where a token is most likely
// to be forgotten, because attention goes to the failure.
func TestRevokesTheTokenEvenWhenRegistrationFails(t *testing.T) {
	f := &fakeIDP{failCreate: true}
	b := newBootstrap(f.server(t).URL)

	if _, err := b.Run(context.Background(), "kubectl", []string{"http://localhost:8000"}); err == nil {
		t.Fatal("a failed registration reported success")
	}
	if !f.revoked {
		t.Fatal("registration failed and the bootstrap token was left live")
	}
}

// A cancelled context must not skip revocation either.
func TestRevokesTheTokenWhenTheContextIsCancelled(t *testing.T) {
	f := &fakeIDP{clientID: "123@proj"}
	b := newBootstrap(f.server(t).URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _ = b.Run(ctx, "kubectl", []string{"http://localhost:8000"})
	if !f.revoked {
		t.Fatal("the context was cancelled and the bootstrap token was left live")
	}
}

// A token that cannot be revoked is reported even when the application was
// created, and the message must name what to revoke by hand.
func TestReportsAnUnrevokedTokenEvenOnSuccess(t *testing.T) {
	f := &fakeIDP{clientID: "123@proj", failRevoke: true}
	b := newBootstrap(f.server(t).URL)

	_, err := b.Run(context.Background(), "kubectl", []string{"http://localhost:8000"})
	if err == nil {
		t.Fatal("a live bootstrap token was reported as a clean run")
	}
	for _, want := range []string{"could not be revoked", "u-1", "t-1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// Without the token's identifiers the credential cannot be revoked, so the run
// is refused before it borrows anything rather than discovering it at the end.
func TestRefusesWhenTheTokenCouldNotBeRevoked(t *testing.T) {
	for _, missing := range []func(*Bootstrap){
		func(b *Bootstrap) { b.UserID = "" },
		func(b *Bootstrap) { b.TokenID = "" },
		func(b *Bootstrap) { b.Token = "" },
		func(b *Bootstrap) { b.ProjectID = "" },
	} {
		b := newBootstrap("https://id.example.test")
		missing(b)
		if _, err := b.Run(context.Background(), "kubectl", nil); err == nil {
			t.Error("an unrevokable bootstrap was accepted")
		}
	}
}

// An application created with no client id is not a usable result: recording an
// empty value would leave the API server trusting an issuer for a client that
// does not exist, and every login would fail instead of this call.
func TestRefusesAnApplicationWithNoClientID(t *testing.T) {
	f := &fakeIDP{clientID: ""}
	b := newBootstrap(f.server(t).URL)

	if _, err := b.Run(context.Background(), "kubectl", []string{"http://localhost:8000"}); err == nil {
		t.Fatal("an application with no client id was accepted")
	}
	if !f.revoked {
		t.Error("the bootstrap token was left live")
	}
}
