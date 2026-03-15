package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	kubeconfigPath string
	dnsToken       string
	ghcrUsername   string
	ghcrToken      string
	hydraPwd       string
	kratosPwd      string
	ketoPwd        string
	hcloudToken    string
)

func NewBootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Bootstrap secrets for a demo environment",
		RunE:  runBootstrap,
	}
	cmd.Flags().StringVar(&kubeconfigPath, "kubeconfig", "secrets/mothership.kubeconfig", "Path to kubeconfig")
	cmd.Flags().StringVar(&dnsToken, "dns-token", "", "Hetzner DNS token (validated against api.hetzner.cloud)")
	cmd.Flags().StringVar(&ghcrUsername, "ghcr-username", "", "GHCR username")
	cmd.Flags().StringVar(&ghcrToken, "ghcr-token", "", "GHCR token")
	cmd.Flags().StringVar(&hydraPwd, "hydra-password", "", "Hydra DB password (auto-generated if empty)")
	cmd.Flags().StringVar(&kratosPwd, "kratos-password", "", "Kratos DB password (auto-generated if empty)")
	cmd.Flags().StringVar(&ketoPwd, "keto-password", "", "Keto DB password (auto-generated if empty)")
	cmd.Flags().StringVar(&hcloudToken, "hcloud-token", "", "Hetzner Cloud token for CCM (defaults to dns-token if empty)")
	cmd.MarkFlagRequired("dns-token")
	cmd.MarkFlagRequired("ghcr-username")
	cmd.MarkFlagRequired("ghcr-token")
	return cmd
}

func runBootstrap(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// 1. Validate DNS token via Hetzner Cloud API
	fmt.Println("→ Validating Hetzner DNS token...")
	zone, err := validateDNSToken(dnsToken)
	if err != nil {
		return fmt.Errorf("DNS token validation failed: %w", err)
	}
	fmt.Printf("  ✓ Zone found: %s (id=%d)\n", zone.Name, zone.ID)

	// 2. Build k8s client
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("kubeconfig: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}

	// 3. Apply secrets
	if hcloudToken == "" {
		hcloudToken = dnsToken
	}
	secrets := []struct {
		ns     string
		secret *corev1.Secret
	}{
		{"cert-manager", hetznerDNSSecret("cert-manager", dnsToken)},
		{"kube-system", hetznerDNSSecret("kube-system", dnsToken)},
		{"kube-system", hcloudSecret(hcloudToken)},
		{"zero-ops-system", postgresPasswordsSecret(hydraPwd, kratosPwd, ketoPwd)},
		{"identity-services", ghcrPullSecret(ghcrUsername, ghcrToken)},
		{"argocd", argoCDRepoSecret(ghcrUsername, ghcrToken)},
	}

	for _, s := range secrets {
		if err := applySecret(ctx, client, s.ns, s.secret); err != nil {
			return err
		}
	}

	// Label ArgoCD repo secret
	if err := labelArgoCDRepoSecret(ctx, client); err != nil {
		return err
	}

	fmt.Println("\n✅ Bootstrap complete. Apply the app-of-apps:")
	fmt.Println("   kubectl apply -f manifests/argocd/app-of-apps.yaml")
	return nil
}

// validateDNSToken calls api.hetzner.cloud/v1/zones and returns the nutgraf.in zone.
func validateDNSToken(token string) (*hetznerZone, error) {
	req, _ := http.NewRequest("GET", "https://api.hetzner.cloud/v1/zones?name=nutgraf.in", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid token (401)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var result struct {
		Zones []hetznerZone `json:"zones"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Zones) == 0 {
		return nil, fmt.Errorf("zone nutgraf.in not found in account")
	}
	return &result.Zones[0], nil
}

type hetznerZone struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func applySecret(ctx context.Context, client kubernetes.Interface, ns string, s *corev1.Secret) error {
	_, err := client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		ns_obj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
		if _, err := client.CoreV1().Namespaces().Create(ctx, ns_obj, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create namespace %s: %w", ns, err)
		}
	}
	_, err = client.CoreV1().Secrets(ns).Get(ctx, s.Name, metav1.GetOptions{})
	if err != nil {
		_, err = client.CoreV1().Secrets(ns).Create(ctx, s, metav1.CreateOptions{})
	} else {
		_, err = client.CoreV1().Secrets(ns).Update(ctx, s, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("apply secret %s/%s: %w", ns, s.Name, err)
	}
	fmt.Printf("  ✓ secret %s/%s\n", ns, s.Name)
	return nil
}

func labelArgoCDRepoSecret(ctx context.Context, client kubernetes.Interface) error {
	s, err := client.CoreV1().Secrets("argocd").Get(ctx, "repo-soloz-io-zero-ops", metav1.GetOptions{})
	if err != nil {
		return err
	}
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	s.Labels["argocd.argoproj.io/secret-type"] = "repository"
	_, err = client.CoreV1().Secrets("argocd").Update(ctx, s, metav1.UpdateOptions{})
	return err
}

func hcloudSecret(token string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "hcloud", Namespace: "kube-system"},
		StringData: map[string]string{"token": token},
	}
}

func hetznerDNSSecret(ns, token string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "hetzner-dns", Namespace: ns},
		StringData: map[string]string{"api-key": token},
	}
}

func postgresPasswordsSecret(hydra, kratos, keto string) *corev1.Secret {
	if hydra == "" {
		hydra = randomHex()
	}
	if kratos == "" {
		kratos = randomHex()
	}
	if keto == "" {
		keto = randomHex()
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "identity-postgres-passwords", Namespace: "zero-ops-system"},
		StringData: map[string]string{
			"hydra-password":  hydra,
			"kratos-password": kratos,
			"keto-password":   keto,
		},
	}
}

func ghcrPullSecret(username, token string) *corev1.Secret {
	auth := fmt.Sprintf(`{"auths":{"ghcr.io":{"username":%q,"password":%q}}}`, username, token)
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ghcr-pull-secret", Namespace: "identity-services"},
		Type:       corev1.SecretTypeDockerConfigJson,
		StringData: map[string]string{".dockerconfigjson": auth},
	}
}

func argoCDRepoSecret(username, token string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "repo-soloz-io-zero-ops", Namespace: "argocd"},
		StringData: map[string]string{
			"type":     "git",
			"url":      "https://github.com/soloz-io/zero-ops",
			"username": username,
			"password": token,
		},
	}
}

func randomHex() string {
	b := make([]byte, 32)
	f, _ := os.Open("/dev/urandom")
	defer f.Close()
	f.Read(b)
	return fmt.Sprintf("%x", b)
}
