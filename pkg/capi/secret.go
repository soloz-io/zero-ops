package capi

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// SecretManager manages CAPI secrets
type SecretManager struct {
	Kubeconfig string
	Namespace  string
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
	
	cmd := exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", m.Kubeconfig,
		"-f", "-",
	)
	cmd.Stdin = bytes.NewReader([]byte(secretYAML))
	
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create secret: %w\n%s", err, output)
	}
	
	return nil
}
