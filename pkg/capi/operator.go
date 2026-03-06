package capi

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
)

// OperatorInstaller installs cluster-api-operator
type OperatorInstaller struct {
	Kubeconfig string
	Namespace  string
}

func (i *OperatorInstaller) Install(ctx context.Context) error {
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

func (i *OperatorInstaller) installOperator(ctx context.Context) error {
	manifest, err := assets.ReadManifest("core/capi-operator/install.yaml")
	if err != nil {
		return err
	}
	
	cmd := exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", i.Kubeconfig,
		"-f", "-",
	)
	cmd.Stdin = bytes.NewReader(manifest)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w\n%s", err, output)
	}
	
	return nil
}

func (i *OperatorInstaller) waitForOperator(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", i.Kubeconfig,
		"wait", "deployment",
		"-n", "capi-operator-system",
		"capi-operator-controller-manager",
		"--for=condition=Available",
		fmt.Sprintf("--timeout=%s", timeout),
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("operator not ready: %w\n%s", err, output)
	}
	
	return nil
}

func (i *OperatorInstaller) applyProviders(ctx context.Context) error {
	providers := []string{
		"core/capi-operator/providers/core-provider.yaml",
		"core/capi-operator/providers/bootstrap-provider-talos.yaml",
		"core/capi-operator/providers/controlplane-provider-talos.yaml",
		"core/capi-operator/providers/infrastructure-provider-hetzner.yaml",
	}
	
	for _, providerPath := range providers {
		manifest, err := assets.ReadManifest(providerPath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", providerPath, err)
		}
		
		cmd := exec.CommandContext(ctx, "kubectl", "apply",
			"--kubeconfig", i.Kubeconfig,
			"-f", "-",
		)
		cmd.Stdin = bytes.NewReader(manifest)
		
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to apply %s: %w\n%s", providerPath, err, output)
		}
	}
	
	return nil
}

func (i *OperatorInstaller) waitForProviders(ctx context.Context, timeout time.Duration) error {
	providers := []struct {
		kind string
		name string
	}{
		{"CoreProvider", "cluster-api"},
		{"BootstrapProvider", "talos"},
		{"ControlPlaneProvider", "talos"},
		{"InfrastructureProvider", "hetzner"},
	}
	
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
				
				cmd := exec.CommandContext(ctx, "kubectl",
					"--kubeconfig", i.Kubeconfig,
					"get", provider.kind, provider.name,
					"-n", "capi-operator-system",
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}",
				)
				
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
