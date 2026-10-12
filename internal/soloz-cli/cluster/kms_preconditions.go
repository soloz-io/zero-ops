package cluster

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/internal/platform/kms"
)

// KMSPreconditions asserts the 9 prerequisites for KMS v2 before a Cluster is applied.
//
// Active ONLY when EncryptionMode == EncryptionKMSv2. Under secretbox this is a no-op.
type KMSPreconditions struct {
	ClusterName string
	Namespace   string
	KMSConfig   KMSSettings
	Kubeconfig  string

	// SecretGetter allows overriding secret retrieval for unit tests
	SecretGetter func(ctx context.Context, name string) (map[string]any, error)
	// TrustVerifier allows overriding trust anchor verification for unit tests
	TrustVerifier func(ctx context.Context, pool, provider string, caPEM []byte) error
	// KeyVerifier allows overriding KMS key existence check for unit tests
	KeyVerifier func(ctx context.Context, keyName string) error
	// IAMVerifier allows overriding IAM binding verification for unit tests
	IAMVerifier func(ctx context.Context, keyName, expectedMember string) error
}

// Assert verifies all 9 preconditions and returns an error naming the first failed assertion.
func (c *KMSPreconditions) Assert(ctx context.Context) error {
	// 1. CA Secret exists
	caSecretName := assets.KMSCASecretName(c.ClusterName)
	caData, err := c.getSecret(ctx, caSecretName)
	if err != nil {
		return fmt.Errorf("KMS v2 precondition 1/9 failed: CA Secret %s not found in %s: %w", caSecretName, c.Namespace, err)
	}
	caCertB64, _ := caData["tls.crt"].(string)
	if caCertB64 == "" {
		return fmt.Errorf("KMS v2 precondition 1/9 failed: CA Secret %s is missing tls.crt", caSecretName)
	}
	caCertBytes, err := base64.StdEncoding.DecodeString(caCertB64)
	if err != nil || len(caCertBytes) == 0 {
		return fmt.Errorf("KMS v2 precondition 1/9 failed: CA Secret %s has invalid base64 in tls.crt: %w", caSecretName, err)
	}

	// 2. WIF provider exists and trusts the CA
	pool := "soloz-" + c.ClusterName
	provider := "control-plane"
	if c.TrustVerifier != nil {
		if err := c.TrustVerifier(ctx, pool, provider, caCertBytes); err != nil {
			return fmt.Errorf("KMS v2 precondition 2/9 failed: WIF provider does not trust CA: %w", err)
		}
	} else {
		client, err := c.newClient(ctx)
		if err != nil {
			return fmt.Errorf("KMS v2 precondition 2/9 client error: %w", err)
		}
		if err := client.VerifyTrustAnchor(ctx, pool, provider, caCertBytes); err != nil {
			return fmt.Errorf("KMS v2 precondition 2/9 failed: WIF provider does not trust CA: %w", err)
		}
	}

	// 3. KMS key exists
	keyName := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s",
		c.KMSConfig.ProjectID, c.KMSConfig.Location, c.KMSConfig.KeyRing, kms.KeyIDFor(c.ClusterName))
	if c.KeyVerifier != nil {
		if err := c.KeyVerifier(ctx, keyName); err != nil {
			return fmt.Errorf("KMS v2 precondition 3/9 failed: KMS CryptoKey %s check failed: %w", keyName, err)
		}
	} else {
		client, err := c.newClient(ctx)
		if err != nil {
			return fmt.Errorf("KMS v2 precondition 3/9 client error: %w", err)
		}
		cryptoKey, err := client.GetCryptoKey(ctx, keyName)
		if err != nil || cryptoKey == nil {
			return fmt.Errorf("KMS v2 precondition 3/9 failed: KMS CryptoKey %s does not exist: %w", keyName, err)
		}
	}

	// 4. IAM binding allows the node service account to encrypt/decrypt
	expectedMember := fmt.Sprintf("serviceAccount:%s@%s.iam.gserviceaccount.com",
		kms.ServiceAccountIDFor(c.ClusterName), c.KMSConfig.ProjectID)
	if c.IAMVerifier != nil {
		if err := c.IAMVerifier(ctx, keyName, expectedMember); err != nil {
			return fmt.Errorf("KMS v2 precondition 4/9 failed: IAM binding check failed: %w", err)
		}
	} else {
		client, err := c.newClient(ctx)
		if err != nil {
			return fmt.Errorf("KMS v2 precondition 4/9 client error: %w", err)
		}
		bindings, err := client.KeyBindings(ctx, keyName)
		if err != nil {
			return fmt.Errorf("KMS v2 precondition 4/9 failed: reading IAM key bindings for %s: %w", keyName, err)
		}
		allowed := false
		for _, m := range bindings[kms.RoleEncrypterDecrypter] {
			if m == expectedMember {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("KMS v2 precondition 4/9 failed: IAM binding missing for %s with role %s on key %s",
				expectedMember, kms.RoleEncrypterDecrypter, keyName)
		}
	}

	// 5. Both bootstrap and wrapped leaf Secrets exist
	bootSecretName := assets.KMSPluginBootstrapSecretName(c.ClusterName)
	bootData, err := c.getSecret(ctx, bootSecretName)
	if err != nil {
		return fmt.Errorf("KMS v2 precondition 5/9 failed: bootstrap Secret %s not found in %s: %w", bootSecretName, c.Namespace, err)
	}
	wrapSecretName := assets.KMSPluginWrappedSecretName(c.ClusterName)
	wrapData, err := c.getSecret(ctx, wrapSecretName)
	if err != nil {
		return fmt.Errorf("KMS v2 precondition 5/9 failed: wrapped Secret %s not found in %s: %w", wrapSecretName, c.Namespace, err)
	}
	if _, ok := wrapData["leaf.enc"]; !ok {
		return fmt.Errorf("KMS v2 precondition 5/9 failed: wrapped Secret %s is missing key leaf.enc", wrapSecretName)
	}

	// 6. Bootstrap leaf has > 50% validity remaining (> 30m)
	bootCertB64, _ := bootData["tls.crt"].(string)
	if bootCertB64 == "" {
		return fmt.Errorf("KMS v2 precondition 6/9 failed: bootstrap Secret %s is missing tls.crt", bootSecretName)
	}
	bootCertBytes, err := base64.StdEncoding.DecodeString(bootCertB64)
	if err != nil {
		return fmt.Errorf("KMS v2 precondition 6/9 failed: bootstrap Secret %s has invalid base64 tls.crt: %w", bootSecretName, err)
	}
	block, _ := pem.Decode(bootCertBytes)
	if block == nil {
		return fmt.Errorf("KMS v2 precondition 6/9 failed: bootstrap Secret %s tls.crt is not PEM-encoded", bootSecretName)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("KMS v2 precondition 6/9 failed: parsing bootstrap leaf certificate: %w", err)
	}
	remaining := cert.NotAfter.Sub(time.Now())
	threshold := kms.BootstrapLeafValidity / 2 // 30 minutes
	if remaining <= threshold {
		return fmt.Errorf("KMS v2 precondition 6/9 failed: bootstrap leaf in %s has only %v validity remaining (needs > %v)",
			bootSecretName, remaining, threshold)
	}

	// 7. Credential config Secret exists (<cluster>-kms-credential-config)
	credSecretName := assets.KMSCredentialConfigSecretName(c.ClusterName)
	credData, err := c.getSecret(ctx, credSecretName)
	if err != nil {
		return fmt.Errorf("KMS v2 precondition 7/9 failed: credential config Secret %s not found in %s: %w", credSecretName, c.Namespace, err)
	}
	if _, ok := credData["credentials.json"]; !ok {
		return fmt.Errorf("KMS v2 precondition 7/9 failed: credential config Secret %s is missing key credentials.json", credSecretName)
	}
	if _, ok := credData["bootstrap-credentials.json"]; !ok {
		return fmt.Errorf("KMS v2 precondition 7/9 failed: credential config Secret %s is missing key bootstrap-credentials.json", credSecretName)
	}

	// 8. Plugin manifest Secret exists (<cluster>-kms-plugin-manifest)
	manifestSecretName := assets.KMSPluginManifestSecretName(c.ClusterName)
	manifestData, err := c.getSecret(ctx, manifestSecretName)
	if err != nil {
		return fmt.Errorf("KMS v2 precondition 8/9 failed: plugin manifest Secret %s not found in %s: %w", manifestSecretName, c.Namespace, err)
	}
	if _, ok := manifestData["kms-plugin.yaml"]; !ok {
		return fmt.Errorf("KMS v2 precondition 8/9 failed: plugin manifest Secret %s is missing key kms-plugin.yaml", manifestSecretName)
	}

	// 9. Plugin manifest image must be pinned to a published digest (not all-zero placeholder)
	manifestRaw, _ := manifestData["kms-plugin.yaml"].(string)
	manifestBytes, err := base64.StdEncoding.DecodeString(manifestRaw)
	if err != nil {
		manifestBytes = []byte(manifestRaw)
	}
	if err := validatePluginManifestDigest(manifestSecretName, manifestBytes); err != nil {
		return fmt.Errorf("KMS v2 precondition 9/9 failed: %w", err)
	}

	return nil
}

func validatePluginManifestDigest(secretName string, manifestYAML []byte) error {
	re := regexp.MustCompile(`(?m)^\s*image:\s*['"]?([^\s'"]+)['"]?`)
	matches := re.FindAllSubmatch(manifestYAML, -1)
	if len(matches) == 0 {
		return fmt.Errorf("plugin manifest Secret %s contains no image references", secretName)
	}

	hexRegex := regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	for _, m := range matches {
		img := string(m[1])
		parts := strings.Split(img, "@sha256:")
		if len(parts) != 2 {
			return fmt.Errorf("plugin manifest Secret %s has unpinned image %q (missing @sha256: digest)", secretName, img)
		}
		digest := parts[1]
		if strings.Trim(digest, "0") == "" {
			return fmt.Errorf("plugin manifest Secret %s has placeholder/unpinned image digest %q (digest must be published by CI)", secretName, digest)
		}
		if !hexRegex.MatchString(digest) {
			return fmt.Errorf("plugin manifest Secret %s has malformed image digest %q (expected 64 hex characters)", secretName, digest)
		}
	}
	return nil
}

func (c *KMSPreconditions) newClient(ctx context.Context) (*kms.Client, error) {
	return kms.New(ctx, c.KMSConfig.ProjectID, c.KMSConfig.ProjectNumber, "")
}

func (c *KMSPreconditions) getSecret(ctx context.Context, name string) (map[string]any, error) {
	if c.SecretGetter != nil {
		return c.SecretGetter(ctx, name)
	}
	args := []string{"get", "secret", name, "-n", c.Namespace, "-o", "json"}
	if c.Kubeconfig != "" {
		args = append([]string{"--kubeconfig", c.Kubeconfig}, args...)
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, string(out))
	}
	var res struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("decoding secret JSON: %w", err)
	}
	if res.Data == nil {
		return nil, fmt.Errorf("secret %s has no data field", name)
	}
	return res.Data, nil
}
