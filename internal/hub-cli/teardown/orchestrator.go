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
	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
	"github.com/soloz-io/zero-ops/internal/hub-cli/state"
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
		// Try multiple default locations
		possiblePaths := []string{
			fmt.Sprintf("k8-secrets/kubeconfig/%s.kubeconfig", o.ClusterName),
			fmt.Sprintf("%s.kubeconfig", o.ClusterName),
		}

		for _, path := range possiblePaths {
			if _, err := os.Stat(path); err == nil {
				kubeconfig = path
				break
			}
		}

		// If no kubeconfig found, try kind context
		if kubeconfig == "" {
			kubeconfig = fmt.Sprintf("--context=kind-%s", o.ClusterName)
		}
	}

	if o.Force {
		return o.forceDelete(ctx)
	}

	return o.gracefulDelete(ctx, kubeconfig)
}

func (o *Orchestrator) gracefulDelete(ctx context.Context, kubeconfig string) error {
	fmt.Println("\n[teardown] Starting graceful deletion via CAPI...")

	// Check if using context or kubeconfig file
	useContext := strings.HasPrefix(kubeconfig, "--context=")

	if !useContext {
		// Check if kubeconfig file exists
		if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
			fmt.Printf("[teardown] ⚠️  Kubeconfig not found: %s\n", kubeconfig)
			fmt.Println("[teardown] Skipping CAPI deletion, proceeding to local cleanup")
			return o.localCleanup()
		}
	}

	// Delete Cluster resources in both namespaces (old and new)
	namespaces := []string{constants.NamespaceCAPI, "hub-platform-capi"}

	for _, ns := range namespaces {
		fmt.Printf("[teardown] Deleting Cluster resource '%s' in namespace '%s'...\n", o.ClusterName, ns)

		var cmd *exec.Cmd
		if useContext {
			contextName := strings.TrimPrefix(kubeconfig, "--context=")
			cmd = exec.CommandContext(ctx, "kubectl", "--context", contextName,
				"delete", "cluster", o.ClusterName, "-n", ns, "--wait=false", "--ignore-not-found")
		} else {
			cmd = exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
				"delete", "cluster", o.ClusterName, "-n", ns, "--wait=false", "--ignore-not-found")
		}

		if o.Debug {
			fmt.Printf("[DEBUG] kubectl %v\n", cmd.Args)
		}

		if output, err := cmd.CombinedOutput(); err != nil {
			if o.Debug {
				fmt.Printf("[DEBUG] kubectl output: %s\n", string(output))
			}
			if strings.Contains(string(output), "not found") || strings.Contains(string(output), "NotFound") {
				if o.Debug {
					fmt.Printf("[teardown] Cluster resource not found in %s (expected if never bootstrapped)\n", ns)
				}
			} else {
				fmt.Printf("[teardown] ⚠️  Failed to delete cluster in %s: %v\n", ns, err)
			}
		} else {
			fmt.Printf("[teardown] ✓ Cluster deletion initiated in %s\n", ns)
		}
	}

	// Wait for deletion cascade (15 minutes)
	fmt.Println("[teardown] Waiting for CAPI deletion cascade (timeout: 15m)...")

	if err := o.waitForDeletion(ctx, kubeconfig, useContext, 15*time.Minute); err != nil {
		fmt.Printf("[teardown] ⚠️  Deletion timeout: %v\n", err)
		fmt.Println("[teardown] Resources may still be deleting. Check Hetzner Console.")
		fmt.Println("[teardown] Use --force --confirm to force deletion if stuck.")
	} else {
		fmt.Println("[teardown] ✓ All CAPI resources deleted")
	}

	// Clean up any remaining volumes via Hetzner API
	fmt.Println("[teardown] Cleaning up any remaining volumes...")
	hcloudToken := os.Getenv("HCLOUD_TOKEN")
	if hcloudToken == "" {
		// Try to load token from file
		if tokenBytes, err := os.ReadFile("k8-secrets/hetzner/token"); err == nil {
			hcloudToken = strings.TrimSpace(string(tokenBytes))
		}
	}

	if hcloudToken != "" {
		client := hcloud.NewClient(hcloud.WithToken(hcloudToken))

		allVolumes, err := client.Volume.All(ctx)
		if err != nil {
			fmt.Printf("[teardown] ⚠️  Failed to list volumes for cleanup: %v\n", err)
		} else {
			for _, volume := range allVolumes {
				fmt.Printf("[teardown] Deleting remaining volume: %s (ID: %d, Size: %d GB)\n", volume.Name, volume.ID, volume.Size)
				if _, err := client.Volume.Delete(ctx, volume); err != nil {
					fmt.Printf("[teardown] ⚠️  Failed to delete volume %s: %v\n", volume.Name, err)
				} else {
					fmt.Printf("[teardown] ✓ Deleted remaining volume: %s\n", volume.Name)
				}
			}
		}
	}

	// Local cleanup
	return o.localCleanup()
}

func (o *Orchestrator) waitForDeletion(ctx context.Context, kubeconfig string, useContext bool, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	namespaces := []string{constants.NamespaceCAPI, "hub-platform-capi"}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for deletion")
		case <-ticker.C:
			allDeleted := true

			for _, ns := range namespaces {
				var cmd *exec.Cmd
				if useContext {
					contextName := strings.TrimPrefix(kubeconfig, "--context=")
					cmd = exec.CommandContext(ctx, "kubectl", "--context", contextName,
						"get", "cluster", o.ClusterName, "-n", ns, "--ignore-not-found")
				} else {
					cmd = exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
						"get", "cluster", o.ClusterName, "-n", ns, "--ignore-not-found")
				}

				output, err := cmd.CombinedOutput()
				if err == nil && len(output) > 0 && !strings.Contains(string(output), "No resources found") {
					allDeleted = false
					break
				}
			}

			if allDeleted {
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
		// Try to load token from file
		if tokenBytes, err := os.ReadFile("k8-secrets/hetzner/token"); err == nil {
			hcloudToken = strings.TrimSpace(string(tokenBytes))
		}
	}

	if hcloudToken == "" {
		return fmt.Errorf("HCLOUD_TOKEN environment variable required for force deletion")
	}

	client := hcloud.NewClient(hcloud.WithToken(hcloudToken))

	// Query resources by CAPH cluster label pattern (caph-cluster-<name>-*)
	labelPattern := fmt.Sprintf("caph-cluster-%s", o.ClusterName)

	fmt.Printf("[teardown] Querying Hetzner resources with label pattern: %s-*\n", labelPattern)

	// Delete servers
	allServers, err := client.Server.All(ctx)
	if err != nil {
		return fmt.Errorf("failed to list servers: %w", err)
	}

	for _, server := range allServers {
		// Check if any label key starts with our pattern
		for labelKey := range server.Labels {
			if strings.HasPrefix(labelKey, labelPattern) {
				fmt.Printf("[teardown] Deleting server: %s (ID: %d)\n", server.Name, server.ID)
				if _, _, err := client.Server.DeleteWithResult(ctx, server); err != nil {
					fmt.Printf("[teardown] ⚠️  Failed to delete server %s: %v\n", server.Name, err)
				} else {
					fmt.Printf("[teardown] ✓ Deleted server: %s\n", server.Name)
				}
				break
			}
		}
	}

	// Delete load balancers
	allLBs, err := client.LoadBalancer.All(ctx)
	if err != nil {
		return fmt.Errorf("failed to list load balancers: %w", err)
	}

	for _, lb := range allLBs {
		fmt.Printf("[teardown] Deleting load balancer: %s (ID: %d)\n", lb.Name, lb.ID)
		if _, err := client.LoadBalancer.Delete(ctx, lb); err != nil {
			fmt.Printf("[teardown] ⚠️  Failed to delete load balancer %s: %v\n", lb.Name, err)
		} else {
			fmt.Printf("[teardown] ✓ Deleted load balancer: %s\n", lb.Name)
		}
	}

	// Delete networks
	allNetworks, err := client.Network.All(ctx)
	if err != nil {
		return fmt.Errorf("failed to list networks: %w", err)
	}

	for _, network := range allNetworks {
		// Check if network name starts with cluster name
		if strings.HasPrefix(network.Name, o.ClusterName) {
			fmt.Printf("[teardown] Deleting network: %s (ID: %d)\n", network.Name, network.ID)
			if _, err := client.Network.Delete(ctx, network); err != nil {
				fmt.Printf("[teardown] ⚠️  Failed to delete network %s: %v\n", network.Name, err)
			} else {
				fmt.Printf("[teardown] ✓ Deleted network: %s\n", network.Name)
			}
		}
	}

	// Delete volumes
	fmt.Println("[teardown] Deleting associated volumes...")
	allVolumes, err := client.Volume.All(ctx)
	if err != nil {
		return fmt.Errorf("failed to list volumes: %w", err)
	}

	for _, volume := range allVolumes {
		fmt.Printf("[teardown] Deleting volume: %s (ID: %d, Size: %d GB)\n", volume.Name, volume.ID, volume.Size)
		if _, err := client.Volume.Delete(ctx, volume); err != nil {
			fmt.Printf("[teardown] ⚠️  Failed to delete volume %s: %v\n", volume.Name, err)
		} else {
			fmt.Printf("[teardown] ✓ Deleted volume: %s\n", volume.Name)
		}
	}

	fmt.Println("[teardown] ✓ Force deletion complete")

	// Local cleanup
	return o.localCleanup()
}

func (o *Orchestrator) localCleanup() error {
	fmt.Println("\n[teardown] Cleaning up local files...")

	// Delete kind cluster
	fmt.Printf("[teardown] Deleting kind cluster: %s...\n", o.ClusterName)
	cmd := exec.Command("kind", "delete", "cluster", "--name", o.ClusterName)
	if o.Debug {
		fmt.Printf("[DEBUG] Running: kind delete cluster --name %s\n", o.ClusterName)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		if o.Debug {
			fmt.Printf("[DEBUG] kind delete output: %s\n", string(output))
			fmt.Printf("[DEBUG] kind delete error: %v\n", err)
		}
		if !strings.Contains(string(output), "not found") {
			fmt.Printf("[teardown] ⚠️  Failed to delete kind cluster: %v\n%s\n", err, string(output))
		}
	} else {
		fmt.Printf("[teardown] ✓ Deleted kind cluster: %s\n", o.ClusterName)
	}

	// Prune Docker artifacts left by kind + local-path-storage PVCs
	fmt.Println("[teardown] Pruning Docker volumes, containers, and networks...")
	pruneContainers := exec.Command("docker", "container", "prune", "-f")
	pruneContainers.Run()
	pruneVolumes := exec.Command("docker", "volume", "prune", "-f")
	if out, err := pruneVolumes.CombinedOutput(); err != nil {
		fmt.Printf("[teardown] ⚠️  Docker volume prune: %v\n%s\n", err, out)
	} else {
		fmt.Println("[teardown] ✓ Docker volumes pruned")
	}
	pruneBuildCache := exec.Command("docker", "builder", "prune", "-f")
	pruneBuildCache.Run()

	// Remove kubeconfig from k8-secrets/kubeconfig/
	kubeconfigPath := fmt.Sprintf("k8-secrets/kubeconfig/%s.kubeconfig", o.ClusterName)
	if err := os.Remove(kubeconfigPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("[teardown] ⚠️  Failed to remove kubeconfig: %v\n", err)
	} else if err == nil {
		fmt.Printf("[teardown] ✓ Removed %s\n", kubeconfigPath)
	}

	// Remove legacy kubeconfig location
	legacyKubeconfigPath := fmt.Sprintf("%s.kubeconfig", o.ClusterName)
	if err := os.Remove(legacyKubeconfigPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("[teardown] ⚠️  Failed to remove legacy kubeconfig: %v\n", err)
	} else if err == nil {
		fmt.Printf("[teardown] ✓ Removed %s\n", legacyKubeconfigPath)
	}

	// Remove talosconfig
	talosconfigPath := fmt.Sprintf("%s.talosconfig", o.ClusterName)
	if err := os.Remove(talosconfigPath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("[teardown] ⚠️  Failed to remove talosconfig: %v\n", err)
	} else if err == nil {
		fmt.Printf("[teardown] ✓ Removed %s\n", talosconfigPath)
	}

	// Remove state file
	statePath := filepath.Join(".zero-ops", "state", fmt.Sprintf("%s.json", o.ClusterName))
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("[teardown] ⚠️  Failed to remove state file: %v\n", err)
	} else if err == nil {
		fmt.Printf("[teardown] ✓ Removed state file\n")
	}

	// Remove bootstrap state file
	bootstrapStatePath := filepath.Join(".zero-ops", "bootstrap-state.json")
	if err := os.Remove(bootstrapStatePath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("[teardown] ⚠️  Failed to remove bootstrap state file: %v\n", err)
	} else if err == nil {
		fmt.Printf("[teardown] ✓ Removed bootstrap state file\n")
	}

	// TODO: Remove context from ~/.kube/config if merged

	fmt.Println("[teardown] ✓ Local cleanup complete")
	return nil
}
