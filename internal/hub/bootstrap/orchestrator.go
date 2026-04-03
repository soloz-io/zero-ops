package bootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/internal/hub/capi"
	"github.com/soloz-io/zero-ops/internal/hub/cluster"
	"github.com/soloz-io/zero-ops/internal/hub/clusterclass"
	"github.com/soloz-io/zero-ops/internal/hub/components"
	"github.com/soloz-io/zero-ops/internal/hub/config"
	"github.com/soloz-io/zero-ops/internal/hub/constants"
	"github.com/soloz-io/zero-ops/internal/hub/pivot"
	"github.com/soloz-io/zero-ops/internal/hub/state"
	"github.com/soloz-io/zero-ops/internal/hub/versions"
)

// Orchestrator manages the bootstrap process
type Orchestrator struct {
	ClusterName      string
	Region           string
	OSType           string // "ubuntu" or "talos"
	ImageID          string // Talos snapshot ID or ubuntu-24.04
	NetworkCIDR      string
	SSHKey           string
	BootstrapContext string
	KeepBootstrap    bool
	MergeKubeconfig  bool
	HCloudToken      string
	Debug            bool
	Upgrade          bool
}

func (o *Orchestrator) Run(ctx context.Context) error {
	if o.Debug {
		fmt.Println("[DEBUG] Orchestrator.Run() started")
		fmt.Printf("[DEBUG] ClusterName: %s, Region: %s, OSType: %s, Upgrade: %v\n", o.ClusterName, o.Region, o.OSType, o.Upgrade)
	}
	
	stateMgr := state.NewStateManager(o.ClusterName)
	
	// Try to load existing state
	bootstrapState, err := stateMgr.Load()
	if err == nil && bootstrapState != nil {
		fmt.Printf("\n[recovery] Found existing state for cluster '%s'\n", o.ClusterName)
		fmt.Printf("[recovery] Last completed phase: %s\n", bootstrapState.CurrentPhase)
		
		// Check if cluster is fully bootstrapped
		if contains(bootstrapState.CompletedPhases, state.PhaseComplete) {
			if o.Upgrade {
				fmt.Println("[upgrade] Cluster already exists, starting upgrade/reconciliation...")
				return o.runUpgrade(ctx, bootstrapState)
			} else {
				return fmt.Errorf("cluster '%s' already exists. Use --upgrade to reconcile or --name with different name", o.ClusterName)
			}
		}
		
		fmt.Printf("[recovery] Resuming from next phase...\n")
		if o.Debug {
			fmt.Printf("[DEBUG] Completed phases: %v\n", bootstrapState.CompletedPhases)
		}
	} else {
		// Initialize new state
		bootstrapState = &state.BootstrapState{
			Version:      "1.0",
			ClusterName:  o.ClusterName,
			Region:       o.Region,
			TalosImageId: o.ImageID,
			NetworkCIDR:  o.NetworkCIDR,
			CurrentPhase: state.PhaseBootstrapCreate,
		}
		
		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	}
	
	// Determine kubeconfig and context
	var kubeconfig string
	var bootstrapContext string
	var mgmtKubeconfig string
	
	if bootstrapState.BootstrapContext != "" {
		bootstrapContext = bootstrapState.BootstrapContext
	} else if o.BootstrapContext != "" {
		bootstrapContext = o.BootstrapContext
	}
	
	// Restore mgmt kubeconfig from state if available
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
			kindMgr := &KindManager{ClusterName: o.ClusterName}
			
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
			
			// Get kubeconfig path
			homeDir, _ := os.UserHomeDir()
			kubeconfig = filepath.Join(homeDir, ".kube", "config")
			bootstrapState.BootstrapContext = fmt.Sprintf("kind-%s", o.ClusterName)
		}
		
		// Create namespace
		nsMgr := &NamespaceManager{
			Kubeconfig: kubeconfig,
			Context:    bootstrapState.BootstrapContext,
			Namespace:  constants.NamespaceCAPI,
		}
		
		if err := nsMgr.Create(ctx); err != nil {
			return fmt.Errorf("failed to create namespace: %w", err)
		}
		fmt.Println("[bootstrap-create] ✓ Namespace created: hub-platform-capi")
		
		// Update state
		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhaseBootstrapCreate)
		bootstrapState.CurrentPhase = state.PhaseCAPIInit
		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	} else {
		// Recovery: restore kubeconfig from state
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
			OSType:     o.OSType,
			Debug:      o.Debug,
		}
	
		if err := capiInstaller.Install(ctx); err != nil {
			return fmt.Errorf("failed to install CAPI operator: %w", err)
		}
	fmt.Println("[capi-init] ✓ CAPI operator installed")
	
	// Create Hetzner credentials secret
	secretMgr := &capi.SecretManager{
		Kubeconfig: kubeconfig,
		Context:    bootstrapState.BootstrapContext,
		Namespace:  constants.NamespaceCAPI,
	}
	
	if err := secretMgr.CreateHetznerSecret(ctx, o.HCloudToken); err != nil {
		return fmt.Errorf("failed to create Hetzner secret: %w", err)
	}
	fmt.Println("[capi-init] ✓ Hetzner credentials secret created")
	
		// Update state
		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhaseCAPIInit)
		bootstrapState.CurrentPhase = state.PhaseClusterProvision
		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	} else {
		fmt.Println("[capi-init] ✓ Skipped (already completed)")
	}
	
	// Phase 5: Management Cluster Provisioning
	if !contains(bootstrapState.CompletedPhases, state.PhaseClusterProvision) {
		fmt.Println("\n[cluster-provision] Provisioning Management Cluster on Hetzner...")
		
		// Calculate subnet CIDR from network CIDR
		subnetCIDR := o.NetworkCIDR[:len(o.NetworkCIDR)-2] + "24" // Simple: change /16 to /24
		
		// Determine image ID based on OS
		imageID := o.ImageID
		if o.OSType == "ubuntu" {
			imageID = "ubuntu-24.04"
		}
		
		// Load rendered manifests for CRS
		ciliumRaw, err := assets.ReadManifest("addons/cilium-rendered.yaml")
		if err != nil {
			return fmt.Errorf("failed to read cilium manifest: %w", err)
		}
		
		ccmRaw, err := assets.ReadManifest("addons/ccm-rendered.yaml")
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
				ControlPlaneMachineType: "cx23",
				WorkerMachineType:       "cx33",
				ControlPlaneReplicas:    3,
				WorkerReplicas:          2,
				HCloudToken:             o.HCloudToken,
				CiliumManifest:          string(ciliumRaw),
				CCMManifest:             string(ccmRaw),
			},
		}
		
		// Check if cluster already exists and is ready (recovery scenario)
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
		
		// Now wait for cluster to become Ready (CRS will auto-install CNI/CCM)
		fmt.Println("[cluster-provision] Waiting for cluster Ready (CRS installing CNI/CCM)...")
	if err := provisioner.WaitForReady(ctx); err != nil {
		return fmt.Errorf("cluster not ready: %w", err)
	}
	fmt.Println("[cluster-provision] ✓ Management Cluster ready")
	
		// Update state
		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhaseClusterProvision)
		bootstrapState.CurrentPhase = state.PhasePivotMove
		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	} else {
		fmt.Println("[cluster-provision] ✓ Skipped (already completed)")
	}
	
	// Phase 6: CAPI Pivot - Move Resources
	if !contains(bootstrapState.CompletedPhases, state.PhasePivotMove) {
		fmt.Println("\n[pivot] Moving CAPI resources to Management Cluster...")
		
		pivotOrch := &pivot.Orchestrator{
			BootstrapKubeconfig: kubeconfig,
			ClusterName:         o.ClusterName,
			Namespace:           constants.NamespaceCAPI,
			OSType:              o.OSType,
		}
		
		var err error
		mgmtKubeconfig, err = pivotOrch.ExecuteMove(ctx)
		if err != nil {
			return fmt.Errorf("pivot move failed: %w", err)
		}
		fmt.Println("[pivot] ✓ Resources moved to Management Cluster")
		
		bootstrapState.MgmtKubeconfig = mgmtKubeconfig
		
		// Update state
		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhasePivotMove)
		bootstrapState.CurrentPhase = state.PhasePivotReady
		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	} else {
		fmt.Println("[pivot-move] ✓ Skipped (already completed)")
		// Restore mgmtKubeconfig if not set
		if mgmtKubeconfig == "" && bootstrapState.MgmtKubeconfig != "" {
			mgmtKubeconfig = bootstrapState.MgmtKubeconfig
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
		
		if err := pivotOrch.WaitForReady(ctx, mgmtKubeconfig); err != nil {
			return fmt.Errorf("pivot ready failed: %w", err)
		}
		fmt.Println("[pivot-ready] ✓ Cluster ready on Management Cluster")
		
		// Update state
		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhasePivotReady)
		bootstrapState.CurrentPhase = state.PhaseClusterClassDeploy
		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
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
	
	// Phase 7: ClusterClass Library Deployment
	if !contains(bootstrapState.CompletedPhases, state.PhaseClusterClassDeploy) {
		fmt.Println("\n[clusterclass-deploy] Deploying ClusterClass library...")
		
		ccDeployer := &clusterclass.Deployer{
		Kubeconfig: mgmtKubeconfig,
		Namespace:  constants.NamespaceCAPI,
	}
	
	if err := ccDeployer.Deploy(ctx); err != nil {
		return fmt.Errorf("failed to deploy ClusterClass library: %w", err)
	}
	fmt.Println("[clusterclass-deploy] ✓ ClusterClass library deployed")
	
		// Update state
		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhaseClusterClassDeploy)
		bootstrapState.CurrentPhase = state.PhasePostBoot
		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	} else {
		fmt.Println("[clusterclass-deploy] ✓ Skipped (already completed)")
	}
	
	// Phase 8: Post-Bootstrap Components
	if !contains(bootstrapState.CompletedPhases, state.PhasePostBoot) {
		fmt.Println("\n[postboot] Installing platform components...")
		
		compInstaller := &components.Installer{
		Kubeconfig: mgmtKubeconfig,
	}
	
	if err := compInstaller.InstallAll(ctx, o.HCloudToken); err != nil {
		return fmt.Errorf("failed to install components: %w", err)
	}
	fmt.Println("[postboot] ✓ All components installed")
	
	// Apply platform-core app-of-apps
	fmt.Println("[postboot] Applying platform-core app-of-apps...")
	cmd := exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", mgmtKubeconfig,
		"-f", "manifests/argocd/app-of-apps.yaml",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply app-of-apps: %w\n%s", err, output)
	}
	fmt.Println("[postboot] ✓ platform-core app-of-apps applied")
	
		// Update state
		bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhasePostBoot)
		bootstrapState.CurrentPhase = state.PhaseComplete
		if err := stateMgr.Save(bootstrapState); err != nil {
			return fmt.Errorf("failed to save state: %w", err)
		}
	} else {
		fmt.Println("[postboot] ✓ Skipped (already completed)")
	}
	
	// Phase 9: Kubeconfig & Talosconfig Management
	fmt.Println("\n[config] Saving kubeconfig and talosconfig...")
	
	configMgr := &config.Manager{
		BootstrapKubeconfig: mgmtKubeconfig, // Use management cluster kubeconfig
		ClusterName:         o.ClusterName,
		Namespace:           constants.NamespaceCAPI,
	}
	
	kubeconfigPath, err := configMgr.SaveKubeconfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to save kubeconfig: %w", err)
	}
	fmt.Printf("[config] ✓ Kubeconfig saved to: %s\n", kubeconfigPath)
	
	// Talosconfig only exists for Talos clusters
	var talosconfigPath string
	if o.OSType == "talos" {
		talosconfigPath, err = configMgr.SaveTalosconfig(ctx)
		if err != nil {
			return fmt.Errorf("failed to save talosconfig: %w", err)
		}
		fmt.Printf("[config] ✓ Talosconfig saved to: %s\n", talosconfigPath)
	}
	
	// Merge kubeconfig if requested
	if o.MergeKubeconfig {
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
	
	// Print success message
	fmt.Println("\n✓ Management Cluster bootstrap complete!")
	fmt.Printf("  Cluster Name: %s\n", o.ClusterName)
	fmt.Printf("  Region: %s\n", o.Region)
	fmt.Printf("  Kubeconfig: %s\n", kubeconfigPath)
	if o.OSType == "talos" {
		fmt.Printf("  Talosconfig: %s\n", talosconfigPath)
	}
	fmt.Printf("  ArgoCD Password: %s\n", argoCDPassword)
	fmt.Println("\nNext steps:")
	fmt.Printf("1. Verify cluster: kubectl --kubeconfig=%s get nodes\n", kubeconfigPath)
	if o.OSType == "talos" {
		fmt.Printf("2. Access nodes: talosctl --talosconfig=%s -n <node-ip> version\n", talosconfigPath)
	}
	fmt.Println("3. Access ArgoCD UI (username: admin)")
	
	return nil
}


func (o *Orchestrator) getMgmtKubeconfig(ctx context.Context, bootstrapKubeconfig, bootstrapContext string) (string, error) {
	secretName := fmt.Sprintf("%s-kubeconfig", o.ClusterName)
	
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", bootstrapKubeconfig,
		"--context", bootstrapContext,
		"get", "secret", secretName,
		"-n", constants.NamespaceCAPI,
		"-o", "jsonpath={.data.value}",
	)
	
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	
	decoded, err := base64.StdEncoding.DecodeString(string(output))
	if err != nil {
		return "", err
	}
	
	// Save to temp file
	tmpDir := os.TempDir()
	path := filepath.Join(tmpDir, fmt.Sprintf("%s-temp.kubeconfig", o.ClusterName))
	if err := os.WriteFile(path, decoded, 0600); err != nil {
		return "", err
	}
	
	return path, nil
}


func (o *Orchestrator) waitAndGetKubeconfig(ctx context.Context, bootstrapKubeconfig, bootstrapContext string) (string, error) {
	secretName := fmt.Sprintf("%s-kubeconfig", o.ClusterName)
	
	// Wait for secret to exist
	for i := 0; i < 60; i++ {
		cmd := exec.CommandContext(ctx, "kubectl",
			"--kubeconfig", bootstrapKubeconfig,
			"--context", bootstrapContext,
			"get", "secret", secretName,
			"-n", constants.NamespaceCAPI,
			"-o", "jsonpath={.data.value}",
		)
		
		output, err := cmd.Output()
		if err == nil && len(output) > 0 {
			decoded, err := base64.StdEncoding.DecodeString(string(output))
			if err != nil {
				return "", err
			}
			
			tmpDir := os.TempDir()
			path := filepath.Join(tmpDir, fmt.Sprintf("%s-temp.kubeconfig", o.ClusterName))
			if err := os.WriteFile(path, decoded, 0600); err != nil {
				return "", err
			}
			
			return path, nil
		}
		
		time.Sleep(5 * time.Second)
	}
	
	return "", fmt.Errorf("timeout waiting for kubeconfig secret")
}

func (o *Orchestrator) waitForNodeToRegister(ctx context.Context, kubeconfig string) error {
	// Wait for at least one node with control-plane role to appear
	for i := 0; i < 120; i++ {
		cmd := exec.CommandContext(ctx, "kubectl",
			"--kubeconfig", kubeconfig,
			"get", "nodes",
			"-l", "node-role.kubernetes.io/control-plane",
			"-o", "name",
		)
		
		output, err := cmd.Output()
		if err == nil && len(output) > 0 {
			return nil
		}
		
		time.Sleep(5 * time.Second)
	}
	
	return fmt.Errorf("timeout waiting for control plane node to register")
}

func (o *Orchestrator) runUpgrade(ctx context.Context, bootstrapState *state.BootstrapState) error {
	fmt.Println("\n[upgrade] Starting upgrade/reconciliation...")
	
	if bootstrapState.MgmtKubeconfig == "" {
		return fmt.Errorf("management cluster kubeconfig not found in state")
	}
	
	kubeconfig := bootstrapState.MgmtKubeconfig
	
	// Version compatibility check
	if err := o.checkVersionCompatibility(ctx, kubeconfig); err != nil {
		return fmt.Errorf("version compatibility check failed: %w", err)
	}
	
	// Update Provider CRD versions
	fmt.Println("\n[upgrade] Updating CAPI Provider versions...")
	if err := o.updateProviders(ctx, kubeconfig); err != nil {
		return fmt.Errorf("failed to update providers: %w", err)
	}
	fmt.Println("[upgrade] ✓ Providers updated")
	
	// Re-apply ClusterClass definitions
	fmt.Println("\n[upgrade] Updating ClusterClass definitions...")
	ccDeployer := &clusterclass.Deployer{
		Kubeconfig: kubeconfig,
		Namespace:  constants.NamespaceCAPI,
	}
	
	if err := ccDeployer.Deploy(ctx); err != nil {
		return fmt.Errorf("failed to update ClusterClasses: %w", err)
	}
	fmt.Println("[upgrade] ✓ ClusterClasses updated")
	
	// Re-apply component manifests
	fmt.Println("\n[upgrade] Updating platform components...")
	compInstaller := &components.Installer{
		Kubeconfig: kubeconfig,
	}
	
	if err := compInstaller.InstallAll(ctx, o.HCloudToken); err != nil {
		return fmt.Errorf("failed to update components: %w", err)
	}
	fmt.Println("[upgrade] ✓ Components updated")
	
	fmt.Println("\n✓ Upgrade/reconciliation complete")
	fmt.Println("  All Providers, ClusterClasses, and components updated to match CLI version")
	
	return nil
}

func (o *Orchestrator) checkVersionCompatibility(ctx context.Context, kubeconfig string) error {
	if o.Debug {
		fmt.Println("[DEBUG] Checking version compatibility...")
	}
	
	// Check CAPI API version (v1beta1)
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

func (o *Orchestrator) updateProviders(ctx context.Context, kubeconfig string) error {
	providers := []struct {
		kind    string
		name    string
		version string
	}{
		{"CoreProvider", "cluster-api", versions.CAPIVersion},
		{"BootstrapProvider", "talos", versions.TalosBootstrapProviderVersion},
		{"ControlPlaneProvider", "talos", versions.TalosControlPlaneProviderVersion},
		{"InfrastructureProvider", "hetzner", versions.HetznerInfraProviderVersion},
	}
	
	for _, p := range providers {
		if o.Debug {
			fmt.Printf("[DEBUG] Updating %s/%s to %s\n", p.kind, p.name, p.version)
		}
		
		// Patch Provider CRD spec.version
		patch := fmt.Sprintf(`{"spec":{"version":"%s"}}`, p.version)
		cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
			"patch", p.kind, p.name, "-n", "capi-operator-system",
			"--type=merge", "-p", patch)
		
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to update %s/%s: %w\nOutput: %s", p.kind, p.name, err, string(output))
		}
	}
	
	// Wait for providers to reconcile (cluster-api-operator handles this)
	fmt.Println("[upgrade] Waiting for Provider reconciliation...")
	time.Sleep(10 * time.Second) // Give operator time to start reconciliation
	
	return nil
}

func mustReadCatalog(path string) []byte {
	data, err := assets.ReadCatalog(path)
	if err != nil {
		panic(fmt.Sprintf("failed to read catalog %s: %v", path, err))
	}
	return data
}

// contains checks if a phase is in the completed phases list
func contains(phases []state.BootstrapPhase, phase state.BootstrapPhase) bool {
	for _, p := range phases {
		if p == phase {
			return true
		}
	}
	return false
}
