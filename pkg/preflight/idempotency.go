package preflight

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/soloz-io/zero-ops/pkg/state"
)

// IdempotencyValidator checks for existing cluster
type IdempotencyValidator struct {
	ClusterName      string
	Namespace        string
	BootstrapContext string
	Upgrade          bool
}

func (v *IdempotencyValidator) Validate(ctx context.Context) error {
	mgr := state.NewStateManager(v.ClusterName)
	
	if !mgr.Exists() {
		return nil // No existing cluster
	}
	
	if v.Upgrade {
		return nil // Upgrade mode - allow to proceed
	}
	
	// Clean up stale resources automatically
	fmt.Printf("[preflight] Cleaning up stale resources for cluster '%s'...\n", v.ClusterName)
	
	kubectlContext := v.BootstrapContext
	if kubectlContext == "" {
		kubectlContext = "kind-bootstrap-zero-ops"
	}
	
	// Delete cluster first (required before ClusterClass can be deleted)
	cmd := exec.CommandContext(ctx, "kubectl", "delete", "cluster", v.ClusterName,
		"--context", kubectlContext, "-n", v.Namespace, "--wait=true", "--timeout=60s", "--ignore-not-found=true")
	if err := cmd.Run(); err != nil {
		fmt.Printf("[preflight] Warning: failed to delete cluster: %v\n", err)
	}
	
	// Delete state file
	if err := mgr.Delete(); err != nil {
		fmt.Printf("[preflight] Warning: failed to delete state: %v\n", err)
	}
	
	fmt.Println("[preflight] ✓ Stale resources cleaned up")
	return nil
}
