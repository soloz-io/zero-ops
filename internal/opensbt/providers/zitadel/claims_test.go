package zitadel

import (
	"reflect"
	"sort"
	"testing"
)

const (
	orgHere  = "389091373187334477"
	orgOther = "389090776237277520"
)

// Claim shape copied from a real id_token captured 2026-09-03.
func tokenClaims() map[string]interface{} {
	return map[string]interface{}{
		"sub":   "389091433551757645",
		"email": "arun4infra@gmail.com",
		"urn:zitadel:iam:user:resourceowner:id":   orgHere,
		"urn:zitadel:iam:user:resourceowner:name": "waypoint",
	}
}

func TestTenantComesFromTheOwningOrganisation(t *testing.T) {
	if got := tenantFromClaims(tokenClaims()); got != orgHere {
		t.Fatalf("tenant = %q, want %q", got, orgHere)
	}
}

func TestContractClaimWins(t *testing.T) {
	c := tokenClaims()
	c["tenant_id"] = "explicit"
	if got := tenantFromClaims(c); got != "explicit" {
		t.Fatalf("tenant = %q, want the contract's own claim to win", got)
	}
}

func TestAbsentTenantIsEmptyNotDefaulted(t *testing.T) {
	if got := tenantFromClaims(map[string]interface{}{"sub": "u"}); got != "" {
		t.Fatalf("tenant = %q, want empty so the caller rejects rather than defaults", got)
	}
}

func TestRolesGrantedHereAreKept(t *testing.T) {
	c := tokenClaims()
	c[scopedRolesClaim] = map[string]interface{}{
		"admin": map[string]interface{}{orgHere: "waypoint.example.com"},
	}
	if got := rolesGrantedInTenant(c, orgHere); !reflect.DeepEqual(got, []string{"admin"}) {
		t.Fatalf("roles = %v, want [admin]", got)
	}
}

// The reason the nested organisation id is not discarded. A role granted in
// another organisation must never read as a role here.
func TestRoleGrantedElsewhereIsIgnored(t *testing.T) {
	c := tokenClaims()
	c[scopedRolesClaim] = map[string]interface{}{
		"admin": map[string]interface{}{orgOther: "other.example.com"},
	}
	if got := rolesGrantedInTenant(c, orgHere); len(got) != 0 {
		t.Fatalf("roles = %v, want none — that grant belongs to another tenant", got)
	}
}

func TestCrossTenantGrantKeepsOnlyTheLocalHalf(t *testing.T) {
	c := tokenClaims()
	c[scopedRolesClaim] = map[string]interface{}{
		"admin": map[string]interface{}{orgHere: "waypoint.example.com"},
		"owner": map[string]interface{}{orgOther: "other.example.com"},
	}
	got := rolesGrantedInTenant(c, orgHere)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"admin"}) {
		t.Fatalf("roles = %v, want [admin] only", got)
	}
}

func TestAbsentRolesMeansNoneNotAll(t *testing.T) {
	if got := rolesGrantedInTenant(tokenClaims(), orgHere); len(got) != 0 {
		t.Fatalf("roles = %v, want none", got)
	}
}

// Without a tenant there is nothing to scope roles to, so returning any would be
// returning unscoped authority.
func TestNoTenantYieldsNoRoles(t *testing.T) {
	c := tokenClaims()
	c[scopedRolesClaim] = map[string]interface{}{
		"admin": map[string]interface{}{orgHere: "waypoint.example.com"},
	}
	if got := rolesGrantedInTenant(c, ""); len(got) != 0 {
		t.Fatalf("roles = %v, want none when the tenant is unknown", got)
	}
}

func TestSplitNameNeverYieldsAnEmptyGivenName(t *testing.T) {
	for _, in := range []string{"", "   ", "Arun", "Arun Subramanian", "A B C"} {
		g, f := splitName(in)
		if g == "" || f == "" {
			t.Fatalf("splitName(%q) = (%q,%q); the issuer rejects an empty name part", in, g, f)
		}
	}
}
