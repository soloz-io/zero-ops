package authproxy

// Zitadel's OIDC surface (ADR-060).
//
// Zitadel does not use the conventional paths for most of these, and the two
// that matter fail in ways that name neither the path nor the provider:
// /.well-known/jwks.json returns 404, and an issuer built from the wrong port
// renders every URL as https://id.<zone>:8080/... which a relying party rejects
// as a spoofed provider. The literals live here, in one table, rather than being
// spelled inline at each call site (ADR-059).
const (
	zitadelDiscoveryPath     = "/.well-known/openid-configuration"
	zitadelJWKSPath          = "/oauth/v2/keys"
	zitadelAuthorizePath     = "/oauth/v2/authorize"
	zitadelTokenPath         = "/oauth/v2/token"
	zitadelUserinfoPath      = "/oidc/v1/userinfo"
	zitadelRevocationPath    = "/oauth/v2/revoke"
	zitadelIntrospectionPath = "/oauth/v2/introspect"
	zitadelEndSessionPath    = "/oidc/v1/end_session"
)

// Claim names the platform's contract does not define.
//
// Kept byte-identical to internal/kube-sbt/providers/zitadel/claims.go, which is
// the authoritative statement of this contract. It is restated rather than
// imported because that package is a kube-sbt provider and auth-proxy is a
// separate service; importing it would couple the two deployments through a
// package neither owns. The duplication is deliberate and must be changed in
// both places.
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

// tenantFromClaims resolves the tenant a token belongs to.
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
// An absent claim means no roles, never all roles.
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
