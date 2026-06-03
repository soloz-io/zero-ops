package bootstrap

import (
	"context"
	"fmt"

	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
	"github.com/soloz-io/zero-ops/internal/hub-cli/versions"
)

type DockerProvider struct {
	Debug bool
}

func (p *DockerProvider) Name() string {
	return "docker"
}

func (p *DockerProvider) PreflightValidators() []preflight.Validator {
	return []preflight.Validator{
		&preflight.DockerValidator{},
		&preflight.KindValidator{},
	}
}

func (p *DockerProvider) CAPIProviders() []capi.CAPIProvider {
	return []capi.CAPIProvider{
		{Kind: "CoreProvider", Name: "cluster-api", Version: versions.CAPIVersion, Manifest: "core/capi-operator/providers/core-provider.yaml"},
		{Kind: "BootstrapProvider", Name: "kubeadm", Version: versions.KubeadmBootstrapProviderVersion, Manifest: "core/capi-operator/providers/bootstrap-provider-kubeadm.yaml"},
		{Kind: "ControlPlaneProvider", Name: "kubeadm", Version: versions.KubeadmControlPlaneProviderVersion, Manifest: "core/capi-operator/providers/controlplane-provider-kubeadm.yaml"},
		{Kind: "InfrastructureProvider", Name: "docker", Version: versions.DockerInfraProviderVersion, Manifest: "core/capi-operator/providers/infrastructure-provider-docker.yaml"},
	}
}

func (p *DockerProvider) IsSelfProvisioning() bool {
	return true
}

func (p *DockerProvider) ClusterClassPaths() []string {
	return []string{
		"classes/capd-spoke-pool-v1.yaml",
	}
}

func (p *DockerProvider) KindConfigPath() string {
	return "manifests/providers/local/kind-config.yaml"
}

func (p *DockerProvider) OnCAPIInit(ctx context.Context, kubeconfig, context, namespace string) error {
	fmt.Println("[capi-init] ✓ CAPD does not require provider credentials")
	return nil
}

func (p *DockerProvider) PostBootComponents(ctx context.Context, kubeconfig string) error {
	fmt.Println("[postboot] ✓ CAPD does not require cloud-specific components")
	return nil
}
