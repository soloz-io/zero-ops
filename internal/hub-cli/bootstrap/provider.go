package bootstrap

import (
	"context"

	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
)

// Provider defines the contract all infrastructure providers must fulfill.
//
// ADR-036 §1 (Hub CLI Strategy Pattern):
//
//	The orchestrator calls this interface in sequence — it never branches on
//	provider name. Adding a provider means implementing this interface and
//	registering it in cmd/hub/bootstrap.go.
//
// ADR-036 §2 (Strict Provider Isolation):
//
//	The orchestrator imports only this package. No provider-specific package
//	is imported into core orchestration logic. This boundary is enforceable at
//	code review.
//
// ADR-036 §5 (Capability Contract):
//
//	Capabilities() declares what the provider supports. Used by admission and
//	validation workflows to prevent silent cross-cloud failures.
//
// ADR-036 §6 (Provider Kustomize Components):
//
//	DayZeroInfra() returns declarative manifest paths owned by the provider
//	(under manifests/providers/<name>/). The orchestrator applies them
//	imperatively before ArgoCD takes over.
type Provider interface {
	// --- Identity ---

	// Name returns the provider name ("docker", "hetzner").
	Name() string

	// --- Bootstrap Configuration (ADR-036 §3) ---

	// PreflightValidators returns validators run before any infrastructure
	// is created. Each validator checks a required tool or environment condition.
	PreflightValidators() []preflight.Validator

	// KindConfigPath returns the path to a Kind cluster configuration file,
	// or "" if no custom config is needed.
	KindConfigPath() string

	// IsSelfProvisioning reports whether the kind bootstrap cluster IS the
	// management cluster. True for CAPD/Docker; false for cloud providers
	// where a separate management cluster is provisioned and pivoted into.
	IsSelfProvisioning() bool

	// --- CAPI Infrastructure (ADR-036 §1) ---

	// CAPIProviders returns the Cluster API infrastructure providers to
	// install during operator initialization.
	CAPIProviders() []capi.CAPIProvider

	// OnCAPIInit is called after CAPI operator installation completes.
	// Providers use this to create credentials secrets, or perform any
	// provider-specific initialization before cluster provisioning.
	OnCAPIInit(ctx context.Context, kubeconfig, kubeContext, namespace string) error

	// --- ClusterClass Templates (ADR-036 §4) ---

	// ClusterClassPaths returns paths to ClusterClass YAML manifests for
	// spoke pool provisioning.
	ClusterClassPaths() []string

	// --- Day-Zero Infrastructure (ADR-036 §6) ---

	// DayZeroInfra returns declarative manifest paths for provider-owned
	// infrastructure that must exist BEFORE ArgoCD bootstrap boundaries are
	// applied. The orchestrator applies each manifest via kubectl apply -f.
	//
	// These are infrastructure primitives the provider needs (StorageClass,
	// CSI drivers) that ArgoCD cannot self-provision. Manifest paths are
	// relative to the project root and live under manifests/providers/<name>/.
	DayZeroInfra() []InfraManifest

	// OnDayZeroInit is called after DayZeroInfra manifests have been applied.
	// Providers use this for imperative work that depends on runtime data
	// (e.g., creating secrets from CLI-provided tokens, waiting for CSI pods).
	OnDayZeroInit(ctx context.Context, kubeconfig string) error

	// --- Capability Contract (ADR-036 §5) ---

	// Capabilities returns the provider's declared platform capabilities.
	// Versioned and backwards-compatible; used by admission and validation
	// workflows to reject tenant requests for unsupported features.
	Capabilities() CapabilityContract
}

// InfraManifest describes a single provider-owned Day-0 Kubernetes manifest
// applied by the orchestrator before ArgoCD bootstrap boundaries.
//
// ADR-036 §6: cloud-specific manifests are isolated into provider-owned
// directories (manifests/providers/<name>/) and injected dynamically.
type InfraManifest struct {
	// Name is a human-readable label for log output
	// (e.g., "local-path StorageClass (hcloud-volumes)").
	Name string

	// Path is the manifest file path relative to the project root
	// (e.g., "manifests/providers/local/storage-class.yaml").
	Path string
}

// CapabilityContract declares the platform capabilities a provider supports.
//
// ADR-036 §5: versioned and backwards-compatible across provider releases.
// Used by admission webhooks and validation logic to reject tenant requests
// for unsupported features (e.g., GPUs on a provider that doesn't offer them,
// LoadBalancer services on local CAPD).
//
// Zero-value fields indicate the capability is not supported.
type CapabilityContract struct {
	// Version is the semantic version of this capability contract.
	// Incremented when capabilities are added or removed.
	Version string

	// StorageClass is the name of the StorageClass this provider provisions
	// for PersistentVolumeClaims (e.g., "hcloud-volumes").
	StorageClass string

	// BlockStorage indicates support for PVC-based persistent block storage.
	BlockStorage bool

	// LoadBalancer indicates support for LoadBalancer Service types.
	LoadBalancer bool

	// GPU indicates support for GPU-accelerated instances.
	GPU bool

	// MaxNodes is the maximum number of worker nodes per spoke cluster.
	// 0 means no provider-enforced limit.
	MaxNodes int
}
