package components

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/soloz-io/zero-ops/internal/hub/constants"
	"github.com/soloz-io/zero-ops/internal/hub/infisical"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// InstallInfisicalAuth creates the infisical-auth secret for External Secrets Operator
// This is Secret Zero - it enables ESO to authenticate to Infisical.
// CRITICAL: This secret MUST be injected via client-go, NEVER stored in Git.
// 
// Production Workflow:
// 1. Developer accesses Infisical UI (after Infisical pods are running)
// 2. Creates Machine Identity "eso-operator" in Infisical UI
// 3. Copies Client ID and Client Secret from Infisical UI
// 4. Runs: hub platform-core configure-eso --infisical-client-id=<id> --infisical-client-secret=<secret>
// 5. This method uses client-go to inject the secret directly into the cluster
// 6. ArgoCD syncs ClusterSecretStore (wave 5) which reads this secret
// 7. ESO authenticates to Infisical and manages all other secrets via GitOps
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
func (i *Installer) FixArgoCDGitHubAuth(ctx context.Context, githubToken string) error {
	fmt.Println("[bootstrap] Creating ArgoCD GitHub repository secret...")

	// Load kubeconfig and create clientset
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create the secret with ArgoCD auto-discovery label
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hub-platform-git-secret",
			Namespace: constants.NamespaceOps,
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "repository",
				"app.kubernetes.io/managed-by":   "zero-ops-hub-cli",
				"app.kubernetes.io/component":    "secret-zero",
				"app.kubernetes.io/part-of":      "argocd",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"type":     "git",
			"url":      "https://github.com/soloz-io/zero-ops",
			"username": "zero-ops-bot",
			"password": githubToken,
		},
	}

	// Try to create, if exists then update
	_, err = clientset.CoreV1().Secrets(constants.NamespaceOps).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		// Secret might already exist, try to update
		_, err = clientset.CoreV1().Secrets(constants.NamespaceOps).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create or update ArgoCD GitHub secret: %w", err)
		}
		fmt.Println("[bootstrap] ✓ ArgoCD GitHub secret updated")
	} else {
		fmt.Println("[bootstrap] ✓ ArgoCD GitHub secret created")
	}

	fmt.Println("[bootstrap] Note: ESO will take over credential management after deployment")
	return nil
}

// InstallInfisicalSecrets generates and injects Infisical base secrets (Secret Zero)
// This is called during bootstrap BEFORE ArgoCD syncs Infisical.
// CRITICAL: These secrets enable Infisical to boot, NEVER store in Git.
//
// Generated Secrets:
// - ENCRYPTION_KEY: Exactly 32-character ASCII string (32 bytes) for AES-256-GCM encryption
// - AUTH_SECRET: Exactly 32-character ASCII string (32 bytes) for JWT signing
//
// IMPORTANT: Infisical's Node.js backend expects ENCRYPTION_KEY as a 32-byte UTF-8 string.
// We use generateSecurePassword(32) which produces exactly 32 hex characters (32 bytes).
//
// IDEMPOTENCY: Returns (true, nil) if secrets were created/modified, (false, nil) if they already exist.
// This prevents secret drift and unnecessary pod churn from repeated CLI executions.
//
// Production Workflow:
// 1. Developer runs: hub init-secrets
// 2. This method generates secure random keys using crypto/rand
// 3. Creates infisical-secrets in platform-core-db namespace
// 4. ArgoCD syncs Infisical Helm chart (wave 3)
// 5. Infisical pods start and use these secrets
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

	namespace := constants.NamespaceData

	// Check if secrets exist - but always update to ensure correct TLS configuration
	// This is necessary because TLS configuration may change (e.g., adding DB_ROOT_CERT)
	infSecret, err1 := clientset.CoreV1().Secrets(namespace).Get(ctx, "infisical-secrets", metav1.GetOptions{})
	redisSecret, err2 := clientset.CoreV1().Secrets(namespace).Get(ctx, "infisical-redis-credentials", metav1.GetOptions{})

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
		// Parse: redis://:PASSWORD@redis-master.hub-platform-data.svc:6379
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
	redisURL := fmt.Sprintf("redis://:%s@redis-master.hub-platform-data.svc:6379", redisPassword)

	// TLS Configuration Strategy: TLS Everywhere (Production-Grade)
	// Architecture: Infisical → PgBouncer (TLS) → PostgreSQL (TLS)
	//
	// CNPG's PgBouncer pooler provides TLS certificates automatically via:
	// - Server certificate: platform-db-server secret
	// - CA certificate: platform-db-ca secret
	//
	// We extract the CA cert and inject it as DB_ROOT_CERT for Infisical to verify the pooler's certificate.
	// This enables end-to-end encryption across all hops.

	// Read CA certificate from CNPG-managed secret
	caSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "platform-db-ca", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to read platform-db-ca secret: %w", err)
	}

	caCert, ok := caSecret.Data["ca.crt"]
	if !ok {
		return false, fmt.Errorf("ca.crt not found in platform-db-ca secret")
	}

	// Base64 encode the CA certificate for Infisical
	dbRootCert := base64.StdEncoding.EncodeToString(caCert)

	// Create the master infisical-secrets secret with ALL dynamic values
	// This secret is consumed via envFrom in the Helm chart
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "infisical-secrets",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
				"app.kubernetes.io/component":  "secret-zero",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"ENCRYPTION_KEY": encryptionKey,
			"AUTH_SECRET":    authSecret,
			"REDIS_URL":      redisURL,
			"DB_ROOT_CERT":   dbRootCert, // Enable TLS for Infisical → PgBouncer
		},
	}

	// Try to create, if exists then update
	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		// Secret might already exist, try to update
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
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
			Namespace: namespace,
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

	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, redisSecretObj, metav1.CreateOptions{})
	if err != nil {
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, redisSecretObj, metav1.UpdateOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to create or update infisical-redis-credentials: %w", err)
		}
		fmt.Println("[bootstrap-secrets] ✓ infisical-redis-credentials updated")
	} else {
		fmt.Println("[bootstrap-secrets] ✓ infisical-redis-credentials created")
	}

	return true, nil
}

// InstallPostgresConnectionSecret creates the PostgreSQL connection secret for Infisical
// This is Layer 1 Bootstrap (Secret Zero) - called during bootstrap BEFORE ArgoCD syncs Infisical.
// 
// Bootstrap Pattern:
// 1. CLI generates passwords for CNPG bootstrap (platform-db-app) and Infisical bootstrap (infisical-db-credentials)
// 2. CLI injects these secrets directly into K8s (Secret Zero)
// 3. CNPG and Infisical boot using these secrets
// 4. CLI uploads these secrets to Infisical (making Infisical the Source of Truth)
// 5. ESO adopts these secrets (creationPolicy: Owner) and keeps them in sync
//
// This solves the circular dependency: Infisical needs DB → DB needs secrets → Secrets need Infisical
//
// IDEMPOTENCY: Returns (true, nil) if secrets were created/modified, (false, nil) if they already exist.
func (i *Installer) InstallPostgresConnectionSecret(ctx context.Context) (bool, error) {
	// Load kubeconfig and create clientset
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return false, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return false, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	namespace := constants.NamespaceData

	// Step 1: Generate and inject platform-db-app (CNPG Secret Zero)
	appSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "platform-db-app", metav1.GetOptions{})
	var appPassword string
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return false, fmt.Errorf("failed to check platform-db-app secret: %w", err)
		}
		
		fmt.Println("[bootstrap-secrets] Generating platform-db-app (CNPG Secret Zero)...")
		appPassword, err = generateSecurePassword(32)
		if err != nil {
			return false, fmt.Errorf("failed to generate app password: %w", err)
		}
		
		appSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "platform-db-app",
				Namespace: namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
					"app.kubernetes.io/component":  "secret-zero",
				},
			},
			Type: corev1.SecretTypeBasicAuth,
			StringData: map[string]string{
				"username": "app",
				"password": appPassword,
			},
		}
		
		_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, appSecret, metav1.CreateOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to create platform-db-app secret: %w", err)
		}
		fmt.Println("[bootstrap-secrets] ✓ platform-db-app created")
	} else {
		appPassword = string(appSecret.Data["password"])
		fmt.Println("[bootstrap-secrets] ✓ platform-db-app already exists")
	}

	// Step 2: Generate and inject infisical-db-credentials (Infisical Secret Zero)
	infDbSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "infisical-db-credentials", metav1.GetOptions{})
	var infPassword string
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return false, fmt.Errorf("failed to check infisical-db-credentials secret: %w", err)
		}
		
		fmt.Println("[bootstrap-secrets] Generating infisical-db-credentials (Infisical Secret Zero)...")
		infPassword, err = generateSecurePassword(32)
		if err != nil {
			return false, fmt.Errorf("failed to generate infisical password: %w", err)
		}
		
		infDbSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "infisical-db-credentials",
				Namespace: namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
					"app.kubernetes.io/component":  "secret-zero",
				},
			},
			Type: corev1.SecretTypeOpaque,
			StringData: map[string]string{
				"username": "infisical",
				"password": infPassword,
			},
		}
		
		_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, infDbSecret, metav1.CreateOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to create infisical-db-credentials secret: %w", err)
		}
		fmt.Println("[bootstrap-secrets] ✓ infisical-db-credentials created")
	} else {
		infPassword = string(infDbSecret.Data["password"])
		fmt.Println("[bootstrap-secrets] ✓ infisical-db-credentials already exists")
	}

	// Step 3: Create the connection string secret for Infisical to use
	// TLS Configuration: End-to-end encryption (production-grade)
	// - Infisical → PgBouncer: TLS (using CNPG-provided certificates)
	// - PgBouncer → PostgreSQL: TLS (handled by CNPG)
	// DB_ROOT_CERT is provided separately via infisical-secrets (injected via envFrom)
	connSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "infisical-postgres-connection",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
				"app.kubernetes.io/component":  "secret-zero",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"DB_HOST":     "platform-db-pooler.hub-platform-data.svc",  // PgBouncer service
			"DB_PORT":     "5432",
			"DB_USER":     "infisical",
			"DB_PASSWORD": infPassword,
			"DB_NAME":     "infisical",
			"DB_SSL_MODE": "require",  // Enable TLS for client->pooler connection
		},
	}

	// Create or Update connSecret
	_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, connSecret, metav1.UpdateOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, connSecret, metav1.CreateOptions{})
			if err != nil {
				return false, fmt.Errorf("failed to create infisical-postgres-connection: %w", err)
			}
			fmt.Println("[bootstrap-secrets] ✓ infisical-postgres-connection created")
		} else {
			return false, fmt.Errorf("failed to update infisical-postgres-connection: %w", err)
		}
	} else {
		fmt.Println("[bootstrap-secrets] ✓ infisical-postgres-connection updated")
	}

	return true, nil
}

// InstallPlatformDatabaseCredentials generates secure passwords for all platform database users
// and stores them in Infisical (GitOps source of truth).
//
// Layer 1 + Layer 2 Bootstrap Pattern:
// 1. Uploads Layer 1 (Secret Zero) credentials to Infisical:
//    - infisical-db-credentials (already in K8s, now uploaded to Infisical as SOT)
//    - platform-db-app (already in K8s, now uploaded to Infisical as SOT)
// 2. Generates Layer 2 (Application) credentials and stores in Infisical:
//    - control-plane-db-* (mcp_server, agentregistry)
//    - hub-db-* (spoke_controller)
// 3. ExternalSecrets Operator syncs all credentials from Infisical to K8s
//
// This ensures Infisical is the definitive Source of Truth for ALL credentials,
// while solving the bootstrap paradox by having CLI inject Secret Zero first.
//
// IDEMPOTENCY: Checks if secrets exist in Infisical before creating.
func (i *Installer) InstallPlatformDatabaseCredentials(ctx context.Context) (bool, error) {
	// Load kubeconfig and create clientset
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return false, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return false, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	namespace := constants.NamespaceData

	// Create Infisical API client
	infisicalClient, err := infisical.NewClient(ctx, clientset)
	if err != nil {
		return false, fmt.Errorf("failed to create Infisical client: %w", err)
	}

	// Get Infisical configuration (project slug and environment)
	infisicalConfig, err := infisical.GetInfisicalConfig(ctx, clientset)
	if err != nil {
		return false, fmt.Errorf("failed to get Infisical config: %w", err)
	}

	secretPath := "/"

	// Step 1: Upload Layer 1 Bootstrap Credentials to Infisical (making Infisical the SOT)
	fmt.Println("[bootstrap-secrets] Uploading Layer 1 (Secret Zero) credentials to Infisical...")
	
	// Upload infisical-db-credentials
	infDbSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "infisical-db-credentials", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to read infisical-db-credentials: %w", err)
	}
	
	if err := infisicalClient.CreateOrUpdateSecret(ctx, infisicalConfig.ProjectSlug, infisicalConfig.EnvironmentSlug, secretPath, "infisical-db-username", string(infDbSecret.Data["username"])); err != nil {
		return false, fmt.Errorf("failed to upload infisical-db-username: %w", err)
	}
	if err := infisicalClient.CreateOrUpdateSecret(ctx, infisicalConfig.ProjectSlug, infisicalConfig.EnvironmentSlug, secretPath, "infisical-db-password", string(infDbSecret.Data["password"])); err != nil {
		return false, fmt.Errorf("failed to upload infisical-db-password: %w", err)
	}
	fmt.Println("[bootstrap-secrets] ✓ infisical-db credentials uploaded to Infisical")

	// Upload platform-db-app credentials
	appSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "platform-db-app", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to read platform-db-app: %w", err)
	}
	
	if err := infisicalClient.CreateOrUpdateSecret(ctx, infisicalConfig.ProjectSlug, infisicalConfig.EnvironmentSlug, secretPath, "platform-db-app-username", string(appSecret.Data["username"])); err != nil {
		return false, fmt.Errorf("failed to upload platform-db-app-username: %w", err)
	}
	if err := infisicalClient.CreateOrUpdateSecret(ctx, infisicalConfig.ProjectSlug, infisicalConfig.EnvironmentSlug, secretPath, "platform-db-app-password", string(appSecret.Data["password"])); err != nil {
		return false, fmt.Errorf("failed to upload platform-db-app-password: %w", err)
	}
	fmt.Println("[bootstrap-secrets] ✓ platform-db-app credentials uploaded to Infisical")

	// Upload hetzner hcloud token (required for spoke cluster CSI/CCM)
	hetznerSecret, err := clientset.CoreV1().Secrets(constants.NamespaceCloud).Get(ctx, "hetzner", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to read hetzner secret: %w", err)
	}
	
	if err := infisicalClient.CreateOrUpdateSecret(ctx, infisicalConfig.ProjectSlug, infisicalConfig.EnvironmentSlug, secretPath, "hcloud", string(hetznerSecret.Data["hcloud"])); err != nil {
		return false, fmt.Errorf("failed to upload hcloud token: %w", err)
	}
	fmt.Println("[bootstrap-secrets] ✓ hcloud token uploaded to Infisical")

	// Step 2: Generate and store Layer 2 Application Credentials in Infisical
	fmt.Println("[bootstrap-secrets] Generating Layer 2 (Application) credentials and storing in Infisical...")

	// Generate and store control-plane-db credentials
	controlPlanePassword, err := generateSecurePassword(32)
	if err != nil {
		return false, fmt.Errorf("failed to generate control-plane password: %w", err)
	}

	credentials := []struct {
		prefix   string
		username string
		password string
	}{
		{"control-plane-db", "mcp_server", controlPlanePassword},
		{"hub-db", "spoke_controller", ""},
	}

	// Generate hub password
	hubPassword, err := generateSecurePassword(32)
	if err != nil {
		return false, fmt.Errorf("failed to generate hub password: %w", err)
	}
	credentials[1].password = hubPassword

	// Store only username and password in Infisical (host/port/database are static in manifests)
	for _, cred := range credentials {
		fmt.Printf("[bootstrap-secrets] Storing %s credentials in Infisical...\n", cred.prefix)

		// Store only the secrets (username and password)
		secrets := map[string]string{
			cred.prefix + "-username": cred.username,
			cred.prefix + "-password": cred.password,
		}

		for key, value := range secrets {
			if err := infisicalClient.CreateOrUpdateSecret(ctx, infisicalConfig.ProjectSlug, infisicalConfig.EnvironmentSlug, secretPath, key, value); err != nil {
				return false, fmt.Errorf("failed to store %s in Infisical: %w", key, err)
			}
		}

		fmt.Printf("[bootstrap-secrets] ✓ %s credentials stored in Infisical\n", cred.prefix)
	}

	fmt.Println("[bootstrap-secrets] ✓ All credentials stored in Infisical. ExternalSecrets will sync to K8s.")

	return true, nil
}

// InstallSPIREServerCredentials generates secure credentials for SPIRE Server database access
// and stores them in Infisical.
//
// This function:
// 1. Generates cryptographically secure random password for spire_server role
// 2. Stores username and password in Infisical via API
// 3. ExternalSecrets Operator syncs them to K8s secret
//
// Updated Infisical secrets:
// - spire-server-db-username
// - spire-server-db-password
//
// IDEMPOTENCY: Checks if secrets exist in Infisical before creating.
func (i *Installer) InstallSPIREServerCredentials(ctx context.Context) (bool, error) {
	// Load kubeconfig and create clientset
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return false, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return false, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create Infisical API client
	infisicalClient, err := infisical.NewClient(ctx, clientset)
	if err != nil {
		return false, fmt.Errorf("failed to create Infisical client: %w", err)
	}

	// Get Infisical configuration
	infisicalConfig, err := infisical.GetInfisicalConfig(ctx, clientset)
	if err != nil {
		return false, fmt.Errorf("failed to get Infisical config: %w", err)
	}

	fmt.Println("[bootstrap-secrets] Generating SPIRE Server database credentials...")

	secretPath := "/"
	username := "spire_server"

	// Generate secure password
	password, err := generateSecurePassword(32)
	if err != nil {
		return false, fmt.Errorf("failed to generate SPIRE Server password: %w", err)
	}

	// Store username and password in Infisical
	secrets := map[string]string{
		"spire-server-db-username": username,
		"spire-server-db-password": password,
	}

	for key, value := range secrets {
		if err := infisicalClient.CreateOrUpdateSecret(ctx, infisicalConfig.ProjectSlug, infisicalConfig.EnvironmentSlug, secretPath, key, value); err != nil {
			return false, fmt.Errorf("failed to store %s in Infisical: %w", key, err)
		}
	}

	fmt.Println("[bootstrap-secrets] ✓ SPIRE Server credentials stored in Infisical")

	return true, nil
}

// WaitForInfisicalHealth waits for Infisical pods to become ready
// Returns error if timeout exceeded or pods not found
func (i *Installer) WaitForInfisicalHealth(ctx context.Context) error {
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	namespace := "hub-platform-security"
	timeout := 5 * time.Minute
	checkInterval := 5 * time.Second
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		// Check if deployment exists and has ready replicas
		deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, "platform-infisical-standalone", metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				fmt.Println("   Infisical deployment not found yet, waiting...")
				time.Sleep(checkInterval)
				continue
			}
			return fmt.Errorf("failed to get infisical deployment: %w", err)
		}

		// Check if at least one replica is ready
		if deployment.Status.ReadyReplicas > 0 {
			fmt.Println("✓ Infisical is healthy")
			return nil
		}

		fmt.Printf("   Infisical not ready yet (%d/%d replicas ready), waiting...\n", 
			deployment.Status.ReadyReplicas, deployment.Status.Replicas)
		time.Sleep(checkInterval)
	}

	return fmt.Errorf("timeout waiting for Infisical to become healthy after %v", timeout)
}

// RestartPlatformWorkloads performs a rolling restart of StatefulSets/Deployments
// This is called ONLY when secrets are actually modified to sync workloads with new credentials.
func (i *Installer) RestartPlatformWorkloads(ctx context.Context) error {
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	namespace := constants.NamespaceData
	patchData := []byte(fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"%s"}}}}}`, time.Now().Format(time.RFC3339)))

	fmt.Println("[bootstrap-secrets] Changes detected. Triggering workload rollouts to sync...")

	// Restart Redis StatefulSet
	if _, err = clientset.AppsV1().StatefulSets(namespace).Patch(ctx, "redis-master", types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to restart redis-master: %w", err)
		}
	}

	// Restart Infisical Deployment
	if _, err = clientset.AppsV1().Deployments(namespace).Patch(ctx, "platform-infisical-standalone", types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to restart infisical deployment: %w", err)
		}
	}

	fmt.Println("[bootstrap-secrets] ✓ Workloads restarted successfully.")
	return nil
}

// generateSecurePassword generates a cryptographically secure random password
// For encryption keys (32 bytes), this produces exactly 32 hex characters (32 bytes when interpreted as UTF-8)
func generateSecurePassword(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	// Always return hex encoding truncated to exact length
	// For 32-byte keys: hex.EncodeToString produces 64 chars, truncate to 32
	return hex.EncodeToString(bytes)[:length], nil
}

// InstallGHCRPullSecret creates the GHCR pull secret for pulling private container images
// This is Secret Zero - it enables Kubernetes to pull images from ghcr.io.
// CRITICAL: This secret MUST be injected via client-go, NEVER stored in Git.
//
// Production Workflow:
// 1. Developer runs: hub configure-eso --ghcr-username=<username> --github-token=<pat>
// 2. This method uses client-go to inject the Docker config JSON secret
// 3. Deployments reference this secret via imagePullSecrets
// 4. Future: Secret replication operator will propagate to other namespaces
func (i *Installer) InstallGHCRPullSecret(ctx context.Context, username, token string) error {
	fmt.Println("[bootstrap] Creating GHCR pull secret...")

	// Load kubeconfig and create clientset
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	namespace := constants.NamespaceOps

	// Create Docker config JSON for GHCR authentication
	dockerConfigJSON := fmt.Sprintf(`{"auths":{"ghcr.io":{"username":%q,"password":%q}}}`, username, token)

	// Create the secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ghcr-pull-secret",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
				"app.kubernetes.io/component":  "secret-zero",
			},
		},
		Type: corev1.SecretTypeDockerConfigJson,
		StringData: map[string]string{
			".dockerconfigjson": dockerConfigJSON,
		},
	}

	// Try to create, if exists then update
	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		// Secret might already exist, try to update
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create or update ghcr-pull-secret: %w", err)
		}
		fmt.Println("[bootstrap] ✓ ghcr-pull-secret updated")
	} else {
		fmt.Println("[bootstrap] ✓ ghcr-pull-secret created")
	}

	return nil
}
// InstallAWSSecretsManagerAuth creates the AWS credentials secret for hub-operator
// This is Secret Zero - it enables hub-operator to backup/restore Infisical master keys.
// CRITICAL: This secret MUST be injected via client-go, NEVER stored in Git.
//
// Production Workflow:
// 1. Developer creates IAM user with restricted Secrets Manager permissions
// 2. Developer runs: hub configure-aws-secrets-manager --aws-access-key-id=<id> --aws-secret-access-key=<secret> --aws-region=<region>
// 3. This method uses client-go to inject the secret directly into the cluster
// 4. Hub-operator deployment references this secret via secretKeyRef environment variables
// 5. Operator uses AWS SDK to backup/restore ENCRYPTION_KEY and AUTH_SECRET
// 6. Future credential rotations happen via Infisical + ESO (GitOps)
func (i *Installer) InstallAWSSecretsManagerAuth(ctx context.Context, accessKeyID, secretAccessKey, region string) error {
	fmt.Println("[bootstrap] Creating hub-operator-aws-credentials secret...")

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
			Name:      "hub-operator-aws-credentials",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
				"app.kubernetes.io/component":  "secret-zero",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"AWS_ACCESS_KEY_ID":     accessKeyID,
			"AWS_SECRET_ACCESS_KEY": secretAccessKey,
			"AWS_REGION":            region,
		},
	}

	// Try to create, if exists then update
	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		// Secret might already exist, try to update
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create or update hub-operator-aws-credentials secret: %w", err)
		}
		fmt.Println("[bootstrap] ✓ hub-operator-aws-credentials secret updated")
	} else {
		fmt.Println("[bootstrap] ✓ hub-operator-aws-credentials secret created")
	}

	return nil
}