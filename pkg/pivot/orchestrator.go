package pivot

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/pkg/binaries"
)

// Orchestrator manages CAPI pivot from bootstrap to management cluster
type Orchestrator struct {
	BootstrapKubeconfig string
	ClusterName         string
	Namespace           string
	OSType              string // ubuntu or talos
}

// ExecuteMove performs the pivot move operation (without waiting for ready)
func (o *Orchestrator) ExecuteMove(ctx context.Context) (string, error) {
	// 0. Ensure clusterctl is installed
	clusterctlMgr, err := binaries.NewClusterctlManager()
	if err != nil {
		return "", fmt.Errorf("failed to create clusterctl manager: %w", err)
	}
	
	if err := clusterctlMgr.EnsureInstalled(ctx); err != nil {
		return "", fmt.Errorf("failed to install clusterctl: %w", err)
	}
	
	// 1. Retrieve Management Cluster kubeconfig
	mgmtKubeconfig, err := o.getKubeconfig(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve kubeconfig: %w", err)
	}
	
	// 2. Install cluster-api-operator on Management Cluster
	if err := o.installOperatorOnMgmt(ctx, mgmtKubeconfig); err != nil {
		return "", fmt.Errorf("failed to install operator on mgmt cluster: %w", err)
	}
	
	// 2.5. Create namespace and Hetzner credentials secret (required before move)
	fmt.Println("[pivot] Creating namespace...")
	nsCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", mgmtKubeconfig,
		"create", "namespace", o.Namespace,
	)
	if output, err := nsCmd.CombinedOutput(); err != nil && !bytes.Contains(output, []byte("AlreadyExists")) {
		return "", fmt.Errorf("failed to create namespace: %w\n%s", err, output)
	}
	
	fmt.Println("[pivot] Creating Hetzner credentials secret...")
	hcloudToken := os.Getenv("HCLOUD_TOKEN")
	if hcloudToken == "" {
		return "", fmt.Errorf("HCLOUD_TOKEN environment variable not set")
	}
	
	secretCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", mgmtKubeconfig,
		"-n", o.Namespace,
		"create", "secret", "generic", "hetzner-credentials",
		"--from-literal=hcloud="+hcloudToken,
		"--dry-run=client", "-o", "yaml",
	)
	secretYAML, err := secretCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to generate secret: %w", err)
	}
	
	applyCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", mgmtKubeconfig,
		"apply", "-f", "-",
	)
	applyCmd.Stdin = bytes.NewReader(secretYAML)
	if output, err := applyCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to create hetzner secret: %w\n%s", err, output)
	}
	
	// 3. Execute clusterctl move
	if err := o.move(ctx, mgmtKubeconfig); err != nil {
		return "", fmt.Errorf("clusterctl move failed: %w", err)
	}
	
	return mgmtKubeconfig, nil
}

// WaitForReady waits for providers and cluster to be ready after move
func (o *Orchestrator) WaitForReady(ctx context.Context, mgmtKubeconfig string) error {
	// Recreate Hetzner credentials secret (clusterctl move doesn't move secrets)
	fmt.Println("[pivot] Recreating Hetzner credentials secret...")
	hcloudToken := os.Getenv("HCLOUD_TOKEN")
	if hcloudToken == "" {
		return fmt.Errorf("HCLOUD_TOKEN environment variable required")
	}
	
	secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: hetzner-credentials
  namespace: %s
type: Opaque
stringData:
  hcloud: %s
  hetzner-robot-user: ""
  hetzner-robot-password: ""
  hcloud-ssh-key-name: ""
`, o.Namespace, hcloudToken)
	
	cmd := exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", mgmtKubeconfig,
		"-f", "-",
	)
	cmd.Stdin = bytes.NewReader([]byte(secretYAML))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create hetzner secret: %w\n%s", err, output)
	}
	
	// Wait for providers ready on Management Cluster
	if err := o.waitForProvidersReady(ctx, mgmtKubeconfig, 5*time.Minute); err != nil {
		return fmt.Errorf("providers not ready after pivot: %w", err)
	}
	
	// Wait for cluster ready on Management Cluster
	if err := o.waitForClusterReady(ctx, mgmtKubeconfig, 20*time.Minute); err != nil {
		return fmt.Errorf("cluster not ready after pivot: %w", err)
	}
	
	return nil
}

// Execute performs the complete pivot operation (deprecated, use ExecuteMove + WaitForReady)
func (o *Orchestrator) Execute(ctx context.Context) (string, error) {
	// 0. Ensure clusterctl is installed
	clusterctlMgr, err := binaries.NewClusterctlManager()
	if err != nil {
		return "", fmt.Errorf("failed to create clusterctl manager: %w", err)
	}
	
	if err := clusterctlMgr.EnsureInstalled(ctx); err != nil {
		return "", fmt.Errorf("failed to install clusterctl: %w", err)
	}
	
	// 1. Retrieve Management Cluster kubeconfig
	mgmtKubeconfig, err := o.getKubeconfig(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve kubeconfig: %w", err)
	}
	
	// 2. Install cluster-api-operator on Management Cluster
	if err := o.installOperatorOnMgmt(ctx, mgmtKubeconfig); err != nil {
		return "", fmt.Errorf("failed to install operator on mgmt cluster: %w", err)
	}
	
	// 4. Execute clusterctl move
	if err := o.move(ctx, mgmtKubeconfig); err != nil {
		return "", fmt.Errorf("clusterctl move failed: %w", err)
	}
	
	// Wait for ready
	if err := o.WaitForReady(ctx, mgmtKubeconfig); err != nil {
		return "", err
	}
	
	return mgmtKubeconfig, nil
}

func (o *Orchestrator) getKubeconfig(ctx context.Context) (string, error) {
	secretName := fmt.Sprintf("%s-kubeconfig", o.ClusterName)
	
	// Always fetch fresh kubeconfig (load balancer IP may have changed)
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", o.BootstrapKubeconfig,
		"get", "secret", secretName,
		"-n", o.Namespace,
		"-o", "jsonpath={.data.value}",
	)
	
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	
	decoded, decodeErr := base64.StdEncoding.DecodeString(string(output))
	if decodeErr != nil {
		return "", decodeErr
	}
	
	// Save to temp file
	tmpDir := os.TempDir()
	path := filepath.Join(tmpDir, fmt.Sprintf("%s.kubeconfig", o.ClusterName))
	if err = os.WriteFile(path, decoded, 0600); err != nil {
		return "", err
	}
	
	return path, nil
}

func (o *Orchestrator) installOperatorOnMgmt(ctx context.Context, mgmtKubeconfig string) error {
	// 1. Install cert-manager (required for operator webhooks)
	fmt.Println("[pivot] Installing cert-manager...")
	certMgrManifest, err := assets.ReadManifest("core/cert-manager/install.yaml")
	if err != nil {
		return fmt.Errorf("failed to read cert-manager manifest: %w", err)
	}
	
	// Use create with --save-config for initial install (faster than apply)
	cmd := exec.CommandContext(ctx, "kubectl", "create",
		"--kubeconfig", mgmtKubeconfig,
		"--save-config",
		"--validate=false",
		"-f", "-",
	)
	cmd.Stdin = bytes.NewReader(certMgrManifest)
	output, err := cmd.CombinedOutput()
	if err != nil && !bytes.Contains(output, []byte("AlreadyExists")) {
		return fmt.Errorf("cert-manager install failed: %w\n%s", err, output)
	}
	fmt.Println("[pivot] ✓ cert-manager manifests applied")
	
	// 2. Wait for cert-manager webhook
	fmt.Println("[pivot] Waiting for cert-manager webhook...")
	cmd = exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", mgmtKubeconfig,
		"wait", "deployment",
		"-n", "cert-manager",
		"cert-manager-webhook",
		"--for=condition=Available",
		"--timeout=3m",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cert-manager webhook not ready: %w\n%s", err, output)
	}
	
	// Wait additional time for cert-manager to be fully operational
	time.Sleep(30 * time.Second)
	
	// 3. Install full operator manifest (excluding CRDs)
	fmt.Println("[pivot] Installing cluster-api-operator...")
	operatorManifest, err := assets.ReadManifest("core/capi-operator/install.yaml")
	if err != nil {
		return fmt.Errorf("failed to read operator manifest: %w", err)
	}
	
	// Filter out CRDs from manifest (they have caBundle issues)
	// Apply only non-CRD resources
	cmd = exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", mgmtKubeconfig,
		"-f", "-",
	)
	
	// Use kubectl to filter out CRDs
	filterCmd := exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", mgmtKubeconfig,
		"--dry-run=client",
		"-o", "yaml",
		"-f", "-",
	)
	filterCmd.Stdin = bytes.NewReader(operatorManifest)
	filteredOutput, err := filterCmd.Output()
	if err != nil {
		// If dry-run fails, try direct apply
		cmd.Stdin = bytes.NewReader(operatorManifest)
		if output, err := cmd.CombinedOutput(); err != nil {
			// Ignore CRD errors, they'll be handled by cert-manager eventually
			if !bytes.Contains(output, []byte("caBundle")) {
				return fmt.Errorf("operator apply failed: %w\n%s", err, output)
			}
			fmt.Println("[pivot] ⚠ CRD caBundle warnings (will be fixed by cert-manager)")
		}
	} else {
		cmd.Stdin = bytes.NewReader(filteredOutput)
		if output, err := cmd.CombinedOutput(); err != nil {
			if !bytes.Contains(output, []byte("caBundle")) {
				return fmt.Errorf("operator apply failed: %w\n%s", err, output)
			}
			fmt.Println("[pivot] ⚠ CRD caBundle warnings (will be fixed by cert-manager)")
		}
	}
	
	// 4. Wait for operator ready
	fmt.Println("[pivot] Waiting for operator...")
	cmd = exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", mgmtKubeconfig,
		"wait", "deployment",
		"-n", "capi-operator-system",
		"capi-operator-controller-manager",
		"--for=condition=Available",
		"--timeout=3m",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("operator not ready: %w\n%s", err, output)
	}
	
	// 5. Apply provider manifests
	fmt.Println("[pivot] Applying provider manifests...")
	if err := o.applyProviders(ctx, mgmtKubeconfig); err != nil {
		return fmt.Errorf("failed to apply providers: %w", err)
	}
	
	// 6. Wait for CAPI CRDs to be installed by providers
	fmt.Println("[pivot] Waiting for CAPI CRDs...")
	if err := o.waitForCAPICRDs(ctx, mgmtKubeconfig, 5*time.Minute); err != nil {
		return fmt.Errorf("CAPI CRDs not ready: %w", err)
	}
	
	return nil
}

func (o *Orchestrator) waitForCAPICRDs(ctx context.Context, kubeconfig string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	
	requiredCRDs := []string{
		"clusters.cluster.x-k8s.io",
		"machines.cluster.x-k8s.io",
		"machinedeployments.cluster.x-k8s.io",
	}
	
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for CAPI CRDs")
			}
			
			allReady := true
			for _, crd := range requiredCRDs {
				cmd := exec.CommandContext(ctx, "kubectl",
					"--kubeconfig", kubeconfig,
					"get", "crd", crd,
				)
				if err := cmd.Run(); err != nil {
					allReady = false
					break
				}
			}
			
			if allReady {
				fmt.Println("[pivot] ✓ CAPI CRDs ready")
				return nil
			}
			
			fmt.Println("[pivot] Waiting for CAPI CRDs to be installed...")
		}
	}
}

func (o *Orchestrator) applyProviders(ctx context.Context, kubeconfig string) error {
	// Determine which providers to install based on OS type
	var bootstrapProvider, controlPlaneProvider string
	if o.OSType == "talos" {
		bootstrapProvider = "bootstrap-provider-talos.yaml"
		controlPlaneProvider = "controlplane-provider-talos.yaml"
	} else {
		bootstrapProvider = "bootstrap-provider-kubeadm.yaml"
		controlPlaneProvider = "controlplane-provider-kubeadm.yaml"
	}
	
	providers := []string{
		"core-provider.yaml",
		bootstrapProvider,
		controlPlaneProvider,
		"infrastructure-provider-hetzner.yaml",
	}
	
	for _, provider := range providers {
		manifest, err := assets.ReadManifest(fmt.Sprintf("core/capi-operator/providers/%s", provider))
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", provider, err)
		}
		
		cmd := exec.CommandContext(ctx, "kubectl", "apply",
			"--kubeconfig", kubeconfig,
			"-f", "-",
		)
		cmd.Stdin = bytes.NewReader(manifest)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to apply %s: %w\n%s", provider, err, output)
		}
	}
	
	return nil
}

func (o *Orchestrator) move(ctx context.Context, mgmtKubeconfig string) error {
	clusterctlMgr, _ := binaries.NewClusterctlManager()
	clusterctlPath := clusterctlMgr.GetPath()
	
	cmd := exec.CommandContext(ctx, clusterctlPath, "move",
		"--to-kubeconfig", mgmtKubeconfig,
		"--namespace", o.Namespace,
	)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("move failed: %w\n%s", err, output)
	}
	
	return nil
}

func (o *Orchestrator) countResources(ctx context.Context, kubeconfig string) (int, error) {
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfig,
		"get", "clusters,machines,hetznerclusters",
		"-n", o.Namespace,
		"-o", "json",
	)
	
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	
	// Simple count: count occurrences of "kind"
	count := bytes.Count(output, []byte(`"kind":`))
	return count, nil
}

func (o *Orchestrator) waitForProvidersReady(ctx context.Context, kubeconfig string, timeout time.Duration) error {
	// Determine provider names based on OS type
	var bootstrapProvider, controlPlaneProvider string
	if o.OSType == "talos" {
		bootstrapProvider = "talos"
		controlPlaneProvider = "talos"
	} else {
		// ubuntu uses kubeadm
		bootstrapProvider = "kubeadm"
		controlPlaneProvider = "kubeadm"
	}
	
	providers := []struct {
		kind string
		name string
	}{
		{"CoreProvider", "cluster-api"},
		{"BootstrapProvider", bootstrapProvider},
		{"ControlPlaneProvider", controlPlaneProvider},
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
					"--kubeconfig", kubeconfig,
					"get", provider.kind, provider.name,
					"-n", "capi-operator-system",
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}",
				)
				
				output, err := cmd.Output()
				if err != nil {
					continue
				}
				
				if string(output) == "True" {
					fmt.Printf("[pivot] ✓ %s/%s ready on Management Cluster\n", provider.kind, provider.name)
					goto nextProvider
				}
			}
		}
		nextProvider:
	}
	
	return nil
}

func (o *Orchestrator) waitForClusterReady(ctx context.Context, kubeconfig string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for cluster Ready condition")
			}
			
			cmd := exec.CommandContext(ctx, "kubectl",
				"--kubeconfig", kubeconfig,
				"get", "cluster", o.ClusterName,
				"-n", o.Namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}",
			)
			
			output, err := cmd.Output()
			if err != nil {
				continue
			}
			
			if string(output) == "True" {
				fmt.Println("[pivot] ✓ Cluster Ready condition satisfied")
				return nil
			}
			
			fmt.Println("[pivot] Waiting for cluster Ready condition...")
		}
	}
}
