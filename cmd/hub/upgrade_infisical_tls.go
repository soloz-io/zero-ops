package main

import (
	"fmt"
	"os"

	"github.com/soloz-io/zero-ops/internal/hub-cli/components"
	"github.com/spf13/cobra"
)

func newUpgradeInfisicalTLSCmd() *cobra.Command {
	var kubeconfig string

	cmd := &cobra.Command{
		Use:   "upgrade-infisical-tls",
		Short: "Upgrade Infisical to use TLS database connection",
		Long: `Upgrades Infisical from non-TLS to TLS-enabled database connection.

This command:
1. Reads platform-db-ca secret (CNPG-managed)
2. Updates infisical-secrets with DB_ROOT_CERT
3. Restarts Infisical pods to apply TLS configuration

Prerequisites:
- Database must be deployed (platform-db-ca secret exists in platform-data namespace)
- Infisical must be running with non-TLS connection

This is typically run after init-secrets during initial bootstrap, once ArgoCD
has synced and deployed the PostgreSQL database.

Security Note:
- Only Infisical pods restart (~30 seconds downtime)
- Database keeps running (zero downtime)
- No data loss or connection interruption`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			// Set default kubeconfig if not provided
			if kubeconfig == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("failed to get home directory: %w", err)
				}
				kubeconfig = fmt.Sprintf("%s/.kube/config", home)
			}

			// Verify kubeconfig exists
			if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
				return fmt.Errorf("kubeconfig not found at %s", kubeconfig)
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgradeInfisicalTLS(cmd, kubeconfig)
		},
	}

	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (default: ~/.kube/config)")

	return cmd
}

func runUpgradeInfisicalTLS(cmd *cobra.Command, kubeconfig string) error {
	ctx := cmd.Context()

	fmt.Println("🔐 Upgrading Infisical to TLS database connection...")
	fmt.Println("   This will add DB_ROOT_CERT and restart Infisical pods")
	fmt.Println("   Database will continue running without interruption")

	installer := &components.Installer{
		Kubeconfig: kubeconfig,
	}

	// Upgrade Infisical to TLS
	if err := installer.UpgradeInfisicalTLS(ctx); err != nil {
		return fmt.Errorf("failed to upgrade Infisical TLS: %w", err)
	}

	fmt.Println("\n✅ Infisical upgraded to TLS connection!")
	fmt.Println("\nNext steps:")
	fmt.Println("1. Verify Infisical pods are running:")
	fmt.Println("   kubectl get pods -n platform-security")
	fmt.Println("2. Check Infisical logs for TLS connection:")
	fmt.Println("   kubectl logs -n platform-security -l app.kubernetes.io/name=infisical")
	fmt.Println("3. Verify database connection is encrypted:")
	fmt.Println("   kubectl exec -n platform-data platform-db-1 -- psql -U postgres -c \"SELECT * FROM pg_stat_ssl WHERE pid = pg_backend_pid();\"")

	return nil
}
