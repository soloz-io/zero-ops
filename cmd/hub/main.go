package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "hub",
		Short: "Hub Cluster Management",
		Long:  "CLI for bootstrapping and managing Hub (Management) Clusters on Hetzner Cloud",
	}

	rootCmd.AddCommand(newBootstrapCmd())
	rootCmd.AddCommand(newTeardownCmd())
	rootCmd.AddCommand(newSpokeCmd())
	rootCmd.AddCommand(newConfigureAWSSecretsManagerCmd())
	rootCmd.AddCommand(newConfigureGitHubAccessCmd())
	rootCmd.AddCommand(newConfigureTailscaleCmd())
	rootCmd.AddCommand(newInitSecretsCmd())
	rootCmd.AddCommand(newConfigureESOCmd())
	rootCmd.AddCommand(newReseedCmd())
	rootCmd.AddCommand(newTenantCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
