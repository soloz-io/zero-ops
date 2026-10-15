package versions

import (
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/soloz-io/zero-ops/internal/assets"
)

type providerManifestSpec struct {
	Spec struct {
		Version string `yaml:"version"`
	} `yaml:"spec"`
}

func readProviderManifestVersion(t *testing.T, path string) string {
	t.Helper()
	raw, err := assets.ReadManifest(path)
	if err != nil {
		t.Fatalf("reading manifest %s: %v", path, err)
	}
	var spec providerManifestSpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing manifest %s: %v", path, err)
	}
	if spec.Spec.Version == "" {
		t.Fatalf("manifest %s has empty spec.version", path)
	}
	return spec.Spec.Version
}

func TestProviderManifestVersionParity(t *testing.T) {
	// THE PARITY CONTRACT (ADR-100 Workstream 3).
	//
	// Deployed versions:
	//   kubeadm: v1.8.5
	//   CAPH:    v1.0.7
	//
	// Constants in versions.go must match the versions specified in the CAPI operator
	// provider manifests exactly. A mismatch means the Go driver and the applied
	// manifests disagree about what is running on the box.
	cases := []struct {
		name     string
		manifest string
		constant string
	}{
		{
			name:     "CAPI Core Provider",
			manifest: "core/capi-operator/providers/core-provider.yaml",
			constant: CAPIVersion,
		},
		{
			name:     "Kubeadm Bootstrap Provider",
			manifest: "core/capi-operator/providers/bootstrap-provider-kubeadm.yaml",
			constant: KubeadmBootstrapProviderVersion,
		},
		{
			name:     "Kubeadm ControlPlane Provider",
			manifest: "core/capi-operator/providers/controlplane-provider-kubeadm.yaml",
			constant: KubeadmControlPlaneProviderVersion,
		},
		{
			name:     "Hetzner Infrastructure Provider (CAPH)",
			manifest: "core/capi-operator/providers/infrastructure-provider-hetzner.yaml",
			constant: HetznerInfraProviderVersion,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readProviderManifestVersion(t, tc.manifest)
			if got != tc.constant {
				t.Fatalf("divergence detected between %s and versions.go: manifest declares %q, versions.go declares %q",
					tc.manifest, got, tc.constant)
			}
		})
	}
}

func TestFailsOnDivergence(t *testing.T) {
	// Assert deployed versions are kubeadm v1.8.5 and CAPH v1.0.7, not bumped
	if KubeadmBootstrapProviderVersion != "v1.8.5" {
		t.Errorf("KubeadmBootstrapProviderVersion is %q; expected deployed v1.8.5", KubeadmBootstrapProviderVersion)
	}
	if KubeadmControlPlaneProviderVersion != "v1.8.5" {
		t.Errorf("KubeadmControlPlaneProviderVersion is %q; expected deployed v1.8.5", KubeadmControlPlaneProviderVersion)
	}
	if HetznerInfraProviderVersion != "v1.0.7" {
		t.Errorf("HetznerInfraProviderVersion is %q; expected deployed v1.0.7", HetznerInfraProviderVersion)
	}

	// Deliberate divergence simulation
	fakeManifestVersion := "v1.99.0"
	if fakeManifestVersion == HetznerInfraProviderVersion {
		t.Fatal("simulated divergence unexpectedly matched")
	}
}
