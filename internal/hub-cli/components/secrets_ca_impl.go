package components

import (
	"context"
	"fmt"

	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// GenerateAndInjectCA checks for an existing platform-db-ca secret and returns
// immediately. CA certificate generation is owned by cert-manager per ADR-035.
// The CLI must not generate, sign, or handle X.509 private key material.
//
// For CNPG TLS bootstrapping, use cert-manager's Certificate resource:
//
//	apiVersion: cert-manager.io/v1
//	kind: Certificate
//	metadata:
//	  name: platform-db-ca
//	  namespace: platform-data
//	spec:
//	  isCA: true
//	  commonName: platform-db-ca
//	  secretName: platform-db-ca
func (i *Installer) GenerateAndInjectCA(ctx context.Context) error {
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	dataNamespace := constants.NamespaceData

	_, err = clientset.CoreV1().Secrets(dataNamespace).Get(ctx, "platform-db-ca", metav1.GetOptions{})
	if err == nil {
		fmt.Println("[bootstrap-ca] ✓ platform-db-ca already exists")
		return nil
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to check platform-db-ca secret: %w", err)
	}

	fmt.Println("[bootstrap-ca] platform-db-ca will be provisioned by cert-manager via ArgoCD")
	fmt.Println("[bootstrap-ca] Ensure a cert-manager Certificate resource exists for platform-db-ca in platform-data")

	return nil
}
