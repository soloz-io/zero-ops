package versions

const (
	// CAPI Operator
	CAPIOperatorVersion = "v0.13.0"

	// CAPI Core
	CAPIVersion = "v1.10.0"

	// Bootstrap & Control Plane Providers
	//
	// Authoritative for the versions deployed by the CAPI operator manifests in
	// internal/assets/manifests/core/capi-operator/providers/.
	// Divergence between these constants and the provider manifests is caught by
	// TestProviderManifestVersionParity.
	KubeadmBootstrapProviderVersion    = "v1.8.5"
	KubeadmControlPlaneProviderVersion = "v1.8.5"
	TalosBootstrapProviderVersion      = "v0.6.5"
	TalosControlPlaneProviderVersion   = "v0.5.6"

	// Infrastructure Providers
	HetznerInfraProviderVersion = "v1.0.7"

	// Binaries
	ClusterctlVersion = "v1.10.0"
	TalosctlVersion   = "v1.12.0"
	PackerVersion     = "1.11.2"

	// Prerequisites
	CertManagerVersion = "v1.20.2"

	// ArgoCDChartVersion is the argo-proj/argo-cd chart the Day-0 seed installs.
	//
	// It MUST equal the targetRevision of the 'platform-argocd' element in
	// manifests/argocd/environment-manager/templates/01-platform-infra-appset.yaml.
	// That Application adopts the release this constant installs, at sync wave 1 --
	// so a mismatch makes ArgoCD upgrade itself while the rest of boundary 01 is
	// still syncing, restarting the application-controller and repo-server at the
	// worst possible moment. It was 7.7.12 here against 7.8.0 in Git until
	// 2026-09-05; 7.8.0 against 7.9.1 until 2026-09-20.
	//
	// scripts/validate/argocd-version-parity.sh enforces the equality in CI.
	ArgoCDChartVersion = "7.9.1"
)
