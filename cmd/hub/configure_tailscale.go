package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/soloz-io/zero-ops/internal/hub-cli/components"
	"github.com/spf13/cobra"
)

func newConfigureTailscaleCmd() *cobra.Command {
	var authkey string
	var hostname string
	var kubeconfig string

	cmd := &cobra.Command{
		Use:   "configure-tailscale",
		Short: "Configure Tailscale credentials for hybrid spoke control-plane nodes",
		Long: `Configure Tailscale credentials (Secret Zero) for the hybrid provider cell.

This command injects the Tailscale auth key + CP node hostname used by the
shared ClusterClass Tailscale pre-kubeadm hook (contentFrom.secret → the
tailscale-hybrid-psk Secret in platform-capi). It is part of the hybrid
bootstrap flow and must run before a hybrid spoke is provisioned.

Values are read from k8-secrets/tailscale/{authkey,hostname} (gitignored),
matching the hetzner/github Secret Zero pattern.

After running this command:
- hub-operator uploads tailscale-hybrid-psk to Infisical (CLISecretMappings)
- ESO syncs it back, so ArgoCD cannot reset it to empty
- Hybrid spoke CP nodes join the tailnet before kubeadm init`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if authkey == "" {
				return fmt.Errorf("--authkey is required (or set k8-secrets/tailscale/authkey)")
			}
			if hostname == "" {
				return fmt.Errorf("--hostname is required (or set k8-secrets/tailscale/hostname)")
			}

			// Set default kubeconfig if not provided
			if kubeconfig == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("failed to get home directory: %w", err)
				}
				kubeconfig = filepath.Join(home, ".kube", "config")
			}

			// Verify kubeconfig exists
			if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
				return fmt.Errorf("kubeconfig not found at %s", kubeconfig)
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Default values from k8-secrets (gitignored) if not explicitly set.
			if authkey == "" {
				authkey = readK8Secret("k8-secrets/tailscale/authkey")
			}
			if hostname == "" {
				hostname = readK8Secret("k8-secrets/tailscale/hostname")
			}

			return runConfigureTailscale(cmd, authkey, hostname, kubeconfig)
		},
	}

	cmd.Flags().StringVar(&authkey, "authkey", "", "Tailscale auth key (default: k8-secrets/tailscale/authkey)")
	cmd.Flags().StringVar(&hostname, "hostname", "", "Spoke CP tailnet hostname (default: k8-secrets/tailscale/hostname)")
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (default: ~/.kube/config)")

	return cmd
}

func runConfigureTailscale(cmd *cobra.Command, authkey, hostname, kubeconfig string) error {
	ctx := cmd.Context()

	fmt.Println("🔐 Configuring Tailscale credentials (Secret Zero)...")
	fmt.Printf("   Hostname: %s\n", hostname)

	installer := &components.Installer{
		Kubeconfig: kubeconfig,
	}

	if err := installer.InstallTailscalePSK(ctx, authkey, hostname); err != nil {
		return fmt.Errorf("failed to create tailscale-hybrid-psk secret: %w", err)
	}

	fmt.Println("\n✅ Tailscale credentials configured!")
	fmt.Println("\nNext steps:")
	fmt.Println("1. hub-operator uploads tailscale-hybrid-psk to Infisical on next reconcile")
	fmt.Println("2. ESO syncs it back (tailscale-authkey / tailscale-hostname)")
	fmt.Println("3. Delete any stuck hybrid spoke so CAPI recreates the CP with the pre-kubeadm join")
	fmt.Println("4. Run home-worker-join.sh on home nodes once the spoke CP is Ready")

	return nil
}

// readK8Secret reads a gitignored secret file, trimming whitespace. Returns ""
// if the file is missing.
func readK8Secret(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
