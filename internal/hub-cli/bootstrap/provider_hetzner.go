package bootstrap

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
	"github.com/soloz-io/zero-ops/internal/hub-cli/versions"
)

type HetznerProvider struct {
	Token             string
	Region            string
	OSType            string
	ImageID           string
	NetworkCIDR       string
	SSHKey            string
	Debug             bool
	BuildTalosImage   bool
	BuildFlatcarImage bool
}

func (p *HetznerProvider) Name() string {
	return "hetzner"
}

func (p *HetznerProvider) PreflightValidators() []preflight.Validator {
	v := []preflight.Validator{
		&preflight.DockerValidator{},
		&preflight.KindValidator{},
		&preflight.HetznerTokenValidator{Token: p.Token},
	}

	if p.OSType == "talos" {
		v = append(v, &preflight.TalosImageValidator{
			Token:       p.Token,
			ImageID:     p.ImageID,
			BuildImage:  p.BuildTalosImage,
			Region:      p.Region,
			ClusterName: "",
		})
	}

	if p.SSHKey != "" {
		v = append(v, &preflight.SSHKeyValidator{Token: p.Token, KeyName: p.SSHKey})
	}

	return v
}

func (p *HetznerProvider) CAPIProviders() []capi.CAPIProvider {
	providers := []capi.CAPIProvider{
		{Kind: "CoreProvider", Name: "cluster-api", Version: versions.CAPIVersion, Manifest: "core/capi-operator/providers/core-provider.yaml"},
	}

	if p.OSType == "ubuntu" {
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

func (p *HetznerProvider) IsSelfProvisioning() bool {
	return false
}

func (p *HetznerProvider) ClusterClassPaths() []string {
	return []string{
		"classes/hetzner-mgmt-ubuntu-v1.yaml",
	}
}

func (p *HetznerProvider) KindConfigPath() string {
	return ""
}

func (p *HetznerProvider) OnCAPIInit(ctx context.Context, kubeconfig, context, namespace string) error {
	secretMgr := &capi.SecretManager{
		Kubeconfig: kubeconfig,
		Context:    context,
		Namespace:  namespace,
	}

	if err := secretMgr.CreateHetznerSecret(ctx, p.Token); err != nil {
		return fmt.Errorf("failed to create Hetzner secret: %w", err)
	}
	fmt.Println("[capi-init] ✓ Hetzner credentials secret created")

	return nil
}

// --- Day-Zero Infrastructure (ADR-036 §6) ---

// DayZeroInfra returns no declarative manifests. The hcloud-csi driver
// (installed imperatively in OnDayZeroInit from embedded assets) creates
// the "hcloud-volumes" StorageClass dynamically.
func (p *HetznerProvider) DayZeroInfra() []InfraManifest {
	return nil
}

func (p *HetznerProvider) OnDayZeroInit(ctx context.Context, kubeconfig string) error {
	secretCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfig,
		"create", "secret", "generic", "hetzner",
		"-n", constants.NamespaceCloud,
		"--from-literal=hcloud="+p.Token,
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
	if output, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create hetzner secret: %w\n%s", err, output)
	}

	fmt.Println("[postboot] Installing hetzner-csi...")
	csiManifest, err := assets.ReadCatalog("cloud-providers/hetzner/csi/install.yaml")
	if err != nil {
		return fmt.Errorf("failed to read CSI manifest: %w", err)
	}

	cmd := exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", kubeconfig, "-f", "-")
	cmd.Stdin = bytes.NewReader(csiManifest)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install CSI: %w\n%s", err, output)
	}

	if err := waitForDeployment(ctx, kubeconfig, constants.NamespaceCloud, "hcloud-csi-controller", 3*time.Minute); err != nil {
		return fmt.Errorf("failed to verify CSI: %w", err)
	}
	fmt.Println("[postboot] ✓ hetzner-csi ready")

	return nil
}

// --- Capability Contract (ADR-036 §5) ---

func (p *HetznerProvider) Capabilities() CapabilityContract {
	return CapabilityContract{
		Version:      "1.0.0",
		StorageClass: "hcloud-volumes",
		BlockStorage: true,
		LoadBalancer: true,
		GPU:          true,
		MaxNodes:     0, // no provider-enforced limit
	}
}
