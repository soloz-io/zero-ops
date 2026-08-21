package bootstrap

import (
	"context"
	"fmt"

	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/cluster"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
)

// HybridDriver implements CloudDriver for the hybrid provider cell
// (Hetzner control plane + home-lab Flatcar workers over Tailscale).
// The Hetzner driver handles the cloud control-plane and burst pool;
// hybrid-specific behavior (Tailscale-only networking, home worker
// integration) is layered on top here.
//
// ADR-046: The hybrid cell is a platform/provider cell, not a new CAPI
// infrastructure provider. Hetzner CAPI remains the infrastructure provider.
// Key differences from the hetzner driver:
//   - LoadBalancer is disabled (controlPlaneLoadBalancer.enabled = false);
//     the spoke API endpoint is a Tailscale MagicDNS name.
//   - ClusterClassPaths returns nothing — hybrid hub bootstrap doesn't
//     install a separate hub ClusterClass; the spoke ClusterClass lives in
//     the Crossplane composition (manifests/providers/hybrid/).
//   - CCM addon is read from _shared/spoke-addons/ (same shared base).
type HybridDriver struct {
	// Driver provides Hetzner CAPI infra (CP + burst pool).
	Driver *HetznerDriver

	// TailnetName is the Tailscale tailnet (e.g. "example.ts.net").
	// Used to construct the spoke controlPlaneEndpoint MagicDNS name.
	TailnetName string

	// HomeWorkerEnabled controls whether the home-worker join flow is
	// activated in the hub-operator (reconcileHomeWorkerJoin).
	HomeWorkerEnabled bool

	// HomeWorkerTTL is the bootstrap-token TTL for home worker join
	// credentials (e.g. "24h"). Used by hub-operator token minting.
	HomeWorkerTTL string
}

// ── CloudDriver interface ──────────────────────────────────────────────────

func (d *HybridDriver) Name() string   { return "hybrid" }

// HomeWorkersRequested reports whether this cell expects home-lab worker nodes.
// The orchestrator's home-worker-join phase keys off this: only a hybrid cell that
// asked for home workers has one to wait for. Hetzner cells do not implement it.
func (d *HybridDriver) HomeWorkersRequested() bool { return d.HomeWorkerEnabled }
func (d *HybridDriver) OSType() string { return d.Driver.OSType() }

// ── Phase 1: Preflight ──────────────────────────────────────────────────────
// Reuse Hetzner preflight (token, region, SSH key). Tailscale connectivity
// is checked by the Flatcar worker provisioning scripts, not here.

func (d *HybridDriver) PreflightValidators() []preflight.Validator {
	return d.Driver.PreflightValidators()
}

// ── Phase 3: Day-0 Infrastructure ───────────────────────────────────────────
// Hybrid Day-0 = Hetzner Day-0 (Hetzner secret + CSI for the Hub).
// Home-worker join-config Secret is written by hub-operator after CAPI
// provisioning (WS4: reconcileHomeWorkerJoin), not here.

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

func (d *HybridDriver) OnPlatformPreReqs(ctx context.Context, kubeconfig string) error {
	return d.Driver.OnPlatformPreReqs(ctx, kubeconfig)
}

// ── Phase 5: Cluster Provisioning Config ────────────────────────────────────
// Hybrid differs from Hetzner in two ways:
//  1. CCM addon is read from _shared/ (same path as hetzner after WS1).
//  2. Hub mgmt cluster mirrors hetzner (2 workers) — the burst-pool replicas:0
//     for SPOKES lives in the hybrid Crossplane composition, not here.

func (d *HybridDriver) PopulateClusterConfig(cfg *cluster.Config) {
	// Start from Hetzner defaults (region, OS, image, machine types, K8s version).
	d.Driver.PopulateClusterConfig(cfg)

	// ADR-046: the hybrid cell exists so that worker capacity comes from home-lab
	// hardware — "Costs nothing to keep running (no idle Hetzner worker nodes)".
	// The Hetzner driver provisions 2 cx33 workers for the hub, which is right for
	// a pure-Hetzner hub but is exactly the idle capacity this provider avoids.
	//
	// The hub's workers are the Flatcar home-lab nodes: provision-flatcar-worker.sh
	// joins them with hub-role=worker + node-role.kubernetes.io/worker +
	// workload-location=home when its target cluster is "hub" (see home-lab.env,
	// where flatcar-hub-node-1 is registered against the hub).
	//
	// Overriding here rather than in HetznerDriver keeps the tested pure-Hetzner
	// path (WorkerReplicas = 2) untouched — this applies only when --provider=hybrid.
	//
	// Keyed on the PROVIDER, not the environment: choosing --provider=hybrid is
	// itself the statement that this cell's worker capacity comes from home-lab
	// hardware. It therefore applies to every hybrid environment, and topology is
	// not consulted — hybrid's own shape is the default when none is passed.
	// --provider=hetzner is untouched and keeps 2 Hetzner workers.
	//
	// Zero Hetzner workers means SOMETHING else has to be able to run the platform,
	// or every workload sits Pending and the bootstrap hangs.
	//
	// With --home-worker-enabled the home-lab nodes are that something: the
	// home-worker-join phase brings one up before boundary-01, and the control
	// plane keeps its taint exactly as ADR-046 §11 / ADR-014 require ("every
	// stateful workload runs on worker nodes only, control-plane nodes keep
	// control-plane:NoSchedule"). Untainting it here would re-create the failure
	// §11 was written to ban — CNPG landing on the control plane because storage
	// happened to bind there.
	//
	// Without home workers there is no other node at all, so the control plane has
	// to carry the platform and is registered without the taint.
	cfg.WorkerReplicas = 0
	cfg.ControlPlaneSchedulable = !d.HomeWorkerEnabled

	// The cilium operator declares hostPorts, so its two replicas cannot share one
	// node; on a single-node hub the second would sit Pending forever and register
	// as a permanently unhealthy pod. Scaled here rather than in the shared addon
	// manifest, which the multi-node Hetzner hub also consumes.
	cfg.CiliumOperatorReplicas = 1

	// Re-read CCM from _shared/ — driver_hetzner already reads from _shared/
	// after WS1 fix, so this is a no-op path correction guard. Explicit for clarity.
	ccmRaw, err := readTemplateManifest(
		"manifests/providers/_shared/spoke-addons/", "ccm-addon-template.yaml", "ccm.yaml")
	if err != nil {
		fmt.Printf("[cluster-provision] Warning: failed to read CCM manifest (hybrid): %v\n", err)
	} else {
		cfg.CCMManifest = string(ccmRaw)
	}

	// Wire home-worker config into cluster.Config (read by hub-operator: WS4).
	cfg.HomeWorker = cluster.HomeWorkerConfig{
		Enabled:     d.HomeWorkerEnabled,
		TTL:         d.HomeWorkerTTL,
		TailnetName: d.TailnetName,
	}
}

// ── Phase 9: ClusterClass paths ────────────────────────────────────────────
// For the hybrid hub bootstrap, no extra hub ClusterClass is needed.
// The spoke ClusterClass (spokepool-v1) lives in _shared/ and is deployed
// by the Crossplane composition (manifests/providers/hybrid/). Return empty
// so the orchestrator skips the ClusterClass apply step for hybrid.

func (d *HybridDriver) ClusterClassPaths() []string {
	return []string{}
}

// ── Capabilities ────────────────────────────────────────────────────────────
// Hybrid: the spoke API IS fronted by a cloud LoadBalancer — the CAPH-managed
// Hetzner LB (controlPlaneLoadBalancer.enabled=true, ADR-046 "Spoke API
// Endpoint"). The composition sets controlPlaneLoadBalancerEnabled: true and
// the same LB now also carries tenant ingress on 80/443 via extraServices.
// The earlier "Tailscale-only" comment described a pre-ADR-046 design that the
// spoke-api-front removal (addendum 5) already retired.
// StorageClass comes from Hetzner CSI (same as hetzner provider).

func (d *HybridDriver) OperatorWebhookPatterns() []string {
	return d.Driver.OperatorWebhookPatterns()
}

func (d *HybridDriver) Capabilities() CapabilityContract {
	base := d.Driver.Capabilities()
	// CAPH provisions the spoke control-plane LB (and, via extraServices, the
	// tenant ingress ports). Keep the inherited LoadBalancer capability.
	return base
}
