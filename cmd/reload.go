package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Re-read errorprobe.yaml and apply changes without a full stack restart",
	Long: `Re-read errorprobe.yaml, classify every changed field, and apply the minimum
necessary disruption: soft changes (severity patterns, exclusions) are applied
via Vector SIGHUP; hard changes (ports, images) recreate only the affected containers.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("reload: not implemented")
		return nil
	},
}
