package bootstrap

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// NamespaceManager manages Kubernetes namespaces
type NamespaceManager struct {
	Kubeconfig string
	Context    string
	Namespace  string
}

func (m *NamespaceManager) Create(ctx context.Context) error {
	args := []string{"--kubeconfig", m.Kubeconfig}
	if m.Context != "" {
		args = append(args, "--context", m.Context)
	}
	args = append(args, "create", "namespace", m.Namespace)
	
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Ignore if already exists
		if strings.Contains(string(output), "already exists") ||
		   strings.Contains(string(output), "AlreadyExists") {
			return nil
		}
		return fmt.Errorf("failed to create namespace: %w\n%s", err, output)
	}
	
	return nil
}

