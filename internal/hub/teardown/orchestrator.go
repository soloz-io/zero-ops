package teardown

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/hcloud"
	"github.com/soloz-io/zero-ops/internal/hub/constants"
	"github.com/soloz-io/zero-ops/internal/hub/state"
)

// Orchestrator manages the teardown process
type Orchestrator struct {
	ClusterName string
	Force       bool
	Debug       bool
}

func (o *Orchestrator) Run(ctx context.Context) error {
	if o.Debug {
		fmt.Println("[DEBUG] Teardown.Run() started")
		fmt.Printf("[DEBUG] ClusterName: %s, Force: %v\n", o.ClusterName, o.Force)
	}

	// Load state to get kubeconfig path
	stateMgr := state.NewStateManager(o.ClusterName)
	bootstrapState, err := stateMgr.Load()
	if err != nil {
		if o.Debug {
			fmt.Printf("[DEBUG] No state found: %v\n", err)
		}
		// Continue with local cleanup even if state not found
	}

	var kubeconfig string
	if bootstrapState != nil && bootstrapState.MgmtKubeconfig != "" {
		kubeconfig = bootstrapState.MgmtKubeconfig
	} else {
		// Try default location
		kubeconfig = fmt.Sprintf("%s.kubeconfig", o.ClusterName)
	}

	if o.Force {
		return o.forceDelete(ctx)
	}

	return o.gracefulDelete(ctx, kubeconfig)
}

func (o *Orchestrator) gracefulDelete(ctx context.Context, kubeconfig string) error {
	fmt.Println("\n[teardown] Starting graceful deletion via CAPI...")

	// Check if kubeconfig exists
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		fmt.Printf("[teardown] ⚠️  Kubeconfig not found: %s\n", kubeconfig)
		fmt.Println("[teardown] Skipping CAPI deletion, proceeding to local cleanup")
		return o.localCleanup()
	}

	// Delete Cluster resource
	fmt.Printf("[teardown] Deleting Cluster resource '%s'...\n", o.ClusterName)
	cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
		"delete", "cluster", o.ClusterName, "-n", constants.NamespaceCAPI, "--wait=false")
	
	if o.Debug {
		fmt.Printf("[DEBUG] kubectl %v\n", cmd.Args)
	}
	
	if output, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(output), "not found") {
			fmt.Println("[teardown] Cluster resource not found, may already be deleted")
		} else {
			return fmt.Errorf("failed to delete cluster: %w\nOutput: %s", err, string(output))
		}
	} else {
		fmt.Println("[teardown] ✓ Cluster deletion initiated")
	}

	// Wait for deletion cascade (15 minutes)
	fmt.Println("[teardown] Waiting for CAPI deletion cascade (timeout: 15m)...")
	if err := o.waitForDeletion(ctx, kubeconfig, 15*time.Minute); err != nil {
		fmt.Printf("[teardown] ⚠️  Deletion timeout: %v\n", err)
		fmt.Println("[teardown] Resources may still be deleting. Check Hetzner Console.")
		fmt.Println("[teardown] Use --force --confirm to force deletion if stuck.")
	} else {
		fmt.Println("[teardown] ✓ All CAPI resources deleted")
	}

	// Local cleanup
	return o.localCleanup()
}

func (o *Orchestrator) waitForDeletion(ctx context.Context, kubeconfig string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for deletion")
		case <-ticker.C:
			cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
				"get", "cluster", o.ClusterName, "-n", constants.NamespaceCAPI, "--ignore-not-found")
			
			output, err := cmd.CombinedOutput()
			if err != nil || len(output) == 0 || strings.Contains(string(output), "No resources found") {
				// Cluster deleted
				return nil
			}
			
			if o.Debug {
				fmt.Printf("[DEBUG] Cluster still exists, waiting...\n")
			} else {
				fmt.Print(".")
			}
		}
	}
}

func (o *Orchestrator) forceDelete(ctx context.Context) error {
	fmt.Println("\n[teardown] ⚠️  Force deletion mode enabled")
	fmt.Println("[teardown] This will delete resources directly via Hetzner API")

	hcloudToken := os.Getenv("HCLOUD_TOKEN")
	if hcloudToken == "" {
		return fmt.Errorf("HCLOUD_TOKEN environment variable required for force deletion")
	}

	client := hcloud.NewClient(hcloud.WithToken(hcloudToken))

	// Query resources by cluster label
	clusterLabel := fmt.Sprintf("cluster.x-k8s.io/cluster-name=%s", o.ClusterName)
	
	fmt.Printf("[teardown] Querying Hetzner resources with label: %s\n", clusterLabel)

	// Delete servers
	servers, err := client.Server.AllWithOpts(ctx, hcloud.ServerListOpts{
		ListOpts: hcloud.ListOpts{LabelSelector: clusterLabel},
	})
	if err != nil {
		return fmt.Errorf("failed to list servers: %w", err)
	}

	for _, server := range servers {
		fmt.Printf("[teardown] Deleting server: %s (ID: %d)\n", server.Name, server.ID)
		if _, _, err := client.Server.DeleteWithResult(ctx, server); err != nil {
			fmt.Printf("[teardown] ⚠️  Failed to delete server %s: %v\n", server.Name, err)
		}
	}

	// Delete load balancers
	lbs, err := client.LoadBalancer.AllWithOpts(ctx, hcloud.LoadBalancerListOpts{
		ListOpts: hcloud.ListOpts{LabelSelector: clusterLabel},
	})
	if err != nil {
		return fmt.Errorf("failed to list load balancers: %w", err)
	}

	for _, lb := range lbs {
		fmt.Printf("[teardown] Deleting load balancer: %s (ID: %d)\n", lb.Name, lb.ID)
		if _, err := client.LoadBalancer.Delete(ctx, lb); err != nil {
			fmt.Printf("[teardown] ⚠️  Failed to delete load balancer %s: %v\n", lb.Name, err)
		}
	}

	// Delete networks
	networks, err := client.Network.AllWithOpts(ctx, hcloud.NetworkListOpts{
		ListOpts: hcloud.ListOpts{LabelSelector: clusterLabel},
	})
	if err != nil {
		return fmt.Errorf("failed to list networks: %w", err)
	}

	for _, network := range networks {
		fmt.Printf("[teardown] Deleting network: %s (ID: %d)\n", network.Name, network.ID)
		if _, err := client.Network.Delete(ctx, network); err != nil {
			fmt.Printf("[teardown] ⚠️  Failed to delete network %s: %v\n", network.Name, err)
		}
	}

	fmt.Println("[teardown] ✓ Force deletion complete")

	// Local cleanup
	return o.localCleanup()
}

func (o *Orchestrator) localCleanup() error {
	fmt.Println("\n[teardown] Cleaning up local files...")

	// Remove kubeconfig
	kubeconfigPath := fmt.Sprintf("%s.kubeconfig", o.ClusterName)
	if err := os.Remove(kubeconfigPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("[teardown] ⚠️  Failed to remove kubeconfig: %v\n", err)
	} else if err == nil {
		fmt.Printf("[teardown] ✓ Removed %s\n", kubeconfigPath)
	}

	// Remove talosconfig
	talosconfigPath := fmt.Sprintf("%s.talosconfig", o.ClusterName)
	if err := os.Remove(talosconfigPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("[teardown] ⚠️  Failed to remove talosconfig: %v\n", err)
	} else if err == nil {
		fmt.Printf("[teardown] ✓ Removed %s\n", talosconfigPath)
	}

	// Remove state file
	homeDir, _ := os.UserHomeDir()
	statePath := filepath.Join(homeDir, ".zero-ops", "state", fmt.Sprintf("%s.json", o.ClusterName))
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("[teardown] ⚠️  Failed to remove state file: %v\n", err)
	} else if err == nil {
		fmt.Printf("[teardown] ✓ Removed state file\n")
	}

	// TODO: Remove context from ~/.kube/config if merged

	fmt.Println("[teardown] ✓ Local cleanup complete")
	return nil
}
