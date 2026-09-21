package zitadel

import (
	"context"
	"strings"
	"testing"
)

// An application the issuer will not authorize must not be reported as usable.
//
// The app search returns inactive applications alongside active ones. An
// inactive application still has a client id, and that id is refused at the
// authorization endpoint with `Errors.App.NotFound` -- the same answer the
// issuer gives for an identifier that was never allocated, so nothing about the
// refusal says which of the two it is.
//
// Returning one produced a loop that could not repair itself: the caller
// published the identifier as though provisioning had succeeded, the tenant's
// gateway presented it, every login was refused, and each reconciliation found
// the same application and repeated the same answer. Every layer above reported
// success throughout -- the Kubernetes resources healthy, the ExternalSecret
// resolving, the operator logging that the identity was provisioned.
//
// Reactivated rather than replaced: the client id is already held by the
// gateway and by anything configured from it, so a new one would have to be
// converged onto by every consumer.
func TestEnsureApp_ReactivatesAnInactiveApplicationRatherThanReturningIt(t *testing.T) {
	rec := &recorder{
		existingApps: []map[string]any{{
			"id":         "app-1",
			"name":       "acme-public-client",
			"state":      "APP_STATE_INACTIVE",
			"oidcConfig": map[string]any{"clientId": "cid-1"},
		}},
	}
	a, srv := newTestAuth(t, rec)
	defer srv.Close()

	appID, clientID, _, err := a.ensureApp(context.Background(), "org-1", "proj-1",
		"acme-public-client", nil, nil, authMethodNone)
	if err != nil {
		t.Fatalf("ensureApp: %v", err)
	}
	if appID != "app-1" || clientID != "cid-1" {
		t.Errorf("identity changed: got app=%q client=%q, want app-1/cid-1 — "+
			"reactivation must preserve the identifier consumers already hold", appID, clientID)
	}

	var reactivated bool
	for _, c := range rec.calls {
		if strings.HasSuffix(c, "/apps/app-1/_reactivate") {
			reactivated = true
		}
	}
	if !reactivated {
		t.Error("an inactive application was returned without being reactivated: " +
			"its client id does not authorize, so publishing it reports a success that " +
			"no login can use and no later reconciliation repairs")
	}
}

// An active application is returned untouched. Reactivating unconditionally
// would issue a write on every reconciliation for every tenant.
func TestEnsureApp_LeavesAnActiveApplicationAlone(t *testing.T) {
	rec := &recorder{
		existingApps: []map[string]any{{
			"id":         "app-1",
			"name":       "acme-public-client",
			"state":      "APP_STATE_ACTIVE",
			"oidcConfig": map[string]any{"clientId": "cid-1"},
		}},
	}
	a, srv := newTestAuth(t, rec)
	defer srv.Close()

	if _, _, _, err := a.ensureApp(context.Background(), "org-1", "proj-1",
		"acme-public-client", nil, nil, authMethodNone); err != nil {
		t.Fatalf("ensureApp: %v", err)
	}
	for _, c := range rec.calls {
		if strings.Contains(c, "_reactivate") {
			t.Error("an active application was reactivated: a write on every reconciliation")
		}
	}
}
