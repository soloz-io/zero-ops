package infisical

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	infisicalclient "github.com/soloz-io/zero-ops/operators/hub-operator/internal/client"
)

// UploadCLISecrets uploads all CLI-injected secrets to Infisical
// This makes Infisical the Source of Truth for all secrets
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

	// Upload hetzner-dns token from hub-cloud-system
	if err := bc.uploadHetznerDNS(ctx, infisicalClient, projectSlug, environmentSlug, secretPath); err != nil {
		logger.Error(err, "Failed to upload hetzner-dns, continuing...")
	} else {
		logger.Info("Uploaded hetzner-dns to Infisical")
	}

	// Upload hcloud token from hub-cloud-system
	if err := bc.uploadHCloud(ctx, infisicalClient, projectSlug, environmentSlug, secretPath); err != nil {
		logger.Error(err, "Failed to upload hcloud, continuing...")
	} else {
		logger.Info("Uploaded hcloud to Infisical")
	}

	logger.Info("CLI secrets upload complete")
	return nil
}

// uploadHetznerDNS uploads hetzner-dns secret to Infisical
func (bc *BootstrapClient) uploadHetznerDNS(ctx context.Context, infisicalClient *infisicalclient.InfisicalClient, projectSlug, environmentSlug, secretPath string) error {
	logger := log.FromContext(ctx)

	// Read hetzner-dns secret from hub-cloud-system
	// Use uncached client to read secret data (cached client strips data)
	hetznerDNS := &corev1.Secret{}
	if err := bc.uncachedK8sClient.Get(ctx, client.ObjectKey{
		Name:      SecretHetznerDNS,
		Namespace: NamespaceCloudSystem,
	}, hetznerDNS); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("hetzner-dns secret not found in hub-cloud-system, skipping")
			return nil
		}
		return fmt.Errorf("failed to get hetzner-dns secret: %w", err)
	}

	// Extract api-key
	apiKey, ok := hetznerDNS.Data[KeyAPIKey]
	if !ok {
		return fmt.Errorf("hetzner-dns secret missing api-key field")
	}

	// Upload to Infisical with key name matching ExternalSecret mapping
	if err := infisicalClient.CreateOrUpdateSecretRaw(ctx, projectSlug, environmentSlug, secretPath, KeyHetznerDNSAPIKey, string(apiKey)); err != nil {
		return fmt.Errorf("failed to upload hetzner-dns-api-key: %w", err)
	}

	logger.Info("Uploaded hetzner-dns-api-key to Infisical")
	return nil
}

// uploadHCloud uploads hcloud secret to Infisical
func (bc *BootstrapClient) uploadHCloud(ctx context.Context, infisicalClient *infisicalclient.InfisicalClient, projectSlug, environmentSlug, secretPath string) error {
	logger := log.FromContext(ctx)

	// Read hcloud secret from hub-cloud-system
	// Use uncached client to read secret data (cached client strips data)
	hcloud := &corev1.Secret{}
	if err := bc.uncachedK8sClient.Get(ctx, client.ObjectKey{
		Name:      SecretHCloud,
		Namespace: NamespaceCloudSystem,
	}, hcloud); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("hcloud secret not found in hub-cloud-system, skipping")
			return nil
		}
		return fmt.Errorf("failed to get hcloud secret: %w", err)
	}

	// Extract token
	logger.Info("hcloud secret data keys", "keys", func() []string {
		keys := make([]string, 0, len(hcloud.Data))
		for k := range hcloud.Data {
			keys = append(keys, k)
		}
		return keys
	}())
	
	token, ok := hcloud.Data[KeyToken]
	if !ok {
		return fmt.Errorf("hcloud secret missing token field (expected key: %s)", KeyToken)
	}
	
	if len(token) == 0 {
		return fmt.Errorf("hcloud token is empty")
	}

	// Upload to Infisical
	if err := infisicalClient.CreateOrUpdateSecretRaw(ctx, projectSlug, environmentSlug, secretPath, KeyHCloudToken, string(token)); err != nil {
		return fmt.Errorf("failed to upload hcloud-token: %w", err)
	}

	logger.Info("Uploaded hcloud-token to Infisical")
	return nil
}
