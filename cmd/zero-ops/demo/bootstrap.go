package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

var (
	kubeconfigPath string
	dnsToken       string
	ghcrUsername   string
	ghcrToken      string
	githubToken    string
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
	cmd.Flags().StringVar(&githubToken, "github-token", "", "GitHub PAT for ArgoCD repo access")
	cmd.Flags().StringVar(&hcloudToken, "hcloud-token", "", "Hetzner Cloud token for CCM (defaults to dns-token if empty)")
	cmd.MarkFlagRequired("dns-token")
	cmd.MarkFlagRequired("ghcr-username")
	cmd.MarkFlagRequired("ghcr-token")
	cmd.MarkFlagRequired("github-token")
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
	dynClient, err := dynamic.NewForConfig(cfg)
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
		{"ory-system", kratosUISecret()},
		{"identity-services", ghcrPullSecret(ghcrUsername, ghcrToken)},
		{"argocd", argoCDRepoSecret(githubToken)},
	}

	for _, s := range secrets {
		if err := applySecret(ctx, client, s.ns, s.secret); err != nil {
			return err
		}
	}

	// 4. Wait for ArgoCD apps and TLS certs
	fmt.Println("\n→ Waiting for ArgoCD apps to become healthy...")
	if err := waitForArgoCD(ctx, dynClient); err != nil {
		return err
	}

	fmt.Println("\n→ Waiting for TLS certificates...")
	if err := waitForCerts(ctx, dynClient); err != nil {
		return err
	}

	fmt.Println("\n→ Seeding demo user...")
	if err := seedDemoUser(ctx, client, cfg); err != nil {
		return err
	}

	fmt.Println("\n✅ Bootstrap complete. Demo 1 is ready.")
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

func kratosUISecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kratos-ui-secrets", Namespace: "ory-system"},
		StringData: map[string]string{
			"cookie-secret":      randomHex(),
			"csrf-cookie-secret": randomHex(),
		},
	}
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

func argoCDRepoSecret(token string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo-soloz-io-zero-ops",
			Namespace: "argocd",
			Labels:    map[string]string{"argocd.argoproj.io/secret-type": "repository"},
		},
		StringData: map[string]string{
			"type":     "git",
			"url":      "https://github.com/soloz-io/zero-ops",
			"username": "x-access-token",
			"password": token,
		},
	}
}

func seedDemoUser(ctx context.Context, client kubernetes.Interface, cfg *rest.Config) error {
	// Find kratos pod
	pods, err := client.CoreV1().Pods("ory-system").List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=kratos"})
	if err != nil || len(pods.Items) == 0 {
		return fmt.Errorf("kratos pod not found: %w", err)
	}
	podName := pods.Items[0].Name

	// Port-forward to kratos admin (4434)
	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return err
	}
	url := client.CoreV1().RESTClient().Post().
		Resource("pods").Name(podName).Namespace("ory-system").
		SubResource("portforward").URL()

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	defer close(stopCh)

	pf, err := portforward.New(
		spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url),
		[]string{"0:4434"}, stopCh, readyCh, nil, nil,
	)
	if err != nil {
		return err
	}
	go pf.ForwardPorts()
	<-readyCh

	ports, err := pf.GetPorts()
	if err != nil {
		return err
	}
	localPort := ports[0].Local

	// POST identity
	body, _ := json.Marshal(map[string]interface{}{
		"schema_id": "default",
		"traits":    map[string]string{"email": "demo@nutgraf.in", "role": "tenant_admin"},
		"credentials": map[string]interface{}{
			"password": map[string]interface{}{
				"config": map[string]string{"password": "Demo1Password!"},
			},
		},
	})
	resp, err := http.Post(fmt.Sprintf("http://localhost:%d/admin/identities", localPort), "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("seed user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusCreated {
		fmt.Println("  ✓ demo@nutgraf.in")
		return nil
	}
	return fmt.Errorf("seed user unexpected status: %d", resp.StatusCode)
}

func randomHex() string {
	b := make([]byte, 32)
	f, _ := os.Open("/dev/urandom")
	defer f.Close()
	f.Read(b)
	return fmt.Sprintf("%x", b)
}

var appGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
var certGVR = schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}

// skipApps are internal/infra apps not relevant to demo health
var skipApps = map[string]bool{"cert-manager-webhook-hetzner": true}

func waitForArgoCD(ctx context.Context, dyn dynamic.Interface) error {
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		list, err := dyn.Resource(appGVR).Namespace("argocd").List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		unhealthy := []string{}
		for _, item := range list.Items {
			name := item.GetName()
			if skipApps[name] {
				continue
			}
			health, _, _ := unstructuredString(item.Object, "status", "health", "status")
			sync, _, _ := unstructuredString(item.Object, "status", "sync", "status")
			if health != "Healthy" || sync != "Synced" {
				unhealthy = append(unhealthy, fmt.Sprintf("%s(%s/%s)", name, sync, health))
			}
		}
		if len(unhealthy) == 0 {
			fmt.Println("  ✓ all ArgoCD apps Synced/Healthy")
			return nil
		}
		fmt.Printf("  waiting: %v\n", unhealthy)
		time.Sleep(15 * time.Second)
	}
	return fmt.Errorf("timed out waiting for ArgoCD apps")
}

func waitForCerts(ctx context.Context, dyn dynamic.Interface) error {
	targets := []struct{ ns, name string }{
		{"api-gateway", "api-zero-ops-tls"},
		{"identity-services", "auth-zero-ops-tls"},
		{"ory-system", "console-zero-ops-tls"},
	}
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		ready := 0
		for _, t := range targets {
			cert, err := dyn.Resource(certGVR).Namespace(t.ns).Get(ctx, t.name, metav1.GetOptions{})
			if err != nil {
				continue
			}
			conditions, ok := cert.Object["status"].(map[string]interface{})["conditions"].([]interface{})
			if !ok {
				continue
			}
			for _, c := range conditions {
				cm := c.(map[string]interface{})
				if cm["type"] == "Ready" && cm["status"] == "True" {
					fmt.Printf("  ✓ %s/%s\n", t.ns, t.name)
					ready++
				}
			}
		}
		if ready == len(targets) {
			return nil
		}
		fmt.Printf("  waiting for certs (%d/%d ready)\n", ready, len(targets))
		time.Sleep(15 * time.Second)
	}
	return fmt.Errorf("timed out waiting for TLS certificates")
}

func unstructuredString(obj map[string]interface{}, keys ...string) (string, bool, error) {
	cur := obj
	for i, k := range keys {
		if i == len(keys)-1 {
			v, ok := cur[k].(string)
			return v, ok, nil
		}
		next, ok := cur[k].(map[string]interface{})
		if !ok {
			return "", false, nil
		}
		cur = next
	}
	return "", false, nil
}
