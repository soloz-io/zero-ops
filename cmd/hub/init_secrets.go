package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/soloz-io/zero-ops/internal/hub-cli/components"
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

	// Step 1: Generate Infisical base cryptographic secrets FIRST
	// This must happen before connecting to Infisical API to avoid circular dependency
	changed1, err := installer.InstallInfisicalSecrets(ctx)
	if err != nil {
		return fmt.Errorf("failed to install infisical secrets: %w", err)
	}

	// Step 2: Create Infisical PostgreSQL connection secret
	changed2, err := installer.InstallPostgresConnectionSecret(ctx)
	if err != nil {
		return fmt.Errorf("failed to install postgres connection secret: %w", err)
	}

	// Step 3: Wait for Infisical to become healthy before storing credentials via API
	fmt.Println("\n⏳ Waiting for Infisical to become healthy...")
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

	// Step 4: Generate secure passwords for platform database users (requires Infisical API)
	changed3, err := installer.InstallPlatformDatabaseCredentials(ctx)
	if err != nil {
		return fmt.Errorf("failed to install platform database credentials: %w", err)
	}

	// Step 5: Generate SPIRE Server database credentials (requires Infisical API)
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
