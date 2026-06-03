package bootstrap

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// KindManager manages Kind cluster lifecycle
type KindManager struct {
	ClusterName string
	ConfigPath  string // optional path to kind config (e.g., for extraPortMappings)
}

func (m *KindManager) Create(ctx context.Context) error {
	args := []string{"create", "cluster",
		"--name", m.ClusterName,
		"--wait", "2m",
	}
	if m.ConfigPath != "" {
		args = append(args, "--config", m.ConfigPath)
	}
	cmd := exec.CommandContext(ctx, "kind", args...)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kind create failed: %w\n%s", err, output)
	}
	
	return nil
}

func (m *KindManager) Delete(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "kind", "delete", "cluster",
		"--name", m.ClusterName,
	)
	return cmd.Run()
}

func (m *KindManager) Exists(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "kind", "get", "clusters")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	
	clusters := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, cluster := range clusters {
		if cluster == m.ClusterName {
			return true
		}
	}
	return false
}
