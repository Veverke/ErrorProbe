package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Stream real-time health events in an interactive terminal UI",
	Long: `Open an interactive Bubbletea terminal UI that streams health state changes
for all watched containers in real time, updating as new log events arrive.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("watch: not implemented")
		return nil
	},
}
