package components

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
)

// Installer installs management cluster components
type Installer struct {
	Kubeconfig string
}

// InstallAll installs all required components sequentially
func (i *Installer) InstallAll(ctx context.Context) error {
	components := []struct {
		name    string
		path    string
		ns      string
		deploy  string
	}{
		{"hetzner-ccm", "cloud-providers/hetzner/ccm/install.yaml", "kube-system", "hcloud-cloud-controller-manager"},
		{"hetzner-csi", "cloud-providers/hetzner/csi/install.yaml", "kube-system", "hcloud-csi-controller"},
		{"argocd", "gitops/argocd/install.yaml", "argocd", "argocd-server"},
		{"capi2argo", "gitops/capi2argo/install.yaml", "capi2argo-system", "capi2argo-controller-manager"},
		{"cloudnative-pg", "databases/cloudnative-pg/install.yaml", "cnpg-system", "cnpg-controller-manager"},
	}
	
	for _, c := range components {
		fmt.Printf("[postboot] Installing %s...\n", c.name)
		
		if err := i.install(ctx, c.path); err != nil {
			return fmt.Errorf("failed to install %s: %w", c.name, err)
		}
		
		if err := i.verify(ctx, c.ns, c.deploy); err != nil {
			return fmt.Errorf("failed to verify %s: %w", c.name, err)
		}
		
		fmt.Printf("[postboot] ✓ %s ready\n", c.name)
	}
	
	return nil
}

func (i *Installer) install(ctx context.Context, path string) error {
	manifest, err := assets.ReadCatalog(path)
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

func (i *Installer) verify(ctx context.Context, namespace, deployment string) error {
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", i.Kubeconfig,
		"wait", "deployment", deployment,
		"-n", namespace,
		"--for=condition=Available",
		"--timeout=5m",
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("deployment not ready: %w\n%s", err, output)
	}
	
	return nil
}

// GetArgoCDPassword retrieves ArgoCD admin password
func (i *Installer) GetArgoCDPassword(ctx context.Context) (string, error) {
	// Wait a bit for secret to be created
	time.Sleep(5 * time.Second)
	
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", i.Kubeconfig,
		"get", "secret", "argocd-initial-admin-secret",
		"-n", "argocd",
		"-o", "jsonpath={.data.password}",
	)
	
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get ArgoCD password: %w", err)
	}
	
	// Decode base64
	decoded, err := base64.StdEncoding.DecodeString(string(output))
	if err != nil {
		return "", fmt.Errorf("failed to decode password: %w", err)
	}
	
	return string(decoded), nil
}
