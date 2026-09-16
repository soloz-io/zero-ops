package main

import (
	"fmt"

	"github.com/soloz-io/zero-ops/internal/soloz-cli/teardown"
	"github.com/spf13/cobra"
)

var (
	teardownDNSOnly  bool
	teardownDNSOwner string
	teardownDNSZone  string
	teardownForce    bool
	teardownConfirm  bool
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
	// For a box that is already gone and left records behind. external-dns
	// ignores a record whose owner does not match, silently, so an orphan cannot
	// be reclaimed by any later box -- the hostname resolves to a decommissioned
	// address until someone deletes it.
	cmd.Flags().BoolVar(&teardownDNSOnly, "dns-only", false,
		"release this box's DNS records and nothing else")
	cmd.Flags().StringVar(&teardownDNSOwner, "dns-owner", "",
		"external-dns txt-owner-id whose records to release (default: read from the cluster)")
	cmd.Flags().StringVar(&teardownDNSZone, "dns-zone", "",
		"the zone those records live in, e.g. dev.nutgraf.in (default: read from the cluster)")

	// --name is required for a teardown, which destroys a named cluster. A
	// --dns-only run destroys no cluster and names records by owner instead.
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

	if teardownDNSOnly {
		fmt.Printf("🧹 Releasing DNS records owned by %q in %s\n", teardownDNSOwner, teardownDNSZone)
	} else {
		fmt.Printf("🗑️  Tearing down Hub Cluster: %s\n", clusterName)
	}

	orchestrator := &teardown.Orchestrator{
		ClusterName: clusterName,
		Force:       teardownForce,
		Debug:       debug,
		DNSOnly:     teardownDNSOnly,
		DNS: teardown.DNSOverride{
			Owner: teardownDNSOwner,
			Zone:  teardownDNSZone,
		},
	}

	if err := orchestrator.Run(ctx); err != nil {
		return fmt.Errorf("teardown failed: %w", err)
	}

	fmt.Printf("\n✓ Cluster '%s' successfully torn down\n", clusterName)
	return nil
}
