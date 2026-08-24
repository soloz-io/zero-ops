package bootstrap

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/cluster"
	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
	"github.com/soloz-io/zero-ops/internal/hub-cli/versions"
)

// HetznerDriver implements CloudDriver for Hetzner Cloud.
type HetznerDriver struct {
	Token             string
	Region            string
	OS                string // "ubuntu" or "talos"
	ImageID           string
	NetworkCIDR       string
	SSHKey            string
	Debug             bool
	BuildTalosImage   bool
	BuildFlatcarImage bool
}

// ── CloudDriver interface ──────────────────────────────────────────────────

func (d *HetznerDriver) Name() string   { return "hetzner" }
func (d *HetznerDriver) OSType() string { return d.OS }

// ── Phase 1: Preflight ──────────────────────────────────────────────────────

func (d *HetznerDriver) PreflightValidators() []preflight.Validator {
	v := []preflight.Validator{
		&preflight.DockerValidator{},
		&preflight.KindValidator{},
		&preflight.HetznerTokenValidator{Token: d.Token},
	}

	if d.OS == "talos" {
		v = append(v, &preflight.TalosImageValidator{
			Token:       d.Token,
			ImageID:     d.ImageID,
			BuildImage:  d.BuildTalosImage,
			Region:      d.Region,
			ClusterName: "",
		})
	}

	if d.SSHKey != "" {
		v = append(v, &preflight.SSHKeyValidator{Token: d.Token, KeyName: d.SSHKey})
	}

	return v
}

// ── Phase 3: Day-0 Infrastructure ───────────────────────────────────────────

func (d *HetznerDriver) ProvisionDayZero(ctx context.Context, kubeconfig string) error {
	// Create hetzner secret for CSI driver
	fmt.Println("[day0] Creating hetzner secret for CSI...")
	secretCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfig,
		"create", "secret", "generic", "hetzner",
		"-n", constants.NamespaceCloud,
		"--from-literal=hcloud="+d.Token,
		"--dry-run=client", "-o", "yaml",
	)
	secretYAML, err := secretCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to generate hetzner secret: %w", err)
	}

	applyCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfig,
		"apply", "-f", "-",
	)
	applyCmd.Stdin = bytes.NewReader(secretYAML)
	if out, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create hetzner secret: %w\n%s", err, out)
	}
	fmt.Println("[day0] ✓ Hetzner secret created")

	// The hcloud CSI controller can only run on a Hetzner Cloud server: it resolves
	// its own location from the metadata service at 169.254.169.254, and falls back
	// to looking itself up in the Hetzner API by KUBE_NODE_NAME. On an ephemeral
	// kind bootstrap cluster neither exists — the node is kind://docker/... — so the
	// driver container CrashLoops and this phase times out after 3 minutes.
	//
	// Nothing on the bootstrap cluster needs it. Its job is to run the CAPI/CAPH
	// controllers that provision the hub and then pivot; it creates no PVCs. The
	// management cluster gets its own CSI install in the platform-pre phase below,
	// where the nodes really are Hetzner servers.
	//
	// This surfaced with the hybrid provider (ADR-046), where the bootstrap cluster
	// is kind on a workstation. Gate on what the CSI actually requires rather than
	// on the provider name, so a bootstrap cluster that *is* hosted on Hetzner
	// (--bootstrap-context) still gets the driver.
	onHetzner, err := nodesAreHetznerServers(ctx, kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to inspect bootstrap cluster nodes: %w", err)
	}
	if !onHetzner {
		fmt.Println("[day0] Skipping hetzner-csi: bootstrap cluster nodes are not Hetzner servers")
		fmt.Println("[day0]   (the CSI controller requires the Hetzner metadata service;")
		fmt.Println("[day0]    it is installed on the management cluster in platform-pre)")
		return nil
	}

	// Install CSI driver
	fmt.Println("[day0] Installing hetzner-csi...")
	csiManifest, err := assets.ReadCatalog("cloud-providers/hetzner/csi/install.yaml")
	if err != nil {
		return fmt.Errorf("failed to read CSI manifest: %w", err)
	}

	cmd := exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", kubeconfig, "-f", "-")
	cmd.Stdin = bytes.NewReader(csiManifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install CSI: %w\n%s", err, out)
	}

	if err := waitForDeployment(ctx, kubeconfig, constants.NamespaceCloud, "hcloud-csi-controller", 3*time.Minute); err != nil {
		return fmt.Errorf("failed to verify CSI: %w", err)
	}
	fmt.Println("[day0] ✓ hetzner-csi ready")
	return nil
}

// writeTailscaleSecret creates or updates tailscale-hybrid-psk in the target
// namespace. Empty values are meaningful, not a bug: they render the ClusterClass's
// tailscale steps inert on clusters that have no tailnet, while still satisfying
// the contentFrom.secret reference that would otherwise block bootstrap rendering.
func writeTailscaleSecret(ctx context.Context, kubeconfig, namespace, authkey, hostname string) error {
	gen := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfig,
		"create", "secret", "generic", "tailscale-hybrid-psk",
		"-n", namespace,
		"--from-literal=authkey="+authkey,
		"--from-literal=hostname="+hostname,
		"--dry-run=client", "-o", "yaml",
	)
	manifest, err := gen.Output()
	if err != nil {
		return fmt.Errorf("render tailscale secret: %w", err)
	}

	apply := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig, "apply", "-f", "-")
	apply.Stdin = bytes.NewReader(manifest)
	if out, err := apply.CombinedOutput(); err != nil {
		return fmt.Errorf("apply tailscale secret: %w\n%s", err, out)
	}
	return nil
}

// nodesAreHetznerServers reports whether every node in the target cluster is a
// Hetzner Cloud server, judged by providerID (hcloud://...). A kind node reports
// kind://docker/..., and a node that has not yet been assigned a providerID
// reports an empty string — neither can run the hcloud CSI controller.
func nodesAreHetznerServers(ctx context.Context, kubeconfig string) (bool, error) {
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfig,
		"get", "nodes",
		"-o", "jsonpath={range .items[*]}{.spec.providerID}{\"\\n\"}{end}",
	)
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("kubectl get nodes: %w", err)
	}

	found := false
	for _, line := range strings.Split(string(out), "\n") {
		id := strings.TrimSpace(line)
		if id == "" {
			continue
		}
		found = true
		if !strings.HasPrefix(id, "hcloud://") {
			return false, nil
		}
	}
	// No providerIDs at all: the cloud-controller-manager has not run yet, so this
	// is certainly not a ready Hetzner cluster.
	return found, nil
}

// ── Phase 4: CAPI Initialization ────────────────────────────────────────────

func (d *HetznerDriver) CAPIProviders() []capi.CAPIProvider {
	providers := []capi.CAPIProvider{
		{Kind: "CoreProvider", Name: "cluster-api", Version: versions.CAPIVersion, Manifest: "core/capi-operator/providers/core-provider.yaml"},
	}

	if d.OS == "ubuntu" {
		providers = append(providers,
			capi.CAPIProvider{Kind: "BootstrapProvider", Name: "kubeadm", Version: versions.KubeadmBootstrapProviderVersion, Manifest: "core/capi-operator/providers/bootstrap-provider-kubeadm.yaml"},
			capi.CAPIProvider{Kind: "ControlPlaneProvider", Name: "kubeadm", Version: versions.KubeadmControlPlaneProviderVersion, Manifest: "core/capi-operator/providers/controlplane-provider-kubeadm.yaml"},
		)
	} else {
		providers = append(providers,
			capi.CAPIProvider{Kind: "BootstrapProvider", Name: "talos", Version: versions.TalosBootstrapProviderVersion, Manifest: "core/capi-operator/providers/bootstrap-provider-talos.yaml"},
			capi.CAPIProvider{Kind: "ControlPlaneProvider", Name: "talos", Version: versions.TalosControlPlaneProviderVersion, Manifest: "core/capi-operator/providers/controlplane-provider-talos.yaml"},
		)
	}

	providers = append(providers, capi.CAPIProvider{
		Kind:     "InfrastructureProvider",
		Name:     "hetzner",
		Version:  versions.HetznerInfraProviderVersion,
		Manifest: "core/capi-operator/providers/infrastructure-provider-hetzner.yaml",
	})

	return providers
}

func (d *HetznerDriver) OnCAPIInit(ctx context.Context, kubeconfig, context, namespace string) error {
	secretMgr := &capi.SecretManager{
		Kubeconfig: kubeconfig,
		Context:    context,
		Namespace:  namespace,
	}

	if err := secretMgr.CreateHetznerSecret(ctx, d.Token); err != nil {
		return fmt.Errorf("failed to create Hetzner secret: %w", err)
	}
	fmt.Println("[capi-init] ✓ Hetzner credentials secret created")

	// The shared hub ClusterClass reads /etc/tailscale-{authkey,hostname} via
	// contentFrom.secret, so the Secret must exist before the Cluster is created or
	// the KubeadmConfig never renders. A pure-Hetzner hub has no tailnet, so it gets
	// EMPTY values: every tailscale command in the ClusterClass is guarded on
	// `[ -s /etc/tailscale-hostname ]` and becomes a no-op. HybridDriver overwrites
	// this with real credentials.
	if err := writeTailscaleSecret(ctx, kubeconfig, namespace, "", ""); err != nil {
		return fmt.Errorf("failed to create placeholder tailscale secret: %w", err)
	}
	return nil
}

// ── Phase 5: Cluster Provisioning Config ────────────────────────────────────

func (d *HetznerDriver) PopulateClusterConfig(cfg *cluster.Config) {
	subnetCIDR := d.NetworkCIDR[:len(d.NetworkCIDR)-2] + "24"

	imageID := d.ImageID
	if d.OS == "ubuntu" {
		imageID = "ubuntu-24.04"
	}

	cfg.Region = d.Region
	cfg.OSType = d.OS
	cfg.ImageID = imageID
	cfg.KubernetesVersion = "v1.31.6"
	cfg.NetworkCIDR = d.NetworkCIDR
	cfg.SubnetCIDR = subnetCIDR
	cfg.ControlPlaneMachineType = "cx33"
	cfg.WorkerMachineType = "cx33"
	cfg.ControlPlaneReplicas = 1
	cfg.WorkerReplicas = 2
	cfg.HCloudToken = d.Token

	// SSH key name for rescue/emergency access. Defaults to the key present in
	// the Hetzner project when --ssh-key is not supplied.
	if d.SSHKey != "" {
		cfg.SSHKeyName = d.SSHKey
	} else {
		cfg.SSHKeyName = "mac-mini-ssh"
	}

	// Read CCM addon manifest from shared base (ADR-046 §WS1: spoke-addons moved to hetzner/base/).
	ccmRaw, err := readTemplateManifest(
		"manifests/providers/hetzner/base/spoke-addons/", "ccm-addon-template.yaml", "ccm.yaml")
	if err != nil {
		// Log but don't fail — CCM can be installed later
		fmt.Printf("[cluster-provision] Warning: failed to read CCM manifest: %v\n", err)
	} else {
		cfg.CCMManifest = string(ccmRaw)
	}
}

// ── Phase 10: Platform Pre-Requisites ────────────────────────────────────────
// The day-0 CSI install runs against the ephemeral bootstrap cluster, which is
// deleted after pivot. Re-apply the CSI driver + StorageClass to the management
// (Hub) cluster so PVs provision (hcloud-volumes) for hub databases/Redis.
func (d *HetznerDriver) OnPlatformPreReqs(ctx context.Context, kubeconfig string) error {
	fmt.Println("[platform-pre] Installing hetzner-csi on management cluster...")
	csiManifest, err := assets.ReadCatalog("cloud-providers/hetzner/csi/install.yaml")
	if err != nil {
		return fmt.Errorf("failed to read CSI manifest: %w", err)
	}

	cmd := exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", kubeconfig, "-f", "-")
	cmd.Stdin = bytes.NewReader(csiManifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install CSI on management cluster: %w\n%s", err, out)
	}

	if err := waitForDeployment(ctx, kubeconfig, constants.NamespaceCloud, "hcloud-csi-controller", 3*time.Minute); err != nil {
		return fmt.Errorf("failed to verify CSI on management cluster: %w", err)
	}
	fmt.Println("[platform-pre] ✓ hetzner-csi ready on management cluster")
	return nil
}

// ── Phase 9: ClusterClass ───────────────────────────────────────────────────

func (d *HetznerDriver) ClusterClassPaths() []string {
	return []string{"classes/hetzner-mgmt-ubuntu-v1.yaml"}
}

// ── Capabilities ────────────────────────────────────────────────────────────

func (d *HetznerDriver) OperatorWebhookPatterns() []string {
	return []string{"caph"}
}
func (d *HetznerDriver) Capabilities() CapabilityContract {
	return CapabilityContract{
		Version:      "1.0.0",
		StorageClass: "hcloud-volumes",
		BlockStorage: true,
		LoadBalancer: true,
		GPU:          true,
		MaxNodes:     0,
	}
}

// ── Shared helper ───────────────────────────────────────────────────────────

// waitForDeployment polls kubectl rollout status for a deployment.
func waitForDeployment(ctx context.Context, kubeconfig, namespace, deployment string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for deployment %s/%s", namespace, deployment)
			}
			cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
				"rollout", "status", "deployment/"+deployment,
				"-n", namespace, "--timeout=10s")
			if err := cmd.Run(); err == nil {
				return nil
			}
		}
	}
}
