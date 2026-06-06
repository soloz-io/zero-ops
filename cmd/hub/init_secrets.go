package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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

	fmt.Println("🚀 Bootstrapping Layer 1 Infrastructure Secrets...")
	fmt.Println("   (Note: Existing secrets are treated as immutable and will not be overwritten)")

	// Step 0: Generate CA certificate offline (Day-0 Deterministic Injection)
	// This must happen FIRST to break the chicken-and-egg problem
	fmt.Println("\n[Step 0/5] Generating CA certificate offline...")
	if err := installer.GenerateAndInjectCA(ctx); err != nil {
		return fmt.Errorf("failed to generate CA: %w", err)
	}

	// Step 1: Generate Infisical base cryptographic secrets with TLS enabled
	// This uses the CA generated in Step 0
	fmt.Println("\n[Step 1/5] Generating Infisical secrets with TLS...")
	changed1, err := installer.InstallInfisicalSecrets(ctx)
	if err != nil {
		return fmt.Errorf("failed to install infisical secrets: %w", err)
	}

	// Step 2: Create Infisical PostgreSQL connection secret
	fmt.Println("\n[Step 2/5] Creating PostgreSQL connection secret...")
	changed2, err := installer.InstallPostgresConnectionSecret(ctx)
	if err != nil {
		return fmt.Errorf("failed to install postgres connection secret: %w", err)
	}

	// Step 3: Wait for Infisical to become healthy before storing credentials via API
	fmt.Println("\n[Step 3/5] Waiting for Infisical to become healthy...")
	if err := installer.WaitForInfisicalHealth(ctx); err != nil {
		fmt.Println("⚠️  Warning: Infisical not yet healthy. Skipping credential storage in Infisical.")
		fmt.Println("   Run 'hub init-secrets' again after Infisical pods are running.")

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
	fmt.Println("\n[Step 3.5/6] Bootstrapping Infisical (Org, Project, Machine Identity)...")
	if _, err := installer.InstallInfisicalAuthFromInfisical(ctx); err != nil {
		return fmt.Errorf("failed to bootstrap Infisical: %w", err)
	}

	// Step 3.6: Verify that the argocd-bootstrap cert-manager profile exists in Infisical.
	// The cert-operator needs this profile to mint bootstrap certificates for SpokePools.
	// This step is a hard gate: the CLI blocks for up to 10 minutes with clear instructions
	// for the platform admin to create the profile in the Infisical UI.
	fmt.Println("\n[Step 3.6/6] Verifying argocd-bootstrap certificate profile...")
	podName, err := infisical.GetInfisicalPodName(ctx)
	if err != nil {
		return fmt.Errorf("cannot find Infisical pod: %w", err)
	}
	adminJWT, _, err := infisical.GetBootstrapToken(ctx, podName)
	if err != nil {
		return fmt.Errorf("get bootstrap admin token: %w", err)
	}
	projectID, err := infisical.FindProjectBySlug(ctx, podName, adminJWT, "hub-platform")
	if err != nil {
		return fmt.Errorf("find project 'hub-platform': %w", err)
	}
	if err := infisical.WaitForCertificateProfile(ctx, podName, adminJWT, projectID, "argocd-bootstrap", 10*time.Minute); err != nil {
		return fmt.Errorf("certificate profile check failed: %w", err)
	}

	// Step 4: Generate secure passwords for platform database users (requires Infisical API)
	fmt.Println("\n[Step 4/6] Storing platform database credentials in Infisical...")
	changed3, err := installer.InstallPlatformDatabaseCredentials(ctx)
	if err != nil {
		return fmt.Errorf("failed to install platform database credentials: %w", err)
	}

	// Step 5: Generate SPIRE Server database credentials (requires Infisical API)
	fmt.Println("\n[Step 5/6] Storing SPIRE Server credentials in Infisical...")
	changed4, err := installer.InstallSPIREServerCredentials(ctx)
	if err != nil {
		return fmt.Errorf("failed to install SPIRE Server credentials: %w", err)
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
