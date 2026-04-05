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

// SecretUploader handles uploading secrets to Infisical
// Separation of Concerns: This class only uploads to Infisical, does not create K8s secrets
type SecretUploader struct {
	k8sClient         client.Client
	uncachedK8sClient client.Client
}

// NewSecretUploader creates a new secret uploader
func NewSecretUploader(k8sClient, uncachedK8sClient client.Client) *SecretUploader {
	return &SecretUploader{
		k8sClient:         k8sClient,
		uncachedK8sClient: uncachedK8sClient,
	}
}

// UploadCLISecrets uploads all CLI-injected secrets to Infisical
// This makes Infisical the Source of Truth for all secrets
func (su *SecretUploader) UploadCLISecrets(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// Create Infisical client using the infisical-auth secret
	// Use uncached client to read secret data (cached client strips data)
	infisicalClient, err := infisicalclient.NewInfisicalClient(ctx, su.uncachedK8sClient, "")
	if err != nil {
		return fmt.Errorf("failed to create Infisical client: %w", err)
	}

	// Get project configuration from infisical-admin secret
	// Use uncached client to avoid cache staleness after secret creation
	adminSecret := &corev1.Secret{}
	if err := su.uncachedK8sClient.Get(ctx, client.ObjectKey{
		Name:      SecretInfisicalAdmin,
		Namespace: NamespaceOps,
	}, adminSecret); err != nil {
		return fmt.Errorf("failed to get infisical-admin secret: %w", err)
	}

	projectSlug := string(adminSecret.Data[KeyProjectSlug])
	environmentSlug := EnvironmentSlug
	secretPath := "/"

	logger.Info("Uploading CLI secrets to Infisical", "project", projectSlug, "environment", environmentSlug)

	// Upload hetzner-dns token (derived from hcloud token)
	if err := su.uploadHetznerDNS(ctx, infisicalClient, projectSlug, environmentSlug, secretPath); err != nil {
		logger.Error(err, "Failed to upload hetzner-dns, continuing...")
	} else {
		logger.Info("Uploaded hetzner-dns to Infisical")
	}

	logger.Info("CLI secrets upload complete")
	return nil
}

// uploadHetznerDNS uploads hetzner-dns token to Infisical
// Reads the hcloud secret token and uploads it as hetzner-dns key
func (su *SecretUploader) uploadHetznerDNS(ctx context.Context, infisicalClient *infisicalclient.InfisicalClient, projectSlug, environmentSlug, secretPath string) error {
	logger := log.FromContext(ctx)

	// Read hcloud secret from hub-cloud-system
	// Use uncached client to read secret data (cached client strips data)
	hcloud := &corev1.Secret{}
	if err := su.uncachedK8sClient.Get(ctx, client.ObjectKey{
		Name:      SecretHCloud,
		Namespace: NamespaceCloudSystem,
	}, hcloud); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("hcloud secret not found in hub-cloud-system, skipping hetzner-dns upload")
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

	// Upload to Infisical with key name "hetzner-dns"
	// ExternalSecret will sync this to K8s secret with field "api-key"
	if err := infisicalClient.CreateOrUpdateSecretRaw(ctx, projectSlug, environmentSlug, secretPath, "hetzner-dns", string(token)); err != nil {
		return fmt.Errorf("failed to upload hetzner-dns: %w", err)
	}

	logger.Info("Uploaded hetzner-dns to Infisical")
	return nil
}
