package zitadel

// Claim names the platform's contract does not define.
//
// The contract is tenant_id and roles. An issuer that does not emit those spells
// them its own way, and reading it means naming its claims somewhere — no
// abstraction removes the literal, it only decides where it lives. They live
// here, in one table, so the logic below is written against the contract rather
// than against a product (ADR-059).
//
// Order is preference order: the platform's own claim first, so a token that
// already speaks the contract is never reinterpreted.
var tenantIDClaims = []string{
	"tenant_id",
	"urn:zitadel:iam:user:resourceowner:id",
	"urn:zitadel:iam:org:id",
}

// scopedRolesClaim is nested as { roleKey: { grantingTenantID: domain } }.
const scopedRolesClaim = "urn:zitadel:iam:org:project:roles"

func tenantFromClaims(raw map[string]interface{}) string {
	for _, name := range tenantIDClaims {
		if v, ok := raw[name].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// rolesGrantedInTenant returns roles granted WITHIN the given tenant.
//
// The claim is nested because one token can carry the same role granted in
// several tenants. The tenant filter is the security-relevant part, not a
// detail: taking the map's keys would return a role granted in a DIFFERENT
// tenant as if it had been granted here — a cross-tenant privilege leak that
// reads as a correct role list and stays invisible until someone holds access to
// a second tenant.
//
// An absent claim means no roles, never all roles. The issuer emits it only when
// the token was requested with the scope that mints it AND the project asserts
// roles, so "absent" is a very reachable state.
func rolesGrantedInTenant(raw map[string]interface{}, tenantID string) []string {
	if tenantID == "" {
		return nil
	}
	// A flat list is honoured when the issuer emits one, so a token already
	// speaking the contract keeps working.
	if flat, ok := raw["roles"].([]interface{}); ok {
		out := make([]string, 0, len(flat))
		for _, r := range flat {
			if s, ok := r.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}

	nested, ok := raw[scopedRolesClaim].(map[string]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(nested))
	for roleKey, grantedIn := range nested {
		orgs, ok := grantedIn.(map[string]interface{})
		if !ok {
			continue
		}
		if _, here := orgs[tenantID]; here {
			out = append(out, roleKey)
		}
	}
	return out
}
