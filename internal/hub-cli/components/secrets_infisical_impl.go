package components

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func (i *Installer) InstallInfisicalAuth(ctx context.Context, clientID, clientSecret string) error {
	fmt.Println("[bootstrap] Creating infisical-auth secret...")

	// Load kubeconfig and create clientset
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create namespace if it doesn't exist
	namespace := constants.NamespaceOps
	_, err = clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		// Namespace doesn't exist, create it
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		_, err = clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create namespace %s: %w", namespace, err)
		}
		fmt.Printf("[bootstrap] Created namespace %s\n", namespace)
	}

	// Create the secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "infisical-auth",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
				"app.kubernetes.io/component":  "secret-zero",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"client-id":     clientID,
			"client-secret": clientSecret,
		},
	}

	// Try to create, if exists then update
	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		// Secret might already exist, try to update
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create or update infisical-auth secret: %w", err)
		}
		fmt.Println("[bootstrap] ✓ infisical-auth secret updated")
	} else {
		fmt.Println("[bootstrap] ✓ infisical-auth secret created")
	}

	return nil
}

// FixArgoCDGitHubAuth creates the ArgoCD GitHub repository secret
// This is Secret Zero for ArgoCD - it fixes the chicken-and-egg problem.
// CRITICAL: This secret MUST be injected via client-go, NEVER stored in Git.
//
// Chicken-and-Egg Problem:
// - ArgoCD needs GitHub auth to pull ESO manifests from Git
// - ESO manages GitHub credentials declaratively
// - But ESO manifests are in Git, which ArgoCD can't pull without auth
//
// Solution:
// 1. This method injects temporary GitHub PAT using client-go
// 2. ArgoCD can now pull ESO manifests (waves 4-6)
// 3. ESO deploys and takes over credential management
// 4. ESO replaces this bootstrap secret with Infisical-backed secret
// 5. Future credential rotations happen via Infisical + ESO (GitOps)
//
// Production Workflow:
// 1. Developer runs: hub platform-core configure-eso --github-token=<pat>
// 2. This method uses client-go to inject the secret with proper labels
// 3. ArgoCD auto-discovers the secret (via argocd.argoproj.io/secret-type label)
// 4. ArgoCD syncs ESO manifests from GitHub
// 5. ESO takes over and replaces this secret with Infisical-backed version

func (i *Installer) InstallInfisicalSecrets(ctx context.Context) (bool, error) {
	// Load kubeconfig and create clientset
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return false, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return false, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Namespace separation: Infisical runs in security namespace, Redis in data namespace
	securityNamespace := constants.NamespaceSecurity
	dataNamespace := constants.NamespaceData

	// Check if secrets exist in their respective namespaces
	infSecret, err1 := clientset.CoreV1().Secrets(securityNamespace).Get(ctx, "infisical-secrets", metav1.GetOptions{})
	redisSecret, err2 := clientset.CoreV1().Secrets(dataNamespace).Get(ctx, "infisical-redis-credentials", metav1.GetOptions{})

	secretsExist := err1 == nil && err2 == nil &&
		len(infSecret.Data["ENCRYPTION_KEY"]) > 0 &&
		len(infSecret.Data["REDIS_URL"]) > 0 &&
		len(redisSecret.Data["password"]) > 0

	if secretsExist {
		fmt.Println("[bootstrap-secrets] Infisical & Redis secrets exist. Updating TLS configuration...")
	} else {
		fmt.Println("[bootstrap-secrets] Generating initial Infisical & Redis secrets...")
	}

	// Generate secure random keys - MUST be exactly 32 characters for AES-256
	// If secrets exist, reuse existing keys to avoid breaking encryption
	var encryptionKey, authSecret, redisPassword string

	if secretsExist {
		// Reuse existing keys to maintain data integrity
		encryptionKey = string(infSecret.Data["ENCRYPTION_KEY"])
		authSecret = string(infSecret.Data["AUTH_SECRET"])

		// Extract Redis password from URL
		redisURL := string(infSecret.Data["REDIS_URL"])
		// Parse: redis://:PASSWORD@redis-master.platform-data.svc:6379
		if idx := strings.Index(redisURL, "redis://:"); idx >= 0 {
			start := idx + len("redis://:")
			if end := strings.Index(redisURL[start:], "@"); end >= 0 {
				redisPassword = redisURL[start : start+end]
			}
		}

		if redisPassword == "" {
			redisPassword = string(redisSecret.Data["password"])
		}

		fmt.Println("[bootstrap-secrets] Reusing existing ENCRYPTION_KEY and AUTH_SECRET")
	} else {
		// Generate new keys
		var err error
		encryptionKey, err = generateSecurePassword(32)
		if err != nil {
			return false, fmt.Errorf("failed to generate encryption key: %w", err)
		}

		authSecret, err = generateSecurePassword(32)
		if err != nil {
			return false, fmt.Errorf("failed to generate auth secret: %w", err)
		}

		// Generate Redis password (use hex encoding to avoid URL-unsafe characters)
		redisBytes := make([]byte, 32)
		if _, err := rand.Read(redisBytes); err != nil {
			return false, fmt.Errorf("failed to generate redis password: %w", err)
		}
		redisPassword = hex.EncodeToString(redisBytes)[:32]

		fmt.Println("[bootstrap-secrets] Generated new ENCRYPTION_KEY and AUTH_SECRET")
	}
	redisURL := fmt.Sprintf("redis://:%s@redis-master.platform-data.svc:6379", redisPassword)

	// BOOTSTRAP STRATEGY: Day-0 Deterministic CA Injection
	// The CA is provisioned by cert-manager per ADR-035.
	// The platform-db-ca Certificate resource (in platform-data namespace)
	// must be synced by ArgoCD before this method runs.
	
	// Read CA certificate from CLI-generated secret
	caSecret, err := clientset.CoreV1().Secrets(dataNamespace).Get(ctx, "platform-db-ca", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to read platform-db-ca secret: %w\nEnsure cert-manager Certificate resource for platform-db-ca is synced by ArgoCD", err)
	}

	caCert, ok := caSecret.Data["ca.crt"]
	if !ok {
		return false, fmt.Errorf("ca.crt not found in platform-db-ca secret")
	}

	// Base64 encode the CA certificate for Infisical
	dbRootCert := base64.StdEncoding.EncodeToString(caCert)
	
	// Create the master infisical-secrets secret with TLS enabled from Day 0
	// This secret is consumed via envFrom in the Helm chart
	secretData := map[string]string{
		"ENCRYPTION_KEY": encryptionKey,
		"AUTH_SECRET":    authSecret,
		"REDIS_URL":      redisURL,
		"DB_ROOT_CERT":   dbRootCert, // TLS enabled from Day 0
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "infisical-secrets",
			Namespace: securityNamespace, // Infisical runs in platform-security
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
				"app.kubernetes.io/component":  "secret-zero",
			},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: secretData,
	}

	// Try to create, if exists then update
	_, err = clientset.CoreV1().Secrets(securityNamespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		// Secret might already exist, try to update
		_, err = clientset.CoreV1().Secrets(securityNamespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to create or update infisical-secrets: %w", err)
		}
		fmt.Println("[bootstrap-secrets] ✓ infisical-secrets updated (ENCRYPTION_KEY, AUTH_SECRET, REDIS_URL)")
	} else {
		fmt.Println("[bootstrap-secrets] ✓ infisical-secrets created (ENCRYPTION_KEY, AUTH_SECRET, REDIS_URL)")
	}

	// Create Redis credentials secret (for standalone Redis pod only)
	redisSecretObj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "infisical-redis-credentials",
			Namespace: dataNamespace, // Redis runs in platform-data
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
				"app.kubernetes.io/component":  "secret-zero",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"password": redisPassword,
		},
	}

	_, err = clientset.CoreV1().Secrets(dataNamespace).Create(ctx, redisSecretObj, metav1.CreateOptions{})
	if err != nil {
		_, err = clientset.CoreV1().Secrets(dataNamespace).Update(ctx, redisSecretObj, metav1.UpdateOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to create or update infisical-redis-credentials: %w", err)
		}
		fmt.Println("[bootstrap-secrets] ✓ infisical-redis-credentials updated")
	} else {
		fmt.Println("[bootstrap-secrets] ✓ infisical-redis-credentials created")
	}

	return true, nil
}

func (i *Installer) UpgradeInfisicalTLS(ctx context.Context) error {
	// Load kubeconfig and create clientset
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	namespace := constants.NamespaceData

	fmt.Println("\n[1/3] Reading platform-db-ca secret...")

	// Read CA certificate from CNPG-managed secret
	caSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "platform-db-ca", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to read platform-db-ca secret: %w\nEnsure database is deployed: kubectl get cluster platform-db -n platform-data", err)
	}

	caCert, ok := caSecret.Data["ca.crt"]
	if !ok {
		return fmt.Errorf("ca.crt not found in platform-db-ca secret")
	}

	// Base64 encode the CA certificate for Infisical
	dbRootCert := base64.StdEncoding.EncodeToString(caCert)
	fmt.Println("✓ Read CA certificate from platform-db-ca")

	fmt.Println("\n[2/3] Updating infisical-secrets with DB_ROOT_CERT...")

	// Read existing infisical-secrets
	infSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "infisical-secrets", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to read infisical-secrets: %w", err)
	}

	// Check if already has DB_ROOT_CERT
	if existingCert, ok := infSecret.Data["DB_ROOT_CERT"]; ok {
		if string(existingCert) == dbRootCert {
			fmt.Println("✓ DB_ROOT_CERT already configured with correct value")
			fmt.Println("   Infisical is already using TLS connection")
			return nil
		}
		fmt.Println("⚠️  DB_ROOT_CERT exists but differs, updating...")
	}

	// Add DB_ROOT_CERT to existing secret
	if infSecret.Data == nil {
		infSecret.Data = make(map[string][]byte)
	}
	infSecret.Data["DB_ROOT_CERT"] = []byte(dbRootCert)

	// Update the secret
	_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, infSecret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update infisical-secrets: %w", err)
	}
	fmt.Println("✓ Added DB_ROOT_CERT to infisical-secrets")

	fmt.Println("\n[3/3] Restarting Infisical pods to apply TLS configuration...")

	// Restart Infisical pods by deleting them (Deployment will recreate)
	pods, err := clientset.CoreV1().Pods(constants.NamespaceSecurity).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=infisical",
	})
	if err != nil {
		return fmt.Errorf("failed to list Infisical pods: %w", err)
	}

	if len(pods.Items) == 0 {
		fmt.Println("⚠️  No Infisical pods found, skipping restart")
		fmt.Println("   Pods will use TLS when they start")
		return nil
	}

	for _, pod := range pods.Items {
		err = clientset.CoreV1().Pods(constants.NamespaceSecurity).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			fmt.Printf("⚠️  Failed to delete pod %s: %v\n", pod.Name, err)
		} else {
			fmt.Printf("✓ Deleted pod %s (will be recreated with TLS)\n", pod.Name)
		}
	}

	// Wait for pods to be recreated
	fmt.Println("\nWaiting for Infisical pods to be ready...")
	for i := 0; i < 60; i++ {
		time.Sleep(2 * time.Second)

		pods, err := clientset.CoreV1().Pods(constants.NamespaceSecurity).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=infisical",
		})
		if err != nil {
			continue
		}

		allReady := true
		for _, pod := range pods.Items {
			if pod.Status.Phase != corev1.PodRunning {
				allReady = false
				break
			}
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status != corev1.ConditionTrue {
					allReady = false
					break
				}
			}
		}

		if allReady && len(pods.Items) > 0 {
			fmt.Println("✓ Infisical pods are ready")
			return nil
		}
	}

	fmt.Println("⚠️  Timeout waiting for Infisical pods to be ready")
	fmt.Println("   Check pod status: kubectl get pods -n platform-security")
	return nil
}
