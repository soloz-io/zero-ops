package bootstrap

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/components"
	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
	"github.com/soloz-io/zero-ops/internal/hub-cli/versions"
)

// LocalProvider implements Provider for CAPD (Cluster API Provider Docker).
// This is a self-hosting provider: the kind bootstrap cluster IS the management
// cluster — no VM provisioning or CAPI pivot is needed.
//
// ADR-036 §3: CAPD is the official local development and integration testing
// provider. It is NOT production-supported.
type LocalProvider struct {
	Debug       bool
	GitHubToken string
}

// ── Identity ────────────────────────────────────────────────────────────────

func (p *LocalProvider) Name() string                   { return "docker" }
func (p *LocalProvider) IsLocal() bool                  { return true }
func (p *LocalProvider) KindConfigPath() string         { return "manifests/providers/local/kind-config.yaml" }
func (p *LocalProvider) ClusterClassPaths() []string    { return []string{"classes/capd-spoke-pool-v1.yaml"} }

// ── Phase 1: Preflight ──────────────────────────────────────────────────────

func (p *LocalProvider) PreflightValidators() []preflight.Validator {
	return []preflight.Validator{
		&preflight.DockerValidator{},
		&preflight.KindValidator{},
	}
}

// ── Phase 3: Day-0 Infrastructure ───────────────────────────────────────────

// ProvisionDayZero applies the StorageClass, labels the control-plane node as
// worker, and removes the control-plane NoSchedule taint. These are required
// because the single-node kind cluster has no dedicated workers.
func (p *LocalProvider) ProvisionDayZero(ctx context.Context, kubeconfig string) error {
	// 1. Apply the local-path StorageClass aliased as "hcloud-volumes"
	fmt.Println("[day0] Applying local-path StorageClass (hcloud-volumes)...")
	applySC := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
		"apply", "-f", "manifests/providers/local/k8s/storage-class.yaml")
	if out, err := applySC.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to apply StorageClass: %w\n%s", err, out)
	}
	fmt.Println("[day0] ✓ StorageClass applied")

	// 2. Label the control-plane node as worker so pods with
	//    nodeSelector: node-role.kubernetes.io/worker can schedule
	fmt.Println("[day0] Labeling control-plane node as worker...")
	labelCmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
		"label", "node", "--all", "node-role.kubernetes.io/worker=", "--overwrite")
	if out, err := labelCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to label node as worker: %w\n%s", err, out)
	}
	fmt.Println("[day0] ✓ Node labeled as worker")

	// 3. Remove the control-plane NoSchedule taint so hub workloads
	//    (Redis, ClickHouse, kube-sbt-api) can schedule on the single node.
	//    Not all kind/kubeadm versions add this taint — treat "not found" as success.
	fmt.Println("[day0] Removing control-plane NoSchedule taint...")
	untaintCmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
		"taint", "node", "--all", "node-role.kubernetes.io/control-plane:NoSchedule-")
	out, err := untaintCmd.CombinedOutput()
	if err != nil && !bytes.Contains(out, []byte("not found")) {
		return fmt.Errorf("failed to remove control-plane taint: %w\n%s", err, out)
	}
	fmt.Println("[day0] ✓ Control-plane taint handled")

	return nil
}

// ── Phase 4: CAPI Init ──────────────────────────────────────────────────────

func (p *LocalProvider) CAPIProviders() []capi.CAPIProvider {
	return []capi.CAPIProvider{
		{Kind: "CoreProvider", Name: "cluster-api", Version: versions.CAPIVersion, Manifest: "core/capi-operator/providers/core-provider.yaml"},
		{Kind: "BootstrapProvider", Name: "kubeadm", Version: versions.KubeadmBootstrapProviderVersion, Manifest: "core/capi-operator/providers/bootstrap-provider-kubeadm.yaml"},
		{Kind: "ControlPlaneProvider", Name: "kubeadm", Version: versions.KubeadmControlPlaneProviderVersion, Manifest: "core/capi-operator/providers/controlplane-provider-kubeadm.yaml"},
		{Kind: "InfrastructureProvider", Name: "docker", Version: versions.DockerInfraProviderVersion, Manifest: "core/capi-operator/providers/infrastructure-provider-docker.yaml"},
	}
}

func (p *LocalProvider) OnCAPIInit(ctx context.Context, kubeconfig, kubeContext, namespace string) error {
	fmt.Println("[capi-init] ✓ CAPD does not require provider credentials")
	return nil
}

// ── Phase 5: Management Cluster Provisioning (identity) ─────────────────────

func (p *LocalProvider) ProvisionManagementCluster(ctx context.Context, cfg *ProvisionConfig) error {
	fmt.Println("[cluster-provision] ✓ Local provider — bootstrap cluster IS management cluster")
	return nil
}

// ── Phase 6: Pivot Move (identity) ─────────────────────────────────────────

func (p *LocalProvider) PivotMove(ctx context.Context, cfg *PivotConfig) (string, error) {
	fmt.Println("[pivot-move] ✓ Local provider — no pivot needed")
	return cfg.BootstrapKubeconfig, nil
}

// ── Phase 7: Pivot Ready (no-op) ────────────────────────────────────────────

func (p *LocalProvider) PivotReady(ctx context.Context, mgmtKubeconfig string) error {
	fmt.Println("[pivot-ready] ✓ Local provider — no pivot needed")
	return nil
}

// ── Phase 10: Platform Pre-Requisites ───────────────────────────────────────

func (p *LocalProvider) OnPlatformPreReqs(ctx context.Context, kubeconfig string) error {
	fmt.Println("[platform-pre] Creating platform-ops namespace and ArgoCD git credentials...")
	createNS := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig,
		"create", "namespace", constants.NamespaceOps, "--dry-run=client", "-o", "yaml")
	nsYAML, err := createNS.Output()
	if err != nil {
		return fmt.Errorf("failed to generate namespace yaml: %w", err)
	}
	applyNS := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfig, "apply", "-f", "-")
	applyNS.Stdin = bytes.NewReader(nsYAML)
	if out, err := applyNS.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create platform-ops namespace: %w\n%s", err, out)
	}
	fmt.Println("[platform-pre] ✓ Namespace platform-ops created")

	if p.GitHubToken != "" {
		ci := &components.Installer{Kubeconfig: kubeconfig}
		if err := ci.FixArgoCDGitHubAuth(ctx, p.GitHubToken); err != nil {
			return fmt.Errorf("failed to create ArgoCD git secret: %w", err)
		}
	} else {
		fmt.Println("[platform-pre] No GitHub token provided — git credentials will be created by configure-github-access")
	}
	fmt.Println("[platform-pre] ✓ Platform pre-requisites complete")
	return nil
}

// ── Phase 12: Finalize ──────────────────────────────────────────────────────

func (p *LocalProvider) Finalize(ctx context.Context, cfg *FinalizeConfig) (string, error) {
	kubeconfigPath := filepath.Join("k8-secrets", "kubeconfig", cfg.ClusterName+".kubeconfig")
	if err := os.MkdirAll(filepath.Dir(kubeconfigPath), 0755); err != nil {
		return "", fmt.Errorf("failed to create kubeconfig directory: %w", err)
	}
	data, err := os.ReadFile(cfg.MgmtKubeconfig)
	if err != nil {
		return "", fmt.Errorf("failed to read kubeconfig: %w", err)
	}
	if err := os.WriteFile(kubeconfigPath, data, 0600); err != nil {
		return "", fmt.Errorf("failed to write kubeconfig: %w", err)
	}
	fmt.Printf("[finalize] ✓ Kubeconfig saved to %s\n", kubeconfigPath)
	return kubeconfigPath, nil
}

// ── Capability Contract ─────────────────────────────────────────────────────

func (p *LocalProvider) OperatorWebhookPatterns() []string { return nil }
func (p *LocalProvider) Capabilities() CapabilityContract {
	return CapabilityContract{
		Version:      "1.0.0",
		StorageClass: "hcloud-volumes",
		BlockStorage: true,
		LoadBalancer: false,
		GPU:          false,
		MaxNodes:     3,
	}
}

// Delete deletes the kind bootstrap cluster. Used by the orchestrator in
// Phase 8 (cleanup) for cloud providers that need to tear down the ephemeral
// kind cluster after pivot. Called directly by the orchestrator — not part
// of the Provider interface.
func (p *LocalProvider) DeleteKindCluster(ctx context.Context, clusterName string) error {
	return exec.CommandContext(ctx, "kind", "delete", "cluster", "--name", clusterName).Run()
}
