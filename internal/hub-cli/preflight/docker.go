package preflight

import (
	"context"
	"os/exec"

	"github.com/soloz-io/zero-ops/internal/hub-cli/errors"
)

// DockerValidator checks if Docker daemon is running
type DockerValidator struct{}

func (v *DockerValidator) Validate(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "ps")
	if err := cmd.Run(); err != nil {
		return errors.DockerNotRunning(err)
	}
	return nil
}
