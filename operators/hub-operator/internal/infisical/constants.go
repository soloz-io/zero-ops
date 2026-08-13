package infisical

import (
	"os"

	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/constant"
)

const (
	// Infisical Service Configuration
	InfisicalServiceName      = "infisical-standalone-infisical"
	InfisicalServiceNamespace = "platform-security"
	InfisicalServicePort      = 8080
	InfisicalBaseURL          = "http://infisical-standalone-infisical.platform-security.svc.cluster.local:8080"

	// Bootstrap Configuration
	ProjectName      = "Hub-Platform"
	ProjectSlug      = "hub-platform"
	OrganizationName = "hub-ops"
	IdentityName     = "hub-operator"
	IdentityRole     = "admin"
	EnvironmentSlug  = "dev"

	// Secret Names
	SecretInfisicalAuth      = "infisical-auth"
	SecretInfisicalAdmin     = "infisical-admin"
	SecretHetznerDNS         = "hetzner-dns"         // For external-dns and cert-manager
	SecretHetznerCredentials = "hetzner-credentials" // For CAPI/CCM/CSI
	SecretHCloudDeprecated   = "hcloud"              // Deprecated: use SecretHetznerCredentials
	SecretPlatformDBApp      = "platform-db-app"
	SecretInfisicalDB        = "infisical-db-credentials"
	SecretGHCRPull           = "ghcr-pull-secret"
	SecretPlatformGit        = "platform-git-secret"

	// Namespaces - unified across Hub and Spoke
	NamespaceCAPI        = "platform-capi"
	NamespaceOps         = "platform-ops"
	NamespaceCloudSystem = "kube-system"
	NamespaceEdge        = "platform-edge"
	NamespaceData        = "platform-data"
	NamespaceIdentity    = "platform-identity"
	NamespaceMessaging   = "platform-messaging"
	NamespaceSecurity    = "platform-security"

	// Infisical Secret Keys
	KeyHetznerDNSAPIKey      = "hetzner-dns-api-key"
	KeyHCloudToken           = "hcloud-token"
	KeyTailscaleAuthkey      = "tailscale-authkey"
	KeyTailscaleHostname     = "tailscale-hostname"
	KeyClientID              = constant.KeyClientID
	KeyClientSecret          = constant.KeyClientSecret
	KeyAdminToken            = "admin-token"
	KeyAdminEmail            = "admin-email"
	KeyOrgID                 = "org-id"
	KeyProjectID             = constant.KeyProjectID
	KeyProjectSlug           = "project-slug"
	KeyAPIKey                = "api-key"
	KeyToken                 = "token"
	KeyPlatformDBAppUsername = "platform-db-app-username"
	KeyPlatformDBAppPassword = "platform-db-app-password"
	KeyInfisicalDBUsername   = "infisical-db-username"
	KeyInfisicalDBPassword   = "infisical-db-password"
	KeyGHCRPullSecret        = "ghcr-pull-secret"
	KeyGitHubUsername        = "github-username"
	KeyGitHubToken           = "github-token"

	// Application Secret Keys (Operator uploads directly to Infisical)
	KeyControlPlaneDBUsername      = "hub-control-plane-db-username"
	KeyControlPlaneDBPassword      = "hub-control-plane-db-password"
	KeyHubCentralizedDBUsername    = "hub-centralized-db-username"
	KeyHubCentralizedDBPassword    = "hub-centralized-db-password"
	KeyHydraDBUsername             = "hub-hydra-db-username"
	KeyHydraDBPassword             = "hub-hydra-db-password"
	KeyKratosDBUsername            = "hub-kratos-db-username"
	KeyKratosDBPassword            = "hub-kratos-db-password"
	KeyKetoDBUsername              = "hub-keto-db-username"
	KeyKetoDBPassword              = "hub-keto-db-password"
	KeyHydraSystemSecret           = "hub-hydra-system-secret"
	KeyNATSLeafUsername            = "nats-leaf-username"
	KeyNATSLeafPassword            = "nats-leaf-password"
	KeyOpenmeterPostgreSQLUsername = "openmeter-postgresql-username"
	KeyOpenmeterPostgreSQLPassword = "openmeter-postgresql-password"
	KeyClickhouseAdminPassword     = "platform-clickhouse-admin-password"

	// Labels
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	ValueManagedBy = "hub-operator"
	ValueBootstrap = "infisical-bootstrap"
)

// Admin Credentials — overridable via environment variables.
// The .env file at the project root is sourced by hub-bootstrap.sh.
var (
	AdminEmail    = envOrDefault("INFISICAL_ADMIN_EMAIL", "arun4infra@gmail.com")
	AdminPassword = envOrDefault("INFISICAL_ADMIN_PASSWORD", "Password@123")
)

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
