package infisical

// Infisical REST API path constants.
// Source of truth: archived/identity-auth/infisical/backend/src/server/routes/v1/
//
// NOTE: This operator manages Machine Identity lifecycle only.
// No certificate paths are included — PKI is cert-manager's domain per ADR-035.
const (
	PathAuthUniversalAuthLogin          = "/api/v1/auth/universal-auth/login"
	PathIdentities                      = "/api/v1/identities"
	PathIdentityByID                    = "/api/v1/identities/%s"
	PathAuthUniversalAuthIdentities     = "/api/v1/auth/universal-auth/identities/%s"
	PathAuthUniversalAuthClientSecrets  = "/api/v1/auth/universal-auth/identities/%s/client-secrets"
	PathProjectMembershipsIdentities    = "/api/v1/projects/%s/memberships/identities/%s"
)
