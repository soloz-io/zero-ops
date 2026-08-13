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

// Orchestrator manages the forceful teardown process
type Orchestrator struct {
	ClusterName string
	Force       bool
	Debug       bool
}

// Run executes immediate forceful deletion of Kubernetes CAPI resources, Hetzner Cloud infra, Kind/Docker, and local state.
func (o *Orchestrator) Run(ctx context.Context) error {
	if o.Debug {
		fmt.Println("[DEBUG] Teardown.Run() started")
		fmt.Printf("[DEBUG] ClusterName: %s, Force: %v\n", o.ClusterName, o.Force)
	}

	fmt.Printf("\n[teardown] Starting forceful teardown of cluster '%s'...\n", o.ClusterName)

	// Step 1: Strip Kubernetes finalizers & delete CAPI CRs fast (non-blocking)
	o.stripKubernetesFinalizers(ctx)

	// Step 2: Delete Hetzner cloud infrastructure directly via Hetzner API
	if err := o.deleteHetznerResources(ctx); err != nil {
		fmt.Printf("[teardown] ⚠️  Hetzner cloud resource cleanup encountered warnings: %v\n", err)
	}

	// Step 3: Local Kind, Docker, and state cleanup
	if err := o.localCleanup(ctx); err != nil {
		fmt.Printf("[teardown] ⚠️  Local cleanup encountered warnings: %v\n", err)
	}

	fmt.Printf("\n✓ Teardown of cluster '%s' completed\n", o.ClusterName)
	return nil
}

// stripKubernetesFinalizers attempts fast non-blocking removal of finalizers on CAPI CRs
func (o *Orchestrator) stripKubernetesFinalizers(ctx context.Context) {
	stateMgr := state.NewStateManager(o.ClusterName)
	bootstrapState, _ := stateMgr.Load()

	var kubeconfig string
	if bootstrapState != nil && bootstrapState.MgmtKubeconfig != "" {
		kubeconfig = bootstrapState.MgmtKubeconfig
	} else {
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
		if kubeconfig == "" {
			kubeconfig = fmt.Sprintf("--context=kind-%s", o.ClusterName)
		}
	}

	useContext := strings.HasPrefix(kubeconfig, "--context=")
	var baseArgs []string
	if useContext {
		baseArgs = []string{"--context", strings.TrimPrefix(kubeconfig, "--context=")}
	} else if kubeconfig != "" {
		if _, err := os.Stat(kubeconfig); err != nil {
			return
		}
		baseArgs = []string{"--kubeconfig", kubeconfig}
	} else {
		return
	}

	fmt.Println("\n[teardown] Attempting fast Kubernetes finalizer stripping (timeout: 5s)...")
	kctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	namespaces := []string{constants.NamespaceCAPI, "hub-platform-capi", "platform-capi", "default"}
	resourceTypes := []string{"cluster", "hetznercluster", "kubeadmcontrolplane", "machinedeployment", "machine", "hcloudmachine"}

	for _, ns := range namespaces {
		for _, rType := range resourceTypes {
			getArgs := append(baseArgs, "get", rType, "-n", ns, "-o", "jsonpath={.items[*].metadata.name}")
			cmd := exec.CommandContext(kctx, "kubectl", getArgs...)
			out, err := cmd.Output()
			if err != nil || len(out) == 0 {
				continue
			}

			names := strings.Fields(string(out))
			for _, name := range names {
				if strings.Contains(name, o.ClusterName) || (rType == "cluster" && name == o.ClusterName) {
					// Patch finalizers to empty array
					patchArgs := append(baseArgs, "patch", rType, name, "-n", ns, "--type=merge", "-p", `{"metadata":{"finalizers":[]}}`)
					exec.CommandContext(kctx, "kubectl", patchArgs...).Run()
					// Delete immediately without waiting
					delArgs := append(baseArgs, "delete", rType, name, "-n", ns, "--wait=false", "--grace-period=0", "--force", "--ignore-not-found")
					exec.CommandContext(kctx, "kubectl", delArgs...).Run()
				}
			}
		}
	}
}

// deleteHetznerResources deletes all matching Hetzner cloud resources directly via the Hetzner API
func (o *Orchestrator) deleteHetznerResources(ctx context.Context) error {
	fmt.Println("\n[teardown] Forcefully deleting Hetzner Cloud infrastructure resources...")

	hcloudToken := os.Getenv("HCLOUD_TOKEN")
	if hcloudToken == "" {
		if tokenBytes, err := os.ReadFile("k8-secrets/hetzner/token"); err == nil {
			hcloudToken = strings.TrimSpace(string(tokenBytes))
		}
	}

	if hcloudToken == "" {
		fmt.Println("[teardown] ⚠️  HCLOUD_TOKEN not found; skipping Hetzner cloud resource cleanup")
		return nil
	}

	client := hcloud.NewClient(hcloud.WithToken(hcloudToken))
	labelPattern := fmt.Sprintf("caph-cluster-%s", o.ClusterName)

	matchesCluster := func(name string, labels map[string]string) bool {
		if strings.HasPrefix(name, o.ClusterName) || strings.Contains(name, o.ClusterName) {
			return true
		}
		for k, v := range labels {
			if strings.HasPrefix(k, labelPattern) || strings.Contains(k, o.ClusterName) {
				return true
			}
			if v == o.ClusterName || strings.Contains(v, o.ClusterName) {
				return true
			}
		}
		return false
	}

	deletedServerIDs := make(map[int]bool)

	// 1. Delete servers
	allServers, err := client.Server.All(ctx)
	if err == nil {
		for _, server := range allServers {
			if matchesCluster(server.Name, server.Labels) {
				deletedServerIDs[server.ID] = true
				fmt.Printf("[teardown] Deleting server: %s (ID: %d)\n", server.Name, server.ID)
				if _, _, err := client.Server.DeleteWithResult(ctx, server); err != nil {
					fmt.Printf("[teardown] ⚠️  Failed to delete server %s: %v\n", server.Name, err)
				} else {
					fmt.Printf("[teardown] ✓ Deleted server: %s\n", server.Name)
				}
			}
		}
	} else if o.Debug {
		fmt.Printf("[DEBUG] Failed to list servers: %v\n", err)
	}

	// 2. Delete load balancers
	allLBs, err := client.LoadBalancer.All(ctx)
	if err == nil {
		for _, lb := range allLBs {
			if matchesCluster(lb.Name, lb.Labels) {
				fmt.Printf("[teardown] Deleting load balancer: %s (ID: %d)\n", lb.Name, lb.ID)
				if _, err := client.LoadBalancer.Delete(ctx, lb); err != nil {
					fmt.Printf("[teardown] ⚠️  Failed to delete load balancer %s: %v\n", lb.Name, err)
				} else {
					fmt.Printf("[teardown] ✓ Deleted load balancer: %s\n", lb.Name)
				}
			}
		}
	} else if o.Debug {
		fmt.Printf("[DEBUG] Failed to list load balancers: %v\n", err)
	}

	// 3. Delete volumes
	allVolumes, err := client.Volume.All(ctx)
	if err == nil {
		for _, volume := range allVolumes {
			isAttachedToDeletedServer := volume.Server != nil && deletedServerIDs[volume.Server.ID]
			if matchesCluster(volume.Name, volume.Labels) || isAttachedToDeletedServer {
				fmt.Printf("[teardown] Deleting volume: %s (ID: %d, Size: %d GB)\n", volume.Name, volume.ID, volume.Size)
				if volume.Server != nil {
					client.Volume.Detach(ctx, volume)
				}
				if _, err := client.Volume.Delete(ctx, volume); err != nil {
					fmt.Printf("[teardown] ⚠️  Failed to delete volume %s: %v\n", volume.Name, err)
				} else {
					fmt.Printf("[teardown] ✓ Deleted volume: %s\n", volume.Name)
				}
			}
		}
	} else if o.Debug {
		fmt.Printf("[DEBUG] Failed to list volumes: %v\n", err)
	}

	// 4. Delete Placement Groups
	allPGs, err := client.PlacementGroup.All(ctx)
	if err == nil {
		for _, pg := range allPGs {
			if matchesCluster(pg.Name, pg.Labels) {
				fmt.Printf("[teardown] Deleting placement group: %s (ID: %d)\n", pg.Name, pg.ID)
				if _, err := client.PlacementGroup.Delete(ctx, pg); err != nil {
					fmt.Printf("[teardown] ⚠️  Failed to delete placement group %s: %v\n", pg.Name, err)
				} else {
					fmt.Printf("[teardown] ✓ Deleted placement group: %s\n", pg.Name)
				}
			}
		}
	} else if o.Debug {
		fmt.Printf("[DEBUG] Failed to list placement groups: %v\n", err)
	}

	// 5. Delete Firewalls
	allFirewalls, err := client.Firewall.All(ctx)
	if err == nil {
		for _, fw := range allFirewalls {
			if matchesCluster(fw.Name, fw.Labels) {
				fmt.Printf("[teardown] Deleting firewall: %s (ID: %d)\n", fw.Name, fw.ID)
				if _, err := client.Firewall.Delete(ctx, fw); err != nil {
					fmt.Printf("[teardown] ⚠️  Failed to delete firewall %s: %v\n", fw.Name, err)
				} else {
					fmt.Printf("[teardown] ✓ Deleted firewall: %s\n", fw.Name)
				}
			}
		}
	} else if o.Debug {
		fmt.Printf("[DEBUG] Failed to list firewalls: %v\n", err)
	}

	// 6. Delete Floating IPs
	allFloatingIPs, err := client.FloatingIP.All(ctx)
	if err == nil {
		for _, fip := range allFloatingIPs {
			if matchesCluster(fip.Name, fip.Labels) {
				fmt.Printf("[teardown] Deleting floating IP: %s (ID: %d)\n", fip.Name, fip.ID)
				if _, err := client.FloatingIP.Delete(ctx, fip); err != nil {
					fmt.Printf("[teardown] ⚠️  Failed to delete floating IP %s: %v\n", fip.Name, err)
				} else {
					fmt.Printf("[teardown] ✓ Deleted floating IP: %s\n", fip.Name)
				}
			}
		}
	} else if o.Debug {
		fmt.Printf("[DEBUG] Failed to list floating IPs: %v\n", err)
	}

	// 7. Delete Primary IPs
	allPrimaryIPs, err := client.PrimaryIP.All(ctx)
	if err == nil {
		for _, pip := range allPrimaryIPs {
			if matchesCluster(pip.Name, pip.Labels) {
				fmt.Printf("[teardown] Deleting primary IP: %s (ID: %d)\n", pip.Name, pip.ID)
				if _, err := client.PrimaryIP.Delete(ctx, pip); err != nil {
					fmt.Printf("[teardown] ⚠️  Failed to delete primary IP %s: %v\n", pip.Name, err)
				} else {
					fmt.Printf("[teardown] ✓ Deleted primary IP: %s\n", pip.Name)
				}
			}
		}
	} else if o.Debug {
		fmt.Printf("[DEBUG] Failed to list primary IPs: %v\n", err)
	}

	// 8. Delete Networks (with quick retry)
	allNetworks, err := client.Network.All(ctx)
	if err == nil {
		for _, network := range allNetworks {
			if matchesCluster(network.Name, network.Labels) {
				fmt.Printf("[teardown] Deleting network: %s (ID: %d)\n", network.Name, network.ID)
				if _, err := client.Network.Delete(ctx, network); err != nil {
					time.Sleep(2 * time.Second)
					if _, err := client.Network.Delete(ctx, network); err != nil {
						fmt.Printf("[teardown] ⚠️  Failed to delete network %s: %v\n", network.Name, err)
					} else {
						fmt.Printf("[teardown] ✓ Deleted network: %s (on retry)\n", network.Name)
					}
				} else {
					fmt.Printf("[teardown] ✓ Deleted network: %s\n", network.Name)
				}
			}
		}
	} else if o.Debug {
		fmt.Printf("[DEBUG] Failed to list networks: %v\n", err)
	}

	fmt.Println("[teardown] ✓ Hetzner Cloud resource cleanup completed")
	return nil
}

// localCleanup deletes Kind cluster, prunes Docker artifacts, and removes local state/kubeconfig files.
func (o *Orchestrator) localCleanup(ctx context.Context) error {
	fmt.Println("\n[teardown] Cleaning up local files and Docker/Kind resources...")

	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Delete kind cluster
	fmt.Printf("[teardown] Deleting kind cluster: %s...\n", o.ClusterName)
	cmd := exec.CommandContext(cmdCtx, "kind", "delete", "cluster", "--name", o.ClusterName)
	if o.Debug {
		fmt.Printf("[DEBUG] Running: kind delete cluster --name %s\n", o.ClusterName)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		if o.Debug {
			fmt.Printf("[DEBUG] kind delete output: %s\n", string(output))
			fmt.Printf("[DEBUG] kind delete error: %v\n", err)
		}
		if !strings.Contains(string(output), "not found") {
			fmt.Printf("[teardown] ⚠️  Kind cluster deletion note: %v\n", err)
		}
	} else {
		fmt.Printf("[teardown] ✓ Deleted kind cluster: %s\n", o.ClusterName)
	}

	// Prune Docker artifacts left by kind + local-path-storage PVCs
	fmt.Println("[teardown] Pruning Docker volumes, containers, and networks...")
	exec.CommandContext(cmdCtx, "docker", "container", "prune", "-f").Run()
	if out, err := exec.CommandContext(cmdCtx, "docker", "volume", "prune", "-f").CombinedOutput(); err != nil {
		if o.Debug {
			fmt.Printf("[DEBUG] Docker volume prune: %v\n%s\n", err, out)
		}
	} else {
		fmt.Println("[teardown] ✓ Docker volumes pruned")
	}
	exec.CommandContext(cmdCtx, "docker", "builder", "prune", "-f").Run()
	exec.CommandContext(cmdCtx, "docker", "network", "rm", "kind").Run()

	// Remove kubeconfigs
	kubeconfigPaths := []string{
		fmt.Sprintf("k8-secrets/kubeconfig/%s.kubeconfig", o.ClusterName),
		fmt.Sprintf("%s.kubeconfig", o.ClusterName),
	}
	for _, p := range kubeconfigPaths {
		if err := os.Remove(p); err == nil {
			fmt.Printf("[teardown] ✓ Removed %s\n", p)
		}
	}

	// Remove talosconfig
	talosconfigPath := fmt.Sprintf("%s.talosconfig", o.ClusterName)
	if err := os.Remove(talosconfigPath); err == nil {
		fmt.Printf("[teardown] ✓ Removed %s\n", talosconfigPath)
	}

	// Remove state files
	stateFiles := []string{
		filepath.Join(".zero-ops", "state", fmt.Sprintf("%s.json", o.ClusterName)),
		filepath.Join(".zero-ops", "bootstrap-state.json"),
		filepath.Join(".zero-ops", "infisical-bootstrap.json"),
		filepath.Join(".zero-ops", "kind", "kind-config-generated.yaml"),
	}
	for _, p := range stateFiles {
		if err := os.Remove(p); err == nil {
			fmt.Printf("[teardown] ✓ Removed %s\n", p)
		}
	}

	// Clean up kubectl config contexts & clusters
	contextsToDelete := []string{
		fmt.Sprintf("kind-%s", o.ClusterName),
		o.ClusterName,
		fmt.Sprintf("admin@%s", o.ClusterName),
	}
	for _, ctxName := range contextsToDelete {
		exec.CommandContext(cmdCtx, "kubectl", "config", "delete-context", ctxName).Run()
		exec.CommandContext(cmdCtx, "kubectl", "config", "delete-cluster", ctxName).Run()
		exec.CommandContext(cmdCtx, "kubectl", "config", "unset", fmt.Sprintf("users.%s", ctxName)).Run()
	}

	fmt.Println("[teardown] ✓ Local cleanup complete")
	return nil
}

