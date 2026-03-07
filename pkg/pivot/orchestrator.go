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

// Execute performs the pivot operation
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
	
	// 3. Count resources before pivot
	beforeCounts, err := o.countResources(ctx, o.BootstrapKubeconfig)
	if err != nil {
		return "", fmt.Errorf("failed to count resources before pivot: %w", err)
	}
	
	// 4. Execute clusterctl move
	if err := o.move(ctx, mgmtKubeconfig); err != nil {
		return "", fmt.Errorf("clusterctl move failed: %w", err)
	}
	
	// 5. Count resources after pivot
	afterCounts, err := o.countResources(ctx, mgmtKubeconfig)
	if err != nil {
		return "", fmt.Errorf("failed to count resources after pivot: %w", err)
	}
	
	// 6. Verify counts match
	if beforeCounts != afterCounts {
		return "", fmt.Errorf("resource count mismatch: before=%d, after=%d", beforeCounts, afterCounts)
	}
	
	// 7. Wait for providers ready on Management Cluster
	if err := o.waitForProvidersReady(ctx, mgmtKubeconfig, 5*time.Minute); err != nil {
		return "", fmt.Errorf("providers not ready after pivot: %w", err)
	}
	
	// 8. Wait for cluster ready on Management Cluster
	if err := o.waitForClusterReady(ctx, mgmtKubeconfig, 10*time.Minute); err != nil {
		return "", fmt.Errorf("cluster not ready after pivot: %w", err)
	}
	
	return mgmtKubeconfig, nil
}

func (o *Orchestrator) getKubeconfig(ctx context.Context) (string, error) {
	secretName := fmt.Sprintf("%s-kubeconfig", o.ClusterName)
	
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
	
	decoded, err := base64.StdEncoding.DecodeString(string(output))
	if err != nil {
		return "", err
	}
	
	// Save to temp file
	tmpDir := os.TempDir()
	path := filepath.Join(tmpDir, fmt.Sprintf("%s.kubeconfig", o.ClusterName))
	if err := os.WriteFile(path, decoded, 0600); err != nil {
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
	
	cmd := exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", mgmtKubeconfig,
		"-f", "-",
	)
	cmd.Stdin = bytes.NewReader(certMgrManifest)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cert-manager apply failed: %w\n%s", err, output)
	}
	
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
	
	// 3. Install full operator manifest
	fmt.Println("[pivot] Installing cluster-api-operator...")
	operatorManifest, err := assets.ReadManifest("core/capi-operator/install.yaml")
	if err != nil {
		return fmt.Errorf("failed to read operator manifest: %w", err)
	}
	
	cmd = exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", mgmtKubeconfig,
		"--server-side",
		"-f", "-",
	)
	cmd.Stdin = bytes.NewReader(operatorManifest)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("operator apply failed: %w\n%s", err, output)
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
