package mgmt

import "github.com/spf13/cobra"

// NewMgmtCmd creates the mgmt command
func NewMgmtCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mgmt",
		Short: "Management Cluster operations",
		Long:  "Commands for bootstrapping and managing the Management Cluster",
	}

	cmd.AddCommand(NewBootstrapCmd())
	cmd.AddCommand(NewTeardownCmd())

	return cmd
}
