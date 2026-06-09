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
	"github.com/soloz-io/zero-ops/internal/hub-cli/health"
	"github.com/soloz-io/zero-ops/internal/hub-cli/state"
)

// Orchestrator runs the 15-phase hub cluster bootstrap pipeline.
//
// The pipeline is identical for every provider — local or cloud. The
// orchestrator never branches on provider name or type. Cloud-specific
// phases (5-7) are identity/no-ops for local providers by design.
//
//	Phase  1: preflight               Provider.PreflightValidators()
//	Phase  2: bootstrap-create        kind cluster (orchestrator-owned)
//	Phase  3: day0-infra              Provider.ProvisionDayZero(kubeconfig)
//	Phase  4: capi-init               CAPI operator + Provider.OnCAPIInit()
//	Phase  5: cluster-provision       Provider.ProvisionManagementCluster(cfg)
//	Phase  6: pivot-move              Provider.PivotMove(cfg) → mgmtKubeconfig
//	Phase  7: pivot-ready             Provider.PivotReady(mgmtKubeconfig)
//	Phase  8: cleanup                 delete kind if !Provider.IsLocal()
//	Phase  9: clusterclass            ClusterClass deploy (orchestrator-owned)
//	Phase 10: platform-pre-reqs       Provider.OnPlatformPreReqs(kubeconfig)
//	Phase 11a: boundary-01            ArgoCD + infra operators (orchestrator-owned)
//	Phase 11b: generate-local-secrets Bootstrap secrets (crypto keys, platform-db-app)
//	Phase 11c: boundary-02            Data workloads (CNPG, Redis, NATS)
//	Phase 11d: boundary-03            Services (SPIRE, ingress-nginx, apps)
//	Phase 11e: bootstrap-infisical-api Wait for CNPG, inject DB_ROOT_CERT, bootstrap Infisical
//	Phase 12: finalize                Provider.Finalize(cfg) → kubeconfigPath
	type Orchestrator struct {
		Provider         Provider
		ClusterName      string
		BootstrapContext string
		KeepBootstrap    bool
		MergeKubeconfig  bool
		Debug            bool
		EnvironmentSlug  string
	}

// Run executes the full 12-phase bootstrap pipeline with checkpoint/restart.
func (o *Orchestrator) Run(ctx context.Context) error {
	if o.Debug {
		fmt.Println("[DEBUG] Orchestrator.Run() started")
		fmt.Printf("[DEBUG] Provider: %s, ClusterName: %s\n",
			o.Provider.Name(), o.ClusterName)
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

	// ── Phase 11a: Boundary 01 — platform infrastructure ──────────────
	// Installs ArgoCD, CNPG operator, Crossplane, ESO, cert-manager, and
	// other core operators. Waits for webhooks, CRDs, and operator pods
	// before proceeding.
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseBoundary01, "boundary01",
		"Deploying platform infrastructure (boundary 01)...",
		func() error { return o.deployBoundary01(ctx, mgmtKubeconfig) },
		func() { fmt.Println("[boundary01] ✓ Platform infrastructure deployed") },
	); err != nil {
		return err
	}

	// ── Phase 11b: Generate local secrets ─────────────────────────────
	// Generates cryptographic keys (ENCRYPTION_KEY, AUTH_SECRET, REDIS_URL),
	// creates infisical-secrets, infisical-redis-credentials, and
	// platform-db-app. The latter MUST exist before CNPG's initdb runs
	// in B02, so this phase runs between B01 and B02.
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseGenerateLocalSecrets, "generate-local-secrets",
		"Generating local bootstrap secrets...",
		func() error {
			ci := &components.Installer{Kubeconfig: mgmtKubeconfig}
			return ci.GenerateLocalSecrets(ctx)
		},
		func() { fmt.Println("[generate-local-secrets] ✓ Local secrets generated") },
	); err != nil {
		return err
	}

	// ── Phase 11c: Boundary 02 — platform data workloads ─────────────
	// Deploys CNPG Cluster, Redis, NATS, ClickHouse. The CNPG Cluster
	// CR triggers the operator (installed in B01). platform-db-app was
	// created in the previous phase (generate-local-secrets), so CNPG's
	// initdb has the credentials it needs immediately.
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseBoundary02, "boundary02",
		"Deploying platform data (boundary 02)...",
		func() error { return o.deployBoundary02(ctx, mgmtKubeconfig) },
		func() { fmt.Println("[boundary02] ✓ Platform data deployed") },
	); err != nil {
		return err
	}

	// ── Phase 11d: Boundary 03 — platform services ───────────────────
	// Deploys ingress-nginx, API gateway, SPIRE, platform services, and
	// spoke cluster configs. Infisical Helm chart starts here with
	// infisical-secrets (crypto keys only, no DB_ROOT_CERT yet).
	// Ingress resources are applied after the ingress-nginx controller
	// webhook is guaranteed up.
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseBoundary03, "boundary03",
		"Deploying platform services (boundary 03)...",
		func() error { return o.deployBoundary03(ctx, mgmtKubeconfig) },
		func() { fmt.Println("[boundary03] ✓ Platform services deployed") },
	); err != nil {
		return err
	}

	// ── Phase 11e: Bootstrap Infisical API ───────────────────────────
	// Waits for CNPG Cluster to be Ready, injects DB_ROOT_CERT into
	// infisical-secrets, then bootstraps the Infisical API (Org, Project,
	// Machine Identity), and stores Layer 1+2 credentials in Infisical.
	// This MUST run after B03 when Infisical is deployed and CNPG is
	// healthy enough to provide its CA certificate.
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseBootstrapInfisicalAPI, "bootstrap-infisical-api",
		"Bootstrapping Infisical API...",
		func() error {
			ci := &components.Installer{Kubeconfig: mgmtKubeconfig}
			return ci.BootstrapInfisicalAPI(ctx)
		},
		func() { fmt.Println("[bootstrap-infisical-api] ✓ Infisical API bootstrapped") },
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
// Boundary 01: Platform infrastructure (ArgoCD + operators)
// ──────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) deployBoundary01(ctx context.Context, kubeconfig string) error {
	ci := &components.Installer{Kubeconfig: kubeconfig}
	if err := ci.InstallArgoCD(ctx); err != nil {
		return fmt.Errorf("failed to install ArgoCD: %w", err)
	}
	fmt.Println("[boundary01] ✓ ArgoCD installed")

	if err := o.renderAndApplyBoundaries(ctx, kubeconfig, true, false, false); err != nil {
		return err
	}
	fmt.Println("[boundary01] ✓ 01-platform-infra ApplicationSet applied")

	fmt.Println("[boundary01] Waiting for operators to establish webhooks...")
	if err := waitForOperators(ctx, kubeconfig, o.Provider.OperatorWebhookPatterns()); err != nil {
		return fmt.Errorf("operators not ready: %w", err)
	}
	fmt.Println("[boundary01] ✓ Operators ready")

	fmt.Println("[boundary01] Verifying CRDs are queryable...")
	if err := o.waitForCRDs(ctx, kubeconfig); err != nil {
		return fmt.Errorf("CRDs not queryable: %w", err)
	}

	fmt.Println("[boundary01] Waiting for operator pods to be Ready...")
	if err := o.waitForOperatorPods(ctx, kubeconfig); err != nil {
		return fmt.Errorf("operator pods not ready: %w", err)
	}

	fmt.Println("[boundary01] ✓ Platform infrastructure deployed")
	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// Boundary 02: Platform data workloads (CNPG, Redis, NATS, ClickHouse)
// ──────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) deployBoundary02(ctx context.Context, kubeconfig string) error {
	if err := o.renderAndApplyBoundaries(ctx, kubeconfig, true, true, false); err != nil {
		return err
	}
	fmt.Println("[boundary02] ✓ 02-platform-data ApplicationSet applied")
	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// Boundary 03: Platform services (ingress-nginx, SPIRE, platform services)
// ──────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) deployBoundary03(ctx context.Context, kubeconfig string) error {
	if err := o.renderAndApplyBoundaries(ctx, kubeconfig, true, true, true); err != nil {
		return err
	}
	fmt.Println("[boundary03] ✓ 03-platform-services ApplicationSet applied")
	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// renderAndApplyBoundaries: Helm template + kubectl apply with deploy flags
// ──────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) renderAndApplyBoundaries(ctx context.Context, kubeconfig string, deployB01, deployB02, deployB03 bool) error {
	gitBranch := currentGitBranch()
	envRevision := "main"
	if gitBranch != "" && gitBranch != "main" {
		envRevision = gitBranch
		fmt.Printf("[render] Environment revision: %s\n", envRevision)
	}

	providerForHelm := o.Provider.Name()
	if providerForHelm == "docker" {
		providerForHelm = "local"
	}

	helmCmd := exec.CommandContext(ctx, "helm", "template", "environment-manager",
		"manifests/argocd/environment-manager",
		"--set", "environmentRevision="+envRevision,
		"--set", "environmentSlug="+o.EnvironmentSlug,
		"--set", "provider="+providerForHelm,
		"--set", fmt.Sprintf("deploy.boundary01=%t", deployB01),
		"--set", fmt.Sprintf("deploy.boundary02=%t", deployB02),
		"--set", fmt.Sprintf("deploy.boundary03=%t", deployB03),
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
	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// Upgrade / resume path
// ──────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) handleExistingState(ctx context.Context, stateMgr *state.StateManager, bs *state.BootstrapState) error {
	fmt.Printf("\n[recovery] Found existing state for cluster '%s'\n", o.ClusterName)
	fmt.Printf("[recovery] Last completed phase: %s\n", bs.CurrentPhase)

	if o.phaseDone(bs, state.PhaseComplete) {
		fmt.Println("Cluster already bootstrapped. No further CLI operations permitted per ADR-040.")
		return nil
	}

	// Resume from where we left off — the runFresh pipeline will skip
	// completed phases via runPhase.
	fmt.Println("[recovery] Resuming from next phase...")
	if o.Debug {
		fmt.Printf("[DEBUG] Completed phases: %v\n", bs.CompletedPhases)
	}
	return o.runFresh(ctx, stateMgr, bs)
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



// waitForOperators waits for validating webhook configurations matching the
// built-in platform operators and any extraPatterns provided by the
// provider's OperatorWebhookPatterns() to be established.
//
// The check is a thin wrapper over the health package's
// ValidatingWebhookHealth, composing base + extra patterns into a single
// check. This keeps the wait loop, timeout, and context handling
// consistent with the rest of the bootstrap pipeline.
func waitForOperators(ctx context.Context, kubeconfig string, extraPatterns []string) error {
	patterns := []string{"capi", "cert-manager", "cnpg", "externalsecret"}
	patterns = append(patterns, extraPatterns...)

	waiter := &health.HealthWaiter{
		Checkers: []health.HealthChecker{
			health.NewValidatingWebhookHealth(patterns...),
		},
		Interval: 10 * time.Second,
		Timeout:  20 * time.Minute,
	}
	return waiter.Wait(ctx, kubeconfig)
}

// waitForCRDs polls the API server until critical CRDs are registered in
// resource discovery. Validating webhooks existing does not guarantee the
// CRD is registerable — there is a propagation delay.
func (o *Orchestrator) waitForCRDs(ctx context.Context, kubeconfig string) error {
	waiter := &health.HealthWaiter{
		Checkers: []health.HealthChecker{
			health.NewCRDRegisteredHealth(
				"externalsecrets.external-secrets.io",
				"clusters.postgresql.cnpg.io",
			),
		},
		Interval: 5 * time.Second,
		Timeout:  10 * time.Minute,
		OnCheckPass: func(_ health.HealthChecker) {
			fmt.Println("[platform-deploy] ✓ CRDs queryable")
		},
	}
	return waiter.Wait(ctx, kubeconfig)
}

// waitForOperatorPods polls until critical operator pods are Ready. Webhooks
// and CRDs may exist, but webhook endpoints return 503 until their pods start.
func (o *Orchestrator) waitForOperatorPods(ctx context.Context, kubeconfig string) error {
	waiter := &health.HealthWaiter{
		Checkers: []health.HealthChecker{
			health.NewOperatorPodsHealth("cnpg-system", "app.kubernetes.io/name=cloudnative-pg"),
			health.NewOperatorPodsHealth("platform-ops", "app.kubernetes.io/name=external-secrets"),
		},
		Interval: 5 * time.Second,
		Timeout:  15 * time.Minute,
		OnCheckPass: func(_ health.HealthChecker) {
			fmt.Println("[platform-deploy] ✓ Operator pods Ready")
		},
	}
	return waiter.Wait(ctx, kubeconfig)
}

func currentGitBranch() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
