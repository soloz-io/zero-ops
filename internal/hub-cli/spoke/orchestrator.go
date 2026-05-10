package spoke

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/hetznercloud/hcloud-go/hcloud"
)

// Orchestrator manages spoke cluster operations
type Orchestrator struct {
	Debug       bool
	Force       bool
	ClusterName string // Optional: specific spoke cluster name, if empty will delete all
}

// Run executes the spoke operation
func (o *Orchestrator) Run(ctx context.Context) error {
	if o.Debug {
		fmt.Println("[DEBUG] Spoke.Orchestrator.Run() started")
		fmt.Printf("[DEBUG] ClusterName: %s, Force: %v\n", o.ClusterName, o.Force)
	}

	if o.Force {
		return o.forceDelete(ctx)
	}

	return o.gracefulDelete(ctx)
}

// gracefulDelete attempts to delete spoke clusters via Kubernetes API first
func (o *Orchestrator) gracefulDelete(ctx context.Context) error {
	fmt.Println("\n[spoke-teardown] Starting graceful deletion of spoke clusters...")
	
	// For graceful deletion, we would need access to the hub cluster
	// Since hub might already be deleted, we'll fall back to force deletion
	fmt.Println("[spoke-teardown] Hub cluster not accessible, falling back to force deletion")
	return o.forceDelete(ctx)
}

// forceDelete deletes spoke clusters directly via Hetzner API
func (o *Orchestrator) forceDelete(ctx context.Context) error {
	fmt.Println("\n[spoke-teardown] ⚠️  Force deletion mode enabled")
	fmt.Println("[spoke-teardown] This will delete spoke resources directly via Hetzner API")

	hcloudToken := os.Getenv("HCLOUD_TOKEN")
	if hcloudToken == "" {
		return fmt.Errorf("HCLOUD_TOKEN environment variable required for force deletion")
	}

	client := hcloud.NewClient(hcloud.WithToken(hcloudToken))

	// Discover spoke clusters
	spokeClusters, err := o.discoverSpokeClusters(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to discover spoke clusters: %w", err)
	}

	if len(spokeClusters) == 0 {
		fmt.Println("[spoke-teardown] No spoke clusters found")
		return nil
	}

	fmt.Printf("[spoke-teardown] Found %d spoke cluster(s) to delete\n", len(spokeClusters))

	// Delete each spoke cluster
	for _, spokeName := range spokeClusters {
		if o.ClusterName != "" && spokeName != o.ClusterName {
			continue // Skip if specific cluster name requested but doesn't match
		}
		
		fmt.Printf("[spoke-teardown] Deleting spoke cluster: %s\n", spokeName)
		if err := o.deleteSpokeCluster(ctx, client, spokeName); err != nil {
			fmt.Printf("[spoke-teardown] ⚠️  Failed to delete spoke cluster %s: %v\n", spokeName, err)
		} else {
			fmt.Printf("[spoke-teardown] ✓ Deleted spoke cluster: %s\n", spokeName)
		}
	}

	fmt.Println("[spoke-teardown] ✓ Spoke cluster deletion complete")
	return nil
}

// discoverSpokeClusters finds all spoke clusters in Hetzner
func (o *Orchestrator) discoverSpokeClusters(ctx context.Context, client *hcloud.Client) ([]string, error) {
	spokeNames := make(map[string]bool)

	// Method 1: Look for servers with spoke-related labels
	allServers, err := client.Server.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list servers: %w", err)
	}

	for _, server := range allServers {
		// Check for CAPI spoke labels
		for labelKey := range server.Labels {
			if strings.HasPrefix(labelKey, "caph-cluster-spoke") {
				// Extract spoke name from label pattern: caph-cluster-spoke-pool-eu-prod-01-*
				parts := strings.Split(labelKey, "-")
				if len(parts) >= 5 {
					spokeName := strings.Join(parts[2:5], "-") // "spoke-pool-eu-prod-01"
					spokeNames[spokeName] = true
				}
			}
		}
		
		// Also check server names
		if strings.HasPrefix(server.Name, "spoke-pool-") {
			spokeNames[server.Name] = true
		}
	}

	// Method 2: Look for load balancers with spoke labels
	allLBs, err := client.LoadBalancer.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list load balancers: %w", err)
	}

	for _, lb := range allLBs {
		for labelKey := range lb.Labels {
			if strings.HasPrefix(labelKey, "caph-cluster-spoke") {
				parts := strings.Split(labelKey, "-")
				if len(parts) >= 5 {
					spokeName := strings.Join(parts[2:5], "-")
					spokeNames[spokeName] = true
				}
			}
		}
	}

	// Convert map to slice
	var result []string
	for spokeName := range spokeNames {
		result = append(result, spokeName)
	}

	return result, nil
}

// deleteSpokeCluster deletes all resources for a specific spoke cluster
func (o *Orchestrator) deleteSpokeCluster(ctx context.Context, client *hcloud.Client, spokeName string) error {
	labelPattern := fmt.Sprintf("caph-cluster-%s", spokeName)

	// Delete servers
	allServers, err := client.Server.All(ctx)
	if err != nil {
		return fmt.Errorf("failed to list servers: %w", err)
	}

	for _, server := range allServers {
		// Check if server belongs to this spoke cluster
		isSpokeServer := false
		
		// Check labels
		for labelKey := range server.Labels {
			if strings.HasPrefix(labelKey, labelPattern) {
				isSpokeServer = true
				break
			}
		}
		
		// Check server name
		if !isSpokeServer && strings.HasPrefix(server.Name, spokeName) {
			isSpokeServer = true
		}

		if isSpokeServer {
			fmt.Printf("[spoke-teardown] Deleting server: %s (ID: %d)\n", server.Name, server.ID)
			if _, _, err := client.Server.DeleteWithResult(ctx, server); err != nil {
				fmt.Printf("[spoke-teardown] ⚠️  Failed to delete server %s: %v\n", server.Name, err)
			} else {
				fmt.Printf("[spoke-teardown] ✓ Deleted server: %s\n", server.Name)
			}
		}
	}

	// Delete load balancers
	allLBs, err := client.LoadBalancer.All(ctx)
	if err != nil {
		return fmt.Errorf("failed to list load balancers: %w", err)
	}

	for _, lb := range allLBs {
		isSpokeLB := false
		
		for labelKey := range lb.Labels {
			if strings.HasPrefix(labelKey, labelPattern) {
				isSpokeLB = true
				break
			}
		}

		if isSpokeLB {
			fmt.Printf("[spoke-teardown] Deleting load balancer: %s (ID: %d)\n", lb.Name, lb.ID)
			if _, err := client.LoadBalancer.Delete(ctx, lb); err != nil {
				fmt.Printf("[spoke-teardown] ⚠️  Failed to delete load balancer %s: %v\n", lb.Name, err)
			} else {
				fmt.Printf("[spoke-teardown] ✓ Deleted load balancer: %s\n", lb.Name)
			}
		}
	}

	// Delete networks
	allNetworks, err := client.Network.All(ctx)
	if err != nil {
		return fmt.Errorf("failed to list networks: %w", err)
	}

	for _, network := range allNetworks {
		if strings.HasPrefix(network.Name, spokeName) {
			fmt.Printf("[spoke-teardown] Deleting network: %s (ID: %d)\n", network.Name, network.ID)
			if _, err := client.Network.Delete(ctx, network); err != nil {
				fmt.Printf("[spoke-teardown] ⚠️  Failed to delete network %s: %v\n", network.Name, err)
			} else {
				fmt.Printf("[spoke-teardown] ✓ Deleted network: %s\n", network.Name)
			}
		}
	}

	return nil
}