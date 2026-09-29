package controller

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ADR-094: one identity project per application, and an organisation binding
// shared by every application of a tenant. Both decisions are made from the
// application's record, and each wrong answer fails far from here -- a login
// refused with redirect_uri_mismatch, or everyone signed out under a new client
// id -- so they are pinned by value.

func TestApplicationProject(t *testing.T) {
	cases := []struct {
		name                  string
		ownOrg, ownProject    string
		wantName, wantKnownID string
	}{
		{"a new application gets a project of its own", "", "", "oranger", ""},
		{"an application bound before projects existed stays on the legacy project", "org-1", "", "", ""},
		{"a bound project is used by id and never re-resolved by name", "org-1", "proj-9", "", "proj-9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, known := applicationProject("oranger", tc.ownOrg, tc.ownProject)
			if name != tc.wantName || known != tc.wantKnownID {
				t.Errorf("applicationProject = (%q, %q), want (%q, %q)", name, known, tc.wantName, tc.wantKnownID)
			}
		})
	}
}

func appXR(name, tenant, org string) unstructured.Unstructured {
	u := unstructured.Unstructured{Object: map[string]any{}}
	u.SetName(name)
	_ = unstructured.SetNestedField(u.Object, tenant, "spec", "tenantId")
	if org != "" {
		_ = unstructured.SetNestedField(u.Object, org, "status", "identity", "zitadelOrgId")
	}
	return u
}

func TestOrgBindingAmong_InheritsTheTenantsBinding(t *testing.T) {
	items := []unstructured.Unstructured{
		appXR("nutgraf-waypoint-dev-xr", "nutgraf", "org-nutgraf"),
		appXR("nutgraf-oranger-dev-xr", "nutgraf", ""),
	}
	got, err := orgBindingAmong(items, "nutgraf", "nutgraf-oranger-dev-xr")
	if err != nil || got != "org-nutgraf" {
		t.Fatalf("orgBindingAmong = (%q, %v), want (org-nutgraf, nil)", got, err)
	}
}

func TestOrgBindingAmong_NeverCrossesTenants(t *testing.T) {
	items := []unstructured.Unstructured{
		appXR("acme-shop-dev-xr", "acme", "org-acme"),
		appXR("nutgraf-oranger-dev-xr", "nutgraf", ""),
	}
	got, err := orgBindingAmong(items, "nutgraf", "nutgraf-oranger-dev-xr")
	if err != nil || got != "" {
		t.Fatalf("orgBindingAmong = (%q, %v), want (\"\", nil): another tenant's organisation must never be inherited", got, err)
	}
}

func TestOrgBindingAmong_RefusesToChooseBetweenTwoOrganisations(t *testing.T) {
	items := []unstructured.Unstructured{
		appXR("nutgraf-waypoint-dev-xr", "nutgraf", "org-a"),
		appXR("nutgraf-waypoint-staging-xr", "nutgraf", "org-b"),
		appXR("nutgraf-oranger-dev-xr", "nutgraf", ""),
	}
	_, err := orgBindingAmong(items, "nutgraf", "nutgraf-oranger-dev-xr")
	if err == nil || !strings.Contains(err.Error(), "different organisations") {
		t.Fatalf("expected a refusal naming the split, got %v", err)
	}
}

func TestOrgBindingAmong_IgnoresItsOwnRecord(t *testing.T) {
	items := []unstructured.Unstructured{appXR("nutgraf-oranger-dev-xr", "nutgraf", "org-stale")}
	got, _ := orgBindingAmong(items, "nutgraf", "nutgraf-oranger-dev-xr")
	if got != "" {
		t.Fatalf("orgBindingAmong = %q; an application must not inherit from itself", got)
	}
}
