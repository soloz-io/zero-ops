package secrets

import (
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

// GenerateSecurePassword generates a cryptographically secure 32-character hex password
func GenerateSecurePassword() (string, error) {
	bytes := make([]byte, 16) // 16 bytes = 32 hex characters
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// GenerateSelfSignedCA generates a self-signed CA certificate for the database
// Uses 4096-bit RSA with 10-year validity as per Requirement 4.5
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

// GenerateInfisicalSecrets creates the infisical-secrets Kubernetes secret
// Requirement 4.1, 4.2, 4.3, 4.4, 4.8, 4.9
func GenerateInfisicalSecrets(namespace string, caCert []byte, owner metav1.OwnerReference) (*corev1.Secret, error) {
	encryptionKey, err := GenerateSecurePassword()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ENCRYPTION_KEY: %w", err)
	}

	authSecret, err := GenerateSecurePassword()
	if err != nil {
		return nil, fmt.Errorf("failed to generate AUTH_SECRET: %w", err)
	}

	redisPassword, err := GenerateSecurePassword()
	if err != nil {
		return nil, fmt.Errorf("failed to generate Redis password: %w", err)
	}

	// Construct REDIS_URL
	redisURL := fmt.Sprintf("redis://:%s@redis-master.%s.svc:6379", redisPassword, namespace)

	// Base64-encode CA certificate
	dbRootCert := base64.StdEncoding.EncodeToString(caCert)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "infisical-secrets",
			Namespace:       namespace,
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

	return secret, nil
}

// GeneratePlatformDBApp creates the platform-db-app BasicAuth secret
// Requirement 4.10, 4.11
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
// Requirement 4.12, 4.13
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
				"ops.zero-ops.io/db-credentials": "true",
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

// GenerateInfisicalPostgresConnection creates the infisical-postgres-connection secret
// Requirement 4.14
func GenerateInfisicalPostgresConnection(namespace, dbHost, dbName, username, password string, owner metav1.OwnerReference) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "infisical-postgres-connection",
			Namespace:       namespace,
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

// GenerateOryDBCredentials creates database credentials for Ory services (Hydra, Kratos, Keto)
// Requirement 4.15-4.20
func GenerateOryDBCredentials(service, namespace string, owner metav1.OwnerReference) (*corev1.Secret, error) {
	password, err := GenerateSecurePassword()
	if err != nil {
		return nil, fmt.Errorf("failed to generate %s-db-credentials password: %w", service, err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("%s-db-credentials", service),
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{owner},
			Labels: map[string]string{
				"ops.zero-ops.io/db-credentials": "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"username": service,
			"password": password,
		},
	}

	return secret, nil
}

// GeneratePlatformDBCA creates the platform-db-ca secret with self-signed CA
// Requirement 4.5, 4.6
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

// SecretZeroResult contains all generated Secret Zero resources
type SecretZeroResult struct {
	InfisicalSecrets            *corev1.Secret
	PlatformDBApp               *corev1.Secret
	InfisicalDBCredentials      *corev1.Secret
	InfisicalPostgresConnection *corev1.Secret
	HydraDBCredentials          *corev1.Secret
	KratosDBCredentials         *corev1.Secret
	KetoDBCredentials           *corev1.Secret
	PlatformDBCA                *corev1.Secret
}

// GenerateSecretZero orchestrates the generation of all Secret Zero resources
// Implements idempotency by reusing existing passwords (Requirement 4.21)
// Sets ownerReferences on all secrets (Requirement 4.22)
func GenerateSecretZero(namespace, dbHost string, owner metav1.OwnerReference, existingSecrets map[string]*corev1.Secret) (*SecretZeroResult, error) {
	result := &SecretZeroResult{}

	// Generate or reuse platform-db-ca
	if existing, ok := existingSecrets["platform-db-ca"]; ok {
		// Reuse existing CA certificate - return nil to skip creation
		result.PlatformDBCA = nil
		caCert := existing.Data["ca.crt"]

		// Generate infisical-secrets with existing CA
		infisicalSecrets, err := generateOrReuseInfisicalSecrets(namespace, caCert, owner, existingSecrets["infisical-secrets"])
		if err != nil {
			return nil, err
		}
		result.InfisicalSecrets = infisicalSecrets
	} else {
		// Generate new CA certificate
		platformDBCA, caCert, err := GeneratePlatformDBCA(namespace, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to generate platform-db-ca: %w", err)
		}
		result.PlatformDBCA = platformDBCA

		// Generate infisical-secrets with new CA
		infisicalSecrets, err := GenerateInfisicalSecrets(namespace, caCert, owner)
		if err != nil {
			return nil, err
		}
		result.InfisicalSecrets = infisicalSecrets
	}

	// Generate or reuse platform-db-app
	platformDBApp, err := generateOrReusePlatformDBApp(namespace, owner, existingSecrets["platform-db-app"])
	if err != nil {
		return nil, err
	}
	result.PlatformDBApp = platformDBApp

	// Generate or reuse infisical-db-credentials
	infisicalDBCreds, err := generateOrReuseInfisicalDBCredentials(namespace, owner, existingSecrets["infisical-db-credentials"])
	if err != nil {
		return nil, err
	}
	result.InfisicalDBCredentials = infisicalDBCreds

	// Generate infisical-postgres-connection using infisical-db-credentials
	// If infisicalDBCreds is nil (already exists), use existing secret data
	var username, password string
	if infisicalDBCreds != nil {
		username = string(infisicalDBCreds.Data["username"])
		password = string(infisicalDBCreds.Data["password"])
	} else if existing, ok := existingSecrets["infisical-db-credentials"]; ok {
		username = string(existing.Data["username"])
		password = string(existing.Data["password"])
	} else {
		return nil, fmt.Errorf("infisical-db-credentials not found")
	}

	infisicalPostgresConn, err := GenerateInfisicalPostgresConnection(
		namespace,
		dbHost,
		"infisical",
		username,
		password,
		owner,
	)
	if err != nil {
		return nil, err
	}
	result.InfisicalPostgresConnection = infisicalPostgresConn

	// Generate or reuse Ory database credentials
	hydraDBCreds, err := generateOrReuseOryDBCredentials("hydra", namespace, owner, existingSecrets["hydra-db-credentials"])
	if err != nil {
		return nil, err
	}
	result.HydraDBCredentials = hydraDBCreds

	kratosDBCreds, err := generateOrReuseOryDBCredentials("kratos", namespace, owner, existingSecrets["kratos-db-credentials"])
	if err != nil {
		return nil, err
	}
	result.KratosDBCredentials = kratosDBCreds

	ketoDBCreds, err := generateOrReuseOryDBCredentials("keto", namespace, owner, existingSecrets["keto-db-credentials"])
	if err != nil {
		return nil, err
	}
	result.KetoDBCredentials = ketoDBCreds

	return result, nil
}

// generateOrReuseInfisicalSecrets implements idempotency for infisical-secrets
func generateOrReuseInfisicalSecrets(namespace string, caCert []byte, owner metav1.OwnerReference, existing *corev1.Secret) (*corev1.Secret, error) {
	if existing != nil {
		// Return nil to indicate secret already exists (don't try to create)
		return nil, nil
	}
	return GenerateInfisicalSecrets(namespace, caCert, owner)
}

// generateOrReusePlatformDBApp implements idempotency for platform-db-app
func generateOrReusePlatformDBApp(namespace string, owner metav1.OwnerReference, existing *corev1.Secret) (*corev1.Secret, error) {
	if existing != nil {
		// Return nil to indicate secret already exists (don't try to create)
		return nil, nil
	}
	return GeneratePlatformDBApp(namespace, owner)
}

// generateOrReuseInfisicalDBCredentials implements idempotency for infisical-db-credentials
func generateOrReuseInfisicalDBCredentials(namespace string, owner metav1.OwnerReference, existing *corev1.Secret) (*corev1.Secret, error) {
	if existing != nil {
		// Return nil to indicate secret already exists (don't try to create)
		return nil, nil
	}
	return GenerateInfisicalDBCredentials(namespace, owner)
}

// generateOrReuseOryDBCredentials implements idempotency for Ory database credentials
func generateOrReuseOryDBCredentials(service, namespace string, owner metav1.OwnerReference, existing *corev1.Secret) (*corev1.Secret, error) {
	if existing != nil {
		// Return nil to indicate secret already exists (don't try to create)
		return nil, nil
	}
	return GenerateOryDBCredentials(service, namespace, owner)
}
