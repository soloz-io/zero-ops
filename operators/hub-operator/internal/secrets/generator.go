package secrets

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ============================================================================
// CRYPTOGRAPHIC UTILITIES
// ============================================================================

// GenerateSecurePassword generates a cryptographically secure 32-character hex password
// Uses hex encoding for maximum compatibility (no special characters)
func GenerateSecurePassword() (string, error) {
	bytes := make([]byte, 16) // 16 bytes = 32 hex characters
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// GenerateSecurePasswordWithCharset generates a cryptographically secure password
// using a custom character set. Avoids modulo bias by rejecting values that
// would cause non-uniform distribution.
func GenerateSecurePasswordWithCharset(length int, charset string) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("password length must be positive")
	}
	if len(charset) == 0 {
		return "", fmt.Errorf("charset cannot be empty")
	}

	password := make([]byte, length)
	charsetLen := len(charset)
	
	// Calculate the maximum valid random value to avoid modulo bias
	// We want: randomValue % charsetLen to be uniformly distributed
	maxValid := 256 - (256 % charsetLen)
	
	for i := range password {
		for {
			b := make([]byte, 1)
			if _, err := rand.Read(b); err != nil {
				return "", fmt.Errorf("failed to generate random bytes: %w", err)
			}
			randomValue := int(b[0])
			
			// Reject values that would cause modulo bias
			if randomValue < maxValid {
				password[i] = charset[randomValue%charsetLen]
				break
			}
			// If randomValue >= maxValid, retry to avoid bias
		}
	}

	return string(password), nil
}

// GenerateSelfSignedCA generates a self-signed CA certificate for the database
// Uses 4096-bit RSA with 10-year validity
func GenerateSelfSignedCA() (certPEM, keyPEM []byte, err error) {
	// Generate 4096-bit RSA private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	// Create certificate template
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Zero-Ops Platform"},
			CommonName:   "Platform Database CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	// Create self-signed certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode certificate to PEM
	certPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key to PEM
	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	return certPEM, keyPEM, nil
}

// ============================================================================
// BOOTSTRAP SECRET GENERATORS
// These secrets are required for infrastructure to start (CNPG, Infisical, Redis)
// Application secrets are created by ESO from Infisical
// ============================================================================

// GeneratePlatformDBCA creates the platform-db-ca secret with self-signed CA
// Required for CNPG TLS bootstrap
func GeneratePlatformDBCA(namespace string, owner metav1.OwnerReference) (*corev1.Secret, []byte, error) {
	certPEM, keyPEM, err := GenerateSelfSignedCA()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate self-signed CA: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "platform-db-ca",
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"ca.crt":  certPEM,
			"ca.key":  keyPEM,
			"tls.crt": certPEM, // CNPG expects tls.crt
			"tls.key": keyPEM,  // CNPG expects tls.key
		},
	}

	return secret, certPEM, nil
}

// GeneratePlatformDBApp creates the platform-db-app BasicAuth secret
// Required for CNPG bootstrap (superuser)
func GeneratePlatformDBApp(namespace string, owner metav1.OwnerReference) (*corev1.Secret, error) {
	password, err := GenerateSecurePassword()
	if err != nil {
		return nil, fmt.Errorf("failed to generate platform-db-app password: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "platform-db-app",
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Type: corev1.SecretTypeBasicAuth,
		StringData: map[string]string{
			"username": "app",
			"password": password,
		},
	}

	return secret, nil
}

// GenerateInfisicalDBCredentials creates the infisical-db-credentials secret
// Required for Infisical bootstrap
func GenerateInfisicalDBCredentials(namespace string, owner metav1.OwnerReference) (*corev1.Secret, error) {
	password, err := GenerateSecurePassword()
	if err != nil {
		return nil, fmt.Errorf("failed to generate infisical-db-credentials password: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "infisical-db-credentials",
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{owner},
			Labels: map[string]string{
				"ops.nutgraf.in/db-credentials": "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"username": "infisical",
			"password": password,
		},
	}

	return secret, nil
}

// AWSSecretsManagerClient interface for backup/restore operations
// This allows for dependency injection and testing
type AWSSecretsManagerClient interface {
	BackupMasterKeys(ctx context.Context, clusterID string, keys interface{}) error
	RestoreMasterKeys(ctx context.Context, clusterID string) (interface{}, error)
}

// GenerateInfisicalSecretsResult contains both infisical-secrets and the extracted Redis password
type GenerateInfisicalSecretsResult struct {
	InfisicalSecrets *corev1.Secret
	RedisPassword    string
}

// GenerateInfisicalSecrets creates the infisical-secrets Kubernetes secret with backup/restore logic
// Required for Infisical bootstrap (encryption keys, Redis URL, DB cert)
// Returns both the secret and the Redis password for creating infisical-redis-credentials
// NOTE: This secret MUST be created in hub-platform-security namespace where Infisical pods run
//
// Implements REQ-7: Backup/restore logic with bootstrap detection
// - First-time bootstrap: Generate new keys, validate, backup to AWS
// - Restore scenario: Restore from AWS, validate, reconstruct secret
// - Error scenario: Fail if backup missing and cluster already bootstrapped
//
// Implements REQ-7.1: Secret reconstruction logic
// - Reads existing Redis password from infisical-redis-credentials (if available)
// - Reads CA certificate from platform-db-ca (passed as parameter)
// - Reconstructs complete infisical-secrets with all 4 fields
func GenerateInfisicalSecrets(ctx context.Context, securityNamespace, dataNamespace string, caCert []byte, owner metav1.OwnerReference, isFirstTime bool, clusterID string, awsClient AWSSecretsManagerClient, existingRedisPassword string) (*GenerateInfisicalSecretsResult, error) {
	var encryptionKey, authSecret string
	var err error

	// Try to restore from AWS Secrets Manager first
	var backupData interface{}
	if awsClient != nil {
		backupData, err = awsClient.RestoreMasterKeys(ctx, clusterID)
		if err != nil {
			return nil, fmt.Errorf("failed to restore from AWS: %w", err)
		}
	}

	if backupData != nil {
		// Backup exists - restore both keys
		// Type assert to extract keys from backup (AWS client returns map[string]interface{})
		if backupMap, ok := backupData.(map[string]interface{}); ok {
			if ek, ok := backupMap["encryptionKey"].(string); ok {
				encryptionKey = ek
			} else {
				return nil, fmt.Errorf("backup data missing encryptionKey field")
			}
			if as, ok := backupMap["authSecret"].(string); ok {
				authSecret = as
			} else {
				return nil, fmt.Errorf("backup data missing authSecret field")
			}
		} else {
			return nil, fmt.Errorf("invalid backup data format")
		}

		// REQ-7.2: Validate restored keys before use
		if err := ValidateEncryptionKey(encryptionKey); err != nil {
			return nil, fmt.Errorf("restored ENCRYPTION_KEY failed validation: %w", err)
		}
		if err := ValidateAuthSecret(authSecret); err != nil {
			return nil, fmt.Errorf("restored AUTH_SECRET failed validation: %w", err)
		}
	} else if isFirstTime {
		// First-time bootstrap - generate new keys
		encryptionKey, err = GenerateSecurePassword()
		if err != nil {
			return nil, fmt.Errorf("failed to generate ENCRYPTION_KEY: %w", err)
		}

		authSecret, err = GenerateSecurePassword()
		if err != nil {
			return nil, fmt.Errorf("failed to generate AUTH_SECRET: %w", err)
		}

		// REQ-7.2: Validate generated keys
		if err := ValidateEncryptionKey(encryptionKey); err != nil {
			return nil, fmt.Errorf("generated ENCRYPTION_KEY failed validation: %w", err)
		}
		if err := ValidateAuthSecret(authSecret); err != nil {
			return nil, fmt.Errorf("generated AUTH_SECRET failed validation: %w", err)
		}

		// Backup immediately if AWS client is available
		if awsClient != nil {
			// Create backup data structure
			backupKeys := map[string]interface{}{
				"encryptionKey": encryptionKey,
				"authSecret":    authSecret,
				"createdAt":     time.Now().UTC(),
				"clusterId":     clusterID,
				"version":       "1",
			}
			if err := awsClient.BackupMasterKeys(ctx, clusterID, backupKeys); err != nil {
				return nil, fmt.Errorf("failed to backup master keys to AWS, cannot proceed: %w", err)
			}
		}
	} else {
		// NOT first-time AND backup missing - CRITICAL ERROR
		return nil, fmt.Errorf("ENCRYPTION_KEY backup not found in AWS and cluster already bootstrapped. Manual intervention required")
	}

	// REQ-7.1: Reconstruct Redis password from existing secret or generate new
	// During restore scenario, preserve existing Redis password to avoid breaking connections
	var redisPassword string
	if existingRedisPassword != "" {
		// Reuse existing Redis password (restore scenario)
		redisPassword = existingRedisPassword
	} else {
		// Generate new Redis password (first-time bootstrap)
		redisPassword, err = GenerateSecurePassword()
		if err != nil {
			return nil, fmt.Errorf("failed to generate Redis password: %w", err)
		}
	}

	// Construct REDIS_URL - Redis runs in data namespace
	redisURL := fmt.Sprintf("redis://:%s@redis-master.%s.svc:6379", redisPassword, dataNamespace)

	// Base64-encode CA certificate
	dbRootCert := base64.StdEncoding.EncodeToString(caCert)

	// Create secret in security namespace where Infisical pods run
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "infisical-secrets",
			Namespace:       securityNamespace,
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"ENCRYPTION_KEY": encryptionKey,
			"AUTH_SECRET":    authSecret,
			"REDIS_URL":      redisURL,
			"DB_ROOT_CERT":   dbRootCert,
		},
	}

	return &GenerateInfisicalSecretsResult{
		InfisicalSecrets: secret,
		RedisPassword:    redisPassword,
	}, nil
}

// GenerateInfisicalRedisCredentials creates the infisical-redis-credentials secret
// Required for Redis bootstrap
func GenerateInfisicalRedisCredentials(namespace, redisPassword string, owner metav1.OwnerReference) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "infisical-redis-credentials",
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{owner},
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "hub-operator",
				"app.kubernetes.io/component":  "bootstrap-secret",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"password": redisPassword,
		},
	}

	return secret, nil
}

// GenerateInfisicalPostgresConnection creates the infisical-postgres-connection secret
// Required for Infisical to connect to PostgreSQL
// NOTE: This secret MUST be created in hub-platform-security namespace where Infisical pods run
func GenerateInfisicalPostgresConnection(securityNamespace, dbHost, dbName, username, password string, owner metav1.OwnerReference) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "infisical-postgres-connection",
			Namespace:       securityNamespace,
			OwnerReferences: []metav1.OwnerReference{owner},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"DB_HOST":     dbHost,
			"DB_PORT":     "5432",
			"DB_USER":     username,
			"DB_PASSWORD": password,
			"DB_NAME":     dbName,
			"DB_SSL_MODE": "require",
		},
	}

	return secret, nil
}

// ============================================================================
// BOOTSTRAP SECRETS ORCHESTRATION
// ============================================================================

// BootstrapSecretsResult contains bootstrap secrets required for infrastructure to start
// These secrets break circular dependencies (CNPG needs platform-db-app before Infisical can start)
type BootstrapSecretsResult struct {
	PlatformDBCA                *corev1.Secret
	PlatformDBApp               *corev1.Secret
	InfisicalDBCredentials      *corev1.Secret
	InfisicalSecrets            *corev1.Secret
	InfisicalRedisCredentials   *corev1.Secret
	InfisicalPostgresConnection *corev1.Secret
}

// GenerateBootstrapSecrets orchestrates the generation of bootstrap secrets only
// Bootstrap secrets are required for infrastructure components to start (CNPG, Infisical, Redis)
// Application secrets (control-plane-db-credentials, hub-db-credentials, etc.) are created by ESO
// Implements idempotency by reusing existing passwords
// Sets ownerReferences on all secrets
//
// REQ-7: Implements backup/restore logic for Infisical master keys
// - Detects first-time bootstrap using HubEnvironment.Status.Conditions
// - Restores keys from AWS if backup exists
// - Generates and backs up new keys on first-time bootstrap
// - Fails gracefully if backup missing and cluster already bootstrapped
func GenerateBootstrapSecrets(ctx context.Context, dataNamespace, securityNamespace, dbHost string, owner metav1.OwnerReference, existingSecrets map[string]*corev1.Secret, isFirstTime bool, clusterID string, awsClient AWSSecretsManagerClient) (*BootstrapSecretsResult, error) {
	result := &BootstrapSecretsResult{}

	// Step 1: Generate or reuse platform-db-ca (in data namespace)
	var caCert []byte
	if existing, ok := existingSecrets["platform-db-ca"]; ok {
		// Reuse existing CA certificate
		result.PlatformDBCA = nil
		caCert = existing.Data["ca.crt"]
	} else {
		// Generate new CA certificate
		platformDBCA, cert, err := GeneratePlatformDBCA(dataNamespace, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to generate platform-db-ca: %w", err)
		}
		result.PlatformDBCA = platformDBCA
		caCert = cert
	}

	// Step 2: Generate or reuse platform-db-app (in data namespace)
	if _, ok := existingSecrets["platform-db-app"]; ok {
		result.PlatformDBApp = nil
	} else {
		platformDBApp, err := GeneratePlatformDBApp(dataNamespace, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to generate platform-db-app: %w", err)
		}
		result.PlatformDBApp = platformDBApp
	}

	// Step 3: Generate or reuse infisical-db-credentials (in data namespace)
	var infisicalUsername, infisicalPassword string
	if existing, ok := existingSecrets["infisical-db-credentials"]; ok {
		result.InfisicalDBCredentials = nil
		infisicalUsername = string(existing.Data["username"])
		infisicalPassword = string(existing.Data["password"])
	} else {
		infisicalDBCreds, err := GenerateInfisicalDBCredentials(dataNamespace, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to generate infisical-db-credentials: %w", err)
		}
		result.InfisicalDBCredentials = infisicalDBCreds
		infisicalUsername = infisicalDBCreds.StringData["username"]
		infisicalPassword = infisicalDBCreds.StringData["password"]
	}

	// Step 4: Generate or restore infisical-secrets with backup/restore logic (in security namespace)
	var redisPassword string
	if _, ok := existingSecrets["infisical-secrets"]; ok {
		// Secret exists - check if we need to restore from AWS
		// This handles the case where secret was deleted and needs restoration
		result.InfisicalSecrets = nil
		
		// Reuse existing redis credentials if available
		if existing, ok := existingSecrets["infisical-redis-credentials"]; ok {
			redisPassword = string(existing.Data["password"])
			result.InfisicalRedisCredentials = nil
		}
	} else {
		// Secret doesn't exist - use backup/restore logic
		// REQ-7.1: Read existing Redis password to preserve during restore
		var existingRedisPassword string
		if existing, ok := existingSecrets["infisical-redis-credentials"]; ok {
			existingRedisPassword = string(existing.Data["password"])
		}
		
		infisicalResult, err := GenerateInfisicalSecrets(ctx, securityNamespace, dataNamespace, caCert, owner, isFirstTime, clusterID, awsClient, existingRedisPassword)
		if err != nil {
			return nil, fmt.Errorf("failed to generate infisical-secrets: %w", err)
		}
		result.InfisicalSecrets = infisicalResult.InfisicalSecrets
		redisPassword = infisicalResult.RedisPassword

		// Generate infisical-redis-credentials using the Redis password (in data namespace)
		// Only create if it doesn't already exist
		if _, ok := existingSecrets["infisical-redis-credentials"]; !ok {
			redisSecret, err := GenerateInfisicalRedisCredentials(dataNamespace, redisPassword, owner)
			if err != nil {
				return nil, fmt.Errorf("failed to generate infisical-redis-credentials: %w", err)
			}
			result.InfisicalRedisCredentials = redisSecret
		}
	}

	// Step 5: Generate infisical-postgres-connection (in security namespace)
	// Always generate this as it depends on infisical-db-credentials
	infisicalPostgresConn, err := GenerateInfisicalPostgresConnection(
		securityNamespace,
		dbHost,
		"infisical",
		infisicalUsername,
		infisicalPassword,
		owner,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate infisical-postgres-connection: %w", err)
	}
	result.InfisicalPostgresConnection = infisicalPostgresConn

	return result, nil
}
