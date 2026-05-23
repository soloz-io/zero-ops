package main

import (
	"fmt"
	"os"

	"github.com/soloz-io/zero-ops/internal/hub-cli/components"
	"github.com/spf13/cobra"
)

var (
	infisicalClientID     string
	infisicalClientSecret string
	kubeconfig            string
)

func newConfigureESOCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure-eso",
		Short: "Configure External Secrets Operator authentication",
		Long: `Configure External Secrets Operator (ESO) authentication to Infisical.

This command injects ESO authentication credentials:
1. ESO authentication to Infisical (client-id and client-secret)

Prerequisites:
- Infisical must be deployed and accessible
- Create Machine Identity in Infisical UI (Access Control -> Machine Identities)
- Copy the Client ID and Client Secret from Infisical

After running this command:
- ArgoCD will sync ESO manifests (waves 4-6)
- ESO will authenticate to Infisical
- ESO will take over credential management via GitOps
- Future credential rotations happen via Infisical + ESO`,
		PreRunE: validateConfigureESOFlags,
		RunE:    runConfigureESO,
	}

	// Required flags
	cmd.Flags().StringVar(&infisicalClientID, "infisical-client-id", "", "Infisical Machine Identity Client ID")
	cmd.Flags().StringVar(&infisicalClientSecret, "infisical-client-secret", "", "Infisical Machine Identity Client Secret")
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (default: ~/.kube/config)")

	// Mark required flags
	cmd.MarkFlagRequired("infisical-client-id")
	cmd.MarkFlagRequired("infisical-client-secret")

	return cmd
}

func validateConfigureESOFlags(cmd *cobra.Command, args []string) error {
	if infisicalClientID == "" {
		return fmt.Errorf("--infisical-client-id is required")
	}

	if infisicalClientSecret == "" {
		return fmt.Errorf("--infisical-client-secret is required")
	}

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
}

func runConfigureESO(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	fmt.Println("🔐 Configuring External Secrets Operator...")
	fmt.Println("   This will inject ESO authentication credentials for GitOps workflow")

	installer := &components.Installer{
		Kubeconfig: kubeconfig,
	}

	// Create Infisical auth secret for ESO
	fmt.Println("\n[1/1] Creating Infisical authentication secret...")
	if err := installer.InstallInfisicalAuth(ctx, infisicalClientID, infisicalClientSecret); err != nil {
		return fmt.Errorf("failed to create Infisical auth secret: %w", err)
	}

	fmt.Println("\n✅ Configuration complete!")
	fmt.Println("\nNext steps:")
	fmt.Println("1. Wait for ArgoCD to sync ESO manifests (waves 4-6)")
	fmt.Println("2. Verify ClusterSecretStore is ready:")
	fmt.Println("   kubectl get clustersecretstore infisical-backend")
	fmt.Println("3. Verify ExternalSecret is synced:")
	fmt.Println("   kubectl get externalsecret -A")
	fmt.Println("4. Wait for database deployment:")
	fmt.Println("   kubectl wait --for=condition=ready pod -l cnpg.io/cluster=platform-db -n platform-data --timeout=600s")
	fmt.Println("5. Upgrade Infisical to TLS:")
	fmt.Println("   ./bin/hub upgrade-infisical-tls --kubeconfig=k8-secrets/kubeconfig/hub.kubeconfig")

	return nil
}
