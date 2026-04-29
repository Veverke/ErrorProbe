package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/configgen"
	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/stack"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Pull images, generate configs, and start the observability stack",
	Long: `Pull the pinned Vector, Loki, and Grafana images, generate configurations
into ~/.errorprobe/configs/, start the containers via the Docker API, and
health-poll until all services are live. Safe to run against an already-running stack.

NOTE: errorprobe up runs in the foreground and continuously watches for container
changes. Use CTRL+C to stop. A --detach flag is planned for a future release.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		onStatus := func(msg string) {
			fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), msg)
		}

		if err := stack.Up(cmd.Context(), cfg, onStatus); err != nil {
			return fmt.Errorf("starting stack: %w", err)
		}

		// Start reconciler — stays running until SIGINT/SIGTERM.
		cli, err := docker.NewClient()
		if err != nil {
			return fmt.Errorf("connecting to docker for reconciler: %w", err)
		}
		defer cli.Close()

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		gen := configgen.DefaultGenerator{}
		reconciler := discovery.NewReconciler(cfg, cli, gen, func() {
			onStatus("container set changed — Vector config reloaded")
		})

		// Delete the state file so the first reconciler tick always regenerates
		// the Vector config. This is necessary because up.go writes an empty
		// include_containers list on startup; without this the reconciler would
		// skip regeneration if the container set hasn't changed since last run.
		_ = os.Remove(cfg.StateDir() + "containers.json")

		onStatus("watching for container changes… (press CTRL+C to stop)")
		return reconciler.Run(ctx)
	},
}
