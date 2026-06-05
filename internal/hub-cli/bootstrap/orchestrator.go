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

	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/clusterclass"
	"github.com/soloz-io/zero-ops/internal/hub-cli/components"
	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
	"github.com/soloz-io/zero-ops/internal/hub-cli/state"
)

// Orchestrator runs the 12-phase hub cluster bootstrap pipeline.
//
// The pipeline is identical for every provider — local or cloud. The
// orchestrator never branches on provider name or type. Cloud-specific
// phases (5-7) are identity/no-ops for local providers by design.
//
//	Phase  1: preflight          Provider.PreflightValidators()
//	Phase  2: bootstrap-create   kind cluster (orchestrator-owned)
//	Phase  3: day0-infra         Provider.ProvisionDayZero(kubeconfig)
//	Phase  4: capi-init          CAPI operator + Provider.OnCAPIInit()
//	Phase  5: cluster-provision  Provider.ProvisionManagementCluster(cfg)
//	Phase  6: pivot-move         Provider.PivotMove(cfg) → mgmtKubeconfig
//	Phase  7: pivot-ready        Provider.PivotReady(mgmtKubeconfig)
//	Phase  8: cleanup            delete kind if !Provider.IsLocal()
//	Phase  9: clusterclass       ClusterClass deploy (orchestrator-owned)
//	Phase 10: platform-pre-reqs  Provider.OnPlatformPreReqs(kubeconfig)
//	Phase 11: platform-deploy    ArgoCD + bootstrap apps (orchestrator-owned)
//	Phase 12: finalize           Provider.Finalize(cfg) → kubeconfigPath
type Orchestrator struct {
	Provider         Provider
	ClusterName      string
	BootstrapContext string
	KeepBootstrap    bool
	MergeKubeconfig  bool
	Debug            bool
	Upgrade          bool
}

// Run executes the full 12-phase bootstrap pipeline with checkpoint/restart.
func (o *Orchestrator) Run(ctx context.Context) error {
	if o.Debug {
		fmt.Println("[DEBUG] Orchestrator.Run() started")
		fmt.Printf("[DEBUG] Provider: %s, ClusterName: %s, Upgrade: %v\n",
			o.Provider.Name(), o.ClusterName, o.Upgrade)
	}

	stateMgr := state.NewStateManager(o.ClusterName)
	bs, err := stateMgr.Load()
	if err == nil && bs != nil {
		return o.handleExistingState(ctx, stateMgr, bs)
	}

	// Guard: kind cluster must not already exist on fresh bootstrap
	if err := o.checkKindClusterExists(); err != nil {
		return err
	}

	return o.runFresh(ctx, stateMgr, nil)
}

// ──────────────────────────────────────────────────────────────────────────
// Fresh bootstrap (or resume with existing state)
// ──────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) runFresh(ctx context.Context, stateMgr *state.StateManager, existing *state.BootstrapState) error {
	bs := existing
	if bs == nil {
		bs = &state.BootstrapState{
			Version:      "1.0",
			ClusterName:  o.ClusterName,
			Provider:     o.Provider.Name(),
			CurrentPhase: state.PhaseBootstrapCreate,
		}
	}

	// ── Phase 2: Bootstrap cluster (kind) ─────────────────────────────
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseBootstrapCreate, "bootstrap-create",
		"Creating ephemeral bootstrap cluster...",
		func() error { return o.createKindCluster(ctx, bs) },
		nil); err != nil {
		return fmt.Errorf("bootstrap create failed: %w", err)
	}

	kubeconfig, bootstrapCtx := o.kubeconfigPaths(bs)

	// ── Phase 3: Day-0 infrastructure ─────────────────────────────────
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseDayZero, "day0-infra",
		"Applying provider-specific Day-0 infrastructure...",
		func() error { return o.Provider.ProvisionDayZero(ctx, kubeconfig) },
		func() { fmt.Println("[day0-infra] ✓ Day-0 infrastructure applied") },
	); err != nil {
		return err
	}

	// ── Phase 4: CAPI initialization ──────────────────────────────────
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseCAPIInit, "capi-init",
		"Installing cluster-api-operator...",
		func() error { return o.installCAPI(ctx, kubeconfig, bootstrapCtx) },
		func() { fmt.Println("[capi-init] ✓ CAPI operator installed") },
	); err != nil {
		return err
	}

	// ── Phase 5: Management cluster provisioning ──────────────────────
	provCfg := &ProvisionConfig{
		ClusterName:       o.ClusterName,
		BootstrapKubeconfig: kubeconfig,
		BootstrapContext:  bootstrapCtx,
		Debug:             o.Debug,
	}
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseClusterProvision, "cluster-provision",
		"",
		func() error { return o.Provider.ProvisionManagementCluster(ctx, provCfg) },
		nil,
	); err != nil {
		return err
	}

	// ── Phase 6: Pivot move ───────────────────────────────────────────
	pivotCfg := &PivotConfig{
		ClusterName:       o.ClusterName,
		BootstrapKubeconfig: kubeconfig,
		BootstrapContext:  bootstrapCtx,
		Debug:             o.Debug,
	}
	mgmtKubeconfig := bs.MgmtKubeconfig
	if err := o.runPhase(ctx, stateMgr, bs, state.PhasePivotMove, "pivot-move",
		"",
		func() error {
			var err error
			mgmtKubeconfig, err = o.Provider.PivotMove(ctx, pivotCfg)
			return err
		},
		nil,
	); err != nil {
		return err
	}
	if mgmtKubeconfig != "" {
		bs.MgmtKubeconfig = mgmtKubeconfig
	}

	// ── Phase 7: Pivot ready ──────────────────────────────────────────
	if err := o.runPhase(ctx, stateMgr, bs, state.PhasePivotReady, "pivot-ready",
		"",
		func() error { return o.Provider.PivotReady(ctx, mgmtKubeconfig) },
		nil,
	); err != nil {
		return err
	}

	// ── Phase 8: Cleanup bootstrap cluster ────────────────────────────
	if !o.Provider.IsLocal() && !o.KeepBootstrap {
		fmt.Println("\n[cleanup] Deleting bootstrap cluster...")
		kindMgr := &KindManager{ClusterName: o.ClusterName}
		if err := kindMgr.Delete(ctx); err != nil {
			fmt.Printf("[cleanup] Warning: failed to delete bootstrap cluster: %v\n", err)
		} else {
			fmt.Println("[cleanup] ✓ Bootstrap cluster deleted")
		}
	}
	o.markPhaseComplete(bs, state.PhaseCleanup)
	stateMgr.Save(bs)

	// ── Phase 9: ClusterClass deployment ──────────────────────────────
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseClusterClassDeploy, "clusterclass-deploy",
		"Deploying ClusterClass library...",
		func() error {
			ccd := &clusterclass.Deployer{
				Kubeconfig: mgmtKubeconfig,
				Namespace:  constants.NamespaceCAPI,
				ClassPaths: o.Provider.ClusterClassPaths(),
			}
			return ccd.Deploy(ctx)
		},
		func() { fmt.Println("[clusterclass-deploy] ✓ ClusterClass library deployed") },
	); err != nil {
		return err
	}

	// ── Phase 10: Platform pre-requisites ─────────────────────────────
	if err := o.runPhase(ctx, stateMgr, bs, state.PhasePlatformPreReqs, "platform-pre-reqs",
		"Applying platform pre-requisites...",
		func() error { return o.Provider.OnPlatformPreReqs(ctx, mgmtKubeconfig) },
		func() { fmt.Println("[platform-pre-reqs] ✓ Platform pre-requisites applied") },
	); err != nil {
		return err
	}

	// ── Phase 11: Platform deploy (ArgoCD + bootstrap apps) ───────────
	if err := o.runPhase(ctx, stateMgr, bs, state.PhasePlatformDeploy, "platform-deploy",
		"Installing platform components...",
		func() error { return o.deployPlatform(ctx, mgmtKubeconfig) },
		nil,
	); err != nil {
		return err
	}

	// ── Phase 11: Platform deploy (ArgoCD + bootstrap apps) ───────────
	// Wait for CRDs to be queryable before applying data workloads (webhooks
	// guarantee CRDs exist, but API server needs extra seconds to register).
	if err := o.waitForCRDs(ctx, mgmtKubeconfig); err != nil {
		return fmt.Errorf("CRDs not ready: %w", err)
	}
	if err := o.runPhase(ctx, stateMgr, bs, state.PhasePlatformDeploy, "platform-deploy",
		"Installing platform components...",
		func() error { return o.deployPlatform(ctx, mgmtKubeconfig) },
		nil,
	); err != nil {
		return err
	}

	// ── Phase 12: Finalize ────────────────────────────────────────────
	var kubeconfigPath string
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseFinalize, "finalize",
		"Finalizing bootstrap...",
		func() error {
			var err error
			kubeconfigPath, err = o.Provider.Finalize(ctx, &FinalizeConfig{
				MgmtKubeconfig:  mgmtKubeconfig,
				ClusterName:     o.ClusterName,
				Namespace:       constants.NamespaceCAPI,
				MergeKubeconfig: o.MergeKubeconfig,
				Debug:           o.Debug,
			})
			return err
		},
		nil,
	); err != nil {
		return err
	}

	// ── Completion ────────────────────────────────────────────────────
	bs.CompletedPhases = append(bs.CompletedPhases, state.PhaseComplete)
	bs.CurrentPhase = state.PhaseComplete
	stateMgr.Save(bs)

	argoCDPwd := o.getArgoCDPassword(ctx, mgmtKubeconfig)
	fmt.Println("\n✓ Hub Cluster bootstrap complete!")
	fmt.Printf("  Provider: %s\n", o.Provider.Name())
	fmt.Printf("  Cluster Name: %s\n", o.ClusterName)
	fmt.Printf("  Kubeconfig: %s\n", kubeconfigPath)
	fmt.Printf("  ArgoCD Password: %s\n", argoCDPwd)
	fmt.Println("\nNext steps:")
	fmt.Printf("  1. Verify cluster: kubectl --kubeconfig=%s get nodes\n", kubeconfigPath)
	fmt.Println("  2. Access ArgoCD UI (username: admin)")
	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// Phase runner
// ──────────────────────────────────────────────────────────────────────────

// runPhase executes a single bootstrap phase with checkpoint persistence.
// If the phase is already completed, it skips. On success, it saves state
// and advances to the next phase.
func (o *Orchestrator) runPhase(
	ctx context.Context,
	stateMgr *state.StateManager,
	bs *state.BootstrapState,
	phase state.BootstrapPhase,
	label, header string,
	action func() error,
	onSuccess func(),
) error {
	if o.phaseDone(bs, phase) {
		fmt.Printf("[%s] ✓ Skipped (already completed)\n", label)
		return nil
	}
	if header != "" {
		fmt.Println("\n[" + label + "] " + header)
	}
	if o.Debug {
		fmt.Printf("[DEBUG] Phase: %s\n", phase)
	}
	if err := action(); err != nil {
		return fmt.Errorf("[%s] %w", label, err)
	}
	if onSuccess != nil {
		onSuccess()
	}
	o.markPhaseComplete(bs, phase)
	return stateMgr.Save(bs)
}

func (o *Orchestrator) phaseDone(bs *state.BootstrapState, phase state.BootstrapPhase) bool {
	for _, p := range bs.CompletedPhases {
		if p == phase {
			return true
		}
	}
	return false
}

func (o *Orchestrator) markPhaseComplete(bs *state.BootstrapState, phase state.BootstrapPhase) {
	bs.CompletedPhases = append(bs.CompletedPhases, phase)
	// advance current phase
	bs.CurrentPhase = phase
}

// ──────────────────────────────────────────────────────────────────────────
// Phase 2: Kind cluster
// ──────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) createKindCluster(ctx context.Context, bs *state.BootstrapState) error {
	if o.BootstrapContext != "" {
		fmt.Printf("[bootstrap-create] Using existing context: %s\n", o.BootstrapContext)
		bs.BootstrapContext = o.BootstrapContext
		return nil
	}

	kindMgr := &KindManager{
		ClusterName: o.ClusterName,
		ConfigPath:  o.Provider.KindConfigPath(),
	}

	if kindMgr.Exists(ctx) {
		fmt.Println("[bootstrap-create] Bootstrap cluster already exists")
	} else {
		if err := kindMgr.Create(ctx); err != nil {
			return fmt.Errorf("failed to create Kind cluster: %w", err)
		}
		fmt.Println("[bootstrap-create] ✓ Kind cluster created")
	}

	bs.BootstrapContext = "kind-" + o.ClusterName

	// Create platform-capi namespace
	homeDir, _ := os.UserHomeDir()
	kubeconfig := filepath.Join(homeDir, ".kube", "config")
	nsMgr := &NamespaceManager{
		Kubeconfig: kubeconfig,
		Context:    bs.BootstrapContext,
		Namespace:  constants.NamespaceCAPI,
	}
	if err := nsMgr.Create(ctx); err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}
	fmt.Println("[bootstrap-create] ✓ Namespace created: platform-capi")
	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// Phase 4: CAPI initialization
// ──────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) installCAPI(ctx context.Context, kubeconfig, contextName string) error {
	capiInstaller := &capi.OperatorInstaller{
		Kubeconfig: kubeconfig,
		Context:    contextName,
		Namespace:  constants.NamespaceCAPI,
		Providers:  o.Provider.CAPIProviders(),
		Debug:      o.Debug,
	}
	if err := capiInstaller.Install(ctx); err != nil {
		return fmt.Errorf("failed to install CAPI operator: %w", err)
	}
	return o.Provider.OnCAPIInit(ctx, kubeconfig, contextName, constants.NamespaceCAPI)
}

// ──────────────────────────────────────────────────────────────────────────
// Phase 11: Platform deploy (ArgoCD + bootstrap apps)
// ──────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) deployPlatform(ctx context.Context, kubeconfig string) error {
	// Fast check: if all operator webhooks are present, the entire phase is done
	if o.allOperatorsReady(ctx, kubeconfig) {
		fmt.Println("[platform-deploy] ✓ All operators already deployed, skipping")
		return nil
	}

	ci := &components.Installer{Kubeconfig: kubeconfig}
	if err := ci.InstallArgoCD(ctx); err != nil {
		return fmt.Errorf("failed to install ArgoCD: %w", err)
	}
	fmt.Println("[platform-deploy] ✓ ArgoCD installed")

	gitBranch := currentGitBranch()
	envRevision := "main"
	if gitBranch != "" && gitBranch != "main" {
		envRevision = gitBranch
		fmt.Printf("[platform-deploy] Environment revision: %s\n", envRevision)
	}

	// Helm template the environment-manager chart and apply the three boundary
	// ApplicationSets. ArgoCD's ApplicationSet controller generates child
	// Applications with revision from envRevision for platform-owned apps.
	helmCmd := exec.CommandContext(ctx, "helm", "template", "environment-manager",
		"manifests/argocd/environment-manager",
		"--set", "environmentRevision="+envRevision,
	)
	rendered, err := helmCmd.Output()
	if err != nil {
		return fmt.Errorf("helm template failed: %w\n%s", err, rendered)
	}
	applyCmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig, "apply", "-f", "-")
	applyCmd.Stdin = bytes.NewReader(rendered)
	if out, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply boundary ApplicationSets: %w\n%s", err, out)
	}
	fmt.Println("[platform-deploy] ✓ Boundary ApplicationSets applied")

	fmt.Println("[platform-deploy] Waiting for operators to establish webhooks...")
	if err := waitForOperators(ctx, kubeconfig, o.Provider.OperatorWebhookPatterns()); err != nil {
		return fmt.Errorf("operators not ready: %w", err)
	}
	fmt.Println("[platform-deploy] ✓ Operators ready")

	fmt.Println("[platform-deploy] Verifying CRDs are queryable...")
	if err := o.waitForCRDs(ctx, kubeconfig); err != nil {
		return fmt.Errorf("CRDs not queryable: %w", err)
	}

	fmt.Println("[platform-deploy] Waiting for operator pods to be Ready...")
	if err := o.waitForOperatorPods(ctx, kubeconfig); err != nil {
		return fmt.Errorf("operator pods not ready: %w", err)
	}

	fmt.Println("[platform-deploy] ✓ All platform components deployed")
	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// Upgrade / resume path
// ──────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) handleExistingState(ctx context.Context, stateMgr *state.StateManager, bs *state.BootstrapState) error {
	fmt.Printf("\n[recovery] Found existing state for cluster '%s'\n", o.ClusterName)
	fmt.Printf("[recovery] Last completed phase: %s\n", bs.CurrentPhase)

	if o.phaseDone(bs, state.PhaseComplete) {
		if o.Upgrade {
			fmt.Println("[upgrade] Cluster already exists, starting upgrade/reconciliation...")
			return o.runUpgrade(ctx, bs)
		}
		return fmt.Errorf("cluster '%s' already exists. Use --upgrade to reconcile or --name with different name", o.ClusterName)
	}

	// Resume from where we left off — the runFresh pipeline will skip
	// completed phases via runPhase.
	fmt.Println("[recovery] Resuming from next phase...")
	if o.Debug {
		fmt.Printf("[DEBUG] Completed phases: %v\n", bs.CompletedPhases)
	}
	return o.runFresh(ctx, stateMgr, bs)
}

func (o *Orchestrator) runUpgrade(ctx context.Context, bs *state.BootstrapState) error {
	kubeconfig := bs.MgmtKubeconfig
	if kubeconfig == "" {
		return fmt.Errorf("management cluster kubeconfig not found in state")
	}

	if err := o.checkVersionCompatibility(ctx, kubeconfig); err != nil {
		return fmt.Errorf("version check: %w", err)
	}

	fmt.Println("\n[upgrade] Updating CAPI Provider versions...")
	for _, p := range o.Provider.CAPIProviders() {
		if o.Debug {
			fmt.Printf("[DEBUG] Updating %s/%s to %s\n", p.Kind, p.Name, p.Version)
		}
		patch := fmt.Sprintf(`{"spec":{"version":"%s"}}`, p.Version)
		cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
			"patch", p.Kind, p.Name, "-n", constants.NamespaceCAPI,
			"--type=merge", "-p", patch)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to update %s/%s: %w\n%s", p.Kind, p.Name, err, out)
		}
	}
	fmt.Println("[upgrade] ✓ Providers updated")

	fmt.Println("\n[upgrade] Updating ClusterClass definitions...")
	ccd := &clusterclass.Deployer{
		Kubeconfig: kubeconfig,
		Namespace:  constants.NamespaceCAPI,
		ClassPaths: o.Provider.ClusterClassPaths(),
	}
	if err := ccd.Deploy(ctx); err != nil {
		return fmt.Errorf("failed to update ClusterClasses: %w", err)
	}
	fmt.Println("[upgrade] ✓ ClusterClasses updated")

	fmt.Println("\n[upgrade] Updating platform components...")
	ci := &components.Installer{Kubeconfig: kubeconfig}
	if err := ci.InstallAll(ctx, ""); err != nil {
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
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to check CAPI API version: %w", err)
	}
	if !bytes.Contains(out, []byte("v1beta1")) {
		return fmt.Errorf("incompatible CAPI API version — v1beta1 required")
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) kubeconfigPaths(bs *state.BootstrapState) (string, string) {
	homeDir, _ := os.UserHomeDir()
	kubeconfig := filepath.Join(homeDir, ".kube", "config")
	ctx := bs.BootstrapContext
	if ctx == "" {
		ctx = o.BootstrapContext
	}
	if ctx == "" {
		ctx = "kind-" + o.ClusterName
	}
	return kubeconfig, ctx
}

func (o *Orchestrator) getArgoCDPassword(ctx context.Context, kubeconfig string) string {
	ci := &components.Installer{Kubeconfig: kubeconfig}
	pwd, err := ci.GetArgoCDPassword(ctx)
	if err != nil {
		return "<check secret manually>"
	}
	return pwd
}

func (o *Orchestrator) checkKindClusterExists() error {
	cmd := exec.Command("kind", "get", "clusters")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	if strings.Contains(string(out), o.ClusterName) {
		return fmt.Errorf("kind cluster '%s' already exists. Run teardown first", o.ClusterName)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// Shared kubectl / git helpers
// ──────────────────────────────────────────────────────────────────────────

// allOperatorsReady is a one-shot check that returns true if all operator
// webhooks are already registered. Used by deployPlatform to skip the
// entire phase on re-run if the cluster is already fully deployed.
func (o *Orchestrator) allOperatorsReady(ctx context.Context, kubeconfig string) bool {
	cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
		"get", "validatingwebhookconfigurations", "-o", "name")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	hasCAPI, hasCertManager, hasCNPG, hasExternalSecret := false, false, false, false
	for _, line := range lines {
		if strings.Contains(line, "capi") {
			hasCAPI = true
		}
		if strings.Contains(line, "cert-manager") {
			hasCertManager = true
		}
		if strings.Contains(line, "cnpg") {
			hasCNPG = true
		}
		if strings.Contains(line, "externalsecret") || strings.Contains(line, "secretstore") {
			hasExternalSecret = true
		}
	}
	for _, p := range o.Provider.OperatorWebhookPatterns() {
		found := false
		for _, line := range lines {
			if strings.Contains(line, p) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return hasCAPI && hasCertManager && hasCNPG && hasExternalSecret
}

func waitForOperators(ctx context.Context, kubeconfig string, extraPatterns []string) error {
	deadline := time.Now().Add(20 * time.Minute)
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
			cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
				"get", "validatingwebhookconfigurations", "-o", "name")
			out, err := cmd.Output()
			if err != nil {
				continue
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
				hasCAPI := false
				hasCertManager := false
				hasCNPG := false
				hasExternalSecret := false
				extraFound := make(map[string]bool)
				for _, p := range extraPatterns {
					extraFound[p] = false
				}
				for _, line := range lines {
					if strings.Contains(line, "capi") {
						hasCAPI = true
					}
					if strings.Contains(line, "cert-manager") {
						hasCertManager = true
					}
					if strings.Contains(line, "cnpg") {
						hasCNPG = true
					}
					if strings.Contains(line, "externalsecret") || strings.Contains(line, "secretstore") {
						hasExternalSecret = true
					}
					for _, p := range extraPatterns {
						if strings.Contains(line, p) {
							extraFound[p] = true
						}
					}
				}
				allExtra := true
				for _, found := range extraFound {
					if !found {
						allExtra = false
						break
					}
				}
				if hasCAPI && hasCertManager && hasCNPG && hasExternalSecret && allExtra {
					return nil
				}
		}
	}
}

// waitForCRDs polls the API server until critical CRDs are registered in
// resource discovery. Validating webhooks existing does not guarantee the
// CRD is registerable — there is a propagation delay.
func (o *Orchestrator) waitForCRDs(ctx context.Context, kubeconfig string) error {
	required := []string{
		"externalsecrets.external-secrets.io",
		"clusters.postgresql.cnpg.io",
	}
	deadline := time.Now().Add(10 * time.Minute)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for CRDs to be queryable")
			}
			cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
				"get", "crd", "-o", "name")
			out, err := cmd.Output()
			if err != nil {
				continue
			}
			allFound := true
			for _, crd := range required {
				if !strings.Contains(string(out), crd) {
					allFound = false
					break
				}
			}
			if allFound {
				fmt.Println("[platform-deploy] ✓ CRDs queryable")
				return nil
			}
		}
	}
}

// waitForOperatorPods polls until critical operator pods are Ready. Webhooks
// and CRDs may exist, but webhook endpoints return 503 until their pods start.
func (o *Orchestrator) waitForOperatorPods(ctx context.Context, kubeconfig string) error {
	operators := map[string]string{
		"cnpg-system":             "app.kubernetes.io/name=cloudnative-pg",
		"platform-ops":            "app.kubernetes.io/name=external-secrets",
	}
	deadline := time.Now().Add(15 * time.Minute)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for operator pods to be Ready")
			}
			allReady := true
			for ns, label := range operators {
				cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
					"get", "pods", "-n", ns,
					"-l", label,
					"-o", "jsonpath={.items[?(@.status.phase=='Running')].metadata.name}")
				out, err := cmd.Output()
				if err != nil || len(strings.TrimSpace(string(out))) == 0 {
					allReady = false
					break
				}
			}
			if allReady {
				fmt.Println("[platform-deploy] ✓ Operator pods Ready")
				return nil
			}
		}
	}
}

func currentGitBranch() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
