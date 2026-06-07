package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/soloz-io/zero-ops/internal/hub-cli/components"
	"github.com/soloz-io/zero-ops/internal/hub-cli/infisical"
	"github.com/spf13/cobra"
)

var initSecretsKubeconfig string

func newInitSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "init-secrets",
		Aliases: []string{"bootstrap-secrets"},
		Short:   "Bootstrap Layer 1 immutable infrastructure secrets (Secret Zero)",
		RunE:    runInitSecrets,
	}

	cmd.Flags().StringVar(&initSecretsKubeconfig, "kubeconfig", "", "Path to kubeconfig (default: ~/.kube/config)")

	return cmd
}

func runInitSecrets(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	if initSecretsKubeconfig == "" {
		home, _ := os.UserHomeDir()
		initSecretsKubeconfig = filepath.Join(home, ".kube", "config")
	}

	installer := &components.Installer{
		Kubeconfig: initSecretsKubeconfig,
	}

	accessInfo := infisical.GetAccessInfo(ctx)
	defer printManualActions(accessInfo)

	fmt.Println("Bootstrapping Layer 1 Infrastructure Secrets...")
	fmt.Println("  (Note: Existing secrets are treated as immutable and will not be overwritten)")

	// Step 0: Generate CA certificate offline (Day-0 Deterministic Injection)
	printStepBanner("0", "Generate CA certificate offline",
		"Create a self-signed CA for Infisical TLS and DB client certificates", "Seconds")
	if err := installer.GenerateAndInjectCA(ctx); err != nil {
		return fmt.Errorf("failed to generate CA: %w", err)
	}

	// Step 1: Generate Infisical base cryptographic secrets with TLS enabled
	printStepBanner("1", "Generate Infisical secrets with TLS",
		"Create Infisical JWT/encryption keys and TLS certificate signed by Step 0 CA", "Seconds")
	changed1, err := installer.InstallInfisicalSecrets(ctx)
	if err != nil {
		return fmt.Errorf("failed to install infisical secrets: %w", err)
	}

	// Step 2: Create Infisical PostgreSQL connection secret
	printStepBanner("2", "Create PostgreSQL connection secret",
		"Generate platform-db-app and infisical-db-credentials secrets with secure passwords", "Seconds")
	changed2, err := installer.InstallPostgresConnectionSecret(ctx)
	if err != nil {
		return fmt.Errorf("failed to install postgres connection secret: %w", err)
	}

	// Step 3: Wait for Infisical to become healthy before storing credentials via API.
	// Per ADR-022 we check AvailableReplicas >= 1 (REST API reachability), not
	// full desired replica count. The bash bootstrap's step5 has a separate,
	// stricter verification of the `infisical-auth` Secret artifact before
	// marking init_secrets complete — that is the real safety net.
	printStepBanner("3", "Wait for Infisical to become healthy",
		"Ensure Infisical pods are running and accepting connections before API calls", "Up to 5 minutes")
	if err := installer.WaitForInfisicalHealth(ctx); err != nil {
		fmt.Println("Warning: Infisical not yet healthy. Skipping credential storage in Infisical.")
		fmt.Println("  Run 'hub init-secrets' again after Infisical pods are running.")

		// Still trigger pod restart if secrets changed
		if changed1 || changed2 {
			if err := installer.RestartPlatformWorkloads(ctx); err != nil {
				return fmt.Errorf("failed to restart workloads: %w", err)
			}
		}
		return nil
	}

	// Step 3.5: Automatically bootstrap Infisical (Org, Project, Machine Identity).
	// This replaces the manual `hub configure-eso` step and the UI workflow.
	// Creates infisical-auth Secret + patches hub-bootstrap-config ConfigMap.
	printStepBanner("3.5", "Bootstrap Infisical (Org, Project, Machine Identity)",
		"Create Zero-Ops org, hub-platform project, Machine Identity with Universal Auth, infisical-auth Secret", "Up to 10 minutes")
	if _, err := installer.InstallInfisicalAuthFromInfisical(ctx); err != nil {
		return fmt.Errorf("failed to bootstrap Infisical: %w", err)
	}

	// Step 3.6: Verify that the argocd-bootstrap certificate profile exists.
	// Now auto-created in Step 3.5 — this is a quick verification.
	printStepBanner("3.6", "Verify argocd-bootstrap certificate profile",
		"Confirm the profile exists (auto-created in Step 3.5; required by cert-operator)", "Seconds")
	podName, err := infisical.GetInfisicalPodName(ctx)
	if err != nil {
		return fmt.Errorf("cannot find Infisical pod: %w", err)
	}
	adminJWT, _, err := infisical.GetBootstrapToken(ctx, podName)
	if err != nil {
		return fmt.Errorf("get bootstrap admin token: %w", err)
	}
	projectID, err := infisical.FindProjectBySlug(ctx, podName, adminJWT, infisical.ProjectSlug)
	if err != nil {
		return fmt.Errorf("find project %q: %w", infisical.ProjectSlug, err)
	}
	found, err := infisical.CheckCertificateProfile(ctx, podName, adminJWT, projectID, "argocd-bootstrap")
	if err != nil {
		return fmt.Errorf("certificate profile check failed: %w", err)
	}
	if !found {
		return fmt.Errorf("argocd-bootstrap certificate profile not found — should have been created in Step 3.5")
	}
	fmt.Printf("[infisical-bootstrap] ✓ Certificate profile 'argocd-bootstrap' exists\n")

	// Steps 4-5 require the Infisical REST API via infisical.NewClient,
	// which reads the INFISICAL_API_URL env var. On local clusters the
	// API is not reachable via DNS, so we start a kubectl port-forward
	// to tunnel localhost:8080 → the Infisical service inside the cluster.
	// The binary manages its own PF lifecycle so it survives pod restarts.
	pf := infisical.NewPortForwardManager("platform-security", "infisical-standalone-infisical", "8080", "8080", installer.Kubeconfig)
	var pfStarted bool

	if err := pf.EnsureAPIAccess(ctx); err != nil {
		return fmt.Errorf("failed to establish Infisical API access: %w", err)
	}
	pfStarted = true

	// Step 4: Generate secure passwords for platform database users (requires Infisical API)
	printStepBanner("4", "Store platform database credentials in Infisical",
		"Upload Layer 1 credentials to Infisical, generate and store Layer 2 app credentials", "Seconds")
	changed3, err := installer.InstallPlatformDatabaseCredentials(ctx)
	if err != nil {
		if pfStarted {
			pf.Stop()
		}
		return fmt.Errorf("failed to install platform database credentials: %w", err)
	}

	// Step 5: Generate SPIRE Server database credentials (requires Infisical API)
	printStepBanner("5", "Store SPIRE Server credentials in Infisical",
		"Generate and store spire-server-db username/password in Infisical", "Seconds")
	changed4, err := installer.InstallSPIREServerCredentials(ctx)
	if err != nil {
		if pfStarted {
			pf.Stop()
		}
		return fmt.Errorf("failed to install SPIRE Server credentials: %w", err)
	}

	// Tear down the port-forward
	if pfStarted {
		pf.Stop()
	}

	// Only trigger pod churn if a secret was actually created or modified
	if changed1 || changed2 || changed3 || changed4 {
		if err := installer.RestartPlatformWorkloads(ctx); err != nil {
			return fmt.Errorf("failed to restart workloads: %w", err)
		}
		fmt.Println("\n✅ Bootstrap complete! New secrets injected and pods rolling out.")
	} else {
		fmt.Println("\n✅ Bootstrap complete! All dependencies already exist. No pods were restarted.")
	}

	return nil
}
