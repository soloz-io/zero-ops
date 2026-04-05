package infisical

const (
	// Infisical Service Configuration
	InfisicalServiceName      = "platform-infisical-infisical-standalone-infisical"
	InfisicalServiceNamespace = "hub-platform-security"
	InfisicalServicePort      = 8080
	InfisicalBaseURL          = "http://platform-infisical-infisical-standalone-infisical.hub-platform-security.svc:8080"

	// API Endpoints
	APIEndpointLogin              = "/api/v3/auth/login"
	APIEndpointSelectOrg          = "/api/v3/auth/select-organization"
	APIEndpointBootstrap          = "/api/v1/admin/bootstrap"
	APIEndpointUser               = "/api/v1/user"
	APIEndpointProjects           = "/api/v1/projects"
	APIEndpointIdentities         = "/api/v1/identities"
	APIEndpointUniversalAuth      = "/api/v1/auth/universal-auth/identities"
	APIEndpointClientSecrets      = "/client-secrets"
	APIEndpointProjectMemberships = "/api/v1/projects/%s/memberships/identities/%s"

	// Bootstrap Configuration
	ProjectName      = "Hub-Platform"
	ProjectSlug      = "hub-platform"
	OrganizationName = "hub-ops"
	IdentityName     = "eso-operator"
	IdentityRole     = "admin"
	EnvironmentSlug  = "dev"

	// Admin Credentials
	AdminEmail    = "arun4infra@gmail.com"
	AdminPassword = "Password@123" // TODO: Generate secure password

	// Secret Names
	SecretInfisicalAuth  = "infisical-auth"
	SecretInfisicalAdmin = "infisical-admin"
	SecretHetznerDNS     = "hetzner-dns"
	SecretHCloud         = "hcloud"

	// Namespaces
	NamespaceOps         = "hub-platform-ops"
	NamespaceCloudSystem = "hub-cloud-system"
	NamespaceEdge        = "hub-platform-edge"

	// Infisical Secret Keys
	KeyHetznerDNSAPIKey = "hetzner-dns-api-key"
	KeyHCloudToken      = "hcloud-token"
	KeyClientID         = "client-id"
	KeyClientSecret     = "client-secret"
	KeyAdminToken       = "admin-token"
	KeyAdminEmail       = "admin-email"
	KeyOrgID            = "org-id"
	KeyProjectID        = "project-id"
	KeyProjectSlug      = "project-slug"
	KeyAPIKey           = "api-key"
	KeyToken            = "token"

	// Labels
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	ValueManagedBy = "hub-operator"
	ValueBootstrap = "infisical-bootstrap"
)
