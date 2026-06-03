package bootstrap

import (
	"context"

	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
)

// ──────────────────────────────────────────────────────────────────────────────
// Provider — uniform 12-phase bootstrap pipeline (ADR-036 §1)
// ──────────────────────────────────────────────────────────────────────────────
//
// Every provider implements the same interface. The orchestrator calls methods
// in a fixed linear sequence with zero branching on provider type.
//
//  Phase   Method                        Local              Cloud
//  ─────   ──────                        ─────              ─────
//  1       PreflightValidators()         docker+kind        +cloud credentials
//  2       KindConfigPath()              kind-config.yaml   "" (default)
//  3       ProvisionDayZero()            SC+labels+taints   secret+CSI+wait
//  4       CAPIProviders()+OnCAPIInit()  CAPD providers     cloud providers
//  5       ProvisionManagementCluster()  identity (no-op)   provision VMs
//  6       PivotMove()                   identity (no-op)   clusterctl pivot
//  7       PivotReady()                  no-op              wait reconcile
//  8       (orchestrator cleanup)        skip               delete kind
//  9       ClusterClassPaths()           capd-spoke-pool    cloud clusterclass
//  10      OnPlatformPreReqs()           no-op              no-op (reserved)
//  11      (orchestrator ArgoCD+apps)    identical          identical
//  12      Finalize()                    copy kind cfg      extract CAPI secret
//
// The orchestrator is the single source of truth for phases 8 and 11 — these
// are not provider methods because they are identical across all providers.
type Provider interface {
	// ── Identity ──────────────────────────────────────────────────────────
	Name() string
	Capabilities() CapabilityContract

	// IsLocal returns true for self-hosting providers where the bootstrap
	// cluster IS the management cluster (CAPD/kind). Cloud providers return
	// false because they provision separate management VMs and pivot into them.
	IsLocal() bool

	// ── Phase 1: Preflight ────────────────────────────────────────────────
	PreflightValidators() []preflight.Validator

	// ── Phase 2: Bootstrap Cluster (kind) ─────────────────────────────────
	// KindConfigPath returns a kind config YAML path, or "" for defaults.
	KindConfigPath() string

	// ── Phase 3: Day-0 Infrastructure ─────────────────────────────────────
	// Applied BEFORE CAPI and any workload scheduling. This is the first
	// opportunity to configure the cluster for provider-specific primitives.
	//
	// Local:   applies StorageClass (rancher.io/local-path → hcloud-volumes),
	//          labels the control-plane node as worker, removes NoSchedule
	//          taint so hub workloads (Redis, ClickHouse, etc.) can schedule.
	// Cloud:   creates cloud credentials secret, installs CSI driver, waits
	//          for StorageClass to be dynamically provisioned.
	ProvisionDayZero(ctx context.Context, kubeconfig string) error

	// ── Phase 4: CAPI Initialization ──────────────────────────────────────
	CAPIProviders() []capi.CAPIProvider
	OnCAPIInit(ctx context.Context, kubeconfig, kubeContext, namespace string) error

	// ── Phase 5: Management Cluster Provisioning ──────────────────────────
	// Local is a no-op. Cloud provisions VMs via CAPI and waits for ready.
	ProvisionManagementCluster(ctx context.Context, cfg *ProvisionConfig) error

	// ── Phase 6: Pivot Move ───────────────────────────────────────────────
	// Returns the management cluster kubeconfig path.
	// Local returns the bootstrap kubeconfig (identity — no pivot needed).
	// Cloud retrieves the CAPI kubeconfig, installs the operator on the
	// management cluster, and executes clusterctl move.
	PivotMove(ctx context.Context, cfg *PivotConfig) (string, error)

	// ── Phase 7: Pivot Ready ──────────────────────────────────────────────
	// Cloud waits for CAPI reconciliation after pivot. Local no-ops.
	PivotReady(ctx context.Context, mgmtKubeconfig string) error

	// ── Phase 9: ClusterClass ─────────────────────────────────────────────
	ClusterClassPaths() []string

	// ── Phase 10: Platform Pre-Requisites ─────────────────────────────────
	// Final provider-specific setup before ArgoCD bootstrap apps are applied.
	// Reserved for future use (currently no-op for all providers).
	OnPlatformPreReqs(ctx context.Context, kubeconfig string) error

	// ── Phase 12: Finalize ────────────────────────────────────────────────
	// Persists the management kubeconfig and returns its path.
	// Cloud extracts from CAPI secret; local copies the kind kubeconfig.
	Finalize(ctx context.Context, cfg *FinalizeConfig) (string, error)
}

// ──────────────────────────────────────────────────────────────────────────────
// Config types passed between orchestrator and provider phases
// ──────────────────────────────────────────────────────────────────────────────

// ProvisionConfig carries state from the orchestrator into Phase 5.
type ProvisionConfig struct {
	ClusterName      string
	BootstrapKubeconfig string
	BootstrapContext string
	Debug            bool
}

// PivotConfig carries state from the orchestrator into Phase 6.
type PivotConfig struct {
	ClusterName      string
	BootstrapKubeconfig string
	BootstrapContext string
	Debug            bool
}

// FinalizeConfig carries state from the orchestrator into Phase 12.
type FinalizeConfig struct {
	MgmtKubeconfig  string
	ClusterName     string
	Namespace       string
	MergeKubeconfig bool
	Debug           bool
}

// ──────────────────────────────────────────────────────────────────────────────
// Capability Contract (ADR-036 §5)
// ──────────────────────────────────────────────────────────────────────────────

type CapabilityContract struct {
	Version      string
	StorageClass string
	BlockStorage bool
	LoadBalancer bool
	GPU          bool
	MaxNodes     int
}
