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

	// Step 1: Let the CCM deprovision its own load balancers while it is still
	// alive. Must run before anything else is destroyed — see the function comment.
	ccmLoadBalancerIPs := o.drainLoadBalancerServices(ctx)

	// Step 2: Strip Kubernetes finalizers & delete CAPI CRs fast (non-blocking)
	o.stripKubernetesFinalizers(ctx)

	// Step 3: Delete Hetzner cloud infrastructure directly via Hetzner API
	if err := o.deleteHetznerResources(ctx, ccmLoadBalancerIPs); err != nil {
		fmt.Printf("[teardown] ⚠️  Hetzner cloud resource cleanup encountered warnings: %v\n", err)
	}

	// Step 3: Local Kind, Docker, and state cleanup
	if err := o.localCleanup(ctx); err != nil {
		fmt.Printf("[teardown] ⚠️  Local cleanup encountered warnings: %v\n", err)
	}

	fmt.Printf("\n✓ Teardown of cluster '%s' completed\n", o.ClusterName)
	return nil
}

// resolveKubectlBaseArgs locates the kubeconfig (or kind context) for the cluster
// being torn down and returns the kubectl flags that select it, or ok=false when
// no usable target exists.
//
// Shared by every step that talks to the doomed cluster so they cannot drift onto
// different clusters — a teardown that drains load balancers from one cluster and
// strips finalizers on another would be worse than doing neither.
func (o *Orchestrator) resolveKubectlBaseArgs() (baseArgs []string, ok bool) {
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

	if strings.HasPrefix(kubeconfig, "--context=") {
		return []string{"--context", strings.TrimPrefix(kubeconfig, "--context=")}, true
	}
	if kubeconfig == "" {
		return nil, false
	}
	if _, err := os.Stat(kubeconfig); err != nil {
		return nil, false
	}
	return []string{"--kubeconfig", kubeconfig}, true
}

// drainLoadBalancerServices deletes every type=LoadBalancer Service on the cluster
// and gives the Hetzner CCM the chance to deprovision the load balancers it owns,
// while it is still running. It returns the public IPs those services held, so a
// later sweep can reap by IP whatever the CCM did not remove.
//
// This must run before anything else is destroyed. A CCM-created load balancer is
// invisible to the name/label matcher in deleteHetznerResources: the CCM names it
// a<service-uid> and labels it only hcloud-ccm/service-uid, so it carries neither
// the cluster name nor a caph-cluster-* label. The CCM is the only component that
// can deprovision it, and it dies with the cluster — so the load balancer survives
// as a billed orphan that nothing will ever collect.
//
// That is not hypothetical. Four rebuilds on 2026-08-22 leaked four load balancers
// into a five-load-balancer project quota. The fifth request — the spoke's own
// control-plane LB — was refused with resource_limit_exceeded, leaving
// spoke-pool-hybrid-dev-01 at InfrastructureReady=False for five hours while the
// bootstrap polled a terminal error to timeout. See ADR-046 §23.
//
// Order matters in the other direction too: the Service must go first. Deleting the
// cloud load balancer while its Service still exists just makes the CCM recreate it.
func (o *Orchestrator) drainLoadBalancerServices(ctx context.Context) []string {
	baseArgs, ok := o.resolveKubectlBaseArgs()
	if !ok {
		fmt.Println("\n[teardown] ⚠️  No usable kubeconfig; skipping load balancer drain")
		fmt.Println("[teardown]    CCM-owned load balancers will be reaped by name only — see ADR-046 §23")
		return nil
	}

	fmt.Println("\n[teardown] Draining LoadBalancer Services so the CCM deprovisions its load balancers...")

	listCtx, cancelList := context.WithTimeout(ctx, 20*time.Second)
	defer cancelList()

	listArgs := append(append([]string{}, baseArgs...),
		"get", "svc", "--all-namespaces",
		"-o", `jsonpath={range .items[?(@.spec.type=="LoadBalancer")]}{.metadata.namespace}{" "}{.metadata.name}{" "}{.status.loadBalancer.ingress[*].ip}{"\n"}{end}`)
	out, err := exec.CommandContext(listCtx, "kubectl", listArgs...).Output()
	if err != nil {
		fmt.Printf("[teardown] ⚠️  Could not list LoadBalancer Services (%v); continuing\n", err)
		return nil
	}

	type lbService struct{ namespace, name string }
	var services []lbService
	var ips []string

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		services = append(services, lbService{namespace: fields[0], name: fields[1]})
		// Remaining fields are the assigned VIPs. A Hetzner LB publishes IPv4, IPv6
		// and (when attached) a private address; only the IPv4 identifies the load
		// balancer in the API, but collecting all of them costs nothing and the
		// sweep matches exactly.
		ips = append(ips, fields[2:]...)
	}

	if len(services) == 0 {
		fmt.Println("[teardown] ✓ No LoadBalancer Services present")
		return nil
	}

	for _, svc := range services {
		fmt.Printf("[teardown] Deleting LoadBalancer Service: %s/%s\n", svc.namespace, svc.name)
		delCtx, cancelDel := context.WithTimeout(ctx, 15*time.Second)
		delArgs := append(append([]string{}, baseArgs...),
			"delete", "svc", svc.name, "-n", svc.namespace, "--wait=false", "--ignore-not-found")
		if err := exec.CommandContext(delCtx, "kubectl", delArgs...).Run(); err != nil {
			fmt.Printf("[teardown] ⚠️  Failed to delete Service %s/%s: %v\n", svc.namespace, svc.name, err)
		}
		cancelDel()
	}

	// The service controller holds a load-balancer-cleanup finalizer until the CCM
	// confirms the cloud resource is gone, so the Service disappearing IS the
	// deprovision completing. Bounded, because a broken or already-dead CCM never
	// clears it and teardown must not hang on that — the IP sweep is the fallback.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		remaining := 0
		for _, svc := range services {
			checkCtx, cancelCheck := context.WithTimeout(ctx, 10*time.Second)
			getArgs := append(append([]string{}, baseArgs...),
				"get", "svc", svc.name, "-n", svc.namespace, "--ignore-not-found",
				"-o", "jsonpath={.metadata.name}")
			gotOut, gotErr := exec.CommandContext(checkCtx, "kubectl", getArgs...).Output()
			cancelCheck()
			if gotErr == nil && len(strings.TrimSpace(string(gotOut))) > 0 {
				remaining++
			}
		}
		if remaining == 0 {
			fmt.Println("[teardown] ✓ CCM deprovisioned all load balancers")
			return ips
		}
		time.Sleep(5 * time.Second)
	}

	fmt.Printf("[teardown] ⚠️  CCM did not finish within 90s; %d load balancer IP(s) will be reaped directly\n", len(ips))
	return ips
}

// stripKubernetesFinalizers attempts fast non-blocking removal of finalizers on CAPI CRs
func (o *Orchestrator) stripKubernetesFinalizers(ctx context.Context) {
	baseArgs, ok := o.resolveKubectlBaseArgs()
	if !ok {
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

// deleteHetznerResources deletes all matching Hetzner cloud resources directly via
// the Hetzner API.
//
// ccmLoadBalancerIPs comes from drainLoadBalancerServices and lists the public
// addresses of load balancers the CCM owned. Matching on those addresses is what
// catches a CCM load balancer at all: its name and labels carry nothing that ties
// it to this cluster, so the name/label matcher below cannot see it. The IP match
// needs no cooperation from naming conventions, which also means it covers
// LoadBalancer Services authored by a fleet, not just platform ones.
func (o *Orchestrator) deleteHetznerResources(ctx context.Context, ccmLoadBalancerIPs []string) error {
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
	//
	// Two matchers, because they cover disjoint sets. CAPH's control-plane LB is
	// named after the cluster and matches by name. A CCM-created LB is named
	// a<service-uid> with a single hcloud-ccm/service-uid label and matches
	// neither name nor label — it is only reachable by the IP recorded during the
	// drain. Missing that second matcher is what leaked four load balancers and
	// exhausted the project quota (ADR-046 §23).
	orphanIPs := make(map[string]bool, len(ccmLoadBalancerIPs))
	for _, ip := range ccmLoadBalancerIPs {
		if ip = strings.TrimSpace(ip); ip != "" {
			orphanIPs[ip] = true
		}
	}

	matchesDrainedIP := func(lb *hcloud.LoadBalancer) bool {
		if len(orphanIPs) == 0 || lb.PublicNet.IPv4.IP == nil {
			return false
		}
		return orphanIPs[lb.PublicNet.IPv4.IP.String()]
	}

	allLBs, err := client.LoadBalancer.All(ctx)
	if err == nil {
		for _, lb := range allLBs {
			if matchesCluster(lb.Name, lb.Labels) || matchesDrainedIP(lb) {
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

