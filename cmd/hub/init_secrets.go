package main

import (
	"os"
	"path/filepath"

	"github.com/soloz-io/zero-ops/internal/hub-cli/components"
	"github.com/soloz-io/zero-ops/internal/hub-cli/infisical"
	"github.com/spf13/cobra"
)

var initSecretsKubeconfig string

func newInitSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "init-secrets",
		Aliases: []string{"bootstrap-secrets"},
		Short:   "Bootstrap Layer 1 immutable infrastructure secrets (Secret Zero)",
		RunE:    runInitSecrets,
	}

	cmd.Flags().StringVar(&initSecretsKubeconfig, "kubeconfig", "", "Path to kubeconfig (default: ~/.kube/config)")

	return cmd
}

func runInitSecrets(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	if initSecretsKubeconfig == "" {
		home, _ := os.UserHomeDir()
		initSecretsKubeconfig = filepath.Join(home, ".kube", "config")
	}

	installer := &components.Installer{
		Kubeconfig: initSecretsKubeconfig,
	}

	accessInfo := infisical.GetAccessInfo(ctx)
	defer printManualActions(accessInfo)

	return installer.RunInitSecrets(ctx)
}
