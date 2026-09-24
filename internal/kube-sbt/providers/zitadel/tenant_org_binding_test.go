package zitadel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The invariant ADR-088 adds: a tenant's organisation is bound by ID, never
// resolved by name.
//
// The defect these cover cost a fleet its logins on 2026-09-24. ensureOrg
// searched by name and created on a miss, so renaming `waypoint` to `nutgraf`
// was indistinguishable from onboarding a customer: a second organisation
// appeared with its own OAuth clients while every live user, grant and session
// stayed in the first. The gateway went on minting tokens for the old client,
// the BFF began expecting the new one, and /auth/me answered 401.
//
// An organisation is ZITADEL's security boundary and a user belongs to exactly
// one, so that duplicate is not cosmetic -- it is a second security principal
// created by an attribute change.

// orgRecorder answers the organisation endpoints and records what was asked.
type orgRecorder struct {
	// existingName is an organisation that already exists under that name.
	existingName string
	// liveOrgs are ids that resolve; anything else 404s.
	liveOrgs map[string]bool
	created  []string
}

func newOrgAuth(t *testing.T, rec *orgRecorder) (*Auth, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/admin/v1/orgs/_search"):
			res := map[string]any{"result": []any{}}
			if rec.existingName != "" {
				res = map[string]any{"result": []any{
					map[string]any{"id": "org-existing", "name": rec.existingName},
				}}
			}
			_ = json.NewEncoder(w).Encode(res)
		case strings.HasSuffix(r.URL.Path, "/management/v1/orgs/me") && r.Method == http.MethodGet:
			if rec.liveOrgs[r.Header.Get(orgHeader)] {
				_ = json.NewEncoder(w).Encode(map[string]any{"org": map[string]any{"id": r.Header.Get(orgHeader)}})
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":5,"message":"NotFound"}`))
		case strings.HasSuffix(r.URL.Path, "/management/v1/orgs") && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			name, _ := body["name"].(string)
			rec.created = append(rec.created, name)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "org-new"})
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	a := &Auth{api: &apiClient{base: srv.URL, http: srv.Client(), token: "t"}}
	return a, srv
}

// A bound tenant resolves by ID. The name is not consulted, which is what makes
// a rename survivable.
func TestResolveOrg_BoundTenantIgnoresTheName(t *testing.T) {
	rec := &orgRecorder{existingName: "some-other-org", liveOrgs: map[string]bool{"org-bound": true}}
	a, srv := newOrgAuth(t, rec)
	defer srv.Close()

	got, err := a.resolveOrg(context.Background(), "a-name-that-changed", "org-bound")
	if err != nil {
		t.Fatalf("resolveOrg: %v", err)
	}
	if got != "org-bound" {
		t.Errorf("resolved %q, want the bound organisation", got)
	}
	if len(rec.created) != 0 {
		t.Errorf("created %v; a bound tenant must never create an organisation", rec.created)
	}
}

// The case that broke the fleet: an organisation exists under this name, and
// nothing records it as this tenant's. Creating a second one is the one thing
// that must not happen.
func TestResolveOrg_UnboundWithExistingOrgRefusesRatherThanDuplicating(t *testing.T) {
	rec := &orgRecorder{existingName: "waypoint", liveOrgs: map[string]bool{}}
	a, srv := newOrgAuth(t, rec)
	defer srv.Close()

	_, err := a.resolveOrg(context.Background(), "waypoint", "")
	if !errors.Is(err, ErrOrgUnadopted) {
		t.Fatalf("error = %v, want ErrOrgUnadopted", err)
	}
	if len(rec.created) != 0 {
		t.Fatalf("created %v; an unadopted organisation must never be duplicated", rec.created)
	}
	// The operator has to be able to act on this, so it must name the id.
	if !strings.Contains(err.Error(), "org-existing") {
		t.Errorf("error does not name the organisation to adopt: %v", err)
	}
}

// Nothing exists, so there is nothing to conflict with: create, and the caller
// records the binding.
func TestResolveOrg_UnboundWithNoOrgCreates(t *testing.T) {
	rec := &orgRecorder{liveOrgs: map[string]bool{}}
	a, srv := newOrgAuth(t, rec)
	defer srv.Close()

	got, err := a.resolveOrg(context.Background(), "brand-new", "")
	if err != nil {
		t.Fatalf("resolveOrg: %v", err)
	}
	if got != "org-new" || len(rec.created) != 1 || rec.created[0] != "brand-new" {
		t.Errorf("got %q created=%v, want one organisation created", got, rec.created)
	}
}

// A binding pointing at an organisation that no longer exists is reported, not
// silently replaced: recreating it would strip every user and grant the tenant
// had, which is the same loss by a different route.
func TestResolveOrg_BoundToAMissingOrgRefuses(t *testing.T) {
	rec := &orgRecorder{liveOrgs: map[string]bool{}}
	a, srv := newOrgAuth(t, rec)
	defer srv.Close()

	_, err := a.resolveOrg(context.Background(), "whatever", "org-deleted")
	if !errors.Is(err, ErrOrgUnadopted) {
		t.Fatalf("error = %v, want ErrOrgUnadopted", err)
	}
	if len(rec.created) != 0 {
		t.Errorf("created %v; a missing bound organisation must not be recreated automatically", rec.created)
	}
}
