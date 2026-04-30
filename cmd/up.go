package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/configgen"
	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/ingest"
	"github.com/errorprobe/errorprobe/internal/k8s"
	"github.com/errorprobe/errorprobe/internal/logger"
	"github.com/errorprobe/errorprobe/internal/pid"
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
			logger.Info(msg)
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

		// Quick initial container discovery to get the count for the ready banner.
		bind := cfg.Stack.Ingest.Bind
		if bind == "" {
			bind = "127.0.0.1"
		}
		port := cfg.Stack.Ingest.Port
		if port == 0 {
			port = 9099
		}
		ingestAddr := bind + ":" + strconv.Itoa(port)

		containers, err := discovery.ListRunning(ctx, cli)
		if err != nil {
			return fmt.Errorf("initial container discovery: %w", err)
		}
		watched := discovery.ApplyPolicy(containers, cfg)
		printReadyBanner(cfg, len(watched), ingestAddr)

		// Write PID file so 'ep down --purge' can locate and terminate us.
		pidPath := cfg.StateDir() + "ep.pid"
		if err := pid.Write(pidPath); err != nil {
			logger.Error("could not write pid file", "err", err)
		}
		defer pid.Remove(pidPath)

		// Start health engine (loads persisted state if present).
		snapshotPath := cfg.StateDir() + "health.json"
		engine := health.NewEngine(snapshotPath, func(_ health.HealthSnapshot) {
			// onChange: snapshot persisted; nothing extra needed in foreground mode.
		})

		// Start ingest HTTP transport wired to the engine.
		transport := ingest.NewHTTPTransport(ingestAddr)
		transport.OnBatch(engine.ProcessBatch)

		go func() {
			if err := transport.Start(ctx); err != nil {
				logger.Error("ingest transport stopped", "err", err)
			}
		}()

		gen := configgen.DefaultGenerator{}

		// Attempt K8s auto-detect; log result for the startup summary.
		var k8sClient k8s.K8sAPI
		k8cCli, k8sErr := k8s.NewClient("")
		if k8sErr == nil {
			k8sClient = k8cCli
			logger.Info("kubernetes cluster detected — K8s discovery enabled")
		} else {
			logger.Info("kubernetes cluster not available — K8s discovery disabled")
		}

		reconciler := discovery.NewReconciler(cfg, cli, k8sClient, gen, func() {})

		// Delete the state file so the first reconciler tick always regenerates
		// the Vector config. This is necessary because up.go writes an empty
		// include_containers list on startup; without this the reconciler would
		// skip regeneration if the container set hasn't changed since last run.
		_ = os.Remove(cfg.StateDir() + "containers.json")

		return reconciler.Run(ctx)
	},
}

// printBoxed prints msg surrounded by a plain-ASCII single-char box.
func printBoxed(msg string) {
	rule := strings.Repeat("=", len(msg)+6)
	fmt.Println(rule)
	fmt.Printf("|  %s  |\n", msg)
	fmt.Println(rule)
}

func printReadyBanner(cfg *config.Config, watchCount int, ingestAddr string) {
	fmt.Println()
	fmt.Printf("  ErrorProbe is ready — watching %d containers\n", watchCount)
	fmt.Printf("  Grafana  http://localhost:%d\n", cfg.Stack.Grafana.Port)
	fmt.Printf("  Loki     http://localhost:%d\n", cfg.Stack.Loki.Port)
	fmt.Printf("  Ingest   http://%s\n", ingestAddr)
	fmt.Println()
	printBoxed("Run 'ep watch' to monitor in real-time")
	printBoxed("Run 'ep check' to use in CI/scripts")
	fmt.Println()
}
