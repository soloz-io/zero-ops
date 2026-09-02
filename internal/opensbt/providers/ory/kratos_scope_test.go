package ory

import (
	"strings"
	"testing"

	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// A user with neither a tenant nor a platform group can never authenticate: the
// auth-proxy derives tenant_id from metadata_public, so the claim is absent and
// every tenant-scoped service rejects the token. Creation is the only place that
// failure is cheap to catch.
func TestIsPlatformScoped(t *testing.T) {
	cases := []struct {
		name   string
		user   models.User
		scoped bool
	}{
		{"platform admin", models.User{Groups: []string{"platform_admins"}}, true},
		{"platform admin among others", models.User{Groups: []string{"viewers", "platform_admins"}}, true},
		{"tenant user", models.User{TenantID: "waypoint"}, false},
		{"unrelated group only", models.User{Groups: []string{"viewers"}}, false},
		{"nothing at all", models.User{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isPlatformScoped(c.user); got != c.scoped {
				t.Fatalf("isPlatformScoped = %v, want %v", got, c.scoped)
			}
		})
	}
}

// The guard must reject "neither", accept either scope, and — importantly — not
// demand a tenant of a platform admin, which is the contradiction that made a
// correctly-modelled admin unable to log in.
func TestValidateUserScope(t *testing.T) {
	// Rejected: neither scope. This is the state that produced an account which
	// authenticated against Kratos and was then refused by every service.
	err := validateUserScope(models.User{Email: "nobody@example.com"})
	if err == nil {
		t.Fatal("expected an error for a user with neither tenant nor platform group")
	}
	if !strings.Contains(err.Error(), "neither a tenant nor a platform group") {
		t.Fatalf("error should name the missing scope, got: %v", err)
	}

	// Accepted: a platform admin has NO tenant by design. Requiring one here
	// would make a correctly-modelled admin uncreatable — the same contradiction
	// that left one unable to log in, moved to creation time.
	if err := validateUserScope(models.User{
		Email:  "admin@example.com",
		Groups: []string{"platform_admins"},
	}); err != nil {
		t.Fatalf("platform admin must be accepted without a tenant: %v", err)
	}

	// Accepted: an ordinary tenant user.
	if err := validateUserScope(models.User{
		Email:    "user@example.com",
		TenantID: "waypoint",
	}); err != nil {
		t.Fatalf("tenant user must be accepted: %v", err)
	}
}
