package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var errorsOnly bool

var logsCmd = &cobra.Command{
	Use:   "logs <container>",
	Short: "Stream log output for a specific watched container",
	Long: `Stream log output for the named container from Loki. Use --errors-only to
restrict output to log lines classified as ERROR or FATAL severity.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("logs: not implemented")
		return nil
	},
}

func init() {
	logsCmd.Flags().BoolVar(&errorsOnly, "errors-only", false, "show only ERROR and FATAL log lines")
}
