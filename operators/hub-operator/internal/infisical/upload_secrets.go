package infisical

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infisicalclient "github.com/soloz-io/zero-ops/operators/hub-operator/internal/client"
)

// UploadCLISecrets uploads all CLI-injected secrets to Infisical
// This makes Infisical the Source of Truth for all secrets
// Also creates derived secrets needed by platform services
func (bc *BootstrapClient) UploadCLISecrets(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// Create Infisical client using the newly created infisical-auth secret
	// Use uncached client to read secret data (cached client strips data)
	infisicalClient, err := infisicalclient.NewInfisicalClient(ctx, bc.uncachedK8sClient, "")
	if err != nil {
		return fmt.Errorf("failed to create Infisical client: %w", err)
	}

	// Get project configuration from infisical-admin secret
	// Use uncached client to avoid cache staleness after secret creation
	adminSecret := &corev1.Secret{}
	if err := bc.uncachedK8sClient.Get(ctx, client.ObjectKey{
		Name:      SecretInfisicalAdmin,
		Namespace: NamespaceOps,
	}, adminSecret); err != nil {
		return fmt.Errorf("failed to get infisical-admin secret: %w", err)
	}

	projectSlug := string(adminSecret.Data[KeyProjectSlug])
	environmentSlug := EnvironmentSlug
	secretPath := "/"

	logger.Info("Uploading CLI secrets to Infisical", "project", projectSlug, "environment", environmentSlug)

	// Create hetzner-dns secret in hub-platform-edge namespace using hcloud token
	// This is needed by external-dns and cert-manager-webhook-hetzner
	if err := bc.createHetznerDNSSecret(ctx); err != nil {
		logger.Error(err, "Failed to create hetzner-dns secret, continuing...")
	} else {
		logger.Info("Created hetzner-dns secret in hub-platform-edge")
	}

	logger.Info("CLI secrets upload complete")
	return nil
}

// createHetznerDNSSecret creates hetzner-dns secret in hub-platform-edge namespace
// using the token from hcloud secret in hub-cloud-system
func (bc *BootstrapClient) createHetznerDNSSecret(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// Read hcloud secret from hub-cloud-system to get the token
	hcloud := &corev1.Secret{}
	if err := bc.uncachedK8sClient.Get(ctx, client.ObjectKey{
		Name:      SecretHCloud,
		Namespace: NamespaceCloudSystem,
	}, hcloud); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("hcloud secret not found in hub-cloud-system, skipping hetzner-dns creation")
			return nil
		}
		return fmt.Errorf("failed to get hcloud secret: %w", err)
	}

	// Extract token
	token, ok := hcloud.Data[KeyToken]
	if !ok {
		return fmt.Errorf("hcloud secret missing token field")
	}
	
	if len(token) == 0 {
		return fmt.Errorf("hcloud token is empty")
	}

	// Create hetzner-dns secret in hub-platform-edge namespace
	hetznerDNS := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hetzner-dns",
			Namespace: NamespaceEdge,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "hub-operator",
				"app.kubernetes.io/component":  "dns-credentials",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"api-key": token,
		},
	}

	// Check if secret already exists
	existingSecret := &corev1.Secret{}
	err := bc.k8sClient.Get(ctx, client.ObjectKey{
		Name:      "hetzner-dns",
		Namespace: NamespaceEdge,
	}, existingSecret)
	
	if err == nil {
		// Secret exists, update it
		existingSecret.Data = hetznerDNS.Data
		if err := bc.k8sClient.Update(ctx, existingSecret); err != nil {
			return fmt.Errorf("failed to update hetzner-dns secret: %w", err)
		}
		logger.Info("Updated hetzner-dns secret in hub-platform-edge")
	} else if errors.IsNotFound(err) {
		// Secret doesn't exist, create it
		if err := bc.k8sClient.Create(ctx, hetznerDNS); err != nil {
			return fmt.Errorf("failed to create hetzner-dns secret: %w", err)
		}
		logger.Info("Created hetzner-dns secret in hub-platform-edge")
	} else {
		return fmt.Errorf("failed to check hetzner-dns secret: %w", err)
	}

	return nil
}
