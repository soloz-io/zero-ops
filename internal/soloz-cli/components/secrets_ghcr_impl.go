package components

import (
	"context"
	"fmt"

	"github.com/soloz-io/zero-ops/internal/soloz-cli/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func (i *Installer) InstallGHCRPullSecret(ctx context.Context, username, token string) error {
	fmt.Println("[bootstrap] Creating GHCR pull secret...")

	// Load kubeconfig and create clientset
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	namespace := constants.NamespaceOps

	// Create Docker config JSON for GHCR authentication
	dockerConfigJSON := fmt.Sprintf(`{"auths":{"ghcr.io":{"username":%q,"password":%q}}}`, username, token)

	// Create the secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ghcr-pull-secret",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
				"app.kubernetes.io/component":  "secret-zero",
			},
		},
		Type: corev1.SecretTypeDockerConfigJson,
		StringData: map[string]string{
			".dockerconfigjson": dockerConfigJSON,
		},
	}

	// Try to create, if exists then update
	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		// Secret might already exist, try to update
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create or update ghcr-pull-secret: %w", err)
		}
		fmt.Println("[bootstrap] ✓ ghcr-pull-secret updated")
	} else {
		fmt.Println("[bootstrap] ✓ ghcr-pull-secret created")
	}

	return nil
}

// InstallAWSSecretsManagerAuth creates the AWS credentials secret for hub-operator
// This is Secret Zero - it enables hub-operator to backup/restore Infisical master keys.
// CRITICAL: This secret MUST be injected via client-go, NEVER stored in Git.
//
// Production Workflow:
// 1. Developer creates IAM user with restricted Secrets Manager permissions
// 2. Developer runs: hub configure-aws-secrets-manager --aws-access-key-id=<id> --aws-secret-access-key=<secret> --aws-region=<region>
// 3. This method uses client-go to inject the secret directly into the cluster
// 4. Hub-operator deployment references this secret via secretKeyRef environment variables
// 5. Operator uses AWS SDK to backup/restore ENCRYPTION_KEY and AUTH_SECRET
// 6. Future credential rotations happen via Infisical + ESO (GitOps)
