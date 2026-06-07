package components

import (
	"context"
	"fmt"
	"time"

	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
	"github.com/soloz-io/zero-ops/internal/hub-cli/health"
	"github.com/soloz-io/zero-ops/internal/hub-cli/infisical"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// InstallInfisicalAuthFromInfisical fully automates the Infisical Day-0 bootstrap:
// 1. Runs `infisical bootstrap` via pod exec to create admin + org
// 2. Creates the hub-platform project via REST API
// 3. Creates a Machine Identity with Universal Auth + client secret
// 4. Grants project admin role
// 5. Creates the `infisical-auth` Secret in platform-ops
// 6. Patches `hub-bootstrap-config` ConfigMap with OrgID, ProjectID, ProjectSlug
func (i *Installer) InstallInfisicalAuthFromInfisical(ctx context.Context) (bool, error) {
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return false, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return false, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	result, err := infisical.BootstrapInfisicalDayZeroWithRetry(ctx, 10*time.Minute)
	if err != nil {
		return false, fmt.Errorf("infisical bootstrap failed: %w", err)
	}

	fmt.Println("[bootstrap-secrets] Creating infisical-auth secret...")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "infisical-auth",
			Namespace: constants.NamespaceOps,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
				"app.kubernetes.io/component":  "secret-zero",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"client-id":     result.ClientID,
			"client-secret": result.ClientSecret,
		},
	}

	_, err = clientset.CoreV1().Secrets(constants.NamespaceOps).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			_, err = clientset.CoreV1().Secrets(constants.NamespaceOps).Update(ctx, secret, metav1.UpdateOptions{})
			if err != nil {
				return false, fmt.Errorf("failed to update infisical-auth: %w", err)
			}
			fmt.Println("[bootstrap-secrets] ✓ infisical-auth secret updated")
		} else {
			return false, fmt.Errorf("failed to create infisical-auth: %w", err)
		}
	} else {
		fmt.Println("[bootstrap-secrets] ✓ infisical-auth secret created")
	}

	fmt.Println("[bootstrap-secrets] Patching hub-bootstrap-config with OrgID/ProjectID...")
	cm, err := clientset.CoreV1().ConfigMaps(constants.NamespaceOps).Get(ctx, "hub-bootstrap-config", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to get hub-bootstrap-config ConfigMap: %w", err)
	}

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data["INFISICAL_ORGANIZATION_ID"] = result.OrgID
	cm.Data["INFISICAL_PROJECT_ID"] = result.ProjectID
	cm.Data["INFISICAL_PROJECT_SLUG"] = result.ProjectSlug
	cm.Data["INFISICAL_SECRETS_PROJECT_ID"] = result.SecretsProjectID
	cm.Data["INFISICAL_SECRETS_PROJECT_SLUG"] = result.SecretsProjectSlug
	cm.Data["INFISICAL_ENVIRONMENT_SLUG"] = "dev"

	_, err = clientset.CoreV1().ConfigMaps(constants.NamespaceOps).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to update hub-bootstrap-config ConfigMap: %w", err)
	}
	fmt.Println("[bootstrap-secrets] ✓ hub-bootstrap-config patched with OrgID/ProjectID")

	return true, nil
}

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

	// Define explicit bounded contexts - "Secrets live exactly where they are consumed"
	dataNamespace := constants.NamespaceData         // platform-data: CNPG, Redis, DB Init Jobs
	securityNamespace := constants.NamespaceSecurity // platform-security: Infisical pods

	// Step 1: Generate and inject platform-db-app (CNPG Secret Zero)
	// MUST be in dataNamespace - consumed by CNPG cluster
	appSecret, err := clientset.CoreV1().Secrets(dataNamespace).Get(ctx, "platform-db-app", metav1.GetOptions{})
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
				Namespace: dataNamespace, // CNPG cluster in platform-data
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

		_, err = clientset.CoreV1().Secrets(dataNamespace).Create(ctx, appSecret, metav1.CreateOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to create platform-db-app secret: %w", err)
		}
		fmt.Println("[bootstrap-secrets] ✓ platform-db-app created")
	} else {
		appPassword = string(appSecret.Data["password"])
		fmt.Println("[bootstrap-secrets] ✓ platform-db-app already exists")
	}

	// Step 2: Generate and inject infisical-db-credentials (Infisical Secret Zero)
	// MUST be in dataNamespace - consumed by DB Init Job that creates infisical role
	infDbSecret, err := clientset.CoreV1().Secrets(dataNamespace).Get(ctx, "infisical-db-credentials", metav1.GetOptions{})
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
				Namespace: dataNamespace, // DB Init Job in platform-data
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

		_, err = clientset.CoreV1().Secrets(dataNamespace).Create(ctx, infDbSecret, metav1.CreateOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to create infisical-db-credentials secret: %w", err)
		}
		fmt.Println("[bootstrap-secrets] ✓ infisical-db-credentials created")
	} else {
		infPassword = string(infDbSecret.Data["password"])
		fmt.Println("[bootstrap-secrets] ✓ infisical-db-credentials already exists")
	}

	// Step 3: Create the connection string secret for Infisical to use
	// MUST be in securityNamespace - consumed by Infisical pods
	// TLS Configuration: End-to-end encryption (production-grade)
	// - Infisical → PgBouncer: TLS (using CNPG-provided certificates)
	// - PgBouncer → PostgreSQL: TLS (handled by CNPG)
	// DB_ROOT_CERT is provided separately via infisical-secrets (injected via envFrom)
	connSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "infisical-postgres-connection",
			Namespace: securityNamespace, // Infisical pods in platform-security
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "zero-ops-hub-cli",
				"app.kubernetes.io/component":  "secret-zero",
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"DB_HOST":     "platform-db-pooler.platform-data.svc", // PgBouncer service
			"DB_PORT":     "5432",
			"DB_USER":     "infisical",
			"DB_PASSWORD": infPassword,
			"DB_NAME":     "infisical",
			"DB_SSL_MODE": "require", // Enable TLS for client->pooler connection
		},
	}

	// Create or Update connSecret in securityNamespace
	_, err = clientset.CoreV1().Secrets(securityNamespace).Update(ctx, connSecret, metav1.UpdateOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			_, err = clientset.CoreV1().Secrets(securityNamespace).Create(ctx, connSecret, metav1.CreateOptions{})
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
//   - infisical-db-credentials (already in K8s, now uploaded to Infisical as SOT)
//   - platform-db-app (already in K8s, now uploaded to Infisical as SOT)
//
// 2. Generates Layer 2 (Application) credentials and stores in Infisical:
//   - control-plane-db-* (mcp_server, agentregistry)
//
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

	if err := infisicalClient.CreateOrUpdateSecret(ctx, infisicalConfig.ProjectSlug, infisicalConfig.EnvironmentSlug, secretPath, infisical.KeyPlatformDBAppUsername, string(appSecret.Data["username"])); err != nil {
		return false, fmt.Errorf("failed to upload %s: %w", infisical.KeyPlatformDBAppUsername, err)
	}

	if err := infisicalClient.CreateOrUpdateSecret(ctx, infisicalConfig.ProjectSlug, infisicalConfig.EnvironmentSlug, secretPath, infisical.KeyPlatformDBAppPassword, string(appSecret.Data["password"])); err != nil {
		return false, fmt.Errorf("failed to upload %s: %w", infisical.KeyPlatformDBAppPassword, err)
	}
	if err := infisicalClient.CreateOrUpdateSecret(ctx, infisicalConfig.ProjectSlug, infisicalConfig.EnvironmentSlug, secretPath, "platform-db-app-password", string(appSecret.Data["password"])); err != nil {
		return false, fmt.Errorf("failed to upload platform-db-app-password: %w", err)
	}
	fmt.Println("[bootstrap-secrets] ✓ platform-db-app credentials uploaded to Infisical")

	// Step 2: Generate and store Layer 2 Application Credentials in Infisical
	fmt.Println("[bootstrap-secrets] Generating Layer 2 (Application) credentials and storing in Infisical...")

	// Generate and store control-plane-db and hub-db credentials.
	// Both are logical databases within the same CNPG cluster:
	//   control_plane → used by MCP server + Ory stack (role: mcp_server)
	//   hub          → used by hub platform (role: spoke_controller)
	// The hub- prefix on Infisical keys follows the convention in
	// operators/hub-operator/internal/infisical/constants.go.
	controlPlanePassword, err := generateSecurePassword(32)
	if err != nil {
		return false, fmt.Errorf("failed to generate control-plane password: %w", err)
	}
	hubDBPassword, err := generateSecurePassword(32)
	if err != nil {
		return false, fmt.Errorf("failed to generate hub-db password: %w", err)
	}

	credentials := []struct {
		prefix   string
		username string
		password string
	}{
		{"hub-control-plane-db", "mcp_server", controlPlanePassword},
		{"hub-centralized-db", "spoke_controller", hubDBPassword},
	}

	// Store only username and password in Infisical (host/port/database are static in manifests)
	for _, cred := range credentials {
		fmt.Printf("[bootstrap-secrets] Storing %s credentials in Infisical...\n", cred.prefix)

		var usernameKey, passwordKey string
		switch cred.prefix {
		case "hub-control-plane-db":
			usernameKey = infisical.KeyControlPlaneDBUsername
			passwordKey = infisical.KeyControlPlaneDBPassword
		case "hub-centralized-db":
			usernameKey = infisical.KeyHubCentralizedDBUsername
			passwordKey = infisical.KeyHubCentralizedDBPassword
		}

		secrets := map[string]string{
			usernameKey: cred.username,
			passwordKey: cred.password,
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
		infisical.KeySpireServerDBUsername: username,
		infisical.KeySpireServerDBPassword: password,
	}

	for key, value := range secrets {
		if err := infisicalClient.CreateOrUpdateSecret(ctx, infisicalConfig.ProjectSlug, infisicalConfig.EnvironmentSlug, secretPath, key, value); err != nil {
			return false, fmt.Errorf("failed to store %s in Infisical: %w", key, err)
		}
	}

	fmt.Println("[bootstrap-secrets] ✓ SPIRE Server credentials stored in Infisical")

	return true, nil
}

// WaitForInfisicalAuth waits for the infisical-auth secret to be created by 'hub configure-eso'.
// This secret contains the Machine Identity credentials needed to authenticate with the Infisical API.
// It must exist before Steps 4-5 of init-secrets can run.
func (i *Installer) WaitForInfisicalAuth(ctx context.Context) error {
	printed := false
	waiter := &health.HealthWaiter{
		Checkers: []health.HealthChecker{
			health.NewSecretKeyHealth(
				constants.NamespaceOps,
				"infisical-auth",
				"client-id",
				"client-secret",
			),
		},
		Interval: 15 * time.Second,
		Timeout:  30 * time.Minute,
		OnCheckStart: func(c health.HealthChecker) {
			if !printed {
				fmt.Println("\n⏳ Waiting for infisical-auth secret...")
				fmt.Println("   NOTE: This secret is now auto-created by 'hub init-secrets' Step 3.5.")
				fmt.Println("   Run: hub init-secrets --kubeconfig=<path>")
				fmt.Println("   This process will continue automatically once the secret is created.")
				printed = true
			}
			fmt.Printf("   %s not found yet, checking again in 15s...\n", c.Name())
		},
	}
	if err := waiter.Wait(ctx, i.Kubeconfig); err != nil {
		return fmt.Errorf("timeout after 30m waiting for infisical-auth secret — run 'hub init-secrets' to create it: %w", err)
	}
	fmt.Println("✓ infisical-auth secret found — Machine Identity credentials ready")
	return nil
}

// WaitForInfisicalHealth waits for the data layer (CNPG + PgBouncer) and
// Infisical itself to be ready.
//
// This is the single entry point used by `hub init-secrets` Step 3 to
// guarantee that the REST API call in Step 3.5 will succeed. The
// dependency chain is encoded by composing the DataLayerReadiness and
// InfisicalReadiness phases from the health package — adding a new
// prerequisite (e.g., Redis) is a one-line change to phases.go.
//
// Per ADR-022 (Stable-but-Not-Ready Application Semantics), Day-0
// choreography embraces Kubernetes' eventual consistency. We do NOT
// require the full desired replica count to be Ready — that creates a
// fatal bottleneck when ArgoCD is mid-reconciliation (e.g., the
// multi-source race where the chart briefly renders with upstream
// defaults before our valueFiles resolve).
//
// Note: the bash bootstrap's step5 has a separate, stricter verification
// of the `infisical-auth` Secret artifact before marking init_secrets
// complete — that is the real safety net.
func (i *Installer) WaitForInfisicalHealth(ctx context.Context) error {
	waiter := &health.HealthWaiter{
		Checkers: append(
			(&health.DataLayerReadiness{}).Checkers(),
			(&health.InfisicalReadiness{}).Checkers()...,
		),
		Interval: 5 * time.Second,
		Timeout:  15 * time.Minute,
		OnCheckStart: func(c health.HealthChecker) {
			fmt.Printf("   → checking %s\n", c.Name())
		},
		OnCheckPass: func(c health.HealthChecker) {
			fmt.Printf("   ✓ %s healthy\n", c.Name())
		},
	}
	if err := waiter.Wait(ctx, i.Kubeconfig); err != nil {
		return fmt.Errorf("infisical health check failed: %w", err)
	}
	fmt.Println("✓ Infisical is healthy (data layer + workload)")
	return nil
}

// RestartPlatformWorkloads performs a rolling restart of StatefulSets/Deployments
func (i *Installer) RestartPlatformWorkloads(ctx context.Context) error {
	config, err := clientcmd.BuildConfigFromFlags("", i.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	dataNamespace := constants.NamespaceData
	securityNamespace := constants.NamespaceSecurity
	patchData := []byte(fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"%s"}}}}}`, time.Now().Format(time.RFC3339)))

	fmt.Println("[bootstrap-secrets] Changes detected. Triggering workload rollouts to sync...")

	// Restart Redis StatefulSet (Redis is in platform-data)
	if _, err = clientset.AppsV1().StatefulSets(dataNamespace).Patch(ctx, "redis-master", types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to restart redis-master: %w", err)
		}
	}

	// Restart Infisical (chart may create StatefulSet or Deployment)
	if _, err = clientset.AppsV1().StatefulSets(securityNamespace).Patch(ctx, "infisical-standalone-infisical", types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
		if k8serrors.IsNotFound(err) {
			// Try Deployment if StatefulSet not found (current chart)
			if _, depErr := clientset.AppsV1().Deployments(securityNamespace).Patch(ctx, "infisical-standalone-infisical", types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); depErr != nil {
				if !k8serrors.IsNotFound(depErr) {
					return fmt.Errorf("failed to restart infisical: %w", depErr)
				}
			}
		} else {
			return fmt.Errorf("failed to restart infisical: %w", err)
		}
	}

	fmt.Println("[bootstrap-secrets] ✓ Workloads restarted successfully.")
	return nil
}
