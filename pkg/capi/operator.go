package capi

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/pkg/versions"
)

// OperatorInstaller installs cluster-api-operator
type OperatorInstaller struct {
	Kubeconfig string
	Context    string
	Namespace  string
	OSType     string // "ubuntu" or "talos"
	Debug      bool
}

func (i *OperatorInstaller) kubectlArgs(args ...string) []string {
	result := []string{"--kubeconfig", i.Kubeconfig}
	if i.Context != "" {
		result = append(result, "--context", i.Context)
	}
	return append(result, args...)
}

func (i *OperatorInstaller) runKubectl(ctx context.Context, args ...string) error {
	fullArgs := i.kubectlArgs(args...)
	if i.Debug {
		fmt.Printf("[DEBUG] kubectl %v\n", fullArgs)
	}
	cmd := exec.CommandContext(ctx, "kubectl", fullArgs...)
	output, err := cmd.CombinedOutput()
	if i.Debug && len(output) > 0 {
		fmt.Printf("[DEBUG] Output: %s\n", string(output))
	}
	if err != nil && i.Debug {
		fmt.Printf("[DEBUG] Error: %v\n", err)
	}
	return err
}

func (i *OperatorInstaller) Install(ctx context.Context) error {
	if i.Debug {
		fmt.Println("[DEBUG] OperatorInstaller.Install() started")
	}
	
	if err := i.ensureCertManager(ctx); err != nil {
		return err
	}
	
	if err := i.installOperator(ctx); err != nil {
		return err
	}
	
	if err := i.waitForOperator(ctx, 3*time.Minute); err != nil {
		return err
	}
	
	if err := i.applyProviders(ctx); err != nil {
		return err
	}
	
	return i.waitForProviders(ctx, 5*time.Minute)
}

func (i *OperatorInstaller) ensureCertManager(ctx context.Context) error {
	// Check if cert-manager namespace exists
	cmd := exec.CommandContext(ctx, "kubectl", i.kubectlArgs("get", "namespace", "cert-manager")...)
	
	if err := cmd.Run(); err == nil {
		fmt.Println("[capi-init] ✓ cert-manager already installed")
		return i.waitForCertManagerAPI(ctx)
	}
	
	// Install cert-manager
	fmt.Println("[capi-init] Installing cert-manager...")
	certManagerURL := fmt.Sprintf("https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.yaml", versions.CertManagerVersion)
	
	cmd = exec.CommandContext(ctx, "kubectl", i.kubectlArgs("apply", "-f", certManagerURL)...)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cert-manager install failed: %w\n%s", err, output)
	}
	
	return i.waitForCertManagerAPI(ctx)
}

func (i *OperatorInstaller) waitForCertManagerAPI(ctx context.Context) error {
	fmt.Println("[capi-init] Waiting for cert-manager API...")
	
	// Wait for webhook deployment
	cmd := exec.CommandContext(ctx, "kubectl", i.kubectlArgs("wait", "deployment",
		"-n", "cert-manager",
		"cert-manager-webhook",
		"--for=condition=Available",
		"--timeout=2m")...)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cert-manager webhook not ready: %w\n%s", err, output)
	}
	
	fmt.Println("[capi-init] ✓ cert-manager API ready")
	return nil
}

func (i *OperatorInstaller) installOperator(ctx context.Context) error {
	operatorURL := fmt.Sprintf("https://github.com/kubernetes-sigs/cluster-api-operator/releases/download/%s/operator-components.yaml", versions.CAPIOperatorVersion)
	
	cmd := exec.CommandContext(ctx, "kubectl", i.kubectlArgs("apply", "-f", operatorURL)...)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w\n%s", err, output)
	}
	
	return nil
}

func (i *OperatorInstaller) waitForOperator(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	
	cmd := exec.CommandContext(ctx, "kubectl", i.kubectlArgs("wait", "deployment",
		"-n", "capi-operator-system",
		"capi-operator-controller-manager",
		"--for=condition=Available",
		fmt.Sprintf("--timeout=%s", timeout))...)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("operator not ready: %w\n%s", err, output)
	}
	
	return nil
}

func (i *OperatorInstaller) applyProviders(ctx context.Context) error {
	// Wait for operator CRDs to be registered
	if err := i.waitForCRDs(ctx, 2*time.Minute); err != nil {
		return err
	}

	// Core provider (always required)
	providers := []string{
		"core/capi-operator/providers/core-provider.yaml",
	}

	// OS-specific bootstrap and control plane providers
	if i.OSType == "ubuntu" {
		providers = append(providers,
			"core/capi-operator/providers/bootstrap-provider-kubeadm.yaml",
			"core/capi-operator/providers/controlplane-provider-kubeadm.yaml",
		)
	} else {
		// Talos
		providers = append(providers,
			"core/capi-operator/providers/bootstrap-provider-talos.yaml",
			"core/capi-operator/providers/controlplane-provider-talos.yaml",
		)
	}

	// Infrastructure provider (always Hetzner)
	providers = append(providers, "core/capi-operator/providers/infrastructure-provider-hetzner.yaml")

	for _, providerPath := range providers {
		manifest, err := assets.ReadManifest(providerPath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", providerPath, err)
		}

		cmd := exec.CommandContext(ctx, "kubectl", i.kubectlArgs("apply", "-f", "-")...)
		cmd.Stdin = bytes.NewReader(manifest)

		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to apply %s: %w\n%s", providerPath, err, output)
		}
	}

	return nil
}

func (i *OperatorInstaller) waitForCRDs(ctx context.Context, timeout time.Duration) error {
	crds := []string{
		"coreproviders.operator.cluster.x-k8s.io",
		"bootstrapproviders.operator.cluster.x-k8s.io",
		"controlplaneproviders.operator.cluster.x-k8s.io",
		"infrastructureproviders.operator.cluster.x-k8s.io",
	}
	
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	for _, crd := range crds {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				if time.Now().After(deadline) {
					return fmt.Errorf("timeout waiting for CRD %s", crd)
				}
				
				cmd := exec.CommandContext(ctx, "kubectl", i.kubectlArgs("get", "crd", crd)...)
				
				if err := cmd.Run(); err == nil {
					fmt.Printf("[capi-init] ✓ CRD %s registered\n", crd)
					goto nextCRD
				}
				
				fmt.Printf("[capi-init] Waiting for CRD %s...\n", crd)
			}
		}
		nextCRD:
	}
	
	return nil
}

func (i *OperatorInstaller) waitForProviders(ctx context.Context, timeout time.Duration) error {
	// Core provider (always required)
	providers := []struct {
		kind string
		name string
	}{
		{"CoreProvider", "cluster-api"},
	}

	// OS-specific providers
	if i.OSType == "ubuntu" {
		providers = append(providers,
			struct{ kind, name string }{"BootstrapProvider", "kubeadm"},
			struct{ kind, name string }{"ControlPlaneProvider", "kubeadm"},
		)
	} else {
		// Talos
		providers = append(providers,
			struct{ kind, name string }{"BootstrapProvider", "talos"},
			struct{ kind, name string }{"ControlPlaneProvider", "talos"},
		)
	}

	// Infrastructure provider (always Hetzner)
	providers = append(providers, struct{ kind, name string }{"InfrastructureProvider", "hetzner"})

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for _, provider := range providers {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				if time.Now().After(deadline) {
					return fmt.Errorf("timeout waiting for %s/%s", provider.kind, provider.name)
				}

				cmd := exec.CommandContext(ctx, "kubectl", i.kubectlArgs("get", provider.kind, provider.name,
					"-n", "capi-operator-system",
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")...)

				output, err := cmd.Output()
				if err != nil {
					continue
				}

				if string(output) == "True" {
					fmt.Printf("[capi-init] ✓ %s/%s ready\n", provider.kind, provider.name)
					goto nextProvider
				}

				fmt.Printf("[capi-init] Waiting for %s/%s...\n", provider.kind, provider.name)
			}
		}
		nextProvider:
	}

	return nil
}
