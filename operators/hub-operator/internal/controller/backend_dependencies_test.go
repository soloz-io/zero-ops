package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ADR-094 Part 2: a fleet declares a RELATIONSHIP; the platform decides what it
// renders into. These pin the two properties that make the declaration mean
// something rather than being advisory.

func app(name, tenant, appId, projectID string, deps ...string) unstructured.Unstructured {
	o := map[string]interface{}{
		"spec": map[string]interface{}{"tenantId": tenant, "appId": appId},
	}
	if projectID != "" {
		o["status"] = map[string]interface{}{
			"identity": map[string]interface{}{"zitadelProjectId": projectID},
		}
	}
	if len(deps) > 0 {
		di := make([]interface{}, 0, len(deps))
		for _, d := range deps {
			di = append(di, d)
		}
		o["spec"].(map[string]interface{})["identity"] = map[string]interface{}{"backendDependencies": di}
	}
	u := unstructured.Unstructured{Object: o}
	u.SetName(name)
	return u
}

func TestAudienceScopeNamesTheTargetsProject(t *testing.T) {
	// The scope must carry the TARGET's project, not the caller's. Getting this
	// backwards produces a token the target refuses while everything looks
	// configured.
	got := audienceScopesFor([]resolvedDependency{{AppID: "waypoint", ProjectID: "391737112937890261"}})
	want := "urn:zitadel:iam:org:project:id:391737112937890261:aud"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAudienceScopesAreDeterministic(t *testing.T) {
	// A reconcile that changes nothing must publish nothing. Unsorted output
	// would rewrite the secret on every pass and churn every consumer.
	a := audienceScopesFor([]resolvedDependency{{AppID: "b", ProjectID: "2"}, {AppID: "a", ProjectID: "1"}})
	b := audienceScopesFor([]resolvedDependency{{AppID: "b", ProjectID: "2"}, {AppID: "a", ProjectID: "1"}})
	if a != b {
		t.Fatalf("not deterministic: %q vs %q", a, b)
	}
}

func TestNoDependenciesRendersNoScope(t *testing.T) {
	// An application that declares nothing can call nothing. An empty string,
	// not a stray separator that would reach the issuer as a malformed scope.
	if got := audienceScopesFor(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestBackendDependenciesAreReadFromTheXR(t *testing.T) {
	u := app("nutgraf-oranger", "nutgraf", "oranger", "", "waypoint")
	if got := backendDependenciesOf(&u); len(got) != 1 || got[0] != "waypoint" {
		t.Errorf("got %v, want [waypoint]", got)
	}
	plain := app("nutgraf-waypoint", "nutgraf", "waypoint", "p1")
	if got := backendDependenciesOf(&plain); len(got) != 0 {
		t.Errorf("an app declaring none should read as none, got %v", got)
	}
}
