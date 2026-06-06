package infisical

// Infisical REST API path constants.
// Source of truth: archived/identity-auth/infisical/backend/src/server/routes/v1/
const (
	ProjectSlug        = "hub-platform"
	SecretsProjectSlug = "hub-secrets"

	PathProjects                = "/api/v1/projects"
	PathIdentities              = "/api/v1/identities"
	PathAuthUniversalAuthLogin  = "/api/v1/auth/universal-auth/login"
	PathWorkspace               = "/api/v1/workspace"

	PathAuthUniversalAuthIdentities    = "/api/v1/auth/universal-auth/identities/%s"
	PathAuthUniversalAuthClientSecrets = "/api/v1/auth/universal-auth/identities/%s/client-secrets"
	PathProjectMembershipsIdentities   = "/api/v1/projects/%s/memberships/identities/%s"
	PathOrgIdentityMemberships         = "/api/v1/organization/identity-memberships/%s"
	PathCertificateProfilesBySlug      = "/api/v1/cert-manager/certificate-profiles/slug/%s?projectId=%s"
	PathCertificateAuthorities         = "/api/v1/cert-manager/ca"
	PathCertificatePolicies            = "/api/v1/cert-manager/certificate-policies"
	PathCertificateProfiles            = "/api/v1/cert-manager/certificate-profiles"
	PathCertificateAuthoritiesCreate   = "/api/v1/cert-manager/ca/internal"
	PathSecretsRaw                     = "/api/v3/secrets/raw/%s"
)
