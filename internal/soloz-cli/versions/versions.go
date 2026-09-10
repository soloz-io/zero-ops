package versions

const (
	// CAPI Operator
	CAPIOperatorVersion = "v0.13.0"

	// CAPI Core
	CAPIVersion = "v1.10.0"

	// Bootstrap & Control Plane Providers
	KubeadmBootstrapProviderVersion    = "v1.10.0"
	KubeadmControlPlaneProviderVersion = "v1.10.0"
	TalosBootstrapProviderVersion      = "v0.7.0"
	TalosControlPlaneProviderVersion   = "v0.7.0"

	// Infrastructure Providers
	HetznerInfraProviderVersion = "v1.0.0-beta.47"

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
	// 2026-09-05.
	//
	// scripts/validate/argocd-version-parity.sh enforces the equality in CI.
	ArgoCDChartVersion = "7.8.0"
)
