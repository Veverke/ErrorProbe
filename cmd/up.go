package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/stack"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Pull images, generate configs, and start the observability stack",
	Long: `Pull the pinned Vector, Loki, and Grafana images, generate configurations
into ~/.errorprobe/configs/, start the containers via the Docker API, and
health-poll until all services are live. Safe to run against an already-running stack.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		onStatus := func(msg string) {
			fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), msg)
		}

		if err := stack.Up(cmd.Context(), cfg, onStatus); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return nil
	},
}
