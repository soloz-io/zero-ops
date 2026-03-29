package main

import (
	"context"
	"fmt"
	"os"

	"github.com/soloz-io/zero-ops/internal/hub/components"
	"github.com/spf13/cobra"
)

var (
	infisicalClientID     string
	infisicalClientSecret string
	githubToken           string
	kubeconfig            string
)

func newConfigureESOCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure-eso",
		Short: "Configure External Secrets Operator authentication",
		Long: `Configure External Secrets Operator (ESO) authentication to Infisical and ArgoCD GitHub access.

This command injects Secret Zero credentials that enable the GitOps workflow:
1. ESO authentication to Infisical (client-id and client-secret)
2. ArgoCD GitHub repository access (GitHub PAT)

Prerequisites:
- Infisical must be deployed and accessible
- Create Machine Identity in Infisical UI (Access Control -> Machine Identities)
- Copy the Client ID and Client Secret from Infisical
- Generate GitHub Personal Access Token with repo scope

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
	cmd.Flags().StringVar(&githubToken, "github-token", "", "GitHub Personal Access Token")
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (default: ~/.kube/config)")

	// Mark required flags
	cmd.MarkFlagRequired("infisical-client-id")
	cmd.MarkFlagRequired("infisical-client-secret")
	cmd.MarkFlagRequired("github-token")

	return cmd
}

func validateConfigureESOFlags(cmd *cobra.Command, args []string) error {
	if infisicalClientID == "" {
		return fmt.Errorf("--infisical-client-id is required")
	}

	if infisicalClientSecret == "" {
		return fmt.Errorf("--infisical-client-secret is required")
	}

	if githubToken == "" {
		return fmt.Errorf("--github-token is required")
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
	fmt.Println("   This will inject Secret Zero credentials for GitOps workflow")

	installer := &components.Installer{
		Kubeconfig: kubeconfig,
	}

	// Step 1: Create Infisical auth secret for ESO
	fmt.Println("\n[1/2] Creating Infisical authentication secret...")
	if err := installer.InstallInfisicalAuth(ctx, infisicalClientID, infisicalClientSecret); err != nil {
		return fmt.Errorf("failed to create Infisical auth secret: %w", err)
	}

	// Step 2: Create ArgoCD GitHub auth secret
	fmt.Println("\n[2/2] Creating ArgoCD GitHub authentication secret...")
	if err := installer.FixArgoCDGitHubAuth(ctx, githubToken); err != nil {
		return fmt.Errorf("failed to create ArgoCD GitHub secret: %w", err)
	}

	fmt.Println("\n✅ Configuration complete!")
	fmt.Println("\nNext steps:")
	fmt.Println("1. Wait for ArgoCD to sync ESO manifests (waves 4-6)")
	fmt.Println("2. Verify ClusterSecretStore is ready:")
	fmt.Println("   kubectl get clustersecretstore infisical-backend")
	fmt.Println("3. Verify ExternalSecret is synced:")
	fmt.Println("   kubectl get externalsecret argocd-github-creds -n argocd")
	fmt.Println("4. Run validation script:")
	fmt.Println("   ./test/e2e/check-secret-health.sh")

	return nil
}
