package infisical

import (
	"context"
	"fmt"
	"os"

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

// UploadCLISecrets uploads all CLI-injected and bootstrap secrets to Infisical
// This makes Infisical the Source of Truth for all secrets
func (su *SecretUploader) UploadCLISecrets(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// Create Infisical client using the infisical-auth secret
	// Use uncached client to read secret data (cached client strips data)
	infisicalClient, err := infisicalclient.NewInfisicalClient(ctx, su.uncachedK8sClient, "")
	if err != nil {
		return fmt.Errorf("failed to create Infisical client: %w", err)
	}

	// Get project slug from environment variable (set via configmap hub-bootstrap-config)
	// Use secrets project for secret operations (hub-secrets is type "secret-manager")
	// Fall back to INFISICAL_PROJECT_SLUG for backward compatibility
	projectSlug := os.Getenv("INFISICAL_SECRETS_PROJECT_SLUG")
	if projectSlug == "" {
		projectSlug = os.Getenv("INFISICAL_PROJECT_SLUG")
	}
	if projectSlug == "" {
		return fmt.Errorf("neither INFISICAL_SECRETS_PROJECT_SLUG nor INFISICAL_PROJECT_SLUG environment variable is set")
	}

	environmentSlug := EnvironmentSlug
	secretPath := "/"

	logger.Info("Uploading CLI secrets to Infisical", "project", projectSlug, "environment", environmentSlug)

	// Upload all configured secrets from the registry
	// See secret_mappings.go for the complete list
	mappings := CLISecretMappings
	successCount := 0
	var failedKeys []string

	for _, mapping := range mappings {
		if err := su.uploadSecret(ctx, infisicalClient, projectSlug, environmentSlug, secretPath, mapping); err != nil {
			logger.Error(err, "Failed to upload secret",
				"description", mapping.Description,
				"source", fmt.Sprintf("%s/%s", mapping.SourceNamespace, mapping.SourceName),
				"infisicalKey", mapping.InfisicalKey)
			failedKeys = append(failedKeys, mapping.InfisicalKey)
		} else {
			successCount++
			logger.Info("Uploaded secret to Infisical",
				"description", mapping.Description,
				"infisicalKey", mapping.InfisicalKey)
		}
	}

	logger.Info("CLI secrets upload complete", "uploaded", successCount, "total", len(mappings), "failed", len(failedKeys))

	if successCount == 0 && len(mappings) > 0 {
		return fmt.Errorf("all %d secret uploads failed, first failure: %v", len(mappings), failedKeys)
	}

	return nil
}

// uploadSecret uploads a single secret to Infisical based on the mapping configuration
func (su *SecretUploader) uploadSecret(ctx context.Context, infisicalClient *infisicalclient.InfisicalClient, projectSlug, environmentSlug, secretPath string, mapping SecretMapping) error {
	logger := log.FromContext(ctx)

	// Read source secret from K8s
	// Use uncached client to read secret data (cached client strips data)
	secret := &corev1.Secret{}
	if err := su.uncachedK8sClient.Get(ctx, client.ObjectKey{
		Name:      mapping.SourceName,
		Namespace: mapping.SourceNamespace,
	}, secret); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Source secret not found, skipping",
				"secret", fmt.Sprintf("%s/%s", mapping.SourceNamespace, mapping.SourceName))
			return nil
		}
		return fmt.Errorf("failed to get source secret: %w", err)
	}

	// Extract the specified key
	value, ok := secret.Data[mapping.SourceKey]
	if !ok {
		return fmt.Errorf("source secret missing key %s", mapping.SourceKey)
	}

	if len(value) == 0 {
		return fmt.Errorf("source secret key %s is empty", mapping.SourceKey)
	}

	// Upload to Infisical
	if err := infisicalClient.CreateOrUpdateSecretRaw(ctx, projectSlug, environmentSlug, secretPath, mapping.InfisicalKey, string(value)); err != nil {
		return fmt.Errorf("failed to upload to Infisical: %w", err)
	}

	return nil
}
