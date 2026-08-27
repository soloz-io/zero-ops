package bootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/clusterclass"
	"github.com/soloz-io/zero-ops/internal/hub-cli/components"
	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
	"github.com/soloz-io/zero-ops/internal/hub-cli/health"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
	"github.com/soloz-io/zero-ops/internal/hub-cli/state"
	"gopkg.in/yaml.v3"
)

// Orchestrator runs the 16-phase hub cluster bootstrap pipeline.
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
//	Phase  8: cleanup                 delete kind unless --keep-bootstrap
//	Phase  9: clusterclass            ClusterClass deploy (orchestrator-owned)
//	Phase 10: platform-pre-reqs       Provider.OnPlatformPreReqs(kubeconfig)
//	Phase 11a: boundary-01            ArgoCD + infra operators (orchestrator-owned)
//	Phase 11b: generate-local-secrets Static Secrets (crypto, postgres connection, platform-db-app)
//	Phase 11c: boundary-02            Data workloads (CNPG, Redis, NATS)
//	Phase 11d: inject-ca-cert         Wait for CNPG Ready → inject DB_ROOT_CERT into infisical-secrets
//	Phase 11e: boundary-03            Services (Infisical, hub Gateway, apps)
//	Phase 11f: bootstrap-infisical-api Wait for Infisical health → bootstrap Org/Project/MI → store credentials
//	Phase 12: finalize                Provider.Finalize(cfg) → kubeconfigPath
type Orchestrator struct {
	Provider         Provider
	ClusterName      string
	BootstrapContext string
	KeepBootstrap    bool
	MergeKubeconfig  bool
	Debug            bool
	EnvironmentSlug  string
	Topology         string
	// Gating selects how the cluster is created (ADR-055). Empty means
	// sequenced: converged creation is requested by name or it does not occur.
	Gating GatingMode
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
			CurrentPhase: state.PhasePreFlight,
		}
	}

	// ── Phase 1: Preflight (checkpointed per-bootstrap) ───────────────
	// Once passed for this bootstrap, skipped on resume via runPhase's
	// phaseDone check — like every other phase. Dry-run bypasses the
	// orchestrator entirely and validates without checkpointing.
	if err := o.runPhase(ctx, stateMgr, bs, state.PhasePreFlight, "preflight",
		"Running preflight validation...",
		func() error {
			r := preflight.NewRunner()
			for _, v := range o.Provider.PreflightValidators() {
				r.Add(v)
			}
			return r.Run(ctx)
		},
		func() { fmt.Println("[preflight] ✓ All checks passed") },
	); err != nil {
		return err
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
		ClusterName:         o.ClusterName,
		BootstrapKubeconfig: kubeconfig,
		BootstrapContext:    bootstrapCtx,
		Debug:               o.Debug,
	}
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseClusterProvision, "cluster-provision",
		"",
		func() error { return o.Provider.ProvisionManagementCluster(ctx, provCfg) },
		nil,
	); err != nil {
		return err
	}

	// ── Phase 5b: Home-lab worker join (hybrid) ───────────────────────
	// The hybrid hub runs no Hetzner workers (ADR-046 §19/§21), so its only worker
	// capacity is a home-lab Flatcar node — and it must exist BEFORE anything is
	// installed on the hub.
	//
	// This sits between cluster-provision and pivot-move on purpose. The hub API is
	// already up (the control plane is Ready), but nothing has been deployed to it
	// yet. pivot-move immediately installs cert-manager and the CAPI operators, and
	// the control plane keeps its ADR-014 taint, so with no worker present those
	// pods sit Pending and pivot-move fails on
	// "timed out waiting for the condition on deployments/cert-manager".
	//
	// CAPI cannot provision these nodes (they are Hyper-V VMs on a workstation),
	// which is why this shells out to the provisioning script rather than creating a
	// Machine. For --cluster hub the script mints its own bootstrap token from the
	// hub kubeconfig, so it depends on nothing that pivot installs.
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseHomeWorkerJoin, "home-worker-join",
		"Joining home-lab worker(s) to the hub...",
		func() error { return o.joinHomeWorkers(ctx, o.hubKubeconfigFromBootstrap(ctx, kubeconfig)) },
		nil,
	); err != nil {
		return err
	}

	// ── Phase 6: Pivot move ───────────────────────────────────────────
	pivotCfg := &PivotConfig{
		ClusterName:         o.ClusterName,
		BootstrapKubeconfig: kubeconfig,
		BootstrapContext:    bootstrapCtx,
		Debug:               o.Debug,
	}

	// Self-heal: pivot-move writes the mgmt kubeconfig on success. A phase
	// marked complete but with no kubeconfig recorded is a torn write from a
	// pre-1.x run (the kubeconfig was only persisted after runPhase saved).
	// Treat it as incomplete so the move re-runs and pivot-ready receives a
	// real path instead of "".
	if bs.MgmtKubeconfig == "" && o.phaseDone(bs, state.PhasePivotMove) {
		fmt.Println("[recovery] pivot-move completed without a recorded mgmt kubeconfig; re-running it")
		bs.CompletedPhases = removePhase(bs.CompletedPhases, state.PhasePivotMove)
	}

	mgmtKubeconfig := bs.MgmtKubeconfig
	if err := o.runPhase(ctx, stateMgr, bs, state.PhasePivotMove, "pivot-move",
		"",
		func() error {
			var err error
			mgmtKubeconfig, err = o.Provider.PivotMove(ctx, pivotCfg)
			if err == nil {
				// Persist the mgmt kubeconfig with the completed phase so a
				// resumed run passes a real path to pivot-ready instead of "".
				bs.MgmtKubeconfig = mgmtKubeconfig
			}
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
	if !o.KeepBootstrap {
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

	// ── Phase 11d: Inject CNPG CA certificate ────────────────────────
	// Waits for CNPG Cluster to be Ready, reads platform-db-ca, and
	// injects DB_ROOT_CERT into infisical-secrets. This MUST run after
	// B02 (CNPG Cluster applied) and before B03 (Infisical deployed)
	// so that Infisical Helm chart renders with DB_ROOT_CERT present,
	// enabling TLS connectivity on first boot.
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseInjectCACert, "inject-ca-cert",
		"Injecting CNPG CA certificate into infisical-secrets...",
		func() error {
			ci := &components.Installer{Kubeconfig: mgmtKubeconfig}
			return ci.UpdateInfisicalSecretsWithCNPGCert(ctx)
		},
		func() { fmt.Println("[inject-ca-cert] ✓ CNPG CA certificate injected") },
	); err != nil {
		return err
	}

	// ── Phase 11e: Boundary 03 — platform services ───────────────────
	// Deploys the hub Gateway, API gateway, Infisical, and spoke
	// cluster configs. Infisical Helm chart starts with ALL secrets
	// already present (infisical-secrets includes DB_ROOT_CERT from the
	// previous phase), preventing CreateContainerConfigError deadlocks.
	// Routes are applied after the Gateway exists, so they have a parent to attach to
	// webhook is guaranteed up.
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseBoundary03, "boundary03",
		"Deploying platform services (boundary 03)...",
		func() error { return o.deployBoundary03(ctx, mgmtKubeconfig) },
		func() { fmt.Println("[boundary03] ✓ Platform services deployed") },
	); err != nil {
		return err
	}

	// ── Phase 11f: Bootstrap Infisical API ───────────────────────────
	// Waits for Infisical to be healthy, then bootstraps the Infisical
	// REST API (Org, Project, Machine Identity), creates the infisical-auth
	// Secret, and stores Layer 1+2 credentials in Infisical vault.
	// This MUST run after B03 when Infisical pods are Running.
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

	// ── Phase 11g: Boundary 04 — tenant services ──────────────────────
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseBoundary04, "boundary04",
		"Deploying tenant services (boundary 04)...",
		func() error { return o.deployBoundary04(ctx, mgmtKubeconfig) },
		func() { fmt.Println("[boundary04] ✓ Tenant services deployed") },
	); err != nil {
		return err
	}

	// ── Phase 11h: Boundary 05 — fleet provisioning (ADR-047) ────────────
	// Deploys the tenant-fleet provisioning ApplicationSets (xr / spoke /
	// workloads). Requires boundary04 (tenant services + ArgoCD fleet-registry
	// auth) to be in place first.
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseBoundary05, "boundary05",
		"Deploying fleet provisioning (boundary 05)...",
		func() error { return o.deployBoundary05(ctx, mgmtKubeconfig) },
		func() { fmt.Println("[boundary05] ✓ Fleet provisioning deployed") },
	); err != nil {
		return err
	}

	// ── Phase 11i: Boundary 06 — public tenant TLS (ADR-051) ─────────────
	// Deploys the tenant-public-tls ApplicationSet: per-tenant public ACME
	// Certificates + the :443 tenant-tls-gateway in platform-ops on each spoke,
	// rendered from fleet-registry public.hosts declarations. Requires
	// boundary05 (fleet provisioning) so fleet values are already reconciling.
	if err := o.runPhase(ctx, stateMgr, bs, state.PhaseBoundary06, "boundary06",
		"Deploying public tenant TLS (boundary 06)...",
		func() error { return o.deployBoundary06(ctx, mgmtKubeconfig) },
		func() { fmt.Println("[boundary06] ✓ Public tenant TLS deployed") },
	); err != nil {
		return err
	}

	// ── Phase 11g: Commit + verify ADR-045 artifacts ─────────────────
	// Auto-commits generated artifacts, then polls ArgoCD until the
	// affected apps reconcile (Synced+Healthy). This ensures the
	// platform is in a GitOps-consistent state before Finalize.
	fmt.Println("\n[adr045-commit] Validating and committing ADR-045 artifacts...")
	if err := o.validateADR045Artifacts(ctx); err != nil {
		return fmt.Errorf("[adr045-commit] %w", err)
	}

	generatedPaths := []string{
		"manifests/hub-core-services/security/generated/",
		"manifests/environments/base/generated/",
	}

	// Try to auto-commit (local-only — git remote not required)
	if err := o.gitCommitArtifacts(ctx, generatedPaths); err != nil {
		fmt.Printf("[adr045-commit] ⚠️  Auto-commit failed: %v\n", err)
		fmt.Println("[adr045-commit] Manual commit required. Run:")
		fmt.Println("  git add manifests/*/generated/")
		fmt.Println("  git commit -m \"chore: bootstrap-generated-gitops-artifacts [skip ci]\"")
		fmt.Println("  git push")
	}

	// Poll ArgoCD apps for health. The apps will not reconcile until
	// the generated files are in Git (committed + pushed). If the
	// auto-commit succeeded, only a git push is needed.
	appsToWait := []string{"platform-security-infra", "hub-environment"}
	fmt.Printf("[adr045-commit] Waiting for ArgoCD apps to reconcile: %v\n", appsToWait)
	fmt.Println("[adr045-commit] This requires the generated files to be committed AND pushed.")
	fmt.Println("[adr045-commit] If auto-push failed, push manually.")

	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	if err := o.waitForArgoCDAppsHealthy(waitCtx, mgmtKubeconfig, appsToWait); err != nil {
		// Print diagnostic info even on timeout
		fmt.Println("[adr045-commit] ❌ Some ArgoCD apps did not become healthy.")
		fmt.Println("[adr045-commit] Diagnostic commands:")
		for _, app := range appsToWait {
			fmt.Printf("  kubectl describe application %s -n platform-ops\n", app)
		}
		return fmt.Errorf("[adr045-commit] %w", err)
	}
	fmt.Println("[adr045-commit] ✓ All ArgoCD apps reconciled successfully")

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
	completed := time.Now().UTC()
	bs.CompletedAt = &completed
	stateMgr.Save(bs)

	o.printTimingSummary(bs)

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

	// Timed here because runPhase is the single choke point every phase passes
	// through, so no phase can be added later and silently escape accounting.
	started := time.Now().UTC()
	if err := action(); err != nil {
		fmt.Printf("[%s] ✗ failed after %s\n", label, formatDuration(time.Since(started)))
		return fmt.Errorf("[%s] %w", label, err)
	}
	elapsed := time.Since(started)

	if onSuccess != nil {
		onSuccess()
	}
	fmt.Printf("[%s] ⏱  %s\n", label, formatDuration(elapsed))

	bs.PhaseTimings = append(bs.PhaseTimings, state.PhaseTiming{
		Phase:       phase,
		StartedAt:   started,
		CompletedAt: started.Add(elapsed),
		Seconds:     elapsed.Round(time.Millisecond).Seconds(),
	})
	o.markPhaseComplete(bs, phase)
	return stateMgr.Save(bs)
}

// hubKubeconfigFromBootstrap persists the hub's admin kubeconfig by reading the
// CAPI-generated Secret out of the bootstrap cluster.
//
// Needed because home-worker-join runs BEFORE pivot-move, and pivot-move is what
// normally writes this file. The control plane is up by this point, so the Secret
// already exists.
//
// It writes to the CANONICAL path, not a temp file. provision-flatcar-worker.sh
// sources scripts/hybrid/home-lab.env, which sets HUB_KUBECONFIG to exactly this
// location unconditionally — so passing a temp path through the environment is
// silently overridden and the script aborts with "HUB_KUBECONFIG not found".
// home-lab.env is gitignored (it holds real values), so the fix cannot live there.
// pivot-move later rewrites the same file with the same content.
//
// Returns "" if it cannot be read; joinHomeWorkers treats that as fatal rather
// than silently skipping the join.
func (o *Orchestrator) hubKubeconfigFromBootstrap(ctx context.Context, bootstrapKubeconfig string) string {
	out, err := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", bootstrapKubeconfig,
		"-n", constants.NamespaceCAPI,
		"get", "secret", o.ClusterName+"-kubeconfig",
		"-o", "jsonpath={.data.value}",
	).Output()
	if err != nil || len(out) == 0 {
		return ""
	}

	decoded, err := base64.StdEncoding.DecodeString(string(out))
	if err != nil {
		return ""
	}

	path := filepath.Join("k8-secrets", "kubeconfig", o.ClusterName+".kubeconfig")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ""
	}
	if err := os.WriteFile(path, decoded, 0o600); err != nil {
		return ""
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// hubWorkerSelector identifies a joined home-lab hub worker. provision-flatcar-worker.sh
// applies hub-role=worker for its "hub" target, both via kubelet --node-labels and
// again cluster-side after the join.
const hubWorkerSelector = "hub-role=worker"

// joinHomeWorkers brings up the home-lab worker(s) this hub needs before any
// platform workload is deployed. No-op unless the provider asked for home workers.
func (o *Orchestrator) joinHomeWorkers(ctx context.Context, kubeconfig string) error {
	hw, ok := o.Provider.(interface{ HomeWorkersRequested() bool })
	if !ok || !hw.HomeWorkersRequested() {
		fmt.Println("[home-worker-join] Not a home-worker cell — skipping")
		return nil
	}

	if kubeconfig == "" {
		return fmt.Errorf("could not read the hub kubeconfig from the bootstrap cluster;\n" +
			"the home worker cannot join, and pivot would then fail to schedule cert-manager")
	}

	// Idempotent: provisioning a Flatcar VM takes ~10 minutes, and a resumed
	// bootstrap must not pay that again for a node that is already serving.
	if ready, name := o.readyHubWorker(ctx, kubeconfig); ready {
		fmt.Printf("[home-worker-join] ✓ %s already Ready — skipping provisioning\n", name)
		return nil
	}

	script := filepath.Join("scripts", "hybrid", "provision-flatcar-worker.sh")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("home-lab worker is required before boundary-01 but %s is missing.\n"+
			"Provision it manually, then re-run:\n"+
			"    ./scripts/hybrid/provision-flatcar-worker.sh --cluster hub", script)
	}

	fmt.Println("[home-worker-join] Running provision-flatcar-worker.sh --cluster hub")
	fmt.Println("[home-worker-join] (Hyper-V VM creation over SSH — this takes several minutes)")

	cmd := exec.CommandContext(ctx, "bash", script, "--cluster", "hub")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "HUB_KUBECONFIG="+kubeconfig)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("home-lab worker provisioning failed: %w\n"+
			"Fix the cause, then re-run this bootstrap — the phase is idempotent and will\n"+
			"skip if the node is already Ready:\n"+
			"    ./scripts/hybrid/provision-flatcar-worker.sh --cluster hub", err)
	}

	// The script has its own Ready gate, but the cluster's view is what the next
	// phase depends on, so confirm it here too.
	if ready, name := o.readyHubWorker(ctx, kubeconfig); ready {
		fmt.Printf("[home-worker-join] ✓ %s Ready\n", name)
		return nil
	}
	return fmt.Errorf("provisioning reported success but no Ready node carries %s;\n"+
		"platform workloads would have nowhere to schedule (ADR-046 §11)", hubWorkerSelector)
}

// readyHubWorker reports whether a home-lab hub worker is joined and Ready.
func (o *Orchestrator) readyHubWorker(ctx context.Context, kubeconfig string) (bool, string) {
	out, err := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfig,
		"get", "nodes", "-l", hubWorkerSelector,
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"=\"}"+
			"{.status.conditions[?(@.type==\"Ready\")].status}{\"\\n\"}{end}",
	).Output()
	if err != nil {
		return false, ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		name, status, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && status == "True" {
			return true, name
		}
	}
	return false, ""
}

// formatDuration renders a duration for humans reading a bootstrap log: seconds
// below a minute, m/s above it. time.Duration's own String() gives "7m12.3841s",
// which is noisy in a column.
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// printTimingSummary reports where end-to-end cluster creation time actually
// went. Phases are listed slowest-first: the point is to answer "what should we
// optimise", which a chronological list buries.
func (o *Orchestrator) printTimingSummary(bs *state.BootstrapState) {
	if len(bs.PhaseTimings) == 0 {
		return
	}

	var worked time.Duration
	for _, t := range bs.PhaseTimings {
		worked += time.Duration(t.Seconds * float64(time.Second))
	}

	ranked := make([]state.PhaseTiming, len(bs.PhaseTimings))
	copy(ranked, bs.PhaseTimings)
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].Seconds > ranked[j].Seconds })

	fmt.Println("\n⏱  Bootstrap timing")
	fmt.Printf("   %-28s %8s  %s\n", "PHASE", "TIME", "SHARE")
	for _, t := range ranked {
		d := time.Duration(t.Seconds * float64(time.Second))
		share := 0.0
		if worked > 0 {
			share = 100 * float64(d) / float64(worked)
		}
		fmt.Printf("   %-28s %8s  %4.1f%%\n", t.Phase, formatDuration(d), share)
	}

	fmt.Printf("   %-28s %8s\n", "phases run this invocation", formatDuration(worked))

	// Wall time spans any gap between a failed run and its resume, so it is
	// reported separately rather than presented as the cost of building a cluster.
	if bs.StartedAt != nil && bs.CompletedAt != nil {
		wall := bs.CompletedAt.Sub(*bs.StartedAt)
		fmt.Printf("   %-28s %8s  (%s → %s)\n", "wall clock, first start → end",
			formatDuration(wall),
			bs.StartedAt.Format("15:04:05"), bs.CompletedAt.Format("15:04:05"))
		if wall > worked+30*time.Second {
			fmt.Println("   note: wall clock exceeds phase time — this bootstrap was resumed,")
			fmt.Println("         so it includes time the process was not running.")
		}
	}
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

// removePhase returns phases with the given entry removed (no-op if absent).
func removePhase(phases []state.BootstrapPhase, phase state.BootstrapPhase) []state.BootstrapPhase {
	out := phases[:0]
	for _, p := range phases {
		if p != phase {
			out = append(out, p)
		}
	}
	return out
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

	// ADR-055: establish the seed (boundary AppProjects + the Application that
	// owns all boundary content) and open boundary 01. In sequenced mode the
	// remaining boundaries are created inactive and opened by their own phases.
	if err := o.deployBoundary(ctx, kubeconfig, 1); err != nil {
		return err
	}
	fmt.Println("[boundary01] ✓ 01-platform-infra boundary activated")

	// ArgoCD apps read manifests from the private soloz-io/zero-ops repo. The
	// repo credentials must exist before any app can sync (otherwise webhook
	// waits below block on "authentication required"). Provision them from the
	// GitHub PAT now so the raw CLI is self-contained (previously only the
	// shell script's step3 did this, causing a chicken-and-egg on fresh Hubs).
	if err := o.ensureArgoCDGitHubAuth(ctx, kubeconfig); err != nil {
		return fmt.Errorf("failed to configure ArgoCD GitHub access: %w", err)
	}

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
	if err := o.deployBoundary(ctx, kubeconfig, 2); err != nil {
		return err
	}
	fmt.Println("[boundary02] ✓ 02-platform-data boundary activated")
	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// Boundary 03: Platform services (hub Gateway, platform services)
// ──────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) deployBoundary03(ctx context.Context, kubeconfig string) error {
	if err := o.deployBoundary(ctx, kubeconfig, 3); err != nil {
		return err
	}
	fmt.Println("[boundary03] ✓ 03-platform-services boundary activated")
	return nil
}

func (o *Orchestrator) deployBoundary04(ctx context.Context, kubeconfig string) error {
	if err := o.deployBoundary(ctx, kubeconfig, 4); err != nil {
		return err
	}
	fmt.Println("[boundary04] ✓ 04-tenant-services boundary activated")
	return nil
}

func (o *Orchestrator) deployBoundary05(ctx context.Context, kubeconfig string) error {
	if err := o.deployBoundary(ctx, kubeconfig, 5); err != nil {
		return err
	}
	fmt.Println("[boundary05] ✓ 05-tenant-fleet boundary activated")
	return nil
}

func (o *Orchestrator) deployBoundary06(ctx context.Context, kubeconfig string) error {
	if err := o.deployBoundary(ctx, kubeconfig, 6); err != nil {
		return err
	}
	fmt.Println("[boundary06] ✓ 06-tenant-public-tls boundary activated")
	return nil
}

// publicTlsIssuerFor returns the ACME ClusterIssuer for this environment — the
// issuer half of ADR-051's public-TLS policy. The mapping lives HERE because
// the bootstrap is the environment boundary: it already owns environmentSlug,
// and no chart or ApplicationSet may re-derive policy (ADR-047). There is no
// default: an unknown or empty slug fails the bootstrap rather than silently
// inheriting a throwaway value — staging is an explicit ephemeral policy, never
// an accidental default.
func (o *Orchestrator) publicTlsIssuerFor() (string, error) {
	switch o.EnvironmentSlug {
	case "ephemeral":
		// Ephemeral clusters are created and destroyed continuously, so they are
		// the only environment whose certificate churn can realistically exhaust
		// Let's Encrypt's per-domain weekly quota. That risk is what staging is
		// for, and it is confined to where the risk actually exists.
		return "letsencrypt-staging", nil
	case "dev", "stg", "prod":
		// dev is browser-facing: people sign in through it, and the OAuth2 flow
		// redirects between hub and tenant hostnames. A staging certificate makes
		// the browser refuse the tenant host outright, so the redirect chain
		// breaks rather than merely warning — the login looks broken for a reason
		// that has nothing to do with identity. The hub's own dev hostnames
		// already issue from letsencrypt-prod, so staging here bought no quota
		// protection on a long-lived cluster and only left tenant hosts untrusted
		// by the browser that had just trusted the hub they authenticate against.
		return "letsencrypt-prod", nil
	default:
		return "", fmt.Errorf(
			"publicTlsIssuer: no issuer mapping for environment slug %q (expected dev|ephemeral|stg|prod) — refusing to guess TLS policy",
			o.EnvironmentSlug)
	}
}

// hubIngressAddress returns the public IPv4 that fronts :80/:443 for this hub —
// the CAPH-managed control-plane LoadBalancer. Returns "" when it cannot be
// resolved, in which case the chart falls back to its default (publishing the
// controller Service address) and public ingress DNS will be wrong.
func (o *Orchestrator) hubIngressAddress(ctx context.Context, kubeconfig string) string {
	cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
		"get", "hetznercluster", "-n", "platform-capi",
		"-o", "jsonpath={.items[0].spec.controlPlaneEndpoint.host}")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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

// ensureArgoCDGitHubAuth provisions the ArgoCD repo-creds secret (GitHub PAT)
// so private-repo apps can sync. Token from GITHUB_TOKEN env or the local
// k8-secrets/github/github-pat-token file.
func (o *Orchestrator) ensureArgoCDGitHubAuth(ctx context.Context, kubeconfig string) error {
	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken == "" {
		data, err := os.ReadFile("k8-secrets/github/github-pat-token")
		if err != nil {
			return fmt.Errorf("GITHUB_TOKEN not set and github-pat-token file unreadable: %w", err)
		}
		githubToken = strings.TrimSpace(string(data))
	}
	if githubToken == "" {
		return fmt.Errorf("GITHUB_TOKEN is empty — ArgoCD cannot sync the private zero-ops repo")
	}

	ci := &components.Installer{Kubeconfig: kubeconfig}
	if err := ci.FixArgoCDGitHubAuth(ctx, githubToken); err != nil {
		return err
	}
	return nil
}

func currentGitBranch() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ──────────────────────────────────────────────────────────────────────────
// ADR-045 artifact validation
// ──────────────────────────────────────────────────────────────────────────

// adr045Artifact describes a single required generated artifact.
type adr045Artifact struct {
	File           string   `yaml:"file"`
	RequiredFields []string `yaml:"requiredFields"`
}

// adr045Registry is the top-level structure of manifests/generated/artifacts.yaml.
type adr045Registry struct {
	Artifacts []adr045Artifact `yaml:"artifacts"`
}

// validateADR045Artifacts reads manifests/generated/artifacts.yaml and validates
// that every required artifact exists on disk with all required fields populated.
// This runs after PhaseBootstrapInfisicalAPI when the CLI has generated the patches.
func (o *Orchestrator) validateADR045Artifacts(ctx context.Context) error {
	projectRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	registryPath := filepath.Join(projectRoot, "manifests", "generated", "artifacts.yaml")
	registryData, err := os.ReadFile(registryPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", registryPath, err)
	}

	var registry adr045Registry
	if err := yaml.Unmarshal(registryData, &registry); err != nil {
		return fmt.Errorf("parse %s: %w", registryPath, err)
	}

	if len(registry.Artifacts) == 0 {
		fmt.Println("[adr045-validate] ⚠️  No artifacts registered in artifacts.yaml")
		return nil
	}

	for _, a := range registry.Artifacts {
		absPath := filepath.Join(projectRoot, a.File)

		// Check file exists
		data, err := os.ReadFile(absPath)
		if err != nil {
			return fmt.Errorf("artifact %s: file not found — ensure BootstrapInfisicalAPI completed successfully", a.File)
		}

		// Parse the patch YAML
		var doc map[string]interface{}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("artifact %s: invalid YAML: %w", a.File, err)
		}

		// Verify each required field
		for _, field := range a.RequiredFields {
			parts := strings.Split(field, ".")
			current := doc
			found := true

			for i, part := range parts {
				val, ok := current[part]
				if !ok {
					found = false
					break
				}
				if i == len(parts)-1 {
					// Last part — check it's non-empty
					strVal, ok := val.(string)
					if !ok || strings.TrimSpace(strVal) == "" {
						found = false
					}
				} else {
					// Intermediate — must be a map
					next, ok := val.(map[string]interface{})
					if !ok {
						found = false
						break
					}
					current = next
				}
			}

			if !found {
				return fmt.Errorf("artifact %s: required field %s is missing or empty", a.File, field)
			}
		}

		fmt.Printf("[adr045-validate]   ✓ %s (%d fields)\n", a.File, len(a.RequiredFields))
	}

	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// ADR-045: auto-commit + wait for ArgoCD
// ──────────────────────────────────────────────────────────────────────────

// gitCommitArtifacts runs git add + git commit for the generated artifact
// directories. It only requires local Git — no remote access. If git is
// unavailable or the working tree is dirty, it returns an error but does
// not halt the bootstrap (the user can commit manually).
func (o *Orchestrator) gitCommitArtifacts(ctx context.Context, paths []string) error {
	// Check if git is available
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git not found: %w", err)
	}

	// git add for each path
	for _, p := range paths {
		cmd := exec.CommandContext(ctx, "git", "add", p)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git add %s: %w\n%s", p, err, out)
		}
	}

	// git commit (idempotent — fails cleanly if nothing to commit)
	cmd := exec.CommandContext(ctx, "git", "commit", "-m",
		"chore: bootstrap-generated-gitops-artifacts [skip ci]")
	if out, err := cmd.CombinedOutput(); err != nil {
		// Check if it's just "nothing to commit"
		if bytes.Contains(out, []byte("nothing to commit")) {
			fmt.Println("[adr045-commit]   Nothing new to commit (already up to date)")
			return nil
		}
		return fmt.Errorf("git commit: %w\n%s", err, out)
	}

	fmt.Println("[adr045-commit]   ✓ Generated artifacts committed locally")

	// Try git pull --rebase before pushing to avoid non-fast-forward errors
	// (e.g. if another agent or user pushed commits to this branch while we were bootstrapping)
	pullCtx, pullCancel := context.WithTimeout(ctx, 60*time.Second)
	defer pullCancel()
	pullCmd := exec.CommandContext(pullCtx, "git", "pull", "--rebase")
	if out, err := pullCmd.CombinedOutput(); err != nil {
		fmt.Printf("[adr045-commit]   ⚠️  Auto-pull (rebase) failed, continuing to push: %s\n", strings.TrimSpace(string(out)))
	}

	// Try git push (non-fatal — user may need to push manually)
	pushCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	pushCmd := exec.CommandContext(pushCtx, "git", "push")
	if out, err := pushCmd.CombinedOutput(); err != nil {
		fmt.Printf("[adr045-commit]   ⚠️  Auto-push failed (manual push required): %s\n",
			strings.TrimSpace(string(out)))
	} else {
		fmt.Println("[adr045-commit]   ✓ Generated artifacts pushed")
	}

	return nil
}

// waitForArgoCDAppsHealthy polls ArgoCD Application resources until all
// specified apps report Sync=Synced and Health=Healthy, or the context
// expires. Uses kubectl with the provided kubeconfig.
func (o *Orchestrator) waitForArgoCDAppsHealthy(ctx context.Context, kubeconfig string, appNames []string) error {
	// helper to check a single app
	checkApp := func(app string) (synced, healthy bool, err error) {
		syncCmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
			"get", "application", app, "-n", "platform-ops",
			"-o", "jsonpath={.status.sync.status}")
		syncOut, syncErr := syncCmd.Output()
		if syncErr != nil {
			return false, false, fmt.Errorf("get sync status: %w", syncErr)
		}
		synced = strings.TrimSpace(string(syncOut)) == "Synced"

		healthCmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
			"get", "application", app, "-n", "platform-ops",
			"-o", "jsonpath={.status.health.status}")
		healthOut, healthErr := healthCmd.Output()
		if healthErr != nil {
			return false, false, fmt.Errorf("get health status: %w", healthErr)
		}
		healthy = strings.TrimSpace(string(healthOut)) == "Healthy"
		return synced, healthy, nil
	}

	getStatus := func(app string) string {
		syncOut, _ := exec.CommandContext(context.Background(), "kubectl",
			"--kubeconfig", kubeconfig,
			"get", "application", app, "-n", "platform-ops",
			"-o", "jsonpath={.status.sync.status}").Output()
		healthOut, _ := exec.CommandContext(context.Background(), "kubectl",
			"--kubeconfig", kubeconfig,
			"get", "application", app, "-n", "platform-ops",
			"-o", "jsonpath={.status.health.status}").Output()
		return fmt.Sprintf("sync=%s health=%s",
			strings.TrimSpace(string(syncOut)), strings.TrimSpace(string(healthOut)))
	}

	pollTicker := time.NewTicker(15 * time.Second)
	defer pollTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			var failures []string
			for _, app := range appNames {
				synced, healthy, _ := checkApp(app)
				if !synced || !healthy {
					failures = append(failures, fmt.Sprintf("%s: %s", app, getStatus(app)))
				}
			}
			if len(failures) > 0 {
				return fmt.Errorf("timeout waiting for ArgoCD apps:\n  %s",
					strings.Join(failures, "\n  "))
			}
			return ctx.Err()

		case <-pollTicker.C:
			allHealthy := true
			for _, app := range appNames {
				synced, healthy, err := checkApp(app)
				if err != nil {
					fmt.Printf("[adr045-commit]   ⏳ %s: error (%v), retrying...\n", app, err)
					allHealthy = false
					continue
				}
				if !synced || !healthy {
					fmt.Printf("[adr045-commit]   ⏳ %s: %s\n", app, getStatus(app))
					allHealthy = false
				}
			}
			if allHealthy {
				return nil
			}
		}
	}
}

// writeExternalDNSTargetArtifact records the hub's public ingress address as an
// ADR-045 bootstrap-generated GitOps artifact.
//
// external-dns publishes the hostnames it finds on the hub Gateway's HTTPRoutes
// but has no address to point them at: in hostNetwork mode the Gateway's
// generated Service is ClusterIP, so nothing in the cluster carries a routable
// address. The address is the control-plane load balancer's, known only once that
// load balancer exists, which is why it cannot live in the base manifest.
//
// It is written rather than passed as a Helm value because the consumer is a plain
// kustomization, and it is committed rather than patched onto the live objects so
// Git stays the source of truth and ArgoCD does not have to ignore the field.
func writeExternalDNSTargetArtifact(hubIngressAddress string) error {
	projectRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}
	path := filepath.Join(projectRoot, "manifests", "hub-core-services", "gateway",
		"generated", "external-dns-target-patch.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(path), err)
	}
	content := fmt.Sprintf(`# ADR-045 bootstrap-generated artifact — DO NOT EDIT BY HAND.
#
# Written by the hub CLI from the CAPH control-plane load balancer address
# (HetznerCluster.spec.controlPlaneEndpoint.host), the same hubIngressAddress it
# resolves for the environment-manager chart.
#
# external-dns publishes the hostnames it finds on the hub Gateway's HTTPRoutes but
# has no address to point them at: in hostNetwork mode the Gateway's generated
# Service is ClusterIP. kustomize applies this to the Gateway and to every route,
# because external-dns reads the target from the route and falls back to the
# Gateway's address, which hostNetwork mode leaves unroutable.
#
# Regenerate with the CLI rather than editing: a stale value here reaches public
# DNS, and every hub hostname resolves to whatever it says.
apiVersion: v1
kind: Placeholder
metadata:
  name: external-dns-target
  annotations:
    external-dns.alpha.kubernetes.io/target: %q
`, hubIngressAddress)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	fmt.Println("[boundary] ✓ manifests/hub-core-services/gateway/generated/external-dns-target-patch.yaml")
	return nil
}
