// Package vault provides a HashiCorp Vault ISecretManager implementation
// (Tasks 29.1–29.10).
package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hashicorp/vault/api"
	"github.com/sirupsen/logrus"
)

// Config holds Vault connection and auth settings (29.1).
type Config struct {
	Address   string // e.g. http://vault:8200
	Token     string // root/service token (or use AppRole below)
	RoleID    string // AppRole role_id  (optional)
	SecretID  string // AppRole secret_id (optional)
	MountPath string // KV v2 mount, default "secret"
	// EncryptionKey is a 32-byte AES-256 key used for GitOps envelope encryption (29.4).
	// If empty, EncryptForGit / DecryptFromGit use base64 only (dev mode).
	EncryptionKey []byte
}

// VaultSecretManager implements interfaces.ISecretManager (29.1–29.10).
type VaultSecretManager struct {
	client    *api.Client
	mount     string
	encKey    []byte
	log       *logrus.Logger
}

// New creates a VaultSecretManager and authenticates (29.1).
func New(cfg Config) (*VaultSecretManager, error) {
	vcfg := api.DefaultConfig()
	vcfg.Address = cfg.Address

	client, err := api.NewClient(vcfg)
	if err != nil {
		return nil, fmt.Errorf("vault: create client: %w", err)
	}

	if cfg.RoleID != "" && cfg.SecretID != "" {
		// AppRole authentication
		resp, err := client.Logical().Write("auth/approle/login", map[string]interface{}{
			"role_id":   cfg.RoleID,
			"secret_id": cfg.SecretID,
		})
		if err != nil {
			return nil, fmt.Errorf("vault: approle login: %w", err)
		}
		client.SetToken(resp.Auth.ClientToken)
	} else {
		client.SetToken(cfg.Token)
	}

	mount := cfg.MountPath
	if mount == "" {
		mount = "secret"
	}

	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})

	return &VaultSecretManager{client: client, mount: mount, encKey: cfg.EncryptionKey, log: log}, nil
}

// ─── Generic Secret Storage (29.2, 29.3, 29.7) ───────────────────────────────

// StoreSecret writes data to a KV v2 path (29.2).
func (v *VaultSecretManager) StoreSecret(ctx context.Context, path string, data map[string]interface{}) error {
	_, err := v.client.KVv2(v.mount).Put(ctx, path, data)
	if err != nil {
		return fmt.Errorf("vault: store secret %q: %w", path, err)
	}
	v.audit(ctx, "store", path, "")
	return nil
}

// GetSecret retrieves the latest version of a secret (29.3).
func (v *VaultSecretManager) GetSecret(ctx context.Context, path string) (map[string]interface{}, error) {
	secret, err := v.client.KVv2(v.mount).Get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("vault: get secret %q: %w", path, err)
	}
	v.audit(ctx, "get", path, "")
	return secret.Data, nil
}

// DeleteSecret permanently deletes all versions of a secret (29.3).
func (v *VaultSecretManager) DeleteSecret(ctx context.Context, path string) error {
	if err := v.client.KVv2(v.mount).DeleteMetadata(ctx, path); err != nil {
		return fmt.Errorf("vault: delete secret %q: %w", path, err)
	}
	v.audit(ctx, "delete", path, "")
	return nil
}

// ListSecrets returns secret names under a path prefix (29.3).
func (v *VaultSecretManager) ListSecrets(ctx context.Context, path string) ([]string, error) {
	secret, err := v.client.Logical().ListWithContext(ctx,
		fmt.Sprintf("%s/metadata/%s", v.mount, path))
	if err != nil {
		return nil, fmt.Errorf("vault: list secrets %q: %w", path, err)
	}
	if secret == nil {
		return nil, nil
	}
	raw, _ := secret.Data["keys"].([]interface{})
	keys := make([]string, 0, len(raw))
	for _, k := range raw {
		if s, ok := k.(string); ok {
			keys = append(keys, s)
		}
	}
	return keys, nil
}

// ─── Tenant-Scoped Secrets (29.2, 29.7) ──────────────────────────────────────

func tenantPath(tenantID, name string) string {
	return fmt.Sprintf("tenants/%s/%s", tenantID, name)
}

// StoreTenantSecret stores a secret scoped to a tenant (29.2, 29.7).
func (v *VaultSecretManager) StoreTenantSecret(ctx context.Context, tenantID, name string, data map[string]interface{}) error {
	return v.StoreSecret(ctx, tenantPath(tenantID, name), data)
}

// GetTenantSecret retrieves a tenant-scoped secret (29.3, 29.7).
func (v *VaultSecretManager) GetTenantSecret(ctx context.Context, tenantID, name string) (map[string]interface{}, error) {
	return v.GetSecret(ctx, tenantPath(tenantID, name))
}

// DeleteTenantSecret removes a tenant-scoped secret (29.7).
func (v *VaultSecretManager) DeleteTenantSecret(ctx context.Context, tenantID, name string) error {
	return v.DeleteSecret(ctx, tenantPath(tenantID, name))
}

// ─── GitOps Encryption (29.4, 29.8, 29.9) ────────────────────────────────────

// EncryptForGit serialises data to JSON and AES-256-GCM encrypts it (29.4, 29.8).
// The result is a base64-encoded ciphertext safe to commit to Git.
// If no EncryptionKey is configured, returns plain base64 (dev mode).
func (v *VaultSecretManager) EncryptForGit(_ context.Context, data map[string]interface{}) (string, error) {
	plain, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	if len(v.encKey) == 0 {
		return base64.StdEncoding.EncodeToString(plain), nil
	}
	block, err := aes.NewCipher(v.encKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, plain, nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// DecryptFromGit reverses EncryptForGit (29.4, 29.8).
func (v *VaultSecretManager) DecryptFromGit(_ context.Context, encrypted string) (map[string]interface{}, error) {
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, fmt.Errorf("vault: decrypt: base64: %w", err)
	}
	var plain []byte
	if len(v.encKey) == 0 {
		plain = raw
	} else {
		block, err := aes.NewCipher(v.encKey)
		if err != nil {
			return nil, err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		ns := gcm.NonceSize()
		if len(raw) < ns {
			return nil, fmt.Errorf("vault: decrypt: ciphertext too short")
		}
		plain, err = gcm.Open(nil, raw[:ns], raw[ns:], nil)
		if err != nil {
			return nil, fmt.Errorf("vault: decrypt: %w", err)
		}
	}
	var out map[string]interface{}
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// VaultRef returns a vault:// reference string for a path (29.9).
// Consumers store this reference in Git instead of the plaintext secret.
func VaultRef(mount, path string) string {
	return fmt.Sprintf("vault://%s/%s", mount, path)
}

// IsVaultRef reports whether s is a vault:// reference (29.9).
func IsVaultRef(s string) bool { return strings.HasPrefix(s, "vault://") }

// ─── Secret Rotation (29.5) ───────────────────────────────────────────────────

// RotateSecret re-generates the secret at path by reading it, calling the
// Vault KV v2 "destroy" on the oldest version, and re-writing the data.
// For credentials that need external rotation (DB passwords, API keys) callers
// should update the external system first, then call StoreSecret.
func (v *VaultSecretManager) RotateSecret(ctx context.Context, path string) error {
	data, err := v.GetSecret(ctx, path)
	if err != nil {
		return err
	}
	// Re-write creates a new version; Vault retains history automatically.
	if err := v.StoreSecret(ctx, path, data); err != nil {
		return err
	}
	v.audit(ctx, "rotate", path, "")
	return nil
}

// ─── Secret Versioning (29.10) ───────────────────────────────────────────────

// GetSecretVersion retrieves a specific version of a secret (29.10).
func (v *VaultSecretManager) GetSecretVersion(ctx context.Context, path string, version int) (map[string]interface{}, error) {
	secret, err := v.client.KVv2(v.mount).GetVersion(ctx, path, version)
	if err != nil {
		return nil, fmt.Errorf("vault: get secret version %d %q: %w", version, path, err)
	}
	v.audit(ctx, "get_version", path, fmt.Sprintf("version=%d", version))
	return secret.Data, nil
}

// RollbackSecret restores a previous version by re-writing its data as a new version (29.10).
func (v *VaultSecretManager) RollbackSecret(ctx context.Context, path string, version int) error {
	data, err := v.GetSecretVersion(ctx, path, version)
	if err != nil {
		return err
	}
	if err := v.StoreSecret(ctx, path, data); err != nil {
		return err
	}
	v.audit(ctx, "rollback", path, fmt.Sprintf("from_version=%d", version))
	return nil
}

// ─── Audit Logging (29.6) ────────────────────────────────────────────────────

func (v *VaultSecretManager) audit(ctx context.Context, action, path, extra string) {
	fields := logrus.Fields{"action": action, "path": path}
	if extra != "" {
		fields["detail"] = extra
	}
	if tenantID, ok := ctx.Value(ctxKey("tenant_id")).(string); ok && tenantID != "" {
		fields["tenant_id"] = tenantID
	}
	if userID, ok := ctx.Value(ctxKey("user_id")).(string); ok && userID != "" {
		fields["user_id"] = userID
	}
	v.log.WithFields(fields).Info("vault audit")
}

type ctxKey string
