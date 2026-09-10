package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/soloz-io/zero-ops/internal/soloz-cli/capi"
	"github.com/soloz-io/zero-ops/internal/soloz-cli/cluster"
	"github.com/soloz-io/zero-ops/internal/soloz-cli/preflight"
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
//   - CCM addon is read from hetzner/base/spoke-addons/ (same shared base).
type HybridDriver struct {
	// Driver provides Hetzner CAPI infra (CP + burst pool).
	Driver *HetznerDriver

	// TailnetName is the Tailscale tailnet (e.g. "example.ts.net").
	// Used to construct the spoke controlPlaneEndpoint MagicDNS name.
	TailnetName string

	// OnPremEnabled controls whether the home-worker join flow is
	// activated in the hub-operator (reconcileHomeWorkerJoin).
	OnPremEnabled bool

	// HomeWorkerTTL is the bootstrap-token TTL for home worker join
	// credentials (e.g. "24h"). Used by hub-operator token minting.
	HomeWorkerTTL string

	// ClusterName names the control plane on the tailnet (<name>-cp).
	ClusterName string
}

// ── CloudDriver interface ──────────────────────────────────────────────────

func (d *HybridDriver) Name() string { return "hybrid" }

// CiliumAddonPath is this cell's own artifact. It carries the standalone
// cilium-envoy DaemonSet (ADR-046 addendum 10) and the hostNetwork mangle guard
// (addenda 8 and 27), which its config base requires and the shared template does
// not ship.
func (d *HybridDriver) CiliumAddonPath() string {
	return "manifests/providers/hybrid/k8s/cilium-addon-hybrid.yaml"
}

// OnPremRequested reports whether this cell expects home-lab worker nodes.
// The orchestrator's on-prem-join phase keys off this: only a hybrid cell that
// asked for home workers has one to wait for. Hetzner cells do not implement it.
func (d *HybridDriver) OnPremRequested() bool { return d.OnPremEnabled }
func (d *HybridDriver) OSType() string        { return d.Driver.OSType() }

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
	if err := d.Driver.OnCAPIInit(ctx, kubeconfig, kubeContext, namespace); err != nil {
		return err
	}

	// ADR-046 invariant 6. Cilium derives its VXLAN tunnel endpoint from the node's
	// InternalIP, and a home-lab worker's only InternalIP is its tailnet address.
	// A home node cannot route the hub CP's Hetzner private IP, so unless the CP
	// also advertises a tailnet address, cross-node pod traffic dies in one
	// direction and everything that talks pod-to-pod across the boundary fails.
	//
	// The ClusterClass brings tailscaled up in preKubeadmCommands and re-asserts
	// kubelet --node-ip from it, but it reads the credentials from this Secret —
	// which must therefore exist in the BOOTSTRAP cluster before the hub Cluster is
	// created. That is why this runs at capi-init rather than post-bootstrap.
	if !d.OnPremEnabled {
		fmt.Println("[capi-init] Home workers disabled — leaving tailscale credentials empty")
		return nil
	}

	authkey, err := readTailscaleAuthkey()
	if err != nil {
		// Not fatal: the cluster still builds, but home workers will not be able to
		// exchange pod traffic with it. Say so loudly rather than failing later with
		// an unexplained timeout.
		fmt.Printf("[capi-init] ⚠️  %v\n", err)
		fmt.Println("[capi-init] ⚠️  Control plane will NOT join the tailnet; cross-node pod traffic to")
		fmt.Println("[capi-init]     home workers will fail (ADR-046 invariant 6).")
		return nil
	}

	hostname := d.hubTailnetHostname()
	if err := writeTailscaleSecret(ctx, kubeconfig, namespace, authkey, hostname); err != nil {
		return fmt.Errorf("failed to write tailscale credentials: %w", err)
	}
	fmt.Printf("[capi-init] ✓ Tailscale credentials staged for control plane (%s)\n", hostname)
	return nil
}

// hubTailnetHostname is the name the control plane registers on the tailnet.
func (d *HybridDriver) hubTailnetHostname() string {
	if d.ClusterName != "" {
		return d.ClusterName + "-cp"
	}
	return "hub-cp"
}

// readTailscaleAuthkey loads the tailnet auth key.
//
// The environment comes first, the on-disk path second. That order is the whole
// point: the file is `k8-secrets/`, which is the platform operator's own
// gitignored directory on their own laptop. Day-0 now runs in the tenant's CI
// under the tenant's secrets (ADR-072), where that directory does not exist and
// never will -- so a tenant bootstrapping a hybrid box got the warning below,
// built a control plane that never joined the tailnet, and discovered it when
// pod traffic to their home workers died in one direction (ADR-046 invariant 6).
//
// The file is retained because the home-worker provisioning script reads the same
// path, so an operator running both halves from one machine still enrols both
// sides of the tailnet from one credential.
func readTailscaleAuthkey() (string, error) {
	if key := strings.TrimSpace(os.Getenv("TS_AUTHKEY")); key != "" {
		return key, nil
	}
	if key := strings.TrimSpace(os.Getenv("TAILSCALE_AUTHKEY")); key != "" {
		return key, nil
	}
	const path = "k8-secrets/tailscale/authkey"
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("no tailnet auth key: TS_AUTHKEY is unset and %s is not readable: %w", path, err)
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return "", fmt.Errorf("tailscale authkey at %s is empty", path)
	}
	return key, nil
}

func (d *HybridDriver) OnPlatformPreReqs(ctx context.Context, kubeconfig string) error {
	return d.Driver.OnPlatformPreReqs(ctx, kubeconfig)
}

// ── Phase 5: Cluster Provisioning Config ────────────────────────────────────
// Hybrid differs from Hetzner in two ways:
//  1. CCM addon is read from hetzner/base/ (same path as hetzner after WS1).
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
	// With --on-prem the home-lab nodes are that something: the
	// on-prem-join phase brings one up before boundary-01, and the control
	// plane keeps its taint exactly as ADR-046 §11 / ADR-014 require ("every
	// stateful workload runs on worker nodes only, control-plane nodes keep
	// control-plane:NoSchedule"). Untainting it here would re-create the failure
	// §11 was written to ban — CNPG landing on the control plane because storage
	// happened to bind there.
	//
	// Without home workers there is no other node at all, so the control plane has
	// to carry the platform and is registered without the taint.
	cfg.WorkerReplicas = 0
	cfg.ControlPlaneSchedulable = !d.OnPremEnabled

	// The cilium operator declares hostPorts, so its two replicas cannot share one
	// node; on a single-node hub the second would sit Pending forever and register
	// as a permanently unhealthy pod. Scaled here rather than in the shared addon
	// manifest, which the multi-node Hetzner hub also consumes.
	cfg.CiliumOperatorReplicas = 1

	// Re-read CCM from hetzner/base/ — driver_hetzner already reads from hetzner/base/
	// after WS1 fix, so this is a no-op path correction guard. Explicit for clarity.
	ccmRaw, err := readTemplateManifest(
		"manifests/providers/hetzner/base/spoke-addons/", "ccm-addon-template.yaml", "ccm.yaml")
	if err != nil {
		fmt.Printf("[cluster-provision] Warning: failed to read CCM manifest (hybrid): %v\n", err)
	} else {
		cfg.CCMManifest = string(ccmRaw)
	}

	// Wire home-worker config into cluster.Config (read by hub-operator: WS4).
	cfg.HomeWorker = cluster.HomeWorkerConfig{
		Enabled:     d.OnPremEnabled,
		TTL:         d.HomeWorkerTTL,
		TailnetName: d.TailnetName,
	}
}

// ── Phase 9: ClusterClass paths ────────────────────────────────────────────
// For the hybrid hub bootstrap, no extra hub ClusterClass is needed.
// The spoke ClusterClass (spokepool-v1) lives in hetzner/base/ and is deployed
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
