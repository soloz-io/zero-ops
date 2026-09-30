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
		"nutgraf-01", "nutgraf", "oranger", deps, clients, nil)

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
		"nutgraf-01", "nutgraf", "oranger", deps, clients, nil)

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
		"nutgraf-01", "nutgraf", "oranger", deps, clients, nil)

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
		"nutgraf-01", "nutgraf", "oranger", deps, clients, nil)

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
		"nutgraf-01", "nutgraf", "oranger", deps, nil /* no Clients this reconcile */, nil /* no service identity */)

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
			"nutgraf-01", "nutgraf", app, deps, clients, nil)
	}

	got := store.data[k(tenantPath("nutgraf-01", "waypoint"), allowedAzpKey)]
	if want := "atlas-gateway-exchange@nutgraf=atlas oranger-gateway-exchange@nutgraf=oranger"; got != want {
		t.Fatalf("waypoint's %s = %q; want both callers, sorted: %q", allowedAzpKey, got, want)
	}
}

// ── The service identity (ADR-097) ───────────────────────────────────────────
//
// Client credentials is the flow that actually works on the deployed issuer, so
// these cover the wiring that carries it: the caller's own credential, and the
// allowlist entry without which the target refuses every service call.

const orangerServiceClientID = "oranger-service@nutgraf"

// A receiver matches `azp || client_id`, and the two flows fill different claims:
// a gateway exchange stamps azp with the exchange client, client credentials
// stamps client_id with the machine client. Both are oranger calling. Publishing
// only one leaves the other flow refused by a target that looks configured.
func TestBothCallerIdentitiesReachTheTargetAllowlist(t *testing.T) {
	store := newFakeSecretStore()
	r := &AINativeSaaSReconciler{InfisicalClient: store}
	deps := []resolvedDependency{{AppID: "waypoint", ProjectID: waypointProjectID}}
	clients := []client2.DeclaredClient{
		{Name: gatewayExchangeClientName("oranger"), ClientID: orangerExchangeClientID},
	}

	r.publishCrossApplicationTrust(context.Background(), logr.Discard(),
		"nutgraf-01", "nutgraf", "oranger", deps, clients,
		&client2.ServiceClient{ClientID: orangerServiceClientID, ClientSecret: "svc-secret"})

	got := store.data[k(tenantPath("nutgraf-01", "waypoint"), allowedAzpKey)]
	want := orangerExchangeClientID + "=oranger " + orangerServiceClientID + "=oranger"
	if got != want {
		t.Fatalf("waypoint's %s = %q; want both of oranger's identities: %q", allowedAzpKey, got, want)
	}
}

// The caller needs its own credential to mint a token with. Published onto the
// CALLER's path -- the target's folder holds only the allowlist entry, and
// keeping them apart is what lets one be revoked without the other.
func TestTheServiceCredentialIsPublishedToTheCaller(t *testing.T) {
	store := newFakeSecretStore()
	r := &AINativeSaaSReconciler{InfisicalClient: store}
	deps := []resolvedDependency{{AppID: "waypoint", ProjectID: waypointProjectID}}

	r.publishCrossApplicationTrust(context.Background(), logr.Discard(),
		"nutgraf-01", "nutgraf", "oranger", deps,
		[]client2.DeclaredClient{{Name: gatewayExchangeClientName("oranger"), ClientID: orangerExchangeClientID}},
		&client2.ServiceClient{ClientID: orangerServiceClientID, ClientSecret: "svc-secret"})

	caller := tenantPath("nutgraf-01", "oranger")
	if got := store.data[k(caller, serviceClientIDKey)]; got != orangerServiceClientID {
		t.Fatalf("caller's %s = %q, want %q", serviceClientIDKey, got, orangerServiceClientID)
	}
	if got := store.data[k(caller, serviceClientSecretKey)]; got != "svc-secret" {
		t.Fatalf("caller's %s = %q, want the minted secret", serviceClientSecretKey, got)
	}
	// The credential belongs to the caller and must not appear in the target's
	// folder, which the target's workload reads.
	if got, ok := store.data[k(tenantPath("nutgraf-01", "waypoint"), serviceClientSecretKey)]; ok {
		t.Fatalf("caller's secret leaked into the target's folder: %q", got)
	}
}

// The issuer discloses a generated secret once, so most reconciles carry none.
// Writing the empty value would overwrite the credential the running workload is
// holding, and the failure lands on its next call rather than on this reconcile.
func TestALaterReconcileDoesNotBlankTheServiceSecret(t *testing.T) {
	store := newFakeSecretStore()
	r := &AINativeSaaSReconciler{InfisicalClient: store}
	deps := []resolvedDependency{{AppID: "waypoint", ProjectID: waypointProjectID}}
	clients := []client2.DeclaredClient{
		{Name: gatewayExchangeClientName("oranger"), ClientID: orangerExchangeClientID},
	}

	r.publishCrossApplicationTrust(context.Background(), logr.Discard(),
		"nutgraf-01", "nutgraf", "oranger", deps, clients,
		&client2.ServiceClient{ClientID: orangerServiceClientID, ClientSecret: "svc-secret"})
	// The next reconcile: the identity exists, so the issuer discloses nothing.
	r.publishCrossApplicationTrust(context.Background(), logr.Discard(),
		"nutgraf-01", "nutgraf", "oranger", deps, clients,
		&client2.ServiceClient{ClientID: orangerServiceClientID})

	caller := tenantPath("nutgraf-01", "oranger")
	if got := store.data[k(caller, serviceClientSecretKey)]; got != "svc-secret" {
		t.Fatalf("second reconcile left %s = %q; want the stored secret untouched", serviceClientSecretKey, got)
	}
	// The id is still republished: it is what the target matches, and dropping it
	// would read as a caller that no longer exists.
	if got := store.data[k(caller, serviceClientIDKey)]; got != orangerServiceClientID {
		t.Fatalf("second reconcile lost %s = %q", serviceClientIDKey, got)
	}
}

// An absent service identity must publish NOTHING for it, and must not fall back
// to the exchange client. The exchange client is an OIDC application, not a
// machine user; it cannot authenticate client credentials, so an allowlist entry
// naming it can never be matched by a service token while looking configured.
func TestAPendingServiceIdentityPublishesNothingAndDoesNotFallBack(t *testing.T) {
	store := newFakeSecretStore()
	r := &AINativeSaaSReconciler{InfisicalClient: store}
	deps := []resolvedDependency{{AppID: "waypoint", ProjectID: waypointProjectID}}

	r.publishCrossApplicationTrust(context.Background(), logr.Discard(),
		"nutgraf-01", "nutgraf", "oranger", deps,
		[]client2.DeclaredClient{{Name: gatewayExchangeClientName("oranger"), ClientID: orangerExchangeClientID}},
		nil)

	caller := tenantPath("nutgraf-01", "oranger")
	if got, ok := store.data[k(caller, serviceClientIDKey)]; ok {
		t.Fatalf("published a service client id with no service identity: %q", got)
	}
	// The exchange half still stands -- the browser path does not wait on this.
	got := store.data[k(tenantPath("nutgraf-01", "waypoint"), allowedAzpKey)]
	if want := orangerExchangeClientID + "=oranger"; got != want {
		t.Fatalf("waypoint's %s = %q; want only the exchange caller: %q", allowedAzpKey, got, want)
	}
}

// The name the operator sends to the identity service is the name the credential
// is published against, and the two must not drift apart.
func TestTheServiceUserNameIsDistinctFromTheExchangeClient(t *testing.T) {
	if serviceUserName("oranger") == gatewayExchangeClientName("oranger") {
		t.Fatal("the service identity and the exchange client share a name; one authenticates a user exchange, the other a service")
	}
	if got, want := serviceUserName("oranger"), "oranger-service"; got != want {
		t.Fatalf("serviceUserName = %q, want %q", got, want)
	}
}
