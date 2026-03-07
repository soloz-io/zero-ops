package bootstrap

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/pkg/capi"
	"github.com/soloz-io/zero-ops/pkg/cluster"
	"github.com/soloz-io/zero-ops/pkg/clusterclass"
	"github.com/soloz-io/zero-ops/pkg/components"
	"github.com/soloz-io/zero-ops/pkg/config"
	"github.com/soloz-io/zero-ops/pkg/pivot"
	"github.com/soloz-io/zero-ops/pkg/state"
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
}

func (o *Orchestrator) Run(ctx context.Context) error {
	stateMgr := state.NewStateManager(o.ClusterName)
	
	// Initialize state
	bootstrapState := &state.BootstrapState{
		Version:     "1.0",
		ClusterName: o.ClusterName,
		Region:      o.Region,
		TalosImageId: o.ImageID,
		NetworkCIDR: o.NetworkCIDR,
		CurrentPhase: state.PhaseBootstrapCreate,
	}
	
	if err := stateMgr.Save(bootstrapState); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}
	
	// Phase 3: Bootstrap Cluster Creation
	fmt.Println("\n[bootstrap-create] Creating ephemeral bootstrap cluster...")
	
	var kubeconfig string
	if o.BootstrapContext != "" {
		fmt.Printf("[bootstrap-create] Using existing context: %s\n", o.BootstrapContext)
		homeDir, _ := os.UserHomeDir()
		kubeconfig = filepath.Join(homeDir, ".kube", "config")
		bootstrapState.BootstrapContext = o.BootstrapContext
	} else {
		kindMgr := &KindManager{ClusterName: "bootstrap-zero-ops"}
		
		if kindMgr.Exists(ctx) {
			fmt.Println("[bootstrap-create] Bootstrap cluster already exists")
		} else {
			if err := kindMgr.Create(ctx); err != nil {
				return fmt.Errorf("failed to create Kind cluster: %w", err)
			}
			fmt.Println("[bootstrap-create] ✓ Kind cluster created")
		}
		
		// Get kubeconfig path
		homeDir, _ := os.UserHomeDir()
		kubeconfig = filepath.Join(homeDir, ".kube", "config")
		bootstrapState.BootstrapContext = "kind-bootstrap-zero-ops"
	}
	
	// Create namespace
	nsMgr := &NamespaceManager{
		Kubeconfig: kubeconfig,
		Context:    bootstrapState.BootstrapContext,
		Namespace:  "zero-ops-system",
	}
	
	if err := nsMgr.Create(ctx); err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}
	fmt.Println("[bootstrap-create] ✓ Namespace created: zero-ops-system")
	
	// Update state
	bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhaseBootstrapCreate)
	bootstrapState.CurrentPhase = state.PhaseCAPIInit
	if err := stateMgr.Save(bootstrapState); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}
	
	// Phase 4: CAPI Initialization
	fmt.Println("\n[capi-init] Installing cluster-api-operator...")
	
	capiInstaller := &capi.OperatorInstaller{
		Kubeconfig: kubeconfig,
		Context:    bootstrapState.BootstrapContext,
		Namespace:  "zero-ops-system",
		OSType:     o.OSType,
	}
	
	if err := capiInstaller.Install(ctx); err != nil {
		return fmt.Errorf("failed to install CAPI operator: %w", err)
	}
	fmt.Println("[capi-init] ✓ CAPI operator installed")
	
	// Create Hetzner credentials secret
	secretMgr := &capi.SecretManager{
		Kubeconfig: kubeconfig,
		Context:    bootstrapState.BootstrapContext,
		Namespace:  "zero-ops-system",
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
	
	// Phase 5: Management Cluster Provisioning
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
		Config: &cluster.Config{
			ClusterName:             o.ClusterName,
			Namespace:               "zero-ops-system",
			Region:                  o.Region,
			OSType:                  o.OSType,
			ImageID:                 imageID,
			KubernetesVersion:       "v1.31.6",
			NetworkCIDR:             o.NetworkCIDR,
			SubnetCIDR:              subnetCIDR,
			ControlPlaneMachineType: "cx23",
			WorkerMachineType:       "cx23",
			ControlPlaneReplicas:    3,
			WorkerReplicas:          2,
			HCloudToken:             o.HCloudToken,
			CiliumManifest:          string(ciliumRaw),
			CCMManifest:             string(ccmRaw),
		},
	}
	
	if err := provisioner.Provision(ctx); err != nil {
		return fmt.Errorf("failed to provision cluster: %w", err)
	}
	fmt.Println("[cluster-provision] ✓ Cluster resources and CRS applied")
	
	// Now wait for cluster to become Ready (CRS will auto-install CNI/CCM)
	fmt.Println("[cluster-provision] Waiting for cluster Ready (CRS installing CNI/CCM)...")
	if err := provisioner.WaitForReady(ctx); err != nil {
		return fmt.Errorf("cluster not ready: %w", err)
	}
	fmt.Println("[cluster-provision] ✓ Management Cluster ready")
	
	// Update state
	bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhaseClusterProvision)
	bootstrapState.CurrentPhase = state.PhasePivot
	if err := stateMgr.Save(bootstrapState); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}
	
	// Phase 6: CAPI Pivot
	fmt.Println("\n[pivot] Moving CAPI resources to Management Cluster...")
	
	pivotOrch := &pivot.Orchestrator{
		BootstrapKubeconfig: kubeconfig,
		ClusterName:         o.ClusterName,
		Namespace:           "zero-ops-system",
	}
	
	mgmtKubeconfig, err := pivotOrch.Execute(ctx)
	if err != nil {
		return fmt.Errorf("pivot failed: %w", err)
	}
	fmt.Println("[pivot] ✓ CAPI pivot complete")
	
	bootstrapState.MgmtKubeconfig = mgmtKubeconfig
	
	// Update state
	bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhasePivot)
	bootstrapState.CurrentPhase = state.PhaseClusterClassDeploy
	if err := stateMgr.Save(bootstrapState); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}
	
	// Cleanup bootstrap cluster if not keeping
	if !o.KeepBootstrap {
		fmt.Println("\n[cleanup] Deleting bootstrap cluster...")
		kindMgr := &KindManager{ClusterName: "bootstrap-zero-ops"}
		if err := kindMgr.Delete(ctx); err != nil {
			fmt.Printf("[cleanup] Warning: failed to delete bootstrap cluster: %v\n", err)
		} else {
			fmt.Println("[cleanup] ✓ Bootstrap cluster deleted")
		}
	}
	
	// Phase 7: ClusterClass Library Deployment
	fmt.Println("\n[clusterclass-deploy] Deploying ClusterClass library...")
	
	ccDeployer := &clusterclass.Deployer{
		Kubeconfig: mgmtKubeconfig,
		Namespace:  "zero-ops-system",
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
	
	// Phase 8: Post-Bootstrap Components
	fmt.Println("\n[postboot] Installing platform components...")
	
	compInstaller := &components.Installer{
		Kubeconfig: mgmtKubeconfig,
	}
	
	if err := compInstaller.InstallAll(ctx); err != nil {
		return fmt.Errorf("failed to install components: %w", err)
	}
	fmt.Println("[postboot] ✓ All components installed")
	
	// Update state
	bootstrapState.CompletedPhases = append(bootstrapState.CompletedPhases, state.PhasePostBoot)
	bootstrapState.CurrentPhase = state.PhaseComplete
	if err := stateMgr.Save(bootstrapState); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}
	
	// Phase 9: Kubeconfig & Talosconfig Management
	fmt.Println("\n[config] Saving kubeconfig and talosconfig...")
	
	configMgr := &config.Manager{
		BootstrapKubeconfig: kubeconfig,
		ClusterName:         o.ClusterName,
		Namespace:           "zero-ops-system",
	}
	
	kubeconfigPath, err := configMgr.SaveKubeconfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to save kubeconfig: %w", err)
	}
	fmt.Printf("[config] ✓ Kubeconfig saved to: %s\n", kubeconfigPath)
	
	talosconfigPath, err := configMgr.SaveTalosconfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to save talosconfig: %w", err)
	}
	fmt.Printf("[config] ✓ Talosconfig saved to: %s\n", talosconfigPath)
	
	// Merge kubeconfig if requested
	if o.MergeKubeconfig {
		if err := configMgr.MergeKubeconfig(ctx, kubeconfigPath); err != nil {
			fmt.Printf("[config] Warning: failed to merge kubeconfig: %v\n", err)
		} else {
			fmt.Println("[config] ✓ Kubeconfig merged into ~/.kube/config")
		}
	}
	
	// Get ArgoCD password
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
	fmt.Printf("  Talosconfig: %s\n", talosconfigPath)
	fmt.Printf("  ArgoCD Password: %s\n", argoCDPassword)
	fmt.Println("\nNext steps:")
	fmt.Printf("1. Verify cluster: kubectl --kubeconfig=%s get nodes\n", kubeconfigPath)
	fmt.Printf("2. Access nodes: talosctl --talosconfig=%s -n <node-ip> version\n", talosconfigPath)
	fmt.Println("3. Access ArgoCD UI (username: admin)")
	
	return nil
}


func (o *Orchestrator) getMgmtKubeconfig(ctx context.Context, bootstrapKubeconfig, bootstrapContext string) (string, error) {
	secretName := fmt.Sprintf("%s-kubeconfig", o.ClusterName)
	
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", bootstrapKubeconfig,
		"--context", bootstrapContext,
		"get", "secret", secretName,
		"-n", "zero-ops-system",
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
			"-n", "zero-ops-system",
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


func mustReadCatalog(path string) []byte {
	data, err := assets.ReadCatalog(path)
	if err != nil {
		panic(fmt.Sprintf("failed to read catalog %s: %v", path, err))
	}
	return data
}
