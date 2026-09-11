package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/soloz-io/zero-ops/internal/soloz-cli/bootstrap"
	"github.com/soloz-io/zero-ops/internal/soloz-cli/preflight"
	"github.com/spf13/cobra"
)

var (
	// Required flags
	clusterName string
	gitopsDir   string
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
	gating            string

	// Hybrid-provider flags (ADR-046 §WS4)
	onPremEnabled bool
	// -1 rather than 0: zero workers is a real answer, and the default depends on
	// the environment, which is not known when flags are declared.
	workerReplicas int
	onPremJoinTTL  string
	tailnetName    string
)

// workerReplicasOverride is the --workers value, or nil when it was not passed.
// Zero is a real count, so the flag uses a negative sentinel and this converts it.
func workerReplicasOverride() *int {
	if workerReplicas < 0 {
		return nil
	}
	n := workerReplicas
	return &n
}

// resolveHCloudToken makes the Hetzner token available to everything that reads it.
//
// Five places in this CLI read HCLOUD_TOKEN from the environment -- bootstrap,
// pivot twice, spoke and teardown -- so the token is resolved once, here, and put
// back into the environment rather than threaded through each of them.
//
// The file fallback exists because teardown already had it and bootstrap did not:
// the same credential, in the same file, worked for tearing a cluster down and not
// for building one. k8-secrets/ is the operator's own gitignored directory, so this
// helps someone working in a checkout and does nothing in CI, where the variable is
// set from a secret.
func resolveHCloudToken() error {
	if os.Getenv("HCLOUD_TOKEN") != "" {
		return nil
	}
	const rel = "k8-secrets/hetzner/token"
	for _, path := range hcloudTokenPaths(rel) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		token := strings.TrimSpace(string(raw))
		if token == "" {
			continue
		}
		// Set rather than returned: the readers downstream take it from the
		// environment, and passing it explicitly would mean changing all of them.
		if err := os.Setenv("HCLOUD_TOKEN", token); err != nil {
			return fmt.Errorf("could not make the Hetzner token available: %w", err)
		}
		fmt.Printf("[bootstrap] using the Hetzner token from %s\n", path)
		return nil
	}
	return fmt.Errorf("no Hetzner API token.\n\n"+
		"Clusters are created with it, so bootstrap needs one of:\n\n"+
		"  HCLOUD_TOKEN   exported in this shell\n"+
		"  %s   the same token, relative to this checkout\n\n"+
		"Create one at https://console.hetzner.cloud/ under Security > API tokens,\n"+
		"with Read & Write permission.", rel)
}

// hcloudTokenPaths lists where the token file may be, nearest first.
//
// The working directory is no longer where the operator's checkout is. A released
// binary carries its platform content (ADR-063), so bootstrap is run FROM the
// tenant's repository -- and the tenant's repository has no k8-secrets/. The
// fallback is therefore resolved against the checkout rather than the cwd, which
// is what it always meant: k8-secrets/ is the operator's own gitignored directory,
// and reading it from wherever bootstrap happens to be run was an accident of the
// two being the same place.
//
// ZERO_OPS_DIR first because a developer can say where the checkout is; the
// executable's directory second because `bin/soloz` and `./soloz` both sit in one.
func hcloudTokenPaths(rel string) []string {
	paths := []string{rel}
	if root := strings.TrimSpace(os.Getenv("ZERO_OPS_DIR")); root != "" {
		paths = append(paths, filepath.Join(root, rel))
	}
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			dir := filepath.Dir(exe)
			// Both layouts the repository produces: `./soloz` at the root, and
			// `bin/soloz` from the Makefile.
			paths = append(paths,
				filepath.Join(dir, rel),
				filepath.Join(filepath.Dir(dir), rel))
		}
	}
	return paths
}

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
	cmd.Flags().StringVar(&gitopsDir, "gitops-dir", "",
		"a checkout of the tenant's own repository; the cluster is seeded with the declaration it holds (ADR-072)")
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
	cmd.Flags().IntVar(&workerReplicas, "workers", -1,
		"cloud worker nodes to provision (default: 2, every environment). Pass 0 to run "+
			"on on-prem capacity alone, which then has to be there (ADR-075)")
	cmd.Flags().StringVar(&topology, "topology", "single", "Topology mode: single (default) or multi (bridged)")
	cmd.Flags().StringVar(&gating, "gating", "sequenced", "Cluster creation mode (ADR-055): sequenced (default, boundaries activated in phase order) or converged (all boundaries reconcile concurrently)")

	// Hybrid-provider flags (ADR-046 §WS4)
	cmd.Flags().BoolVar(&onPremEnabled, "on-prem", false, "accept nodes on the tenant's own premises, joining this cluster over their tailnet (ADR-075)")
	cmd.Flags().StringVar(&onPremJoinTTL, "on-prem-join-ttl", "24h", "kubeadm bootstrap-token TTL for on-prem nodes")
	cmd.Flags().StringVar(&tailnetName, "tailnet-name", "", "the tenant's Tailscale tailnet, e.g. acme.ts.net. Required with --on-prem")

	// Mark required flags
	cmd.MarkFlagRequired("name")

	return cmd
}

func validateFlags(cmd *cobra.Command, args []string) error {
	// Validate provider
	validProviders := map[string]bool{
		"hetzner": true,
		"hybrid":  true,
	}
	if !validProviders[provider] {
		return fmt.Errorf("invalid provider: must be 'hetzner' or 'hybrid'")
	}

	// Validate cluster name (alphanumeric + hyphens)
	nameRegex := regexp.MustCompile(`^[a-zA-Z0-9-]+$`)
	if !nameRegex.MatchString(clusterName) {
		return fmt.Errorf("invalid cluster name: must contain only alphanumeric characters and hyphens")
	}

	validRegions := map[string]bool{
		"fsn1": true,
		"nbg1": true,
		"hel1": true,
	}

	// Provider-specific validation (hetzner and hybrid share region/token requirements)
	switch provider {
	case "hetzner", "hybrid":
		if region == "" {
			return fmt.Errorf("--region is required for provider '%s'", provider)
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
		if err := resolveHCloudToken(); err != nil {
			return err
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
	if provider == "hetzner" || provider == "hybrid" {
		fmt.Printf("   OS Type: %s\n", osType)
		fmt.Printf("   Region: %s\n", region)
		fmt.Printf("   Network CIDR: %s\n", networkCIDR)
	}
	if provider == "hybrid" {
		fmt.Printf("   On-prem nodes: %v\n", onPremEnabled)
		fmt.Printf("   Tailnet: %s\n", tailnetName)
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
			Environment:       environment,
			OnPremEnabled:     onPremEnabled,
			WorkerReplicas:    workerReplicasOverride(),
			ClusterName:       clusterName,
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
			Environment:       environment,
			OnPremEnabled:     onPremEnabled,
			WorkerReplicas:    workerReplicasOverride(),
			ClusterName:       clusterName,
		}
		hybridDriver := &bootstrap.HybridDriver{
			Driver:        driver,
			TailnetName:   tailnetName,
			OnPremEnabled: onPremEnabled,
			HomeWorkerTTL: onPremJoinTTL,
			ClusterName:   clusterName,
		}
		bp = bootstrap.NewCloudProvider(hybridDriver, clusterName, debug)
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	if dryRun {
		// Dry-run validates without checkpointing — no state is written.
		fmt.Println("\n[preflight] Running validation checks...")
		runner := preflight.NewRunner()
		for _, v := range bp.PreflightValidators() {
			runner.Add(v)
		}
		if err := runner.Run(ctx); err != nil {
			return fmt.Errorf("preflight validation failed: %w", err)
		}
		fmt.Println("[preflight] ✓ All checks passed")
		fmt.Println("\n✓ Dry-run mode: Validation successful")
		fmt.Println("  All prerequisites validated. Ready to bootstrap.")
		return nil
	}

	// Derive environment slug
	envSlug := environment
	if envSlug == "" {
		envSlug = "prod"
	}

	// ADR-055: mode and environment are independent inputs. Every environment
	// class may be created in either mode; no combination is prohibited.
	gatingMode, err := bootstrap.ValidGatingMode(gating)
	if err != nil {
		return err
	}
	fmt.Printf("[bootstrap] Cluster creation mode: %s\n", gatingMode)

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
		Gating:           gatingMode,
		GitopsDir:        gitopsDir,
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
