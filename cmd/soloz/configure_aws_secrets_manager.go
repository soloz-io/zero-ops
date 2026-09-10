package main

import (
	"fmt"
	"os"

	"github.com/soloz-io/zero-ops/internal/soloz-cli/aws"
	"github.com/soloz-io/zero-ops/internal/soloz-cli/components"
	"github.com/spf13/cobra"
)

var (
	awsRegion      string
	awsKubeconfig  string
	awsEnvironment string
)

func newConfigureAWSSecretsManagerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure-aws-secrets-manager",
		Short: "Configure AWS Secrets Manager authentication for Infisical encryption key recovery",
		Long: `Configure AWS Secrets Manager authentication for disaster recovery of Infisical master keys.

This command will:
1. Create IAM user: hub-operator-secrets-manager-{environment}
2. Create and attach IAM policy with Secrets Manager permissions
3. Generate access keys for the new IAM user
4. Inject credentials into Kubernetes secret: hub-operator-aws-credentials

Prerequisites:
- AWS CLI configured with admin credentials (aws configure or AWS_PROFILE)
- IAM permissions to create users, policies, and access keys
- Kubernetes cluster access via kubeconfig

Example:
  hub configure-aws-secrets-manager \
    --environment=production \
    --aws-region=ap-south-1 \
    --kubeconfig=k8-secrets/kubeconfig/hub.kubeconfig

After running this command:
- Hub-operator will backup ENCRYPTION_KEY and AUTH_SECRET to AWS on first bootstrap
- Operator will restore keys from AWS if secrets are deleted

Security:
- Credentials follow Secret Zero pattern (CLI → K8s → Operator)
- IAM policy restricts access to /hub-operator/* paths only
- All backup/restore operations are logged via CloudTrail`,
		PreRunE: validateConfigureAWSFlags,
		RunE:    runConfigureAWSSecretsManager,
	}

	cmd.Flags().StringVar(&awsEnvironment, "environment", "production", "Environment name for IAM user (e.g., production, staging, dev)")
	cmd.Flags().StringVar(&awsRegion, "aws-region", "", "AWS region for Secrets Manager (e.g., ap-south-1)")
	cmd.Flags().StringVar(&awsKubeconfig, "kubeconfig", "", "Path to kubeconfig file (default: ~/.kube/config)")

	cmd.MarkFlagRequired("aws-region")

	return cmd
}

func validateConfigureAWSFlags(cmd *cobra.Command, args []string) error {
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
	fmt.Println()

	// Step 1: Create IAM user with Secrets Manager permissions
	fmt.Println("[1/2] Creating IAM user with Secrets Manager permissions...")
	fmt.Println("      Using AWS credentials from environment (AWS_PROFILE or default credentials)")
	fmt.Println()

	iamClient, err := aws.NewIAMClient(ctx, awsRegion)
	if err != nil {
		return fmt.Errorf("failed to create IAM client: %w\nEnsure AWS CLI is configured: aws configure or export AWS_PROFILE=<profile>", err)
	}

	iamResult, err := iamClient.CreateHubOperatorUser(ctx, awsEnvironment)
	if err != nil {
		return fmt.Errorf("failed to create IAM user: %w", err)
	}

	fmt.Println()
	fmt.Println("✅ IAM Setup Complete!")
	fmt.Printf("   User: %s\n", iamResult.UserName)
	fmt.Printf("   Access Key ID: %s\n", iamResult.AccessKeyID)
	fmt.Printf("   Policy: %s\n", iamResult.PolicyARN)
	fmt.Println()
	fmt.Println("⚠️  IMPORTANT: Save the Secret Access Key securely!")
	fmt.Printf("   Secret Access Key: %s\n", iamResult.SecretAccessKey)
	fmt.Println()

	// Step 2: Inject credentials into Kubernetes
	fmt.Println("[2/2] Injecting credentials into Kubernetes secret...")

	installer := &components.Installer{
		Kubeconfig: awsKubeconfig,
	}

	if err := installer.InstallAWSSecretsManagerAuth(ctx, iamResult.AccessKeyID, iamResult.SecretAccessKey, awsRegion); err != nil {
		return fmt.Errorf("failed to create Kubernetes secret: %w", err)
	}

	fmt.Println()
	fmt.Println("✅ Configuration complete!")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("1. Hub-operator will use these credentials for ENCRYPTION_KEY backup/restore")
	fmt.Println("2. On first bootstrap, operator will backup keys to AWS Secrets Manager")
	fmt.Println("3. If infisical-secrets is deleted, operator will restore from AWS backup")
	fmt.Println()
	fmt.Println("Verify:")
	fmt.Println("  kubectl get secret hub-operator-aws-credentials -n platform-ops -o yaml")
	fmt.Println()
	fmt.Println("Security reminders:")
	fmt.Println("  - Store the Secret Access Key in a password manager")
	fmt.Println("  - Enable CloudTrail for audit logging")
	fmt.Println("  - Rotate access keys periodically")

	return nil
}