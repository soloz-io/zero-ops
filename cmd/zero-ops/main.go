package main

import (
	"os"

	"github.com/soloz-io/zero-ops/cmd/zero-ops/demo"
	"github.com/soloz-io/zero-ops/cmd/zero-ops/mgmt"
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "zero-ops",
		Short: "Zero-Ops Platform CLI",
		Long:  "CLI for managing Zero-Ops Platform infrastructure and clusters",
	}

	rootCmd.AddCommand(mgmt.NewMgmtCmd())
	rootCmd.AddCommand(demo.NewDemoCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

