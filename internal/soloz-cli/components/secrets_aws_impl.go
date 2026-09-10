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

func (i *Installer) InstallAWSSecretsManagerAuth(ctx context.Context, accessKeyID, secretAccessKey, region string) error {
	fmt.Println("[bootstrap] Creating hub-operator-aws-credentials secret...")

	// Load kubeconfig and create clientset
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create namespace if it doesn't exist
	namespace := constants.NamespaceOps
	_, err = clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		// Namespace doesn't exist, create it
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		_, err = clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create namespace %s: %w", namespace, err)
		}
		fmt.Printf("[bootstrap] Created namespace %s\n", namespace)
	}

	// Create the secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hub-operator-aws-credentials",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
				"app.kubernetes.io/component":  "secret-zero",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"AWS_ACCESS_KEY_ID":     accessKeyID,
			"AWS_SECRET_ACCESS_KEY": secretAccessKey,
			"AWS_REGION":            region,
		},
	}

	// Try to create, if exists then update
	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		// Secret might already exist, try to update
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create or update hub-operator-aws-credentials secret: %w", err)
		}
		fmt.Println("[bootstrap] ✓ hub-operator-aws-credentials secret updated")
	} else {
		fmt.Println("[bootstrap] ✓ hub-operator-aws-credentials secret created")
	}

	return nil
}

// UpgradeInfisicalTLS upgrades Infisical from non-TLS to TLS-enabled database connection
// This is Phase 2 of the two-phase TLS bootstrap strategy.
//
// Phase 1 (init-secrets): Infisical starts with non-TLS connection (database may not exist)
// Phase 2 (upgrade-infisical-tls): After database is deployed, upgrade to TLS
//
// This method:
// 1. Reads platform-db-ca secret (CNPG-managed)
// 2. Updates infisical-secrets with DB_ROOT_CERT
// 3. Restarts Infisical pods to apply TLS configuration
//
// Security Note:
// - Only Infisical pods restart (~30 seconds downtime)
// - Database keeps running (zero downtime)
// - PostgreSQL accepts both TLS and non-TLS connections simultaneously
