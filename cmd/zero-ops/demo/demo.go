package demo

import "github.com/spf13/cobra"

func NewDemoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Demo operations",
	}
	cmd.AddCommand(NewBootstrapCmd())
	return cmd
}
