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

func (i *Installer) FixArgoCDGitHubAuth(ctx context.Context, githubToken string) error {
	fmt.Println("[bootstrap] Creating ArgoCD GitHub repository secret...")

	// Load kubeconfig and create clientset
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create the secret with ArgoCD auto-discovery label
	// Use repo-creds type for organization-wide credentials
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-git-secret",
			Namespace: constants.NamespaceOps,
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "repo-creds", // Organization-wide credentials
				"app.kubernetes.io/managed-by":   "zero-ops-hub-cli",
				"app.kubernetes.io/component":    "secret-zero",
				"app.kubernetes.io/part-of":      "argocd",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"type":     "git",
			"url":      "https://github.com/soloz-io", // Organization-scoped for all repos
			"username": "zero-ops-bot",
			"password": githubToken,
		},
	}

	// Try to create, if exists then update
	_, err = clientset.CoreV1().Secrets(constants.NamespaceOps).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		// Secret might already exist, try to update
		_, err = clientset.CoreV1().Secrets(constants.NamespaceOps).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create or update ArgoCD GitHub secret: %w", err)
		}
		fmt.Println("[bootstrap] ✓ ArgoCD GitHub secret updated")
	} else {
		fmt.Println("[bootstrap] ✓ ArgoCD GitHub secret created")
	}

	fmt.Println("[bootstrap] Note: ESO will take over credential management after deployment")
	return nil
}

// InstallInfisicalSecrets generates and injects Infisical base secrets (Secret Zero)
// This is called during bootstrap BEFORE ArgoCD syncs Infisical.
// CRITICAL: These secrets enable Infisical to boot, NEVER store in Git.
//
// Generated Secrets:
// - ENCRYPTION_KEY: Exactly 32-character ASCII string (32 bytes) for AES-256-GCM encryption
// - AUTH_SECRET: Exactly 32-character ASCII string (32 bytes) for JWT signing
//
// IMPORTANT: Infisical's Node.js backend expects ENCRYPTION_KEY as a 32-byte UTF-8 string.
// We use generateSecurePassword(32) which produces exactly 32 hex characters (32 bytes).
//
// IDEMPOTENCY: Returns (true, nil) if secrets were created/modified, (false, nil) if they already exist.
// This prevents secret drift and unnecessary pod churn from repeated CLI executions.
//
// Production Workflow:
// 1. Developer runs: hub init-secrets
// 2. This method generates secure random keys using crypto/rand
// 3. Creates infisical-secrets in platform-core-db namespace
// 4. ArgoCD syncs Infisical Helm chart (wave 3)
// 5. Infisical pods start and use these secrets
