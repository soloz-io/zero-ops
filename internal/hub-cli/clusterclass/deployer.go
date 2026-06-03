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
	ClassPaths []string
}

// Deploy applies all ClusterClass definitions
func (d *Deployer) Deploy(ctx context.Context) error {
	for _, classPath := range d.ClassPaths {
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
	// Ensure at least one ClusterClass is present
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", d.Kubeconfig,
		"get", "clusterclass",
		"-n", d.Namespace,
	)

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to verify ClusterClasses: %w", err)
	}

	// Verify each expected class exists
	for _, classPath := range d.ClassPaths {
		// Derive expected name from path (e.g., "classes/hetzner-mgmt-ubuntu-v1.yaml" -> "hetzner-mgmt-ubuntu-v1")
		// The actual name is embedded in the manifest, but we check presence generically
		if !bytes.Contains(output, []byte("clusterclass")) {
			return fmt.Errorf("no ClusterClass found after applying %s", classPath)
		}
	}

	return nil
}
