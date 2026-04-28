package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all containers currently watched by ErrorProbe",
	Long: `List all Docker containers that match the current watch policy defined in
errorprobe.yaml, showing their names, IDs, and current health state.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("list: not implemented")
		return nil
	},
}
