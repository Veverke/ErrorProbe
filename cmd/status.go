package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the current health status of all watched containers",
	Long: `Display the current health state (OK / HAS_ERRORS / FAILING) for each
container watched by ErrorProbe, along with the last seen error timestamp.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("status: not implemented")
		return nil
	},
}
