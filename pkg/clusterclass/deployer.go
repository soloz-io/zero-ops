package clusterclass

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"github.com/soloz-io/zero-ops/internal/assets"
)

// Deployer deploys ClusterClass library to Management Cluster
type Deployer struct {
	Kubeconfig string
	Namespace  string
}

// Deploy applies all ClusterClass definitions
func (d *Deployer) Deploy(ctx context.Context) error {
	classes := []string{
		"classes/hetzner-prod-talos-v1.yaml",
		"classes/hetzner-dev-talos-v1.yaml",
		"classes/hetzner-staging-talos-v1.yaml",
	}
	
	for _, classPath := range classes {
		manifest, err := assets.ReadManifest(classPath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", classPath, err)
		}
		
		cmd := exec.CommandContext(ctx, "kubectl", "apply",
			"--kubeconfig", d.Kubeconfig,
			"-f", "-",
		)
		cmd.Stdin = bytes.NewReader(manifest)
		
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to apply %s: %w\n%s", classPath, err, output)
		}
		
		fmt.Printf("[clusterclass-deploy] ✓ Applied %s\n", classPath)
	}
	
	return d.verify(ctx)
}

func (d *Deployer) verify(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", d.Kubeconfig,
		"get", "clusterclass",
		"-n", d.Namespace,
	)
	
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to verify ClusterClasses: %w", err)
	}
	
	expected := []string{
		"hetzner-mgmt-talos-v1",
		"hetzner-prod-talos-v1",
		"hetzner-dev-talos-v1",
		"hetzner-staging-talos-v1",
	}
	
	for _, name := range expected {
		if !bytes.Contains(output, []byte(name)) {
			return fmt.Errorf("ClusterClass %s not found", name)
		}
	}
	
	return nil
}
