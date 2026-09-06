package bootstrap

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/internal/hub-cli/binaries"
	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/cluster"
	"github.com/soloz-io/zero-ops/internal/hub-cli/config"
	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
	"github.com/soloz-io/zero-ops/internal/hub-cli/health"
	"github.com/soloz-io/zero-ops/internal/hub-cli/pivot"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
)

// ──────────────────────────────────────────────────────────────────────────────
// CloudProvider — single implementation for ALL cloud providers (ADR-036 §1)
// ──────────────────────────────────────────────────────────────────────────────
//
// CloudProvider implements the full 12-phase Provider pipeline for any cloud
// (Hetzner, AWS, GCP, etc.). Cloud-specific details are delegated to a
// CloudDriver implementation. Adding a new cloud means implementing CloudDriver
// — the CloudProvider itself never changes.
//
// Phase mapping:
//   5 (provision):  creates VMs via CAPI, waits for cluster ready
//   6 (pivot):      retrieves mgmt kubeconfig, clusterctl move
//   7 (pivot-ready): waits for CAPI reconciliation post-pivot
//   12 (finalize):  extracts kubeconfig from CAPI secret

type CloudProvider struct {
	driver      CloudDriver
	clusterName string
	debug       bool
}

func NewCloudProvider(driver CloudDriver, clusterName string, debug bool) *CloudProvider {
	return &CloudProvider{
		driver:      driver,
		clusterName: clusterName,
		debug:       debug,
	}
}

// ── Identity ────────────────────────────────────────────────────────────────

func (p *CloudProvider) Name() string { return p.driver.Name() }
func (p *CloudProvider) KindConfigPath() string {
	// Cloud providers use default kind config (no custom config needed).
	return ""
}

// ── Phase 1: Preflight ──────────────────────────────────────────────────────

func (p *CloudProvider) PreflightValidators() []preflight.Validator {
	return p.driver.PreflightValidators()
}

// ── Phase 3: Day-0 Infrastructure ───────────────────────────────────────────

func (p *CloudProvider) ProvisionDayZero(ctx context.Context, kubeconfig string) error {
	return p.driver.ProvisionDayZero(ctx, kubeconfig)
}

// ── Phase 4: CAPI Init ──────────────────────────────────────────────────────

func (p *CloudProvider) CAPIProviders() []capi.CAPIProvider {
	return p.driver.CAPIProviders()
}

func (p *CloudProvider) OnCAPIInit(ctx context.Context, kubeconfig, kubeContext, namespace string) error {
	return p.driver.OnCAPIInit(ctx, kubeconfig, kubeContext, namespace)
}

// ── Phase 5: Management Cluster Provisioning ────────────────────────────────

func (p *CloudProvider) ProvisionManagementCluster(ctx context.Context, cfg *ProvisionConfig) error {
	fmt.Println("\n[cluster-provision] Provisioning Management Cluster...")

	// Build cluster.Config from driver (cloud-specific fields) + shared fields
	clusterCfg := &cluster.Config{}
	p.driver.PopulateClusterConfig(clusterCfg)

	// Fill shared fields known to CloudProvider
	clusterCfg.ClusterName = p.clusterName
	clusterCfg.Namespace = constants.NamespaceCAPI

	// Read the shared cilium addon manifest and recompose it with the config half.
	//
	// ADR-046 §24 split the cilium-config ConfigMap out of the addon Secret so that
	// exactly one component renders the copy a SPOKE receives (hub-operator, which
	// injects the spoke's control-plane endpoint). The HUB is not a spoke: it has no
	// SpokePool and no renderer, and it keeps kube-proxy, so the in-cluster ClusterIP
	// resolves and no endpoint injection is needed. It therefore installs the static
	// base verbatim, joined back onto the addon here.
	//
	// Reading the same base file both halves use is deliberate: a second copy of 160
	// cilium settings would drift, and drift in this file is a cluster that boots
	// with a datapath nobody intended.
	//
	// That reasoning applies to the WORKLOAD half too, and it did not used to. The
	// hub took its agent and operator from spoke-bootstrap/cilium-addon-template.yaml
	// while its settings came from the provider base, and the two disagreed: the base
	// declares external-envoy-proxy true (ADR-046 addendum 10 decoupled Envoy), but
	// that template ships no cilium-envoy at all. The hub therefore ran with an agent
	// that delegates L7 to an Envoy nobody deployed. Nothing surfaced until the hub
	// served a Gateway, at which point the agent could not push the listener
	// configuration anywhere ("failed to upsert envoy resources: context deadline
	// exceeded") and the Gateway reported Programmed with no dataplane behind it.
	//
	// The provider's rendered addon is the one artifact that carries the workloads and
	// the settings in agreement, including the pieces hostNetwork Gateway mode needs:
	// the standalone cilium-envoy DaemonSet (addendum 10) and the mangle guard that
	// keeps the host-bound listeners reachable (addendum 8, addendum 27). A hub that
	// binds :80/:443 through Envoy needs both for the same reasons a spoke does.
	ciliumAddonDir := filepath.Join("manifests", "providers", p.driver.Name(), "k8s") + string(filepath.Separator)
	ciliumAddonFile := fmt.Sprintf("cilium-addon-%s.yaml", p.driver.Name())
	ciliumRaw, err := readTemplateManifest(ciliumAddonDir, ciliumAddonFile, "cilium.yaml")
	if err != nil {
		return fmt.Errorf("failed to read cilium addon for provider %s: %w", p.driver.Name(), err)
	}
	//
	// Two things the base is NOT, and both were wrong here:
	//
	//   1. It is not provider-neutral. The path was hardcoded to hetzner, so a hybrid
	//      hub was built from the hetzner datapath: mtu 1230 instead of 1200, no
	//      `tailscale0` in `devices`, and gateway-api-hostnetwork-enabled false. A
	//      hybrid hub reaches its home workers over the tailnet and binds its Gateway
	//      on the control-plane host, so none of those are cosmetic.
	//
	//   2. It is not the delivered artifact. It is the RENDERER's input —
	//      ConfigMap/cilium-config-base-<provider> in platform-capi, read by
	//      hub-operator to render a spoke's copy. Cilium consumes
	//      ConfigMap/cilium-config in kube-system. Appending the file verbatim shipped
	//      a document targeting platform-capi, a namespace that does not exist on a
	//      workload cluster, so it never applied: cilium-operator hung in
	//      ContainerCreating on `configmap "cilium-config" not found`, the agent
	//      CrashLoopBackOff'd, and every node stayed NotReady with no CNI.
	//
	// Read the provider's own base and retarget it to what Cilium actually consumes.
	ciliumConfigPath := filepath.Join(
		"manifests", "providers", p.driver.Name(), "k8s", "cilium-config-base.yaml")
	ciliumConfig, err := os.ReadFile(ciliumConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read cilium-config base (ADR-046 §24) at %s: %w", ciliumConfigPath, err)
	}
	var ciliumCM map[string]interface{}
	if err := yaml.Unmarshal(ciliumConfig, &ciliumCM); err != nil {
		return fmt.Errorf("failed to parse cilium-config base at %s: %w", ciliumConfigPath, err)
	}
	ciliumMeta, ok := ciliumCM["metadata"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("cilium-config base at %s has no metadata block", ciliumConfigPath)
	}
	ciliumMeta["name"] = "cilium-config"
	ciliumMeta["namespace"] = constants.NamespaceKubeSystem
	// Renderer-selection label; it selects a base on the hub and means nothing here.
	delete(ciliumMeta, "labels")
	ciliumConfigOut, err := yaml.Marshal(ciliumCM)
	if err != nil {
		return fmt.Errorf("failed to render cilium-config for delivery: %w", err)
	}
	// Gateway API CRDs travel WITH Cilium, ahead of it in the document stream.
	//
	// Cilium here runs with enable-gateway-api=true, and its operator blocks until
	// these CRDs exist. Nothing on the cluster gets past that: the operator never
	// settles, the home-lab worker never goes Ready, and the worker is the hub's
	// only worker capacity (ADR-046 §19/§21). Delivering the CRDs through ArgoCD
	// is circular — ArgoCD needs the worker that needs the CRDs that ArgoCD would
	// install — which is why they belong here, in the same ClusterResourceSet
	// addon that already carries the CNI (ADR-041).
	//
	// Ordered first deliberately. Same addon, one document stream, so the CRDs
	// cannot be separated from the CNI that requires them by reordering a list.
	gatewayCRDs, err := assets.ReadManifest("addons/gateway-api-crds.yaml")
	if err != nil {
		return fmt.Errorf("failed to read the Gateway API CRDs delivered with Cilium: %w", err)
	}
	clusterCfg.CiliumManifest = string(gatewayCRDs) + "\n---\n" +
		string(ciliumRaw) + "\n---\n" + string(ciliumConfigOut)

	provisioner := &cluster.Provisioner{
		Kubeconfig: cfg.BootstrapKubeconfig,
		Context:    cfg.BootstrapContext,
		Config:     clusterCfg,
		Debug:      p.debug || cfg.Debug,
	}

	// Check if cluster already exists
	checkCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", cfg.BootstrapKubeconfig,
		"--context", cfg.BootstrapContext,
		// Fully qualified: CNPG registers a Cluster kind too, and the short name
		// resolves to it once cloudnative-pg is installed (health.CAPIClusterKind).
		"get", "clusters.cluster.x-k8s.io", p.clusterName,
		"-n", constants.NamespaceCAPI,
		"-o", "jsonpath={.status.phase}",
	)
	if output, err := checkCmd.Output(); err == nil && string(output) == "Provisioned" {
		fmt.Println("[cluster-provision] ✓ Cluster already exists and provisioned")
	} else {
		if err := provisioner.Provision(ctx); err != nil {
			return fmt.Errorf("failed to provision cluster: %w", err)
		}
		fmt.Println("[cluster-provision] ✓ Cluster resources and CRS applied")
	}

	fmt.Println("[cluster-provision] Waiting for cluster Ready (CRS installing CNI/CCM)...")
	if err := provisioner.WaitForReady(ctx); err != nil {
		return fmt.Errorf("cluster not ready: %w", err)
	}
	fmt.Println("[cluster-provision] ✓ Management Cluster ready")
	return nil
}

// ── Phase 6: Pivot Move ─────────────────────────────────────────────────────

func (p *CloudProvider) PivotMove(ctx context.Context, cfg *PivotConfig) (string, error) {
	fmt.Println("\n[pivot] Moving CAPI resources to Management Cluster...")

	// Refresh kind kubeconfig if using kind bootstrap
	if cfg.BootstrapContext != "" && strings.HasPrefix(cfg.BootstrapContext, "kind-") {
		kindClusterName := strings.TrimPrefix(cfg.BootstrapContext, "kind-")
		cmd := exec.CommandContext(ctx, "kind", "export", "kubeconfig", "--name", kindClusterName)
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("failed to export kind kubeconfig: %w\n%s", err, output)
		}
		if p.debug {
			fmt.Println("[DEBUG] Refreshed kubeconfig from kind")
		}
	}

	fmt.Println("[pivot] Waiting for all nodes to join cluster...")
	if err := waitForAllMachinesRunning(ctx, cfg.BootstrapKubeconfig, cfg.BootstrapContext, 10*time.Minute); err != nil {
		return "", fmt.Errorf("machines not ready for pivot: %w", err)
	}
	fmt.Println("[pivot] ✓ All nodes joined")

	pivotOrch := &pivot.Orchestrator{
		BootstrapKubeconfig: cfg.BootstrapKubeconfig,
		BootstrapContext:    cfg.BootstrapContext,
		ClusterName:         p.clusterName,
		Namespace:           constants.NamespaceCAPI,
		OSType:              p.driver.OSType(),
		Debug:               p.debug || cfg.Debug,
	}

	mgmtKubeconfig, err := pivotOrch.ExecuteMove(ctx)
	if err != nil {
		return "", fmt.Errorf("pivot move failed: %w", err)
	}
	fmt.Println("[pivot] ✓ Resources moved to Management Cluster")
	return mgmtKubeconfig, nil
}

// ── Phase 7: Pivot Ready ────────────────────────────────────────────────────

func (p *CloudProvider) PivotReady(ctx context.Context, mgmtKubeconfig string) error {
	fmt.Println("\n[pivot-ready] Waiting for cluster reconciliation after move...")

	pivotOrch := &pivot.Orchestrator{
		BootstrapKubeconfig: mgmtKubeconfig,
		ClusterName:         p.clusterName,
		Namespace:           constants.NamespaceCAPI,
		OSType:              p.driver.OSType(),
		Debug:               p.debug,
	}

	if err := pivotOrch.WaitForReady(ctx, mgmtKubeconfig); err != nil {
		return fmt.Errorf("pivot ready failed: %w", err)
	}
	fmt.Println("[pivot-ready] ✓ Cluster ready on Management Cluster")
	return nil
}

// ── Phase 9: ClusterClass ───────────────────────────────────────────────────

// HomeWorkersRequested forwards the driver's answer when it has one. Only the
// hybrid driver does; a Hetzner cell has no home workers and reports false.
func (p *CloudProvider) HomeWorkersRequested() bool {
	if hw, ok := p.driver.(interface{ HomeWorkersRequested() bool }); ok {
		return hw.HomeWorkersRequested()
	}
	return false
}

func (p *CloudProvider) ClusterClassPaths() []string {
	return p.driver.ClusterClassPaths()
}

// ── Phase 10: Platform Pre-Requisites ───────────────────────────────────────

func (p *CloudProvider) OnPlatformPreReqs(ctx context.Context, kubeconfig string) error {
	return p.driver.OnPlatformPreReqs(ctx, kubeconfig)
}

// ── Phase 12: Finalize ──────────────────────────────────────────────────────

func (p *CloudProvider) Finalize(ctx context.Context, cfg *FinalizeConfig) (string, error) {
	configMgr := &config.Manager{
		BootstrapKubeconfig: cfg.MgmtKubeconfig,
		ClusterName:         cfg.ClusterName,
		Namespace:           cfg.Namespace,
	}

	kubeconfigPath, err := configMgr.SaveKubeconfig(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to save kubeconfig: %w", err)
	}
	fmt.Printf("[finalize] ✓ Kubeconfig saved to %s\n", kubeconfigPath)

	if cfg.MergeKubeconfig {
		if err := configMgr.MergeKubeconfig(ctx, kubeconfigPath); err != nil {
			fmt.Printf("[finalize] Warning: failed to merge kubeconfig: %v\n", err)
		} else {
			fmt.Println("[finalize] ✓ Kubeconfig merged into ~/.kube/config")
		}
	}

	return kubeconfigPath, nil
}

// ── Capability Contract ─────────────────────────────────────────────────────

func (p *CloudProvider) OperatorWebhookPatterns() []string {
	return p.driver.OperatorWebhookPatterns()
}
func (p *CloudProvider) Capabilities() CapabilityContract {
	return p.driver.Capabilities()
}

// ──────────────────────────────────────────────────────────────────────────────
// CloudDriver — cloud-specific details (ADR-036 §1)
// ──────────────────────────────────────────────────────────────────────────────
//
// CloudDriver is the pluggable extension point for multi-cloud support.
// Implementing this interface is all that's required to add a new cloud
// (AWS, GCP, Azure, etc.). The CloudProvider delegates every cloud-specific
// decision to the driver, including CAPI provider lists, ClusterClass paths,
// CSI installation, credentials secrets, and provisioning config.
//
// Each driver is self-contained: it knows its own CCM manifest paths, machine
// types, region codes, and token sources. The CloudProvider never branches on
// cloud name.

type CloudDriver interface {
	// Name returns the cloud identifier ("hetzner", "aws", "gcp").
	Name() string

	// OSType returns the OS type for CAPI provisioning ("ubuntu" or "talos").
	OSType() string

	// ── Phase 1: Preflight validators (docker, kind, cloud credentials) ───
	PreflightValidators() []preflight.Validator

	// ── Phase 3: ProvisionDayZero (create cloud secret, install CSI) ──────
	ProvisionDayZero(ctx context.Context, kubeconfig string) error

	// ── Phase 4: CAPI providers + init hook ──────────────────────────────
	CAPIProviders() []capi.CAPIProvider
	OnCAPIInit(ctx context.Context, kubeconfig, kubeContext, namespace string) error

	// ── Phase 5: Populate provisioning config ────────────────────────────
	// PopulateClusterConfig fills cloud-specific fields on a cluster.Config.
	// The CloudProvider fills shared fields (ClusterName, Namespace,
	// CiliumManifest) after this call returns.
	PopulateClusterConfig(cfg *cluster.Config)

	// ── Phase 9: ClusterClass paths ──────────────────────────────────────
	ClusterClassPaths() []string

	// ── Operator webhook patterns ───────────────────────────────────────
	OperatorWebhookPatterns() []string

	// ── Phase 10: Platform pre-requisites (post-pivot, on the mgmt cluster) ──
	// Applied against the management (Hub) kubeconfig AFTER pivot. Day-0
	// resources (cloud secret, CSI) installed on the ephemeral bootstrap are
	// lost when it is deleted; anything the Hub itself needs (e.g. CSI +
	// StorageClass) must be re-applied here.
	OnPlatformPreReqs(ctx context.Context, kubeconfig string) error

	// ── Capabilities (ADR-036 §5) ────────────────────────────────────────
	Capabilities() CapabilityContract
}

// ──────────────────────────────────────────────────────────────────────────────
// Shared helpers for CloudProvider
// ──────────────────────────────────────────────────────────────────────────────

// readTemplateManifest reads a manifest template and extracts the content under
// the given data key (used for cilium/CCM addon templates).
func readTemplateManifest(basePath, templateFile, dataKey string) ([]byte, error) {
	biosPath := basePath + templateFile
	data, err := os.ReadFile(biosPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", biosPath, err)
	}

	var template map[string]interface{}
	if err := yaml.Unmarshal(data, &template); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", templateFile, err)
	}

	var manifestContent string
	if stringData, ok := template["stringData"].(map[string]interface{}); ok {
		if content, ok := stringData[dataKey].(string); ok {
			manifestContent = content
		}
	} else if dataMap, ok := template["data"].(map[string]interface{}); ok {
		if content, ok := dataMap[dataKey].(string); ok {
			manifestContent = content
		}
	}

	if manifestContent == "" {
		return nil, fmt.Errorf("manifest content not found in %s under key %s", templateFile, dataKey)
	}

	return []byte(manifestContent), nil
}

// waitForAllMachinesRunning polls CAPI machines until every machine has a
// nodeRef assigned (meaning the node has joined the cluster).
//
// The AllMachinesHaveNodesHealth check encapsulates the same JSONPath
// and per-machine validation logic that was previously inlined here.
//
// The context is named explicitly rather than inherited from the kubeconfig's
// current-context. Current-context is ambient state: earlier phases write the
// hub kubeconfig, and hub-bootstrap.sh exports KUBECONFIG to it whenever that
// file already exists — true on every resumed run. Relying on it made this
// wait query the hub, which has no CAPI CRDs until pivot installs them.
func waitForAllMachinesRunning(ctx context.Context, kubeconfig, kubeContext string, timeout time.Duration) error {
	waiter := &health.HealthWaiter{
		Checkers: []health.HealthChecker{
			health.NewAllMachinesHaveNodesHealth(constants.NamespaceCAPI, kubeContext),
		},
		Interval: 10 * time.Second,
		Timeout:  timeout,
	}
	return waiter.Wait(ctx, kubeconfig)
}

// binaries import is used by pivot but referenced transitively through the
// pivot package. This ensures the import stays.
var _ = binaries.ClusterctlManager{}

// bytes import for manifest reading.
var _ = bytes.NewReader

// filepath import for kubeconfig paths.
var _ = filepath.Join
