package bootstrap

import (
	"context"
	"fmt"

	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
	"github.com/soloz-io/zero-ops/internal/hub-cli/versions"
)

// DockerProvider implements Provider for local CAPD (Cluster API Provider Docker).
//
// ADR-036 §3: CAPD is the official local development and integration testing
// provider. It is NOT a production-supported provider — it lacks production-grade
// networking, storage, and availability guarantees.
type DockerProvider struct {
	Debug bool
}

// --- Identity ---

func (p *DockerProvider) Name() string { return "docker" }

// --- Bootstrap Configuration ---

func (p *DockerProvider) PreflightValidators() []preflight.Validator {
	return []preflight.Validator{
		&preflight.DockerValidator{},
		&preflight.KindValidator{},
	}
}

func (p *DockerProvider) KindConfigPath() string {
	return "manifests/providers/local/kind-config.yaml"
}

// IsSelfProvisioning: CAPD runs IN the kind cluster — no pivot needed.
func (p *DockerProvider) IsSelfProvisioning() bool { return true }

// --- CAPI Infrastructure ---

func (p *DockerProvider) CAPIProviders() []capi.CAPIProvider {
	return []capi.CAPIProvider{
		{Kind: "CoreProvider", Name: "cluster-api", Version: versions.CAPIVersion, Manifest: "core/capi-operator/providers/core-provider.yaml"},
		{Kind: "BootstrapProvider", Name: "kubeadm", Version: versions.KubeadmBootstrapProviderVersion, Manifest: "core/capi-operator/providers/bootstrap-provider-kubeadm.yaml"},
		{Kind: "ControlPlaneProvider", Name: "kubeadm", Version: versions.KubeadmControlPlaneProviderVersion, Manifest: "core/capi-operator/providers/controlplane-provider-kubeadm.yaml"},
		{Kind: "InfrastructureProvider", Name: "docker", Version: versions.DockerInfraProviderVersion, Manifest: "core/capi-operator/providers/infrastructure-provider-docker.yaml"},
	}
}

func (p *DockerProvider) OnCAPIInit(ctx context.Context, kubeconfig, kubeContext, namespace string) error {
	fmt.Println("[capi-init] ✓ CAPD does not require provider credentials")
	return nil
}

// --- ClusterClass Templates ---

func (p *DockerProvider) ClusterClassPaths() []string {
	return []string{"classes/capd-spoke-pool-v1.yaml"}
}

// --- Day-Zero Infrastructure (ADR-036 §6) ---

// DayZeroInfra returns the local-path StorageClass needed by core platform PVCs.
// PVCs reference "hcloud-volumes" by name (shared convention across providers).
// On CAPD this is backed by rancher.io/local-path, not actual Hetzner volumes.
func (p *DockerProvider) DayZeroInfra() []InfraManifest {
	return []InfraManifest{
		{
			Name: "local-path StorageClass (hcloud-volumes)",
			Path: "manifests/providers/local/storage-class.yaml",
		},
	}
}

func (p *DockerProvider) OnDayZeroInit(ctx context.Context, kubeconfig string) error {
	fmt.Println("[postboot] ✓ CAPD — no additional Day-0 init required")
	return nil
}

// --- Capability Contract (ADR-036 §5) ---

func (p *DockerProvider) Capabilities() CapabilityContract {
	return CapabilityContract{
		Version:      "1.0.0",
		StorageClass: "hcloud-volumes",
		BlockStorage: true,
		LoadBalancer: false,
		GPU:          false,
		MaxNodes:     3,
	}
}
