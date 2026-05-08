package components

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// GenerateAndInjectCA generates a CA certificate offline and injects it into Kubernetes
// This is the "Day-0 Deterministic Injection" pattern for breaking the Secret Zero chicken-and-egg problem.
//
// Enterprise Pattern:
// 1. CLI generates CA offline (no dependency on cluster state)
// 2. CLI injects platform-db-ca secret into platform-data namespace
// 3. CNPG uses this CA (configured via spec.certificates.serverCASecret)
// 4. Infisical uses the same CA for TLS verification
//
// This ensures both CNPG and Infisical start with TLS enabled on first boot.
func (i *Installer) GenerateAndInjectCA(ctx context.Context) error {
	// Load kubeconfig and create clientset
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	dataNamespace := constants.NamespaceData

	// Check if CA already exists (idempotency)
	_, err = clientset.CoreV1().Secrets(dataNamespace).Get(ctx, "platform-db-ca", metav1.GetOptions{})
	if err == nil {
		fmt.Println("[bootstrap-ca] ✓ platform-db-ca already exists (reusing existing CA)")
		return nil
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to check platform-db-ca secret: %w", err)
	}

	fmt.Println("[bootstrap-ca] Generating CA certificate offline...")

	// Generate RSA private key for CA
	caPrivateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("failed to generate CA private key: %w", err)
	}

	// Create CA certificate template
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "platform-db-ca",
			Organization: []string{"Zero-Ops Platform"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0), // 10 years validity
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	// Self-sign the CA certificate
	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caPrivateKey.PublicKey, caPrivateKey)
	if err != nil {
		return fmt.Errorf("failed to create CA certificate: %w", err)
	}

	// Encode CA certificate to PEM
	caCertPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caCertDER,
	})

	// Encode CA private key to PEM
	caKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(caPrivateKey),
	})

	// Create platform-db-ca secret in platform-data namespace
	// This secret will be used by:
	// 1. CNPG (via spec.certificates.serverCASecret) to generate server certificates
	// 2. Infisical (via DB_ROOT_CERT) to verify CNPG's TLS certificate
	caSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-db-ca",
			Namespace: dataNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
				"app.kubernetes.io/component":  "secret-zero",
				"cnpg.io/reload":               "true", // CNPG watches for this label
			},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"ca.crt": caCertPEM,
			"ca.key": caKeyPEM,
		},
	}

	_, err = clientset.CoreV1().Secrets(dataNamespace).Create(ctx, caSecret, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create platform-db-ca secret: %w", err)
	}

	fmt.Println("[bootstrap-ca] ✓ platform-db-ca created (10-year validity)")
	fmt.Println("[bootstrap-ca]   CNPG will use this CA for server certificates")
	fmt.Println("[bootstrap-ca]   Infisical will use this CA for TLS verification")

	return nil
}
