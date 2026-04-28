package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Exit non-zero if any watched container exceeds the configured fail_on threshold",
	Long: `Read the persisted health snapshot and exit with a non-zero status code if any
watched container has reached or exceeded the health state configured in fail_on
(default: HAS_ERRORS). Designed for use in CI pipelines.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("check: not implemented")
		return nil
	},
}
