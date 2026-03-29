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
		Use:   "init-secrets",
		Short: "Inject base cryptographic secrets required for Infisical to boot",
		RunE:  runInitSecrets,
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

	fmt.Println("🚀 Injecting Base Cryptographic Secrets (Secret Zero)...")

	// Step 1: Generate secure passwords for platform database users
	if err := installer.InstallPlatformDatabaseCredentials(ctx); err != nil {
		return fmt.Errorf("failed to install platform database credentials: %w", err)
	}

	// Step 2: Generate Infisical base cryptographic secrets
	if err := installer.InstallInfisicalSecrets(ctx); err != nil {
		return fmt.Errorf("failed to install infisical secrets: %w", err)
	}

	// Step 3: Create Infisical PostgreSQL connection secret
	if err := installer.InstallPostgresConnectionSecret(ctx); err != nil {
		return fmt.Errorf("failed to install postgres connection secret: %w", err)
	}

	fmt.Println("\n✅ Base secrets injected! Infisical pods should now transition to Running.")
	fmt.Println("\nNext steps:")
	fmt.Println("1. Restart setup-platform-roles job to create roles with new passwords:")
	fmt.Println("   kubectl delete job setup-platform-roles -n zero-ops-system")
	fmt.Println("2. Wait for Infisical pods to become Ready")
	fmt.Println("3. Access Infisical UI at https://infisical.nutgraf.in")
	return nil
}
