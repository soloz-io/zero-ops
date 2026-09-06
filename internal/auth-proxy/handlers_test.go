package authproxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const (
	testIssuer  = "https://id.dev.nutgraf.in"
	testAuthURL = "https://auth.dev.nutgraf.in"
	testMCPURL  = "https://api.dev.nutgraf.in"
)

func newTestHandler(internalURL string) *Handler {
	return NewHandler(testIssuer, internalURL, 5*time.Second, testMCPURL, testAuthURL, testMCPURL)
}

// The discovery document must pass through UNMODIFIED. It names Zitadel as the
// issuer while being served from auth.<zone>, and rewriting it to name this host
// would produce a document no Zitadel-issued token validates against.
func TestHandler_ProxyOpenIDConfigurationPreservesIssuer(t *testing.T) {
	zitadel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			t.Errorf("expected path /.well-known/openid-configuration, got %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"issuer":"` + testIssuer + `"}`))
	}))
	defer zitadel.Close()

	rec := httptest.NewRecorder()
	newTestHandler(zitadel.URL).ProxyOpenIDConfiguration(rec, httptest.NewRequest("GET", "/.well-known/openid-configuration", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["issuer"] != testIssuer {
		t.Errorf("issuer was rewritten: expected %s, got %v", testIssuer, resp["issuer"])
	}
}

// The conventional JWKS path must be translated to the one Zitadel publishes on.
// Requesting /.well-known/jwks.json from Zitadel returns 404, and the resulting
// failure names neither the path nor the provider.
func TestHandler_ProxyJWKSTranslatesPath(t *testing.T) {
	var gotPath string
	zitadel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"keys":[]}`))
	}))
	defer zitadel.Close()

	rec := httptest.NewRecorder()
	newTestHandler(zitadel.URL).ProxyJWKS(rec, httptest.NewRequest("GET", "/.well-known/jwks.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if gotPath != zitadelJWKSPath {
		t.Errorf("expected upstream path %s, got %s", zitadelJWKSPath, gotPath)
	}
}

// Metadata must advertise Zitadel, not this host. Tokens carry iss=id.<zone>, so
// a client that discovered auth.<zone> as the issuer rejects them; and the
// /oauth2/* endpoints this used to advertise are now redirected by the Gateway,
// which an OAuth client cannot follow on a token endpoint.
func TestHandler_ServeAuthServerMetadataAdvertisesZitadel(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestHandler("http://zitadel.platform-identity.svc.cluster.local:8080").
		ServeAuthServerMetadata(rec, httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	for _, tc := range []struct{ field, want string }{
		{"issuer", testIssuer},
		{"authorization_endpoint", testIssuer + zitadelAuthorizePath},
		{"token_endpoint", testIssuer + zitadelTokenPath},
		{"jwks_uri", testIssuer + zitadelJWKSPath},
	} {
		if resp[tc.field] != tc.want {
			t.Errorf("%s: expected %s, got %v", tc.field, tc.want, resp[tc.field])
		}
	}

	// Zitadel implements no dynamic client registration. Advertising the endpoint
	// Hydra had would turn a clean absence into a 404 mid-flow.
	if _, present := resp["registration_endpoint"]; present {
		t.Error("registration_endpoint advertised, but Zitadel has no dynamic client registration")
	}
}

// Tenancy is structural in Zitadel: the tenant arrives as the user's resource
// owner, not as a literal tenant_id. Reading the literal alone returned "" for
// every real token, which downstream reads as an untenanted request.
func TestTenantFromClaims_FallsBackToResourceOwner(t *testing.T) {
	for _, tc := range []struct {
		name  string
		claim map[string]interface{}
		want  string
	}{
		{"platform contract wins", map[string]interface{}{
			"tenant_id":                             "contract",
			"urn:zitadel:iam:user:resourceowner:id": "owner",
		}, "contract"},
		{"resource owner", map[string]interface{}{
			"urn:zitadel:iam:user:resourceowner:id": "owner",
		}, "owner"},
		{"org id", map[string]interface{}{
			"urn:zitadel:iam:org:id": "org",
		}, "org"},
		{"none", map[string]interface{}{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tenantFromClaims(tc.claim); got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// A role granted in a DIFFERENT tenant must not be returned for this one. Taking
// the claim's keys would leak it, and the result reads as a correct role list.
func TestRolesGrantedInTenant_ExcludesOtherTenants(t *testing.T) {
	claims := map[string]interface{}{
		scopedRolesClaim: map[string]interface{}{
			"admin":  map[string]interface{}{"tenant-a": "a.example"},
			"viewer": map[string]interface{}{"tenant-b": "b.example"},
		},
	}
	got := rolesGrantedInTenant(claims, "tenant-a")
	if len(got) != 1 || got[0] != "admin" {
		t.Errorf("expected [admin] for tenant-a, got %v", got)
	}
}

// An absent claim means no roles, never all roles.
func TestRolesGrantedInTenant_AbsentClaimGrantsNothing(t *testing.T) {
	if got := rolesGrantedInTenant(map[string]interface{}{}, "tenant-a"); len(got) != 0 {
		t.Errorf("expected no roles, got %v", got)
	}
}
