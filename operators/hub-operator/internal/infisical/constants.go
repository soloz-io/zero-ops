package infisical

import "github.com/soloz-io/zero-ops/operators/hub-operator/internal/constant"

const (
	// Infisical Service Configuration
	InfisicalServiceName      = "infisical-standalone-infisical"
	InfisicalServiceNamespace = "platform-security"
	InfisicalServicePort      = 8080
	InfisicalBaseURL          = "http://infisical-standalone-infisical.platform-security.svc.cluster.local:8080"

	// API Endpoints
	APIEndpointLogin              = "/api/v3/auth/login"
	APIEndpointSelectOrg          = "/api/v3/auth/select-organization"
	APIEndpointBootstrap          = "/api/v1/admin/bootstrap"
	APIEndpointProjects           = "/api/v1/projects"
	APIEndpointIdentities         = "/api/v1/identities"
	APIEndpointUniversalAuth      = "/api/v1/auth/universal-auth/identities"
	APIEndpointClientSecrets      = "/client-secrets"
	APIEndpointProjectMemberships = "/api/v1/projects/%s/memberships/identities/%s"

	// Bootstrap Configuration
	ProjectName      = "Hub-Platform"
	ProjectSlug      = "hub-platform"
	OrganizationName = "hub-ops"
	IdentityName     = "hub-operator"
	IdentityRole     = "admin"
	EnvironmentSlug  = "dev"

	// Admin Credentials
	AdminEmail    = "arun4infra@gmail.com"
	AdminPassword = "Password@123" // TODO: Generate secure password

	// Secret Names
	SecretInfisicalAuth         = "infisical-auth"
	SecretInfisicalAdmin        = "infisical-admin"
	SecretHetznerDNS            = "hetzner-dns"           // For external-dns and cert-manager
	SecretHetznerCredentials    = "hetzner-credentials" // For CAPI/CCM/CSI
	SecretHCloudDeprecated      = "hcloud"              // Deprecated: use SecretHetznerCredentials
	SecretPlatformDBApp         = "platform-db-app"
	SecretInfisicalDB           = "infisical-db-credentials"
	SecretGHCRPull              = "ghcr-pull-secret"
	SecretPlatformGit           = "platform-git-secret"

	// Namespaces - unified across Hub and Spoke
	NamespaceOps         = "platform-ops"
	NamespaceCloudSystem = "kube-system"
	NamespaceEdge        = "platform-edge"
	NamespaceData        = "platform-data"

	// Infisical Secret Keys
	KeyHetznerDNSAPIKey        = "hetzner-dns-api-key"
	KeyHCloudToken             = "hcloud-token"
	KeyClientID                = constant.KeyClientID
	KeyClientSecret            = constant.KeyClientSecret
	KeyAdminToken              = "admin-token"
	KeyAdminEmail              = "admin-email"
	KeyOrgID                   = "org-id"
	KeyProjectID               = constant.KeyProjectID
	KeyProjectSlug             = "project-slug"
	KeyAPIKey                  = "api-key"
	KeyToken                   = "token"
	KeyPlatformDBAppUsername   = "platform-db-app-username"
	KeyPlatformDBAppPassword   = "platform-db-app-password"
	KeyInfisicalDBUsername     = "infisical-db-username"
	KeyInfisicalDBPassword     = "infisical-db-password"
	KeyGHCRPullSecret          = "ghcr-pull-secret"
	KeyGitHubUsername          = "github-username"
	KeyGitHubToken             = "github-token"

	// Application Secret Keys (Operator uploads directly to Infisical)
	KeyControlPlaneDBUsername  = "hub-control-plane-db-username"
	KeyControlPlaneDBPassword  = "hub-control-plane-db-password"
	KeyHubCentralizedDBUsername = "hub-centralized-db-username"
	KeyHubCentralizedDBPassword = "hub-centralized-db-password"
	KeySpireServerDBUsername   = "spire-server-db-username"
	KeySpireServerDBPassword   = "spire-server-db-password"
	KeyHydraDBUsername         = "hub-hydra-db-username"
	KeyHydraDBPassword         = "hub-hydra-db-password"
	KeyKratosDBUsername        = "hub-kratos-db-username"
	KeyKratosDBPassword        = "hub-kratos-db-password"
	KeyKetoDBUsername          = "hub-keto-db-username"
	KeyKetoDBPassword          = "hub-keto-db-password"
	KeyHydraSystemSecret       = "hub-hydra-system-secret"
	KeyNATSLeafUsername        = "nats-leaf-username"
	KeyNATSLeafPassword        = "nats-leaf-password"
	KeyOpenmeterPostgreSQLUsername = "openmeter-postgresql-username"
	KeyOpenmeterPostgreSQLPassword = "openmeter-postgresql-password"
	KeyClickhouseAdminPassword = "platform-clickhouse-admin-password"

	// Labels
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	ValueManagedBy = "hub-operator"
	ValueBootstrap = "infisical-bootstrap"
)
