package components

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os/exec"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/internal/hub/constants"
)

// Installer installs management cluster components
type Installer struct {
	Kubeconfig string
}

// InstallAll installs all required components sequentially (CNI/CCM handled by CRS)
func (i *Installer) InstallAll(ctx context.Context, hcloudToken string) error {
	// Create hcloud secret for CSI driver
	// NOTE: CSI manifest expects secret in kube-system (upstream default), not hub-cloud-system
	fmt.Println("[postboot] Creating hcloud secret for CSI...")
	secretCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", i.Kubeconfig,
		"create", "secret", "generic", "hcloud",
		"-n", constants.NamespaceKubeSystem,
		"--from-literal=token="+hcloudToken,
		"--dry-run=client", "-o", "yaml",
	)
	secretYAML, err := secretCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to generate hcloud secret: %w", err)
	}
	
	applyCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", i.Kubeconfig,
		"apply", "-f", "-",
	)
	applyCmd.Stdin = bytes.NewReader(secretYAML)
	if output, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create hcloud secret: %w\n%s", err, output)
	}
	
	// Install CSI via manifest
	fmt.Println("[postboot] Installing hetzner-csi...")
	csiManifest, err := assets.ReadCatalog("cloud-providers/hetzner/csi/install.yaml")
	if err != nil {
		return fmt.Errorf("failed to read CSI manifest: %w", err)
	}
	
	cmd := exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", i.Kubeconfig, "-f", "-")
	cmd.Stdin = bytes.NewReader(csiManifest)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install CSI: %w\n%s", err, output)
	}
	
	if err := i.verify(ctx, constants.NamespaceKubeSystem, "hcloud-csi-controller"); err != nil {
		return fmt.Errorf("failed to verify CSI: %w", err)
	}
	fmt.Println("[postboot] ✓ hetzner-csi ready")
	
	// Install ArgoCD via Helm
	if err := i.InstallArgoCD(ctx); err != nil {
		return fmt.Errorf("failed to install ArgoCD: %w", err)
	}
	
	// capi2argo is now managed by ArgoCD (manifests/argocd/apps/platform-capi2argo.yaml)
	// It will be automatically installed when ArgoCD syncs the app-of-apps
	
	// Install CloudNativePG via Helm
	fmt.Println("[postboot] Installing cloudnative-pg...")
	
	cmd = exec.CommandContext(ctx, "helm", "repo", "add", "cnpg", "https://cloudnative-pg.github.io/charts")
	if output, err := cmd.CombinedOutput(); err != nil {
		if !bytes.Contains(output, []byte("already exists")) {
			return fmt.Errorf("failed to add helm repo: %w\n%s", err, output)
		}
	}
	
	cmd = exec.CommandContext(ctx, "helm", "repo", "update")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update helm repos: %w\n%s", err, output)
	}
	
	cmd = exec.CommandContext(ctx, "helm", "upgrade", "--install", "cnpg", "cnpg/cloudnative-pg",
		"--namespace", constants.NamespaceCNPG,
		"--create-namespace",
		"--kubeconfig", i.Kubeconfig,
		"--wait",
		"--timeout", "5m",
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install cloudnative-pg: %w\n%s", err, output)
	}
	
	fmt.Println("[postboot] ✓ cloudnative-pg ready")
	
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
		"--wait",
		"--timeout", "10m",
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install argocd: %w\n%s", err, output)
	}
	
	fmt.Println("[postboot] ✓ argocd ready")
	return nil
}

// InstallCilium installs Cilium CNI via Helm (CAPH parity)
func (i *Installer) InstallCilium(ctx context.Context) error {
	fmt.Println("[cilium] Installing Cilium CNI via Helm...")
	
	// Add Cilium Helm repo
	cmd := exec.CommandContext(ctx, "helm", "repo", "add", "cilium", "https://helm.cilium.io/")
	if output, err := cmd.CombinedOutput(); err != nil {
		// Ignore "already exists" error
		if !bytes.Contains(output, []byte("already exists")) {
			return fmt.Errorf("failed to add helm repo: %w\n%s", err, output)
		}
	}
	
	// Update repos
	cmd = exec.CommandContext(ctx, "helm", "repo", "update")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update helm repos: %w\n%s", err, output)
	}
	
	// Install Cilium
	cmd = exec.CommandContext(ctx, "helm", "upgrade", "--install", "cilium", "cilium/cilium",
		"--version", "1.15.6",
		"--namespace", constants.NamespaceOps,
		"--kubeconfig", i.Kubeconfig,
		"--set", "ipam.mode=kubernetes",
		"--set", "kubeProxyReplacement=true",
		"--set", "operator.rollOutPods=true",
		"--set", "rollOutCiliumPods=true",
		"--set", "priorityClassName=system-node-critical",
		"--set", "operator.priorityClassName=system-node-critical",
		"--wait",
		"--timeout", "10m",
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install cilium: %w\n%s", err, output)
	}
	
	fmt.Println("[cilium] ✓ Cilium CNI installed")
	return nil
}

// InstallCCM installs Hetzner Cloud Controller Manager via Helm (CAPH parity)
func (i *Installer) InstallCCM(ctx context.Context, hcloudToken string) error {
	fmt.Println("[ccm] Installing Hetzner CCM via Helm...")
	
	// Add syself Helm repo
	cmd := exec.CommandContext(ctx, "helm", "repo", "add", "syself", "https://charts.syself.com")
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
	
	// Install CCM
	cmd = exec.CommandContext(ctx, "helm", "upgrade", "--install", "ccm", "syself/ccm-hetzner",
		"--version", "1.1.10",
		"--namespace", constants.NamespaceCloud,
		"--kubeconfig", i.Kubeconfig,
		"--set", fmt.Sprintf("secret.hcloudApiToken=%s", hcloudToken),
		"--wait",
		"--timeout", "5m",
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install ccm: %w\n%s", err, output)
	}
	
	fmt.Println("[ccm] ✓ Hetzner CCM installed")
	return nil
}
