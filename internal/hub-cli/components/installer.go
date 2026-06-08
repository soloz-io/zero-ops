package components

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
)

// Installer installs management cluster components
type Installer struct {
	Kubeconfig string
}

// InstallAll installs ArgoCD via Helm. Infrastructure components (Cilium CNI,
// Hetzner CCM, CSI) are provisioned by CAPI ClusterResourceSet per ADR-041.
// The CLI is forbidden from infrastructure provisioning.
func (i *Installer) InstallAll(ctx context.Context, hcloudToken string) error {
	if err := i.InstallArgoCD(ctx); err != nil {
		return fmt.Errorf("failed to install ArgoCD: %w", err)
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
	// For Cilium installer job, wait for job completion
	if deployment == "cilium-installer" {
		cmd := exec.CommandContext(ctx, "kubectl",
			"--kubeconfig", i.Kubeconfig,
			"wait", "job", deployment,
			"-n", namespace,
			"--for=condition=Complete",
			"--timeout=10m",
		)
		
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("job not complete: %w\n%s", err, output)
		}
		
		// Wait for Cilium operator deployment
		cmd = exec.CommandContext(ctx, "kubectl",
			"--kubeconfig", i.Kubeconfig,
			"wait", "deployment", "cilium-operator",
			"-n", constants.NamespaceKubeSystem,
			"--for=condition=Available",
			"--timeout=5m",
		)
		
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("cilium-operator not ready: %w\n%s", err, output)
		}
		
		return nil
	}
	
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
		"-n", constants.NamespaceOps,
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


// InstallArgoCD installs ArgoCD via Helm
func (i *Installer) InstallArgoCD(ctx context.Context) error {
	// Check if ArgoCD is already deployed and running
	checkCmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", i.Kubeconfig,
		"get", "deployment", "argocd-server",
		"--namespace", constants.NamespaceOps,
	)
	if err := checkCmd.Run(); err == nil {
		fmt.Println("[postboot] ✓ argocd already installed")
		return nil
	}

	fmt.Println("[postboot] Installing argocd...")
	
	// Add ArgoCD Helm repo
	cmd := exec.CommandContext(ctx, "helm", "repo", "add", "argo", "https://argoproj.github.io/argo-helm")
	if output, err := cmd.CombinedOutput(); err != nil {
		if !bytes.Contains(output, []byte("already exists")) {
			return fmt.Errorf("failed to add helm repo: %w\n%s", err, output)
		}
	}
	
	// Update repos
	cmd = exec.CommandContext(ctx, "helm", "repo", "update")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update helm repos: %w\n%s", err, output)
	}
	
	// Install ArgoCD
	cmd = exec.CommandContext(ctx, "helm", "upgrade", "--install", "argocd", "argo/argo-cd",
		"--version", "7.7.12",
		"--namespace", constants.NamespaceOps,
		"--create-namespace",
		"--kubeconfig", i.Kubeconfig,
		"--set", "networkPolicy.enabled=true",
		"--set", "networkPolicy.defaultDeny=false",
		"--set", "repoServer.env[0].name=ARGOCD_EXEC_TIMEOUT",
		"--set", "repoServer.env[0].value=600s",
		"--wait",
		"--timeout", "10m",
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install argocd: %w\n%s", err, output)
	}
	
	fmt.Println("[postboot] ✓ argocd ready")
	return nil
}

// Cilium CNI is now managed by CAPI ClusterResourceSet per ADR-041.
// The CLI is forbidden from infrastructure provisioning (CNI, CCM, CSI).
// See: manifests/clusters/capi/cluster-resource-set/cilium/

// Hetzner CCM is now managed by CAPI ClusterResourceSet per ADR-041.
// The CLI is forbidden from infrastructure provisioning (CNI, CCM, CSI).
// See: manifests/clusters/capi/cluster-resource-set/ccm-hetzner/
