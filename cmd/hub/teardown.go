package main

import (
	"fmt"

	"github.com/soloz-io/zero-ops/internal/hub/teardown"
	"github.com/spf13/cobra"
)

var (
	teardownForce   bool
	teardownConfirm bool
)

func newTeardownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "teardown",
		Short: "Teardown a Hub Cluster",
		Long: `Teardown a Hub Cluster by deleting the Cluster resource and waiting
for CAPI/CAPH controllers to cascade delete all infrastructure resources.`,
		PreRunE: validateTeardownFlags,
		RunE:    runTeardown,
	}

	cmd.Flags().StringVar(&clusterName, "name", "", "Hub Cluster name (required)")
	cmd.Flags().BoolVar(&teardownForce, "force", false, "Force deletion via Hetzner API (use only if CAPI deletion stuck)")
	cmd.Flags().BoolVar(&teardownConfirm, "confirm", false, "Confirm force deletion (required with --force)")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable verbose logging")

	cmd.MarkFlagRequired("name")

	return cmd
}

func validateTeardownFlags(cmd *cobra.Command, args []string) error {
	if clusterName == "" {
		return fmt.Errorf("--name is required")
	}

	if teardownForce && !teardownConfirm {
		return fmt.Errorf("--force requires --confirm flag for safety")
	}

	return nil
}

func runTeardown(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	fmt.Printf("🗑️  Tearing down Hub Cluster: %s\n", clusterName)

	orchestrator := &teardown.Orchestrator{
		ClusterName: clusterName,
		Force:       teardownForce,
		Debug:       debug,
	}

	if err := orchestrator.Run(ctx); err != nil {
		return fmt.Errorf("teardown failed: %w", err)
	}

	fmt.Printf("\n✓ Cluster '%s' successfully torn down\n", clusterName)
	return nil
}
