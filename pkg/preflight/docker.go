package preflight

import (
	"context"
	"fmt"
	"os/exec"
)

// DockerValidator checks if Docker daemon is running
type DockerValidator struct{}

func (v *DockerValidator) Validate(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "ps")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Docker daemon not running. Please start Docker")
	}
	return nil
}
