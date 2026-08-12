package bootstrap

import (
	"context"

	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/cluster"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
)

// HybridDriver implements CloudDriver for the hybrid provider cell
// (Hetzner control plane + home-lab WSL2 workers over Tailscale).
// The Hetzner driver handles the cloud control-plane and burst pool;
// hybrid-specific behavior (Tailscale-only networking, home worker
// integration) is layered on top here.
//
// ADR-046: The hybrid cell is a platform/provider cell, not a new CAPI
// infrastructure provider. Hetzner CAPI remains the infrastructure provider.
type HybridDriver struct {
	Driver *HetznerDriver
}

// ── CloudDriver interface ──────────────────────────────────────────────────

func (d *HybridDriver) Name() string   { return "hybrid" }
func (d *HybridDriver) OSType() string { return d.Driver.OSType() }

// ── Phase 1: Preflight ──────────────────────────────────────────────────────

func (d *HybridDriver) PreflightValidators() []preflight.Validator {
	return d.Driver.PreflightValidators()
}

// ── Phase 3: Day-0 Infrastructure ───────────────────────────────────────────

func (d *HybridDriver) ProvisionDayZero(ctx context.Context, kubeconfig string) error {
	return d.Driver.ProvisionDayZero(ctx, kubeconfig)
}

// ── Phase 4: CAPI Init ──────────────────────────────────────────────────────

func (d *HybridDriver) CAPIProviders() []capi.CAPIProvider {
	return d.Driver.CAPIProviders()
}

func (d *HybridDriver) OnCAPIInit(ctx context.Context, kubeconfig, kubeContext, namespace string) error {
	return d.Driver.OnCAPIInit(ctx, kubeconfig, kubeContext, namespace)
}

// ── Phase 5: Cluster Provisioning Config ────────────────────────────────────

func (d *HybridDriver) PopulateClusterConfig(cfg *cluster.Config) {
	d.Driver.PopulateClusterConfig(cfg)
}

// ── Phase 9: ClusterClass paths ────────────────────────────────────────────

func (d *HybridDriver) ClusterClassPaths() []string {
	return d.Driver.ClusterClassPaths()
}

// ── Capabilities ────────────────────────────────────────────────────────────

func (d *HybridDriver) OperatorWebhookPatterns() []string {
	return d.Driver.OperatorWebhookPatterns()
}

func (d *HybridDriver) Capabilities() CapabilityContract {
	return d.Driver.Capabilities()
}
