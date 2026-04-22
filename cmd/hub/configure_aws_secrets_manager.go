package main

import (
	"fmt"
	"os"

	"github.com/soloz-io/zero-ops/internal/hub/components"
	"github.com/spf13/cobra"
)

var (
	awsAccessKeyID     string
	awsSecretAccessKey string
	awsRegion          string
	awsKubeconfig      string
)

func newConfigureAWSSecretsManagerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure-aws-secrets-manager",
		Short: "Configure AWS Secrets Manager authentication for Infisical encryption key recovery",
		Long: `Configure AWS Secrets Manager authentication for disaster recovery of Infisical master encryption keys.

This command injects Secret Zero credentials that enable the encryption key recovery system:
1. AWS IAM credentials for Secrets Manager access (access-key-id, secret-access-key, region)
2. Hub-operator will use these credentials to backup and restore ENCRYPTION_KEY and AUTH_SECRET

Prerequisites:
- AWS IAM user with restricted Secrets Manager permissions
- IAM policy allowing secretsmanager:GetSecretValue, PutSecretValue, CreateSecret on /hub-operator/{cluster-id}/* paths
- AWS region where Secrets Manager will store backups

After running this command:
- Hub-operator deployment will have AWS credentials via environment variables
- Operator will backup ENCRYPTION_KEY and AUTH_SECRET to AWS on first bootstrap
- Operator will restore keys from AWS if secrets are deleted
- Future credential rotations happen via Infisical + ESO (GitOps)

Security:
- Credentials follow Secret Zero pattern (CLI → K8s → Infisical → ExternalSecret)
- IAM policy restricts access to hub-operator paths only
- All backup/restore operations are logged via CloudTrail`,
		PreRunE: validateConfigureAWSFlags,
		RunE:    runConfigureAWSSecretsManager,
	}

	// Required flags
	cmd.Flags().StringVar(&awsAccessKeyID, "aws-access-key-id", "", "AWS IAM Access Key ID")
	cmd.Flags().StringVar(&awsSecretAccessKey, "aws-secret-access-key", "", "AWS IAM Secret Access Key")
	cmd.Flags().StringVar(&awsRegion, "aws-region", "", "AWS region for Secrets Manager (e.g., ap-south-1)")
	cmd.Flags().StringVar(&awsKubeconfig, "kubeconfig", "", "Path to kubeconfig file (default: ~/.kube/config)")

	// Mark required flags
	cmd.MarkFlagRequired("aws-access-key-id")
	cmd.MarkFlagRequired("aws-secret-access-key")
	cmd.MarkFlagRequired("aws-region")

	return cmd
}

func validateConfigureAWSFlags(cmd *cobra.Command, args []string) error {
	if awsAccessKeyID == "" {
		return fmt.Errorf("--aws-access-key-id is required")
	}

	if awsSecretAccessKey == "" {
		return fmt.Errorf("--aws-secret-access-key is required")
	}

	if awsRegion == "" {
		return fmt.Errorf("--aws-region is required")
	}

	// Set default kubeconfig if not provided
	if awsKubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		awsKubeconfig = fmt.Sprintf("%s/.kube/config", home)
	}

	// Verify kubeconfig exists
	if _, err := os.Stat(awsKubeconfig); os.IsNotExist(err) {
		return fmt.Errorf("kubeconfig not found at %s", awsKubeconfig)
	}

	return nil
}

func runConfigureAWSSecretsManager(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	fmt.Println("🔐 Configuring AWS Secrets Manager for Infisical encryption key recovery...")
	fmt.Println("   This will inject Secret Zero credentials for disaster recovery workflow")

	installer := &components.Installer{
		Kubeconfig: awsKubeconfig,
	}

	// Create AWS credentials secret for hub-operator
	fmt.Println("\n[1/1] Creating AWS Secrets Manager authentication secret...")
	if err := installer.InstallAWSSecretsManagerAuth(ctx, awsAccessKeyID, awsSecretAccessKey, awsRegion); err != nil {
		return fmt.Errorf("failed to create AWS credentials secret: %w", err)
	}

	fmt.Println("\n✅ Configuration complete!")
	fmt.Println("\nNext steps:")
	fmt.Println("1. Hub-operator will use these credentials for ENCRYPTION_KEY backup/restore")
	fmt.Println("2. On first bootstrap, operator will backup keys to AWS Secrets Manager")
	fmt.Println("3. If infisical-secrets is deleted, operator will restore from AWS backup")
	fmt.Println("4. Verify AWS credentials are working:")
	fmt.Println("   kubectl get secret hub-operator-aws-credentials -n hub-platform-ops -o yaml")
	fmt.Println("5. Check hub-operator logs for backup/restore operations")

	return nil
}