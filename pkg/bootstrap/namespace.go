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
	Namespace  string
}

func (m *NamespaceManager) Create(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", m.Kubeconfig,
		"create", "namespace", m.Namespace,
	)
	
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

