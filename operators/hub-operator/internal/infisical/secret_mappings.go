package infisical

// ============================================================================
// SECRET MAPPINGS REGISTRY
// ============================================================================
// This file is the SINGLE SOURCE OF TRUTH for all CLI secrets uploaded to Infisical.
//
// ARCHITECTURE FLOW:
// 1. CLI/Bootstrap creates secrets in K8s (via ClusterResourceSet or manual deployment)
// 2. Operator uploads them to Infisical (making Infisical the Source of Truth)
// 3. ExternalSecrets (ESO) syncs them back to target namespaces
// 4. Applications consume ESO-managed secrets
//
// HOW TO ADD A NEW SECRET:
// 1. Add entry to CLISecretMappings below
// 2. Create corresponding ExternalSecret manifest in manifests/
// 3. Operator will automatically upload on next reconciliation
// ============================================================================

// SecretMapping defines how to map a K8s secret to Infisical
type SecretMapping struct {
	SourceNamespace string // K8s namespace where secret exists
	SourceName      string // K8s secret name
	SourceKey       string // Key within the K8s secret
	InfisicalKey    string // Key name in Infisical
	Description     string // Human-readable description for logging
}

// CLISecretMappings is the registry of all CLI secrets to upload to Infisical
//
// ┌─────────────────────────────────────────────────────────────────────────┐
// │ CURRENT SECRETS BEING UPLOADED                                          │
// ├─────────────────────────────────────────────────────────────────────────┤
// │ 1. hetzner-dns          → Used by external-dns, cert-manager           │
// │ 2. ghcr-dockerconfigjson → Used by mcp-server (private image pulls)    │
// └─────────────────────────────────────────────────────────────────────────┘
var CLISecretMappings = []SecretMapping{
	// ========================================================================
	// 1. HETZNER DNS API TOKEN
	// ========================================================================
	// Purpose: DNS management for external-dns and cert-manager ACME DNS-01
	// Source: hcloud secret (deployed by CLI via ClusterResourceSet)
	// Consumers: external-dns, cert-manager-webhook-hetzner
	// ExternalSecret: manifests/platform-external-dns/hetzner-dns-externalsecret.yaml
	{
		SourceNamespace: NamespaceCloudSystem,      // hub-cloud-system
		SourceName:      SecretHCloud,              // hcloud
		SourceKey:       KeyToken,                  // token
		InfisicalKey:    "hetzner-dns",             // hetzner-dns
		Description:     "Hetzner DNS API token (from hcloud)",
	},

	// ========================================================================
	// 2. GITHUB CONTAINER REGISTRY PULL SECRET
	// ========================================================================
	// Purpose: Pull private images from ghcr.io
	// Source: ghcr-pull-secret (deployed by CLI/manually in hub-platform-ops)
	// Consumers: mcp-server, other private image deployments
	// ExternalSecret: manifests/api-gateway/mcp-server/ghcr-pull-secret-externalsecret.yaml
	{
		SourceNamespace: NamespaceOps,              // hub-platform-ops
		SourceName:      "ghcr-pull-secret",        // ghcr-pull-secret
		SourceKey:       ".dockerconfigjson",       // .dockerconfigjson
		InfisicalKey:    "ghcr-dockerconfigjson",   // ghcr-dockerconfigjson
		Description:     "GitHub Container Registry pull secret",
	},

	// ========================================================================
	// ADD NEW SECRETS BELOW THIS LINE
	// ========================================================================
	// Template:
	// {
	//     SourceNamespace: "namespace-where-secret-exists",
	//     SourceName:      "k8s-secret-name",
	//     SourceKey:       "key-in-secret-data",
	//     InfisicalKey:    "key-in-infisical",
	//     Description:     "Human readable description",
	// },
}
