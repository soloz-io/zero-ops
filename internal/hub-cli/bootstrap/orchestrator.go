package bootstrap

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/cluster"
	"github.com/soloz-io/zero-ops/internal/hub-cli/clusterclass"
	"github.com/soloz-io/zero-ops/internal/hub-cli/components"
	"github.com/soloz-io/zero-ops/internal/hub-cli/config"
	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
	"github.com/soloz-io/zero-ops/internal/hub-cli/pivot"
	"github.com/soloz-io/zero-ops/internal/hub-cli/state"
)

// Orchestrator manages the bootstrap process
type Orchestrator struct {
	Provider          CloudProvider
	ClusterName       string
	Region            string
	OSType            string
	ImageID           string
	NetworkCIDR       string
	SSHKey            string
	BootstrapContext  string
	KeepBootstrap     bool
	MergeKubeconfig   bool
	HCloudToken       string
	Debug             bool
	Upgrade           bool
}

func (o *Orchestrator) Run(ctx context.Context) error {
	if o.Debug {
		fmt.Println("[DEBUG] Orchestrator.Run() started")
		fmt.Printf("[DEBUG] Provider: %s, ClusterName: %s, Upgrade: %v\n", o.Provider.Name(), o.ClusterName, o.Upgrade)
	}

	stateMgr := state.NewStateManager(o.ClusterName)

	// Try to load existing state
	bootstrapState, err := stateMgr.Load()
	if err == nil && bootstrapState != nil {
		fmt.Printf("\n[recovery] Found existing state for cluster '%s'\n", o.ClusterName)
		fmt.Printf("[recovery] Last completed phase: %s\n", bootstrapState.CurrentPhase)

		if contains(bootstrapState.CompletedPhases, state.PhaseComplete) {
			if o.Upgrade {
				fmt.Println("[upgrade] Cluster already exists, starting upgrade/reconciliation...")
				return o.runUpgrade(ctx, bootstrapState)
			}
			return fmt.Errorf("cluster '%s' already exists. Use --upgrade to reconcile or --name with different name", o.ClusterName)
		}

		fmt.Printf("[recovery] Resuming from next phase...\n")
		if o.Debug {
			fmt.Printf("[DEBUG] Completed phases: %v\n", bootstrapState.CompletedPhases)
		}
	} else {
		if err := o.checkKindClusterExists(); err != nil {
			return err
		}

		bootstrapState = &state.BootstrapState{
			Version:      "1.0",
			ClusterName:  o.ClusterName,
			Region:       o.Region,
			Provider:     o.Provider.Name(),
			TalosImageId: o.ImageID,
			NetworkCIDR:  o.NetworkCIDR,
			CurrentPhase: state.PhaseBootstrapCreate,
		}

		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	}

	var kubeconfig string
	var bootstrapContext string
	var mgmtKubeconfig string

	if bootstrapState.BootstrapContext != "" {
		bootstrapContext = bootstrapState.BootstrapContext
	} else if o.BootstrapContext != "" {
		bootstrapContext = o.BootstrapContext
	}

	if bootstrapState.MgmtKubeconfig != "" {
		mgmtKubeconfig = bootstrapState.MgmtKubeconfig
	}

	// Phase 3: Bootstrap Cluster Creation
	if !contains(bootstrapState.CompletedPhases, state.PhaseBootstrapCreate) {
		fmt.Println("\n[bootstrap-create] Creating ephemeral bootstrap cluster...")
		if o.Debug {
			fmt.Printf("[DEBUG] Phase: %s\n", state.PhaseBootstrapCreate)
		}

		if bootstrapContext != "" {
			fmt.Printf("[bootstrap-create] Using existing context: %s\n", bootstrapContext)
			if o.Debug {
				fmt.Printf("[DEBUG] Bootstrap context provided: %s\n", bootstrapContext)
			}
			homeDir, _ := os.UserHomeDir()
			kubeconfig = filepath.Join(homeDir, ".kube", "config")
			bootstrapState.BootstrapContext = bootstrapContext
		} else {
			kindMgr := &KindManager{
				ClusterName: o.ClusterName,
				ConfigPath:  o.Provider.KindConfigPath(),
			}

			if kindMgr.Exists(ctx) {
				fmt.Println("[bootstrap-create] Bootstrap cluster already exists")
			} else {
				if o.Debug {
					fmt.Printf("[DEBUG] Creating Kind cluster: %s\n", o.ClusterName)
				}
				if err := kindMgr.Create(ctx); err != nil {
					return fmt.Errorf("failed to create Kind cluster: %w", err)
				}
				fmt.Println("[bootstrap-create] ✓ Kind cluster created")
			}

			homeDir, _ := os.UserHomeDir()
			kubeconfig = filepath.Join(homeDir, ".kube", "config")
			bootstrapState.BootstrapContext = fmt.Sprintf("kind-%s", o.ClusterName)
		}

		// Create platform-capi namespace
		nsMgr := &NamespaceManager{
			Kubeconfig: kubeconfig,
			Context:    bootstrapState.BootstrapContext,
			Namespace:  constants.NamespaceCAPI,
		}
		if err := nsMgr.Create(ctx); err != nil {
			return fmt.Errorf("failed to create namespace: %w", err)
		}
		fmt.Println("[bootstrap-create] ✓ Namespace created: platform-capi")

		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhaseBootstrapCreate)
		bootstrapState.CurrentPhase = state.PhaseCAPIInit
		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	} else {
		homeDir, _ := os.UserHomeDir()
		kubeconfig = filepath.Join(homeDir, ".kube", "config")
		fmt.Println("[bootstrap-create] ✓ Skipped (already completed)")
	}

	// Phase 4: CAPI Initialization
	if !contains(bootstrapState.CompletedPhases, state.PhaseCAPIInit) {
		fmt.Println("\n[capi-init] Installing cluster-api-operator...")
		if o.Debug {
			fmt.Printf("[DEBUG] Phase: %s\n", state.PhaseCAPIInit)
		}

		capiInstaller := &capi.OperatorInstaller{
			Kubeconfig: kubeconfig,
			Context:    bootstrapState.BootstrapContext,
			Namespace:  constants.NamespaceCAPI,
			Providers:  o.Provider.CAPIProviders(),
			Debug:      o.Debug,
		}

		if err := capiInstaller.Install(ctx); err != nil {
			return fmt.Errorf("failed to install CAPI operator: %w", err)
		}
		fmt.Println("[capi-init] ✓ CAPI operator installed")

		// Provider-specific CAPI init (e.g., Hetzner credentials secret)
		if err := o.Provider.OnCAPIInit(ctx, kubeconfig, bootstrapState.BootstrapContext, constants.NamespaceCAPI); err != nil {
			return fmt.Errorf("provider CAPI init failed: %w", err)
		}

		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhaseCAPIInit)
		bootstrapState.CurrentPhase = state.PhaseClusterProvision
		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	} else {
		fmt.Println("[capi-init] ✓ Skipped (already completed)")
	}

	// Phase 5 & 6: Management Cluster Provisioning + Pivot (only for non-self-provisioning providers like Hetzner)
	// For self-provisioning providers (CAPD/Docker), the Kind cluster IS the management cluster
	if !o.Provider.IsSelfProvisioning() {
		if o.handleCloudProvisioning(ctx, bootstrapState, kubeconfig, &mgmtKubeconfig) != nil {
			return fmt.Errorf("cloud provisioning failed: %w", err)
		}
	} else {
		if o.handleSelfProvisioning(ctx, bootstrapState, kubeconfig, &mgmtKubeconfig) != nil {
			return fmt.Errorf("self-provisioning failed: %w", err)
		}
	}

	// Phase 8: ClusterClass Library Deployment
	if !contains(bootstrapState.CompletedPhases, state.PhaseClusterClassDeploy) {
		fmt.Println("\n[clusterclass-deploy] Deploying ClusterClass library...")

		ccDeployer := &clusterclass.Deployer{
			Kubeconfig: mgmtKubeconfig,
			Namespace:  constants.NamespaceCAPI,
			ClassPaths: o.Provider.ClusterClassPaths(),
		}

		if err := ccDeployer.Deploy(ctx); err != nil {
			return fmt.Errorf("failed to deploy ClusterClass library: %w", err)
		}
		fmt.Println("[clusterclass-deploy] ✓ ClusterClass library deployed")

		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhaseClusterClassDeploy)
		bootstrapState.CurrentPhase = state.PhasePostBoot
		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	} else {
		fmt.Println("[clusterclass-deploy] ✓ Skipped (already completed)")
	}

	// Phase 9: Post-Bootstrap Components
	if !contains(bootstrapState.CompletedPhases, state.PhasePostBoot) {
		fmt.Println("\n[postboot] Installing platform components...")

		compInstaller := &components.Installer{
			Kubeconfig: mgmtKubeconfig,
		}

		if err := compInstaller.InstallArgoCD(ctx); err != nil {
			return fmt.Errorf("failed to install ArgoCD: %w", err)
		}
		fmt.Println("[postboot] ✓ ArgoCD installed")

		// Provider-specific post-boot components (e.g., Hetzner CSI)
		if err := o.Provider.PostBootComponents(ctx, mgmtKubeconfig); err != nil {
			return fmt.Errorf("provider post-boot failed: %w", err)
		}

		// Apply ArgoCD bootstrap boundaries in sequence (ADR-021)
		// Detect current git branch and inject as targetRevision so ArgoCD
		// syncs from the correct branch (ADR-037 §2: single source of truth).
		gitBranch := currentGitBranch()
		if gitBranch != "" && gitBranch != "main" {
			fmt.Printf("[postboot] Git branch: %s (injecting as targetRevision)\n", gitBranch)
		} else {
			gitBranch = "HEAD"
		}

		if err := applyArgoCDBootstrapApp(ctx, mgmtKubeconfig, "01-platform-infra", gitBranch); err != nil {
			return err
		}

		fmt.Println("[postboot] Waiting for operators to establish webhooks...")
		if err := o.waitForOperators(ctx, mgmtKubeconfig); err != nil {
			return fmt.Errorf("failed to wait for operators: %w", err)
		}
		fmt.Println("[postboot] ✓ Operators ready")

		if err := applyArgoCDBootstrapApp(ctx, mgmtKubeconfig, "02-platform-data", gitBranch); err != nil {
			return err
		}

		if err := applyArgoCDBootstrapApp(ctx, mgmtKubeconfig, "03-platform-services", gitBranch); err != nil {
			return err
		}
		fmt.Println("[postboot] ✓ All bootstrap boundaries applied")

		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhasePostBoot)
		bootstrapState.CurrentPhase = state.PhaseComplete
		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	} else {
		fmt.Println("[postboot] ✓ Skipped (already completed)")
	}

	// Save kubeconfig
	fmt.Println("\n[config] Saving kubeconfig...")
	var kubeconfigPath string
	if o.isSelfProvisioning() {
		// For self-provisioning providers (CAPD/Docker), the bootstrap kubeconfig IS the management kubeconfig.
		kubeconfigPath = filepath.Join("k8-secrets", "kubeconfig", fmt.Sprintf("%s.kubeconfig", o.ClusterName))
		if err := os.MkdirAll(filepath.Dir(kubeconfigPath), 0755); err != nil {
			return fmt.Errorf("failed to create kubeconfig directory: %w", err)
		}
		content, err := os.ReadFile(mgmtKubeconfig)
		if err != nil {
			return fmt.Errorf("failed to read bootstrap kubeconfig: %w", err)
		}
		if err := os.WriteFile(kubeconfigPath, content, 0600); err != nil {
			return fmt.Errorf("failed to write kubeconfig: %w", err)
		}
	} else {
		configMgr := &config.Manager{
			BootstrapKubeconfig: mgmtKubeconfig,
			ClusterName:         o.ClusterName,
			Namespace:           constants.NamespaceCAPI,
		}
		var err error
		kubeconfigPath, err = configMgr.SaveKubeconfig(ctx)
		if err != nil {
			return fmt.Errorf("failed to save kubeconfig: %w", err)
		}
	}
	fmt.Printf("[config] ✓ Kubeconfig saved to: %s\n", kubeconfigPath)

	// Persist kubeconfig path to bootstrap state so downstream steps can find it
	bootstrapState.MgmtKubeconfig = kubeconfigPath
	if err := stateMgr.Save(bootstrapState); err != nil {
		return fmt.Errorf("failed to save kubeconfig path to state: %w", err)
	}

	if !o.isSelfProvisioning() && o.MergeKubeconfig {
		configMgr := &config.Manager{
			BootstrapKubeconfig: mgmtKubeconfig,
			ClusterName:         o.ClusterName,
			Namespace:           constants.NamespaceCAPI,
		}
		if err := configMgr.MergeKubeconfig(ctx, kubeconfigPath); err != nil {
			fmt.Printf("[config] Warning: failed to merge kubeconfig: %v\n", err)
		} else {
			fmt.Println("[config] ✓ Kubeconfig merged into ~/.kube/config")
		}
	}

	// Get ArgoCD password
	compInstaller := &components.Installer{
		Kubeconfig: mgmtKubeconfig,
	}
	argoCDPassword, err := compInstaller.GetArgoCDPassword(ctx)
	if err != nil {
		fmt.Printf("[config] Warning: failed to get ArgoCD password: %v\n", err)
		argoCDPassword = "<check secret manually>"
	}

	fmt.Println("\n✓ Hub Cluster bootstrap complete!")
	fmt.Printf("  Provider: %s\n", o.Provider.Name())
	fmt.Printf("  Cluster Name: %s\n", o.ClusterName)
	fmt.Printf("  Kubeconfig: %s\n", kubeconfigPath)
	fmt.Printf("  ArgoCD Password: %s\n", argoCDPassword)
	fmt.Println("\nNext steps:")
	fmt.Printf("1. Verify cluster: kubectl --kubeconfig=%s get nodes\n", kubeconfigPath)
	fmt.Println("2. Access ArgoCD UI (username: admin)")

	return nil
}

func (o *Orchestrator) handleCloudProvisioning(ctx context.Context, bootstrapState *state.BootstrapState, kubeconfig string, mgmtKubeconfig *string) error {
	stateMgr := state.NewStateManager(o.ClusterName)

	// Phase 5: Management Cluster Provisioning on Hetzner
	if !contains(bootstrapState.CompletedPhases, state.PhaseClusterProvision) {
		fmt.Println("\n[cluster-provision] Provisioning Management Cluster on Hetzner...")

		subnetCIDR := o.NetworkCIDR[:len(o.NetworkCIDR)-2] + "24"
		imageID := o.ImageID
		if o.OSType == "ubuntu" {
			imageID = "ubuntu-24.04"
		}

		ciliumRaw, err := o.readClusterBIOSManifest("manifests/spoke/spoke-bootstrap/", "cilium-addon-template.yaml", "cilium.yaml")
		if err != nil {
			return fmt.Errorf("failed to read cilium manifest: %w", err)
		}

		ccmRaw, err := o.readClusterBIOSManifest("manifests/providers/hetzner/spoke-addons/", "ccm-addon-template.yaml", "ccm.yaml")
		if err != nil {
			return fmt.Errorf("failed to read ccm manifest: %w", err)
		}

		provisioner := &cluster.Provisioner{
			Kubeconfig: kubeconfig,
			Context:    bootstrapState.BootstrapContext,
			Debug:      o.Debug,
			Config: &cluster.Config{
				ClusterName:             o.ClusterName,
				Namespace:               constants.NamespaceCAPI,
				Region:                  o.Region,
				OSType:                  o.OSType,
				ImageID:                 imageID,
				KubernetesVersion:       "v1.31.6",
				NetworkCIDR:             o.NetworkCIDR,
				SubnetCIDR:              subnetCIDR,
				ControlPlaneMachineType: "cx33",
				WorkerMachineType:       "cx33",
				ControlPlaneReplicas:    1,
				WorkerReplicas:          2,
				HCloudToken:             o.HCloudToken,
				CiliumManifest:          string(ciliumRaw),
				CCMManifest:             string(ccmRaw),
			},
		}

		checkCmd := exec.CommandContext(ctx, "kubectl",
			"--kubeconfig", kubeconfig,
			"--context", bootstrapState.BootstrapContext,
			"get", "cluster", o.ClusterName,
			"-n", constants.NamespaceCAPI,
			"-o", "jsonpath={.status.phase}",
		)
		if output, err := checkCmd.Output(); err == nil && string(output) == "Provisioned" {
			fmt.Println("[cluster-provision] ✓ Cluster already exists and provisioned")
		} else {
			if err := provisioner.Provision(ctx); err != nil {
				return fmt.Errorf("failed to provision cluster: %w", err)
			}
			fmt.Println("[cluster-provision] ✓ Cluster resources and CRS applied")
		}

		fmt.Println("[cluster-provision] Waiting for cluster Ready (CRS installing CNI/CCM)...")
		if err := provisioner.WaitForReady(ctx); err != nil {
			return fmt.Errorf("cluster not ready: %w", err)
		}
		fmt.Println("[cluster-provision] ✓ Management Cluster ready")

		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhaseClusterProvision)
		bootstrapState.CurrentPhase = state.PhasePivotMove
		if err := stateMgr.Save(bootstrapState); err != nil {
			return err
		}
	} else {
		fmt.Println("[cluster-provision] ✓ Skipped (already completed)")
	}

	// Phase 6: CAPI Pivot - Move Resources
	if !contains(bootstrapState.CompletedPhases, state.PhasePivotMove) {
		fmt.Println("\n[pivot] Moving CAPI resources to Management Cluster...")

		if bootstrapState.BootstrapContext != "" && strings.HasPrefix(bootstrapState.BootstrapContext, "kind-") {
			kindClusterName := strings.TrimPrefix(bootstrapState.BootstrapContext, "kind-")
			cmd := exec.CommandContext(ctx, "kind", "export", "kubeconfig", "--name", kindClusterName)
			if output, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed to export kind kubeconfig: %w\n%s", err, output)
			}
			if o.Debug {
				fmt.Println("[DEBUG] Refreshed kubeconfig from kind")
			}
		}

		fmt.Println("[pivot] Waiting for all nodes to join cluster...")
		if err := o.waitForAllMachinesRunning(ctx, kubeconfig, bootstrapState.BootstrapContext, 10*time.Minute); err != nil {
			return fmt.Errorf("machines not ready for pivot: %w", err)
		}
		fmt.Println("[pivot] ✓ All nodes joined")

		pivotOrch := &pivot.Orchestrator{
			BootstrapKubeconfig: kubeconfig,
			ClusterName:         o.ClusterName,
			Namespace:           constants.NamespaceCAPI,
			OSType:              o.OSType,
		}

		var err error
		*mgmtKubeconfig, err = pivotOrch.ExecuteMove(ctx)
		if err != nil {
			return fmt.Errorf("pivot move failed: %w", err)
		}
		fmt.Println("[pivot] ✓ Resources moved to Management Cluster")

		bootstrapState.MgmtKubeconfig = *mgmtKubeconfig

		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhasePivotMove)
		bootstrapState.CurrentPhase = state.PhasePivotReady
		if err := stateMgr.Save(bootstrapState); err != nil {
			return err
		}
	} else {
		fmt.Println("[pivot-move] ✓ Skipped (already completed)")
		if *mgmtKubeconfig == "" && bootstrapState.MgmtKubeconfig != "" {
			*mgmtKubeconfig = bootstrapState.MgmtKubeconfig
		}
	}

	// Phase 7: CAPI Pivot - Wait for Ready
	if !contains(bootstrapState.CompletedPhases, state.PhasePivotReady) {
		fmt.Println("\n[pivot-ready] Waiting for cluster reconciliation after move...")

		pivotOrch := &pivot.Orchestrator{
			BootstrapKubeconfig: kubeconfig,
			ClusterName:         o.ClusterName,
			Namespace:           constants.NamespaceCAPI,
			OSType:              o.OSType,
		}

		if err := pivotOrch.WaitForReady(ctx, *mgmtKubeconfig); err != nil {
			return fmt.Errorf("pivot ready failed: %w", err)
		}
		fmt.Println("[pivot-ready] ✓ Cluster ready on Management Cluster")

		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhasePivotReady)
		bootstrapState.CurrentPhase = state.PhaseClusterClassDeploy
		if err := stateMgr.Save(bootstrapState); err != nil {
			return err
		}
	} else {
		fmt.Println("[pivot-ready] ✓ Skipped (already completed)")
	}

	// Cleanup bootstrap cluster if not keeping
	if !o.KeepBootstrap {
		fmt.Println("\n[cleanup] Deleting bootstrap cluster...")
		kindMgr := &KindManager{ClusterName: o.ClusterName}
		if err := kindMgr.Delete(ctx); err != nil {
			fmt.Printf("[cleanup] Warning: failed to delete bootstrap cluster: %v\n", err)
		} else {
			fmt.Println("[cleanup] ✓ Bootstrap cluster deleted")
		}
	}

	return nil
}

func (o *Orchestrator) handleSelfProvisioning(ctx context.Context, bootstrapState *state.BootstrapState, kubeconfig string, mgmtKubeconfig *string) error {
	stateMgr := state.NewStateManager(o.ClusterName)

	// For self-provisioning providers (CAPD/Docker), the Kind bootstrap cluster IS the management cluster.
	// Skip provisioning, pivot, and pivot-ready phases entirely.

	// Mark provisioning as complete (N/A)
	if !contains(bootstrapState.CompletedPhases, state.PhaseClusterProvision) {
		fmt.Println("\n[cluster-provision] ✓ Self-provisioning provider — no external cluster needed")
		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhaseClusterProvision)
		bootstrapState.CurrentPhase = state.PhasePivotMove
		if err := stateMgr.Save(bootstrapState); err != nil {
			return err
		}
	}

	// Mark pivot-move as complete (N/A)
	if !contains(bootstrapState.CompletedPhases, state.PhasePivotMove) {
		fmt.Println("[pivot-move] ✓ Self-provisioning provider — no pivot needed")
		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhasePivotMove)
		bootstrapState.CurrentPhase = state.PhasePivotReady
		if err := stateMgr.Save(bootstrapState); err != nil {
			return err
		}
	}

	// Mark pivot-ready as complete (N/A)
	if !contains(bootstrapState.CompletedPhases, state.PhasePivotReady) {
		fmt.Println("[pivot-ready] ✓ Self-provisioning provider — no pivot needed")
		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhasePivotReady)
		bootstrapState.CurrentPhase = state.PhaseClusterClassDeploy

		// For self-provisioning, the bootstrap kubeconfig IS the management kubeconfig
		*mgmtKubeconfig = kubeconfig
		bootstrapState.MgmtKubeconfig = kubeconfig

		if err := stateMgr.Save(bootstrapState); err != nil {
			return err
		}
	}

	return nil
}

func (o *Orchestrator) runUpgrade(ctx context.Context, bootstrapState *state.BootstrapState) error {
	fmt.Println("\n[upgrade] Starting upgrade/reconciliation...")

	if bootstrapState.MgmtKubeconfig == "" {
		return fmt.Errorf("management cluster kubeconfig not found in state")
	}

	kubeconfig := bootstrapState.MgmtKubeconfig

	if err := o.checkVersionCompatibility(ctx, kubeconfig); err != nil {
		return fmt.Errorf("version compatibility check failed: %w", err)
	}

	fmt.Println("\n[upgrade] Updating CAPI Provider versions...")
	providers := o.Provider.CAPIProviders()
	for _, p := range providers {
		if o.Debug {
			fmt.Printf("[DEBUG] Updating %s/%s to %s\n", p.Kind, p.Name, p.Version)
		}
		patch := fmt.Sprintf(`{"spec":{"version":"%s"}}`, p.Version)
		cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
			"patch", p.Kind, p.Name, "-n", constants.NamespaceCAPI,
			"--type=merge", "-p", patch)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to update %s/%s: %w\nOutput: %s", p.Kind, p.Name, err, string(output))
		}
	}
	fmt.Println("[upgrade] ✓ Providers updated")

	fmt.Println("\n[upgrade] Updating ClusterClass definitions...")
	ccDeployer := &clusterclass.Deployer{
		Kubeconfig: kubeconfig,
		Namespace:  constants.NamespaceCAPI,
		ClassPaths: o.Provider.ClusterClassPaths(),
	}
	if err := ccDeployer.Deploy(ctx); err != nil {
		return fmt.Errorf("failed to update ClusterClasses: %w", err)
	}
	fmt.Println("[upgrade] ✓ ClusterClasses updated")

	fmt.Println("\n[upgrade] Updating platform components...")
	compInstaller := &components.Installer{
		Kubeconfig: kubeconfig,
	}
	if err := compInstaller.InstallAll(ctx, o.HCloudToken); err != nil {
		return fmt.Errorf("failed to update components: %w", err)
	}
	fmt.Println("[upgrade] ✓ Components updated")

	fmt.Println("\n✓ Upgrade/reconciliation complete")
	return nil
}

func (o *Orchestrator) checkVersionCompatibility(ctx context.Context, kubeconfig string) error {
	if o.Debug {
		fmt.Println("[DEBUG] Checking version compatibility...")
	}
	cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
		"api-resources", "--api-group=cluster.x-k8s.io")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to check CAPI API version: %w", err)
	}
	if !bytes.Contains(output, []byte("v1beta1")) {
		return fmt.Errorf("incompatible CAPI API version - v1beta1 required")
	}
	if o.Debug {
		fmt.Println("[DEBUG] ✓ CAPI API version compatible (v1beta1)")
	}
	return nil
}

// readClusterBIOSManifest reads a manifest from the spoke-bootstrap directory
func (o *Orchestrator) readClusterBIOSManifest(basePath, templateFile, dataKey string) ([]byte, error) {
	biosPath := basePath + templateFile
	data, err := os.ReadFile(biosPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", biosPath, err)
	}

	var template map[string]interface{}
	if err := yaml.Unmarshal(data, &template); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", templateFile, err)
	}

	var manifestContent string
	if stringData, ok := template["stringData"].(map[string]interface{}); ok {
		if content, ok := stringData[dataKey].(string); ok {
			manifestContent = content
		}
	} else if dataMap, ok := template["data"].(map[string]interface{}); ok {
		if content, ok := dataMap[dataKey].(string); ok {
			manifestContent = content
		}
	}

	if manifestContent == "" {
		return nil, fmt.Errorf("manifest content not found in %s under key %s", templateFile, dataKey)
	}

	return []byte(manifestContent), nil
}

// isSelfProvisioning is a helper to check the provider type
func (o *Orchestrator) isSelfProvisioning() bool {
	return o.Provider != nil && o.Provider.IsSelfProvisioning()
}

// Helper functions

func contains(phases []state.BootstrapPhase, phase state.BootstrapPhase) bool {
	for _, p := range phases {
		if p == phase {
			return true
		}
	}
	return false
}

func (o *Orchestrator) checkKindClusterExists() error {
	cmd := exec.Command("kind", "get", "clusters")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	clusters := string(output)
	if strings.Contains(clusters, o.ClusterName) {
		return fmt.Errorf("kind cluster '%s' already exists. Run teardown first: ./bin/hub teardown --name=%s", o.ClusterName, o.ClusterName)
	}
	return nil
}

func (o *Orchestrator) waitForAllMachinesRunning(ctx context.Context, kubeconfig, context string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for machines to have nodes joined")
			}

			cmd := exec.CommandContext(ctx, "kubectl",
				"--kubeconfig", kubeconfig,
				"--context", context,
				"get", "machines",
				"-n", constants.NamespaceCAPI,
				"-o", "jsonpath={range .items[*]}{.metadata.name}:{.status.nodeRef.name}{\"\\n\"}{end}",
			)
			output, err := cmd.Output()
			if err != nil {
				continue
			}

			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			allHaveNodes := true
			pendingCount := 0

			for _, line := range lines {
				if line == "" {
					continue
				}
				parts := strings.Split(line, ":")
				if len(parts) != 2 || parts[1] == "" {
					allHaveNodes = false
					pendingCount++
				}
			}

			if allHaveNodes && len(lines) > 0 {
				return nil
			}

			if o.Debug {
				fmt.Printf("[DEBUG] Waiting for %d machines to have nodes joined...\n", pendingCount)
			}
		}
	}
}

func (o *Orchestrator) waitForOperators(ctx context.Context, kubeconfig string) error {
	deadline := time.Now().Add(10 * time.Minute)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for operators to establish webhooks")
			}

			cmd := exec.CommandContext(ctx, "kubectl",
				"--kubeconfig", kubeconfig,
				"get", "validatingwebhookconfigurations",
				"-o", "name",
			)
			output, err := cmd.Output()
			if err != nil {
				continue
			}

			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			for _, line := range lines {
				if line == "" {
					continue
				}
				if strings.Contains(line, "capi") || strings.Contains(line, "caph") || strings.Contains(line, "cert-manager") {
					return nil
				}
			}
		}
	}
}

func waitForDeployment(ctx context.Context, kubeconfig, namespace, deployment string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for deployment %s/%s", namespace, deployment)
			}
			cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
				"rollout", "status", "deployment/"+deployment,
				"-n", namespace, "--timeout=10s")
			if err := cmd.Run(); err == nil {
				return nil
			}
		}
	}
}
// currentGitBranch returns the current git branch name, or empty string if not in a git repo.
func currentGitBranch() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// applyArgoCDBootstrapApp reads a bootstrap Application YAML, injects the
// correct targetRevision, and applies it via kubectl. This ensures the ArgoCD
// Application points to the current feature branch instead of hardcoded HEAD
// (ADR-037 §2: environment-bound hubs must reconcile from a single source of truth).
func applyArgoCDBootstrapApp(ctx context.Context, kubeconfig, boundaryName, targetRevision string) error {
	manifestPath := fmt.Sprintf("manifests/argocd/bootstrap/%s.yaml", boundaryName)
	fmt.Printf("[postboot] Applying %s boundary...\n", boundaryName)

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", manifestPath, err)
	}

	// Replace targetRevision: HEAD with the current branch (unless already HEAD/main)
	patched := strings.Replace(string(raw), "targetRevision: HEAD", "targetRevision: "+targetRevision, 1)

	cmd := exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", kubeconfig,
		"-f", "-",
	)
	cmd.Stdin = strings.NewReader(patched)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply %s: %w\n%s", boundaryName, err, output)
	}
	fmt.Printf("[postboot] ✓ %s boundary applied\n", boundaryName)
	return nil
}
