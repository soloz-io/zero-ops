package capi

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// SecretManager manages CAPI secrets
type SecretManager struct {
	Kubeconfig string
	Context    string
	Namespace  string
}

func (m *SecretManager) kubectlArgs(args ...string) []string {
	var result []string
	
	// Skip --kubeconfig if using default location (kubectl v1.34+ bug workaround)
	homeDir, _ := os.UserHomeDir()
	defaultKubeconfig := filepath.Join(homeDir, ".kube", "config")
	if m.Kubeconfig != "" && m.Kubeconfig != defaultKubeconfig {
		result = append(result, "--kubeconfig", m.Kubeconfig)
	}
	
	if m.Context != "" {
		result = append(result, "--context", m.Context)
	}
	return append(result, args...)
}

func (m *SecretManager) CreateHetznerSecret(ctx context.Context, token string) error {
	secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: hetzner-credentials
  namespace: %s
  labels:
    clusterctl.cluster.x-k8s.io/move: ""
stringData:
  hcloud: %s
`, m.Namespace, token)
	
	cmd := exec.CommandContext(ctx, "kubectl", m.kubectlArgs("apply", "-f", "-")...)
	cmd.Stdin = bytes.NewReader([]byte(secretYAML))
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create secret: %w\n%s", err, output)
	}
	
	return nil
}
