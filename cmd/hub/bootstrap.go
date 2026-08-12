package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/soloz-io/zero-ops/internal/hub-cli/bootstrap"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
	"github.com/spf13/cobra"
)

var (
	// Required flags
	clusterName string
	region      string
	imageID     string

	// Optional flags
	provider          string
	osType            string
	bootstrapContext  string
	keepBootstrap     bool
	dryRun            bool
	mergeKubeconfig   bool
	networkCIDR       string
	upgrade           bool
	sshKey            string
	buildTalosImage   bool
	buildFlatcarImage bool
	debug             bool
	environment       string
	topology          string
)

func newBootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Bootstrap a Hub Cluster",
		Long: `Bootstrap a self-hosted Hub (Management) Cluster using Cluster API (CAPI).
Supports multiple infrastructure providers: hetzner (cloud) and hybrid (home-lab workers).`,
		PreRunE: validateFlags,
		RunE:    runBootstrap,
	}

	// Required flags
	cmd.Flags().StringVar(&clusterName, "name", "", "Hub Cluster name (alphanumeric + hyphens)")
	cmd.Flags().StringVar(&provider, "provider", "hetzner", "Infrastructure provider: hetzner (default) or hybrid (home-lab)")

	// Hetzner-specific flags (required when provider=hetzner)
	cmd.Flags().StringVar(&region, "region", "", "Hetzner region (fsn1, nbg1, hel1)")
	cmd.Flags().StringVar(&imageID, "image-id", "", "OS image ID (Talos snapshot ID or ubuntu image name)")

	// Optional flags
	cmd.Flags().StringVar(&osType, "os", "ubuntu", "OS type: ubuntu (default) or talos")
	cmd.Flags().StringVar(&bootstrapContext, "bootstrap-context", "", "Use existing K8s cluster instead of Kind")
	cmd.Flags().BoolVar(&keepBootstrap, "keep-bootstrap", false, "Preserve Kind cluster after successful pivot")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate prerequisites and generate manifests without provisioning")
	cmd.Flags().BoolVar(&mergeKubeconfig, "merge-kubeconfig", false, "Merge generated kubeconfig into ~/.kube/config")
	cmd.Flags().StringVar(&networkCIDR, "network-cidr", "10.0.0.0/16", "Custom CIDR for Hetzner private network")
	cmd.Flags().BoolVar(&upgrade, "upgrade", false, "Reconcile/update existing cluster components to match CLI version")
	cmd.Flags().StringVar(&sshKey, "ssh-key", "", "SSH key name for Hetzner Rescue Mode emergencies only")
	cmd.Flags().BoolVar(&buildTalosImage, "build-talos-image", false, "Trigger Packer build for Talos image")
	cmd.Flags().BoolVar(&buildFlatcarImage, "build-flatcar-image", false, "Trigger Packer build for Flatcar image")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable verbose logging")
	cmd.Flags().StringVar(&environment, "environment", "", "Environment slug (dev, stg, prod, ephemeral). Defaults to prod for hetzner, hybrid")
	cmd.Flags().StringVar(&topology, "topology", "single", "Topology mode: single (default) or multi (bridged)")

	// Mark required flags
	cmd.MarkFlagRequired("name")

	return cmd
}

func validateFlags(cmd *cobra.Command, args []string) error {
	// Validate provider
	validProviders := map[string]bool{
		"hetzner": true,
	}
	if !validProviders[provider] {
		return fmt.Errorf("invalid provider: must be 'hetzner' or 'hybrid'")
	}

	// Validate cluster name (alphanumeric + hyphens)
	nameRegex := regexp.MustCompile(`^[a-zA-Z0-9-]+$`)
	if !nameRegex.MatchString(clusterName) {
		return fmt.Errorf("invalid cluster name: must contain only alphanumeric characters and hyphens")
	}

	// Provider-specific validation
	if provider == "hetzner" {
		if region == "" {
			return fmt.Errorf("--region is required for provider 'hetzner'")
		}

		validRegions := map[string]bool{
			"fsn1": true,
			"nbg1": true,
			"hel1": true,
		}
		if !validRegions[region] {
			return fmt.Errorf("invalid region: must be one of fsn1, nbg1, hel1")
		}

		if osType != "ubuntu" && osType != "talos" {
			return fmt.Errorf("invalid OS type: must be 'ubuntu' or 'talos'")
		}

		if osType == "talos" && imageID == "" && !buildTalosImage {
			return fmt.Errorf("for Talos: either --image-id or --build-talos-image must be provided")
		}

		hcloudToken := os.Getenv("HCLOUD_TOKEN")
		if hcloudToken == "" {
			return fmt.Errorf("HCLOUD_TOKEN environment variable is required for provider 'hetzner'")
		}
	}

	return nil
}

func runBootstrap(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	if debug {
		fmt.Println("[DEBUG] Bootstrap command started")
		fmt.Printf("[DEBUG] Provider: %s\n", provider)
		fmt.Printf("[DEBUG] Cluster Name: %s\n", clusterName)
		fmt.Printf("[DEBUG] OS Type: %s\n", osType)
		fmt.Printf("[DEBUG] Dry Run: %v\n", dryRun)
	}

	fmt.Println("🚀 Starting Hub Cluster bootstrap...")
	fmt.Printf("   Provider: %s\n", provider)
	fmt.Printf("   Cluster Name: %s\n", clusterName)
	if provider == "hetzner" {
		fmt.Printf("   OS Type: %s\n", osType)
		fmt.Printf("   Region: %s\n", region)
		fmt.Printf("   Network CIDR: %s\n", networkCIDR)
	}

	// Build provider based on --provider flag
	var bp bootstrap.Provider

	switch provider {
	case "hetzner":
		hcloudToken := os.Getenv("HCLOUD_TOKEN")
		driver := &bootstrap.HetznerDriver{
			Token:             hcloudToken,
			Region:            region,
			OS:                osType,
			ImageID:           imageID,
			NetworkCIDR:       networkCIDR,
			SSHKey:            sshKey,
			Debug:             debug,
			BuildTalosImage:   buildTalosImage,
			BuildFlatcarImage: buildFlatcarImage,
		}
		bp = bootstrap.NewCloudProvider(driver, clusterName, debug)
	case "hybrid":
		hcloudToken := os.Getenv("HCLOUD_TOKEN")
		driver := &bootstrap.HetznerDriver{
			Token:             hcloudToken,
			Region:            region,
			OS:                osType,
			ImageID:           imageID,
			NetworkCIDR:       networkCIDR,
			SSHKey:            sshKey,
			Debug:             debug,
			BuildTalosImage:   buildTalosImage,
			BuildFlatcarImage: buildFlatcarImage,
		}
		hybridDriver := &bootstrap.HybridDriver{Driver: driver}
		bp = bootstrap.NewCloudProvider(hybridDriver, clusterName, debug)
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	// Phase 1: Preflight Validation
	fmt.Println("\n[preflight] Running validation checks...")
	runner := preflight.NewRunner()
	for _, v := range bp.PreflightValidators() {
		runner.Add(v)
	}
	if err := runner.Run(ctx); err != nil {
		return fmt.Errorf("preflight validation failed: %w", err)
	}
	fmt.Println("[preflight] ✓ All checks passed")

	if dryRun {
		fmt.Println("\n✓ Dry-run mode: Validation successful")
		fmt.Println("  All prerequisites validated. Ready to bootstrap.")
		return nil
	}

	// Derive environment slug
	envSlug := environment
	if envSlug == "" {
		envSlug = "prod"
	}

	// Phase 2-12: Bootstrap pipeline
	orchestrator := &bootstrap.Orchestrator{
		Provider:         bp,
		ClusterName:      clusterName,
		BootstrapContext: bootstrapContext,
		KeepBootstrap:    keepBootstrap,
		MergeKubeconfig:  mergeKubeconfig,
		Debug:            debug,
		EnvironmentSlug:  envSlug,
		Topology:         topology,
	}

	if err := orchestrator.Run(ctx); err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	return nil
}

func maskToken(token string) string {
	if len(token) <= 8 {
		return "***"
	}
	return token[:4] + "..." + token[len(token)-4:]
}

func readGitHubToken() string {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	data, err := os.ReadFile("k8-secrets/github/github-pat-token")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
