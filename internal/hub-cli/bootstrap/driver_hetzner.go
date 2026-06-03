package bootstrap

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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
		"--from-literal=robot-user=",
		"--from-literal=robot-password=",
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

	// Read Hetzner CCM addon manifest
	ccmRaw, err := readTemplateManifest(
		"manifests/providers/hetzner/spoke-addons/", "ccm-addon-template.yaml", "ccm.yaml")
	if err != nil {
		// Log but don't fail — CCM can be installed later
		fmt.Printf("[cluster-provision] Warning: failed to read CCM manifest: %v\n", err)
	} else {
		cfg.CCMManifest = string(ccmRaw)
	}
}

// ── Phase 9: ClusterClass ───────────────────────────────────────────────────

func (d *HetznerDriver) ClusterClassPaths() []string {
	return []string{"classes/hetzner-mgmt-ubuntu-v1.yaml"}
}

// ── Capabilities ────────────────────────────────────────────────────────────

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
