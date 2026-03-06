package mgmt

import (
	"context"
	"fmt"
	"os"
	"regexp"

	"github.com/soloz-io/zero-ops/pkg/bootstrap"
	"github.com/soloz-io/zero-ops/pkg/preflight"
	"github.com/spf13/cobra"
)

var (
	// Required flags
	clusterName   string
	region        string
	talosImageID  string

	// Optional flags
	bootstrapContext  string
	keepBootstrap     bool
	dryRun            bool
	mergeKubeconfig   bool
	networkCIDR       string
	upgrade           bool
	sshKey            string
	buildTalosImage   bool
	debug             bool
)

// NewBootstrapCmd creates the bootstrap command
func NewBootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Bootstrap a Management Cluster on Hetzner Cloud",
		Long: `Bootstrap a self-hosted Management Cluster on Hetzner Cloud using 
Cluster API (CAPI), Cluster API Provider Hetzner (CAPH), and Talos Linux.`,
		PreRunE: validateFlags,
		RunE:    runBootstrap,
	}

	// Required flags
	cmd.Flags().StringVar(&clusterName, "name", "", "Management Cluster name (alphanumeric + hyphens)")
	cmd.Flags().StringVar(&region, "region", "", "Hetzner region (fsn1, nbg1, hel1)")
	cmd.Flags().StringVar(&talosImageID, "talos-image-id", "", "Talos Linux snapshot ID in Hetzner")

	// Optional flags
	cmd.Flags().StringVar(&bootstrapContext, "bootstrap-context", "", "Use existing K8s cluster instead of Kind")
	cmd.Flags().BoolVar(&keepBootstrap, "keep-bootstrap", false, "Preserve Kind cluster after successful pivot")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate prerequisites and generate manifests without provisioning")
	cmd.Flags().BoolVar(&mergeKubeconfig, "merge-kubeconfig", false, "Merge generated kubeconfig into ~/.kube/config")
	cmd.Flags().StringVar(&networkCIDR, "network-cidr", "10.0.0.0/16", "Custom CIDR for Hetzner private network")
	cmd.Flags().BoolVar(&upgrade, "upgrade", false, "Reconcile/update existing cluster components to match CLI version")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "SSH key name for Hetzner Rescue Mode emergencies only")
	cmd.Flags().BoolVar(&buildTalosImage, "build-talos-image", false, "Trigger Packer build for Talos image")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable verbose logging")

	// Mark required flags
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("region")

	return cmd
}

// validateFlags validates command flags
func validateFlags(cmd *cobra.Command, args []string) error {
	// Validate cluster name (alphanumeric + hyphens)
	nameRegex := regexp.MustCompile(`^[a-zA-Z0-9-]+$`)
	if !nameRegex.MatchString(clusterName) {
		return fmt.Errorf("invalid cluster name: must contain only alphanumeric characters and hyphens")
	}

	// Validate region
	validRegions := map[string]bool{
		"fsn1": true,
		"nbg1": true,
		"hel1": true,
	}
	if !validRegions[region] {
		return fmt.Errorf("invalid region: must be one of fsn1, nbg1, hel1")
	}

	// Validate Talos image ID or build flag
	if talosImageID == "" && !buildTalosImage {
		return fmt.Errorf("either --talos-image-id or --build-talos-image must be provided")
	}

	// Validate HCLOUD_TOKEN environment variable
	hcloudToken := os.Getenv("HCLOUD_TOKEN")
	if hcloudToken == "" {
		return fmt.Errorf("HCLOUD_TOKEN environment variable is required")
	}

	return nil
}

// runBootstrap executes the bootstrap process
func runBootstrap(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	hcloudToken := os.Getenv("HCLOUD_TOKEN")
	
	if debug {
		fmt.Println("[DEBUG] Bootstrap command started")
		fmt.Printf("[DEBUG] Cluster Name: %s\n", clusterName)
		fmt.Printf("[DEBUG] Region: %s\n", region)
		fmt.Printf("[DEBUG] Talos Image ID: %s\n", talosImageID)
		fmt.Printf("[DEBUG] Network CIDR: %s\n", networkCIDR)
		fmt.Printf("[DEBUG] Dry Run: %v\n", dryRun)
		fmt.Printf("[DEBUG] HCLOUD_TOKEN: %s\n", maskToken(hcloudToken))
	}

	fmt.Println("🚀 Starting Management Cluster bootstrap...")
	fmt.Printf("   Cluster Name: %s\n", clusterName)
	fmt.Printf("   Region: %s\n", region)
	fmt.Printf("   Network CIDR: %s\n", networkCIDR)

	// Phase 2: Preflight Validation
	fmt.Println("\n[preflight] Running validation checks...")
	if err := runPreflight(ctx, hcloudToken); err != nil {
		return fmt.Errorf("preflight validation failed: %w", err)
	}
	fmt.Println("[preflight] ✓ All checks passed")

	if dryRun {
		fmt.Println("\n✓ Dry-run mode: Validation successful")
		fmt.Println("  All prerequisites validated. Ready to bootstrap.")
		return nil
	}

	// Phase 3: Bootstrap Cluster Creation
	orchestrator := &bootstrap.Orchestrator{
		ClusterName:      clusterName,
		Region:           region,
		TalosImageID:     talosImageID,
		NetworkCIDR:      networkCIDR,
		SSHKey:           sshKey,
		BootstrapContext: bootstrapContext,
		KeepBootstrap:    keepBootstrap,
		MergeKubeconfig:  mergeKubeconfig,
		HCloudToken:      hcloudToken,
		Debug:            debug,
	}
	
	if err := orchestrator.Run(ctx); err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	// TODO: Implement remaining phases
	fmt.Println("\n⚠️  Bootstrap implementation in progress...")
	fmt.Println("   Phase 1: CLI Framework - Complete")
	fmt.Println("   Phase 2: Preflight Validation - Complete")
	fmt.Println("   Phase 3: Bootstrap Cluster Creation - Complete")
	fmt.Println("   Phase 4: CAPI Initialization - Complete")
	fmt.Println("   Phase 5: Management Cluster Provisioning - Pending")

	return nil
}

// maskToken masks the Hetzner API token for logging
func maskToken(token string) string {
	if len(token) <= 8 {
		return "***"
	}
	return token[:4] + "..." + token[len(token)-4:]
}

// runPreflight executes all preflight validation checks
func runPreflight(ctx context.Context, hcloudToken string) error {
	runner := preflight.NewRunner()
	
	// Add validators in order
	runner.Add(&preflight.DockerValidator{})
	runner.Add(&preflight.KindValidator{SkipIfBootstrapContext: bootstrapContext != ""})
	runner.Add(&preflight.HetznerTokenValidator{Token: hcloudToken})
	runner.Add(&preflight.TalosImageValidator{
		Token:       hcloudToken,
		ImageID:     talosImageID,
		BuildImage:  buildTalosImage,
		Region:      region,
		ClusterName: clusterName,
	})
	runner.Add(&preflight.SSHKeyValidator{Token: hcloudToken, KeyName: sshKey})
	runner.Add(&preflight.IdempotencyValidator{ClusterName: clusterName, Upgrade: upgrade})
	
	return runner.Run(ctx)
}
