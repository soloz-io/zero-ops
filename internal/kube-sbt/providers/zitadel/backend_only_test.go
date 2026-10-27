package zitadel

import (
	"context"
	"strings"
	"testing"

	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// A backend-only application (zero-ops ADR-109) gets its project -- the audience
// other applications are admitted to -- and nothing anyone signs in to: no
// browser client, and nothing set on the tenant's organisation.
func TestABackendOnlyApplicationGetsAProjectAndNoBrowserClient(t *testing.T) {
	rec := &projectRecorder{projects: map[string]string{}}
	a, done := newProjectAuth(t, rec)
	defer done()

	id, err := a.EnsureTenantIdentity(context.Background(), "nutgraf", "org-1",
		models.ProjectBinding{Name: "credits"}, "", false, true, nil, nil, nil)
	if err != nil {
		t.Fatalf("EnsureTenantIdentity: %v", err)
	}
	if id.ProjectRef == "" || id.TenantRef != "org-1" {
		t.Fatalf("identity = %+v; want the organisation and a project", id)
	}
	if id.ClientID != "" {
		t.Fatalf("a backend-only application got a browser client %q", id.ClientID)
	}
	if in := rec.appCreatedIn(); in != "" {
		t.Fatalf("an OIDC application was created in project %q", in)
	}
	for _, c := range rec.calls {
		// The organisation's sign-in policy belongs to the applications people
		// sign in to; a backend application must never assert its own.
		if strings.Contains(c, "/policies/") {
			t.Fatalf("touched the organisation's policy: %s", c)
		}
	}
}

func TestABackendOnlyApplicationThatDeclaresSignInIsRefused(t *testing.T) {
	rec := &projectRecorder{projects: map[string]string{}}
	a, done := newProjectAuth(t, rec)
	defer done()

	if _, err := a.EnsureTenantIdentity(context.Background(), "nutgraf", "org-1",
		models.ProjectBinding{Name: "credits"}, "owner@example.test", false, true, nil, nil, nil); err == nil {
		t.Fatal("a backend-only application with an owner was provisioned")
	}
	if _, err := a.EnsureTenantIdentity(context.Background(), "nutgraf", "org-1",
		models.ProjectBinding{Name: "credits"}, "", false, true, []string{"https://x.example/oauth/callback"}, nil, nil); err == nil {
		t.Fatal("a backend-only application with a callback was provisioned")
	}
}
