package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/soloz-io/zero-ops/internal/hub/components"
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

	// Step 1: Generate secure passwords for platform database users
	changed1, err := installer.InstallPlatformDatabaseCredentials(ctx)
	if err != nil {
		return fmt.Errorf("failed to install platform database credentials: %w", err)
	}

	// Step 2: Generate Infisical base cryptographic secrets
	changed2, err := installer.InstallInfisicalSecrets(ctx)
	if err != nil {
		return fmt.Errorf("failed to install infisical secrets: %w", err)
	}

	// Step 3: Create Infisical PostgreSQL connection secret
	changed3, err := installer.InstallPostgresConnectionSecret(ctx)
	if err != nil {
		return fmt.Errorf("failed to install postgres connection secret: %w", err)
	}

	// Only trigger pod churn if a secret was actually created or modified
	if changed1 || changed2 || changed3 {
		if err := installer.RestartPlatformWorkloads(ctx); err != nil {
			return fmt.Errorf("failed to restart workloads: %w", err)
		}
		fmt.Println("\n✅ Bootstrap complete! New secrets injected and pods rolling out.")
	} else {
		fmt.Println("\n✅ Bootstrap complete! All dependencies already exist. No pods were restarted.")
	}

	return nil
}
