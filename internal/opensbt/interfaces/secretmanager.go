package interfaces

import "context"

// ISecretManager provides secure secret management for GitOps workflows
type ISecretManager interface {
	// Secret Storage
	StoreSecret(ctx context.Context, path string, data map[string]interface{}) error
	GetSecret(ctx context.Context, path string) (map[string]interface{}, error)
	DeleteSecret(ctx context.Context, path string) error
	ListSecrets(ctx context.Context, path string) ([]string, error)

	// Tenant-Scoped Secrets
	StoreTenantSecret(ctx context.Context, tenantID string, name string, data map[string]interface{}) error
	GetTenantSecret(ctx context.Context, tenantID string, name string) (map[string]interface{}, error)
	DeleteTenantSecret(ctx context.Context, tenantID string, name string) error

	// GitOps Integration
	EncryptForGit(ctx context.Context, data map[string]interface{}) (string, error)
	DecryptFromGit(ctx context.Context, encrypted string) (map[string]interface{}, error)

	// Secret Rotation
	RotateSecret(ctx context.Context, path string) error
}
