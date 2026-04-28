package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/stack"
)

var purgeFlag bool

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop and remove the observability stack containers",
	Long: `Stop and remove the Vector, Loki, and Grafana containers managed by ErrorProbe.
Named Docker volumes (log data, Grafana state) are preserved unless --purge is given.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		if err := stack.Down(cmd.Context(), cfg, purgeFlag); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	downCmd.Flags().BoolVar(&purgeFlag, "purge", false, "also remove data volumes (loki-data, grafana-data)")
}
