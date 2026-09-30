package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	client2 "github.com/soloz-io/zero-ops/operators/hub-operator/internal/client"
	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/secrets"
)

// The PROVISIONING CALL SITE, not the resolver (ADR-094 invariant 2, external
// security review 2026-09-30).
//
// The original defect was one argument: publishAllowedCaller(..., identity.ClientID)
// where it should have been the exchange client. exchangeClientIDFrom was always
// correct, so resolver tests would have passed either way. These tests assert the
// chain the review named:
//
//	dependency: waypoint -> resolve oranger's exchange client
//	  -> publish that id -> waypoint's OIDC_ALLOWED_AZP
//
// and, as the other half of the same assertion, that oranger's PUBLIC client id
// never appears in waypoint's allowlist.

const (
	orangerBrowserClientID  = "oranger-public-pkce@nutgraf"
	orangerExchangeClientID = "oranger-gateway-exchange@nutgraf"
	waypointProjectID       = "392885920103137720"
)

// fakeSecretStore records writes by path and key. It implements only
// TenantSecretStore, which is the point of that interface being narrow.
type fakeSecretStore struct {
	data map[string]string // "path\x00key" -> value
}

func newFakeSecretStore() *fakeSecretStore {
	return &fakeSecretStore{data: map[string]string{}}
}

func k(path, name string) string { return path + "\x00" + name }

func (f *fakeSecretStore) GetSecret(_ context.Context, path, name string) (string, error) {
	if v, ok := f.data[k(path, name)]; ok {
		return v, nil
	}
	return "", fmt.Errorf("absent: %s/%s", path, name)
}

func (f *fakeSecretStore) SecretExists(_ context.Context, path, name string) (bool, error) {
	_, ok := f.data[k(path, name)]
	return ok, nil
}

func (f *fakeSecretStore) CreateSecret(_ context.Context, path, name, value string) error {
	f.data[k(path, name)] = value
	return nil
}

func (f *fakeSecretStore) UpdateSecret(_ context.Context, path, name, value string) error {
	f.data[k(path, name)] = value
	return nil
}

func (f *fakeSecretStore) EnsureTenantFolderAndCredentials(
	_ context.Context, _, _, _ string, _ bool, _ []secrets.OAuthClient, _, _, _ bool,
) (*secrets.EnsureTenantCredentialsResult, error) {
	return &secrets.EnsureTenantCredentialsResult{}, nil
}

func tenantPath(cellId, appId string) string {
	return fmt.Sprintf(secrets.InfisicalTenantPathFormat, cellId, appId)
}

// orangerCallsWaypoint is the fixture the review's chain describes: oranger
// declares waypoint as a backend dependency, and oranger's exchange client has
// been minted.
func orangerCallsWaypoint() (*AINativeSaaSReconciler, *fakeSecretStore, []resolvedDependency, []client2.DeclaredClient) {
	store := newFakeSecretStore()
	r := &AINativeSaaSReconciler{InfisicalClient: store}
	deps := []resolvedDependency{{AppID: "waypoint", ProjectID: waypointProjectID}}
	clients := []client2.DeclaredClient{
		{Name: "bff", ClientID: "oranger-bff@nutgraf"},
		{Name: gatewayExchangeClientName("oranger"), ClientID: orangerExchangeClientID},
	}
	return r, store, deps, clients
}

func TestTheTargetsAllowlistGetsTheCallersExchangeClient(t *testing.T) {
	r, store, deps, clients := orangerCallsWaypoint()

	r.publishCrossApplicationTrust(context.Background(), logr.Discard(),
		"nutgraf-01", "nutgraf", "oranger", deps, clients)

	// A MAP entry, not a bare id: waypoint must be able to NAME its caller, or
	// ADR-042's per-consumer record ownership has to take the name from the
	// request, which invariant 2b forbids.
	got := store.data[k(tenantPath("nutgraf-01", "waypoint"), allowedAzpKey)]
	if want := orangerExchangeClientID + "=oranger"; got != want {
		t.Fatalf("waypoint's %s = %q; want %q", allowedAzpKey, got, want)
	}
}

func TestTheTargetsAllowlistNeverGetsTheCallersBrowserClient(t *testing.T) {
	// The defect, stated as a test. If the call site is ever rewired to
	// identity.ClientID -- the tenant's public PKCE client -- this fails.
	//
	// Both halves matter. Publishing the browser client would refuse every genuine
	// exchanged call (no exchanged token carries it), AND, if it ever did match,
	// would admit any token from oranger's own login, an ID token included.
	r, store, deps, clients := orangerCallsWaypoint()

	r.publishCrossApplicationTrust(context.Background(), logr.Discard(),
		"nutgraf-01", "nutgraf", "oranger", deps, clients)

	got := store.data[k(tenantPath("nutgraf-01", "waypoint"), allowedAzpKey)]
	if strings.HasPrefix(got, orangerBrowserClientID+"=") || got == orangerBrowserClientID {
		t.Fatal("published the PUBLIC PKCE client as the allowed caller: " +
			"a browser-obtained token would satisfy waypoint's allowlist")
	}
	if got == "" {
		t.Fatal("published nothing; the exchange client was available")
	}
}

func TestTheAudienceScopeIsPublishedOntoTheCallerNotTheTarget(t *testing.T) {
	// The two halves go to different folders, and swapping them is silent: the
	// caller would ask for no audience while the target admitted nobody.
	r, store, deps, clients := orangerCallsWaypoint()

	r.publishCrossApplicationTrust(context.Background(), logr.Discard(),
		"nutgraf-01", "nutgraf", "oranger", deps, clients)

	want := "urn:zitadel:iam:org:project:id:" + waypointProjectID + ":aud"
	if got := store.data[k(tenantPath("nutgraf-01", "oranger"), "OIDC_BACKEND_AUDIENCE_SCOPES")]; got != want {
		t.Fatalf("oranger's audience scopes = %q; want %q", got, want)
	}
	if _, onTarget := store.data[k(tenantPath("nutgraf-01", "waypoint"), "OIDC_BACKEND_AUDIENCE_SCOPES")]; onTarget {
		t.Fatal("audience scopes landed on the TARGET's folder; they belong to the caller")
	}
}

func TestNoExchangeClientPublishesNoAllowlistAtAll(t *testing.T) {
	// Pending, and pending must write nothing. A target holding some other client
	// id looks configured and admits the wrong caller; a target holding nothing
	// fails closed until the next reconcile.
	store := newFakeSecretStore()
	r := &AINativeSaaSReconciler{InfisicalClient: store}
	deps := []resolvedDependency{{AppID: "waypoint", ProjectID: waypointProjectID}}

	// The exchange client has not been minted and nothing is published for it.
	clients := []client2.DeclaredClient{{Name: "bff", ClientID: "oranger-bff@nutgraf"}}

	r.publishCrossApplicationTrust(context.Background(), logr.Discard(),
		"nutgraf-01", "nutgraf", "oranger", deps, clients)

	if got, ok := store.data[k(tenantPath("nutgraf-01", "waypoint"), allowedAzpKey)]; ok {
		t.Fatalf("published %q while the exchange client was unknown; want no write", got)
	}
}

func TestTheExchangeClientIsReadFromInfisicalOnLaterReconciles(t *testing.T) {
	// The issuer discloses a generated secret once, so a reconcile after the mint
	// carries no Clients entry. The id published at mint time is the source then,
	// and without this fallback every later reconcile would treat a provisioned
	// application as pending.
	store := newFakeSecretStore()
	r := &AINativeSaaSReconciler{InfisicalClient: store}
	store.data[k(tenantPath("nutgraf-01", "oranger"),
		secrets.InfisicalOAuthClientIDKey(gatewayExchangeClientName("oranger")))] = orangerExchangeClientID

	deps := []resolvedDependency{{AppID: "waypoint", ProjectID: waypointProjectID}}

	r.publishCrossApplicationTrust(context.Background(), logr.Discard(),
		"nutgraf-01", "nutgraf", "oranger", deps, nil /* no Clients this reconcile */)

	got := store.data[k(tenantPath("nutgraf-01", "waypoint"), allowedAzpKey)]
	if want := orangerExchangeClientID + "=oranger"; got != want {
		t.Fatalf("waypoint's %s = %q; want %q from the caller's published id",
			allowedAzpKey, got, want)
	}
}

func TestASecondCallerIsAddedNotSubstituted(t *testing.T) {
	// Several applications may depend on one target, each reconciled separately.
	// A write that replaced the list would revoke the other callers.
	store := newFakeSecretStore()
	r := &AINativeSaaSReconciler{InfisicalClient: store}
	deps := []resolvedDependency{{AppID: "waypoint", ProjectID: waypointProjectID}}

	for _, app := range []string{"oranger", "atlas"} {
		clients := []client2.DeclaredClient{
			{Name: gatewayExchangeClientName(app), ClientID: app + "-gateway-exchange@nutgraf"},
		}
		r.publishCrossApplicationTrust(context.Background(), logr.Discard(),
			"nutgraf-01", "nutgraf", app, deps, clients)
	}

	got := store.data[k(tenantPath("nutgraf-01", "waypoint"), allowedAzpKey)]
	if want := "atlas-gateway-exchange@nutgraf=atlas oranger-gateway-exchange@nutgraf=oranger"; got != want {
		t.Fatalf("waypoint's %s = %q; want both callers, sorted: %q", allowedAzpKey, got, want)
	}
}
