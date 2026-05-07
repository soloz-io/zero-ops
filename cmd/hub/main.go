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
	rootCmd.AddCommand(newInitSecretsCmd())
	rootCmd.AddCommand(newConfigureESOCmd())
	rootCmd.AddCommand(newConfigureAWSSecretsManagerCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
