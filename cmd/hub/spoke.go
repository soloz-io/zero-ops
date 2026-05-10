package main

import (
	"fmt"

	"github.com/soloz-io/zero-ops/internal/hub-cli/spoke"
	"github.com/spf13/cobra"
)

var (
	spokeForce       bool
	spokeClusterName string
)

func newSpokeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "spoke",
		Short: "Manage Spoke Clusters",
		Long: `Manage and teardown Spoke Clusters provisioned via Zero-Ops.
This command works independently of the hub cluster and can delete spoke clusters
even when the hub cluster is no longer available.`,
	}

	// Add subcommands
	cmd.AddCommand(newSpokeTeardownCmd())

	return cmd
}

func newSpokeTeardownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "teardown",
		Short: "Teardown Spoke Clusters",
		Long: `Teardown Spoke Clusters by deleting resources directly via Hetzner API.
This discovers spoke clusters by examining server labels and names in Hetzner.`,
		PreRunE: validateSpokeTeardownFlags,
		RunE:    runSpokeTeardown,
	}

	cmd.Flags().StringVar(&spokeClusterName, "name", "", "Specific spoke cluster name to delete (optional, deletes all if not specified)")
	cmd.Flags().BoolVar(&spokeForce, "force", false, "Force deletion via Hetzner API (required since graceful deletion needs hub)")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable verbose logging")

	return cmd
}

func validateSpokeTeardownFlags(cmd *cobra.Command, args []string) error {
	// No required flags for spoke teardown - force is optional since graceful deletion isn't available
	return nil
}

func runSpokeTeardown(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	if spokeClusterName != "" {
		fmt.Printf("🗑️  Tearing down Spoke Cluster: %s\n", spokeClusterName)
	} else {
		fmt.Println("🗑️  Tearing down ALL Spoke Clusters")
	}

	orchestrator := &spoke.Orchestrator{
		ClusterName: spokeClusterName,
		Force:       spokeForce,
		Debug:       debug,
	}

	if err := orchestrator.Run(ctx); err != nil {
		return fmt.Errorf("spoke teardown failed: %w", err)
	}

	if spokeClusterName != "" {
		fmt.Printf("\n✓ Spoke Cluster '%s' successfully torn down\n", spokeClusterName)
	} else {
		fmt.Println("\n✓ All Spoke Clusters successfully torn down")
	}
	
	return nil
}