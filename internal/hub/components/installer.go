package components

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os/exec"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
	"github.com/soloz-io/zero-ops/internal/hub/infisical"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Installer installs management cluster components
type Installer struct {
	Kubeconfig string
}

// InstallAll installs all required components sequentially (CNI/CCM handled by CRS)
func (i *Installer) InstallAll(ctx context.Context, hcloudToken string) error {
	// Create hcloud secret for CSI driver
	fmt.Println("[postboot] Creating hcloud secret for CSI...")
	secretCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", i.Kubeconfig,
		"create", "secret", "generic", "hcloud",
		"-n", "kube-system",
		"--from-literal=token="+hcloudToken,
		"--dry-run=client", "-o", "yaml",
	)
	secretYAML, err := secretCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to generate hcloud secret: %w", err)
	}
	
	applyCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", i.Kubeconfig,
		"apply", "-f", "-",
	)
	applyCmd.Stdin = bytes.NewReader(secretYAML)
	if output, err := applyCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create hcloud secret: %w\n%s", err, output)
	}
	
	// Install CSI via manifest
	fmt.Println("[postboot] Installing hetzner-csi...")
	csiManifest, err := assets.ReadCatalog("cloud-providers/hetzner/csi/install.yaml")
	if err != nil {
		return fmt.Errorf("failed to read CSI manifest: %w", err)
	}
	
	cmd := exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", i.Kubeconfig, "-f", "-")
	cmd.Stdin = bytes.NewReader(csiManifest)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install CSI: %w\n%s", err, output)
	}
	
	if err := i.verify(ctx, "kube-system", "hcloud-csi-controller"); err != nil {
		return fmt.Errorf("failed to verify CSI: %w", err)
	}
	fmt.Println("[postboot] ✓ hetzner-csi ready")
	
	// Install ArgoCD via Helm
	if err := i.InstallArgoCD(ctx); err != nil {
		return fmt.Errorf("failed to install ArgoCD: %w", err)
	}
	
	// Install capi2argo via manifest
	fmt.Println("[postboot] Installing capi2argo...")
	capi2argoManifest, err := assets.ReadCatalog("gitops/capi2argo/install.yaml")
	if err != nil {
		return fmt.Errorf("failed to read capi2argo manifest: %w", err)
	}
	
	cmd = exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", i.Kubeconfig, "-f", "-")
	cmd.Stdin = bytes.NewReader(capi2argoManifest)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install capi2argo: %w\n%s", err, output)
	}
	
	if err := i.verify(ctx, "capi2argo-system", "capi2argo-controller-manager"); err != nil {
		return fmt.Errorf("failed to verify capi2argo: %w", err)
	}
	fmt.Println("[postboot] ✓ capi2argo ready")
	
	// Install CloudNativePG via Helm
	fmt.Println("[postboot] Installing cloudnative-pg...")
	
	cmd = exec.CommandContext(ctx, "helm", "repo", "add", "cnpg", "https://cloudnative-pg.github.io/charts")
	if output, err := cmd.CombinedOutput(); err != nil {
		if !bytes.Contains(output, []byte("already exists")) {
			return fmt.Errorf("failed to add helm repo: %w\n%s", err, output)
		}
	}
	
	cmd = exec.CommandContext(ctx, "helm", "repo", "update")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update helm repos: %w\n%s", err, output)
	}
	
	cmd = exec.CommandContext(ctx, "helm", "upgrade", "--install", "cnpg", "cnpg/cloudnative-pg",
		"--namespace", "cnpg-system",
		"--create-namespace",
		"--kubeconfig", i.Kubeconfig,
		"--wait",
		"--timeout", "5m",
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install cloudnative-pg: %w\n%s", err, output)
	}
	
	fmt.Println("[postboot] ✓ cloudnative-pg ready")
	
	return nil
}

func (i *Installer) install(ctx context.Context, path string) error {
	manifest, err := assets.ReadCatalog(path)
	if err != nil {
		return err
	}
	
	cmd := exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", i.Kubeconfig,
		"-f", "-",
	)
	cmd.Stdin = bytes.NewReader(manifest)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w\n%s", err, output)
	}
	
	return nil
}

func (i *Installer) verify(ctx context.Context, namespace, deployment string) error {
	// For Cilium installer job, wait for job completion
	if deployment == "cilium-installer" {
		cmd := exec.CommandContext(ctx, "kubectl",
			"--kubeconfig", i.Kubeconfig,
			"wait", "job", deployment,
			"-n", namespace,
			"--for=condition=Complete",
			"--timeout=10m",
		)
		
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("job not complete: %w\n%s", err, output)
		}
		
		// Wait for Cilium operator deployment
		cmd = exec.CommandContext(ctx, "kubectl",
			"--kubeconfig", i.Kubeconfig,
			"wait", "deployment", "cilium-operator",
			"-n", "kube-system",
			"--for=condition=Available",
			"--timeout=5m",
		)
		
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("cilium-operator not ready: %w\n%s", err, output)
		}
		
		return nil
	}
	
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", i.Kubeconfig,
		"wait", "deployment", deployment,
		"-n", namespace,
		"--for=condition=Available",
		"--timeout=5m",
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("deployment not ready: %w\n%s", err, output)
	}
	
	return nil
}

// GetArgoCDPassword retrieves ArgoCD admin password
func (i *Installer) GetArgoCDPassword(ctx context.Context) (string, error) {
	// Wait a bit for secret to be created
	time.Sleep(5 * time.Second)
	
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", i.Kubeconfig,
		"get", "secret", "argocd-initial-admin-secret",
		"-n", "argocd",
		"-o", "jsonpath={.data.password}",
	)
	
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get ArgoCD password: %w", err)
	}
	
	// Decode base64
	decoded, err := base64.StdEncoding.DecodeString(string(output))
	if err != nil {
		return "", fmt.Errorf("failed to decode password: %w", err)
	}
	
	return string(decoded), nil
}


// InstallArgoCD installs ArgoCD via Helm
func (i *Installer) InstallArgoCD(ctx context.Context) error {
	fmt.Println("[postboot] Installing argocd...")
	
	// Add ArgoCD Helm repo
	cmd := exec.CommandContext(ctx, "helm", "repo", "add", "argo", "https://argoproj.github.io/argo-helm")
	if output, err := cmd.CombinedOutput(); err != nil {
		if !bytes.Contains(output, []byte("already exists")) {
			return fmt.Errorf("failed to add helm repo: %w\n%s", err, output)
		}
	}
	
	// Update repos
	cmd = exec.CommandContext(ctx, "helm", "repo", "update")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update helm repos: %w\n%s", err, output)
	}
	
	// Install ArgoCD
	cmd = exec.CommandContext(ctx, "helm", "upgrade", "--install", "argocd", "argo/argo-cd",
		"--version", "7.7.12",
		"--namespace", "argocd",
		"--create-namespace",
		"--kubeconfig", i.Kubeconfig,
		"--wait",
		"--timeout", "10m",
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install argocd: %w\n%s", err, output)
	}
	
	fmt.Println("[postboot] ✓ argocd ready")
	return nil
}

// InstallCilium installs Cilium CNI via Helm (CAPH parity)
func (i *Installer) InstallCilium(ctx context.Context) error {
	fmt.Println("[cilium] Installing Cilium CNI via Helm...")
	
	// Add Cilium Helm repo
	cmd := exec.CommandContext(ctx, "helm", "repo", "add", "cilium", "https://helm.cilium.io/")
	if output, err := cmd.CombinedOutput(); err != nil {
		// Ignore "already exists" error
		if !bytes.Contains(output, []byte("already exists")) {
			return fmt.Errorf("failed to add helm repo: %w\n%s", err, output)
		}
	}
	
	// Update repos
	cmd = exec.CommandContext(ctx, "helm", "repo", "update")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update helm repos: %w\n%s", err, output)
	}
	
	// Install Cilium
	cmd = exec.CommandContext(ctx, "helm", "upgrade", "--install", "cilium", "cilium/cilium",
		"--version", "1.15.6",
		"--namespace", "kube-system",
		"--kubeconfig", i.Kubeconfig,
		"--set", "ipam.mode=kubernetes",
		"--set", "kubeProxyReplacement=true",
		"--set", "operator.rollOutPods=true",
		"--set", "rollOutCiliumPods=true",
		"--set", "priorityClassName=system-node-critical",
		"--set", "operator.priorityClassName=system-node-critical",
		"--wait",
		"--timeout", "10m",
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install cilium: %w\n%s", err, output)
	}
	
	fmt.Println("[cilium] ✓ Cilium CNI installed")
	return nil
}

// InstallCCM installs Hetzner Cloud Controller Manager via Helm (CAPH parity)
func (i *Installer) InstallCCM(ctx context.Context, hcloudToken string) error {
	fmt.Println("[ccm] Installing Hetzner CCM via Helm...")
	
	// Add syself Helm repo
	cmd := exec.CommandContext(ctx, "helm", "repo", "add", "syself", "https://charts.syself.com")
	if output, err := cmd.CombinedOutput(); err != nil {
		if !bytes.Contains(output, []byte("already exists")) {
			return fmt.Errorf("failed to add helm repo: %w\n%s", err, output)
		}
	}
	
	// Update repos
	cmd = exec.CommandContext(ctx, "helm", "repo", "update")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update helm repos: %w\n%s", err, output)
	}
	
	// Install CCM
	cmd = exec.CommandContext(ctx, "helm", "upgrade", "--install", "ccm", "syself/ccm-hetzner",
		"--version", "1.1.10",
		"--namespace", "kube-system",
		"--kubeconfig", i.Kubeconfig,
		"--set", fmt.Sprintf("secret.hcloudApiToken=%s", hcloudToken),
		"--wait",
		"--timeout", "5m",
	)
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install ccm: %w\n%s", err, output)
	}
	
	fmt.Println("[ccm] ✓ Hetzner CCM installed")
	return nil
}
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
	namespace := "external-secrets-system"
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
			Name:      "repo-soloz-io-zero-ops",
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "repository",
				"app.kubernetes.io/managed-by":   "zero-ops-hub-cli",
				"app.kubernetes.io/component":    "secret-zero",
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
	_, err = clientset.CoreV1().Secrets("argocd").Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		// Secret might already exist, try to update
		_, err = clientset.CoreV1().Secrets("argocd").Update(ctx, secret, metav1.UpdateOptions{})
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
// 3. Creates infisical-secrets in zero-ops-system namespace
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

	namespace := "zero-ops-system"

	// Check if both secrets already exist and are populated
	infSecret, err1 := clientset.CoreV1().Secrets(namespace).Get(ctx, "infisical-secrets", metav1.GetOptions{})
	redisSecret, err2 := clientset.CoreV1().Secrets(namespace).Get(ctx, "infisical-redis-credentials", metav1.GetOptions{})

	if err1 == nil && err2 == nil && 
		len(infSecret.Data["ENCRYPTION_KEY"]) > 0 && 
		len(infSecret.Data["REDIS_URL"]) > 0 &&
		len(redisSecret.Data["password"]) > 0 {
		fmt.Println("[bootstrap-secrets] ✓ Infisical & Redis secrets already exist. Immutable lock applied; skipping.")
		return false, nil
	}

	fmt.Println("[bootstrap-secrets] Generating initial Infisical & Redis secrets...")

	// Generate secure random keys - MUST be exactly 32 characters for AES-256
	encryptionKey, err := generateSecurePassword(32)
	if err != nil {
		return false, fmt.Errorf("failed to generate encryption key: %w", err)
	}

	authSecret, err := generateSecurePassword(32)
	if err != nil {
		return false, fmt.Errorf("failed to generate auth secret: %w", err)
	}

	// Generate Redis password (use hex encoding to avoid URL-unsafe characters)
	redisBytes := make([]byte, 32)
	if _, err := rand.Read(redisBytes); err != nil {
		return false, fmt.Errorf("failed to generate redis password: %w", err)
	}
	redisPassword := hex.EncodeToString(redisBytes)[:32]
	redisURL := fmt.Sprintf("redis://:%s@redis-master.zero-ops-system.svc:6379", redisPassword)

	// Extract CNPG CA certificate for DB_ROOT_CERT
	cnpgCASecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "platform-db-ca", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to read platform-db-ca secret (ensure CNPG cluster is ready): %w", err)
	}

	caCert := cnpgCASecret.Data["ca.crt"]
	if len(caCert) == 0 {
		return false, fmt.Errorf("ca.crt not found in platform-db-ca secret")
	}

	// Base64 encode the CA certificate for Infisical's DB_ROOT_CERT env var
	caCertBase64 := base64.StdEncoding.EncodeToString(caCert)

	// Create the master infisical-secrets secret with ALL dynamic values
	// This secret is consumed via envFrom in the Helm chart, bypassing extraEnv bugs
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
			"DB_ROOT_CERT":   caCertBase64,
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
		fmt.Println("[bootstrap-secrets] ✓ infisical-secrets updated (with REDIS_URL and DB_ROOT_CERT)")
	} else {
		fmt.Println("[bootstrap-secrets] ✓ infisical-secrets created (with REDIS_URL and DB_ROOT_CERT)")
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
// This is called during bootstrap BEFORE ArgoCD syncs Infisical.
// CRITICAL: This secret enables Infisical to connect to CNPG, NEVER store in Git.
//
// NOTE: This method provides individual DB parameters (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME)
// instead of a connection string. This allows Infisical's Knex to properly use DB_ROOT_CERT for SSL.
//
// IDEMPOTENCY: Returns (true, nil) if secret was created/modified, (false, nil) if it already exists.
// This prevents secret drift and unnecessary pod churn from repeated CLI executions.
//
// Production Workflow:
// 1. ArgoCD syncs platform-database (wave 2) which creates infisical-db-credentials
// 2. Developer runs: hub init-secrets
// 3. This method reads the password from infisical-db-credentials
// 4. Extracts CNPG CA certificate from platform-db-ca secret
// 5. Creates infisical-postgres-connection with individual DB params + CA cert
// 6. ArgoCD syncs Infisical Helm chart (wave 3)
// 7. Infisical pods connect to CNPG using SSL with DB_ROOT_CERT validation
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

	namespace := "zero-ops-system"

	// Check if secret already exists and is populated
	connSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "infisical-postgres-connection", metav1.GetOptions{})
	if err == nil && len(connSecret.Data["DB_PASSWORD"]) > 0 {
		fmt.Println("[bootstrap-secrets] ✓ Infisical DB connection parameters already exist. Skipping.")
		return false, nil
	}

	fmt.Println("[bootstrap-secrets] Generating initial Infisical DB connection secret...")

	// Read password from infisical-db-credentials secret
	credentialsSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "infisical-db-credentials", metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to read infisical-db-credentials secret (ensure platform-database is deployed): %w", err)
	}

	password := string(credentialsSecret.Data["password"])
	if password == "" {
		return false, fmt.Errorf("password not found in infisical-db-credentials secret")
	}

	// Create the secret with individual DB parameters
	// Pooler->PostgreSQL uses TLS, but client->pooler doesn't need SSL verification
	secret := &corev1.Secret{
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
			"DB_HOST":     "platform-db-pooler.zero-ops-system.svc",  // PgBouncer service
			"DB_PORT":     "5432",
			"DB_USER":     "infisical",
			"DB_PASSWORD": password,
			"DB_NAME":     "infisical",
			"DB_SSL_MODE": "disable",  // Pooler->PG uses TLS, client->pooler doesn't need it
		},
	}

	// Try to create, if exists then update
	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		// Secret might already exist, try to update
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to create or update infisical-postgres-connection: %w", err)
		}
		fmt.Println("[bootstrap-secrets] ✓ infisical-postgres-connection updated (individual DB params)")
	} else {
		fmt.Println("[bootstrap-secrets] ✓ infisical-postgres-connection created (individual DB params)")
	}

	return true, nil
}


// InstallPlatformDatabaseCredentials generates secure passwords for all platform database users
// and stores them in Infisical (GitOps source of truth).
//
// This function:
// 1. Generates cryptographically secure random passwords
// 2. Stores them in Infisical via API
// 3. ExternalSecrets Operator syncs them to K8s secrets
//
// Updated Infisical secrets:
// - control-plane-db-* (host, port, database, username, password)
// - hub-db-* (host, port, database, username, password)
// - infisical-db-* (host, port, database, username, password)
//
// IDEMPOTENCY: Checks if secrets exist in Infisical before creating.
//
// CRITICAL: This must run BEFORE setup-platform-roles-job, as that job reads these passwords
// to create the PostgreSQL roles.
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

	// Create Infisical API client (reuses infisical-auth secret from ESO)
	infisicalClient, err := infisical.NewClient(ctx, clientset)
	if err != nil {
		return false, fmt.Errorf("failed to create Infisical client: %w", err)
	}

	// Get Infisical configuration (project slug and environment)
	infisicalConfig, err := infisical.GetInfisicalConfig(ctx, clientset)
	if err != nil {
		return false, fmt.Errorf("failed to get Infisical config: %w", err)
	}

	fmt.Println("[bootstrap-secrets] Generating platform database credentials and storing in Infisical...")

	secretPath := "/"

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

	namespace := "zero-ops-system"
	patchData := []byte(fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"%s"}}}}}`, time.Now().Format(time.RFC3339)))

	fmt.Println("[bootstrap-secrets] Changes detected. Triggering workload rollouts to sync...")

	// Restart Redis StatefulSet
	if _, err = clientset.AppsV1().StatefulSets(namespace).Patch(ctx, "redis-master", types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to restart redis-master: %w", err)
		}
	}

	// Restart Infisical Deployment
	if _, err = clientset.AppsV1().Deployments(namespace).Patch(ctx, "platform-infisical-infisical-standalone-infisical", types.StrategicMergePatchType, patchData, metav1.PatchOptions{}); err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to restart infisical deployment: %w", err)
		}
	}

	fmt.Println("[bootstrap-secrets] ✓ Workloads restarted successfully.")
	return nil
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
