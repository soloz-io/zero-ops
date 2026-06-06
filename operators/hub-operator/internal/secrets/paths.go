package secrets

// Infisical REST API path constants.
// Source of truth: archived/identity-auth/infisical/backend/src/server/routes/v1/
const (
	PathAuthUniversalAuthLogin = "/api/v1/auth/universal-auth/login"
	PathIdentities             = "/api/v1/identities"
	PathFolders                = "/api/v2/folders"

	PathAuthUniversalAuthIdentities    = "/api/v1/auth/universal-auth/identities/%s"
	PathAuthUniversalAuthClientSecrets = "/api/v1/auth/universal-auth/identities/%s/client-secrets"
	PathProjectMembershipsIdentities   = "/api/v1/projects/%s/memberships/identities/%s"
	PathSecretsRaw                     = "/api/v3/secrets/raw/%s"
)
