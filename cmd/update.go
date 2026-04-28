package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Pull latest pinned images, regenerate configs, and restart the stack",
	Long: `Pull the latest versions of the pinned Vector, Loki, and Grafana images,
regenerate all configurations, and perform a rolling restart of the containers.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("update: not implemented")
		return nil
	},
}
