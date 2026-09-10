package main

import (
	"fmt"

	"github.com/soloz-io/zero-ops/internal/soloz-cli/teardown"
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
		Long: `Teardown a Hub Cluster by forcefully deleting all cloud infrastructure,
Kubernetes CAPI resources, Kind/Docker artifacts, and local state.`,
		PreRunE: validateTeardownFlags,
		RunE:    runTeardown,
	}

	cmd.Flags().StringVar(&clusterName, "name", "", "Hub Cluster name (required)")
	cmd.Flags().BoolVar(&teardownForce, "force", true, "Force deletion of resources (default true)")
	cmd.Flags().BoolVar(&teardownConfirm, "confirm", false, "Confirm deletion (required for safety)")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable verbose logging")

	cmd.MarkFlagRequired("name")

	return cmd
}

func validateTeardownFlags(cmd *cobra.Command, args []string) error {
	if clusterName == "" {
		return fmt.Errorf("--name is required")
	}

	if !teardownConfirm {
		return fmt.Errorf("teardown requires --confirm flag for safety")
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
