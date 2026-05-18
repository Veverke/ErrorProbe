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
	"github.com/errorprobe/errorprobe/internal/learn"
	"github.com/errorprobe/errorprobe/internal/logger"
	"github.com/errorprobe/errorprobe/internal/loki"
	"github.com/errorprobe/errorprobe/internal/pbr"
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

		prog := newCmdProgress()
		onStatus := prog.OnStatus()

		if err := stack.Up(cmd.Context(), cfg, onStatus); err != nil {
			prog.Done()
			return fmt.Errorf("starting stack: %w", err)
		}
		prog.Done()
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
		if err := os.MkdirAll(cfg.StateDir(), 0o755); err != nil {
			logger.Error("could not create state dir", "err", err)
		} else if err := pid.Write(pidPath); err != nil {
			logger.Error("could not write pid file", "err", err)
		}
		defer pid.Remove(pidPath)

		// Load and validate PBR rules. Merge in any overlay (learned) rules first.
		// On error, report and abort so the user fixes the config before the stack starts.
		overlayRules, _ := learn.LoadOverlay(cfg.LearnOverlayFile())
		mergedRuleCfgs := learn.MergeOverlay(cfg.Rules, overlayRules)
		compiledRules, rulesErr := pbr.Load(mergedRuleCfgs, cfg.ContainerOverrides, pbr.BuiltinRules())
		if rulesErr != nil {
			return fmt.Errorf("invalid rules configuration: %w", rulesErr)
		}

		// Start health engine (loads persisted state if present).
		snapshotPath := cfg.StateDir() + "health.json"
		engine := health.NewEngine(snapshotPath, compiledRules, func(_ health.HealthSnapshot) {
			// onChange: snapshot persisted; nothing extra needed in foreground mode.
		})

		// Initialise the history log and prune old entries on startup.
		historyPath := cfg.StateDir() + "history.jsonl"
		historyLog := health.NewHistoryLog(historyPath)
		if retStr := cfg.HistoryRetention; retStr != "" {
			if retention, err := config.ParseDuration(retStr); err == nil {
				if err := historyLog.Prune(retention); err != nil {
					logger.Error("could not prune history log", "err", err)
				}
			} else {
				logger.Error("invalid history_retention value", "value", retStr, "err", err)
			}
		}

		// Start ingest HTTP transport wired to the engine.
		transport := ingest.NewHTTPTransport(ingestAddr)
		transport.OnBatch(engine.ProcessBatch)

		go func() {
			if err := transport.Start(ctx); err != nil {
				logger.Error("ingest transport stopped", "err", err)
			}
		}()

		// Start Tier 2 evaluator (FAILING state detection via Loki queries).
		lokiBase := fmt.Sprintf("http://127.0.0.1:%d", cfg.Stack.Loki.Port)
		lokiClient := loki.NewClient(lokiBase)
		tier2 := health.NewTier2Evaluator(lokiClient, cfg, engine, historyLog)
		go tier2.Run(ctx)

		gen := configgen.DefaultGenerator{}

		// Attempt K8s auto-detect; log result for the startup summary.
		var k8sClient k8s.K8sAPI
		k8cCli, k8sErr := k8s.NewClient("")
		if k8sErr == nil {
			k8sClient = k8cCli
			logger.Info("kubernetes cluster detected — K8s discovery enabled")

			// Apply Vector DaemonSet inside the cluster so it can read pod logs.
			vectorCfgTOML, cfgErr := configgen.VectorK8sConfig(cfg)
			if cfgErr != nil {
				logger.Error("could not render vector-k8s config", "err", cfgErr)
			} else if dsErr := k8cCli.ApplyVectorDaemonSet(ctx, cfg.Stack.Vector.Image, vectorCfgTOML); dsErr != nil {
				logger.Error("could not apply vector daemonset", "err", dsErr)
			} else {
				logger.Info("vector daemonset applied")
			}
		} else {
			logger.Info("kubernetes cluster not available — K8s discovery disabled")
		}

		reconciler := discovery.NewReconciler(cfg, cli, k8sClient, gen, func() {}, compiledRules)

		// Wire the learning module: shared channel for state-transition events.
		transitionCh := make(chan health.StateTransitionEvent, 64)
		engine.SetTransitionEvents(transitionCh)
		reconciler.SetTransitionEvents(transitionCh)

		// Build the learn-module reload callback (mirrors the SIGHUP reload).
		learnReload := func() {
			newCfg, cfgErr := config.Load(cfgFile)
			if cfgErr != nil {
				logger.Error("learn: reload config failed", "err", cfgErr)
				return
			}
			newOverlay, _ := learn.LoadOverlay(newCfg.LearnOverlayFile())
			newMerged := learn.MergeOverlay(newCfg.Rules, newOverlay)
			newRules, rulesErr := pbr.Load(newMerged, newCfg.ContainerOverrides, pbr.BuiltinRules())
			if rulesErr != nil {
				logger.Error("learn: invalid rules after reload", "err", rulesErr)
				return
			}
			engine.SetRules(newRules)
			reconciler.SetRules(newRules)
			logger.Info("PBR rules reloaded (learning module)")
		}

		applier := learn.NewApplier(
			cfg.LearnOverlayFile(),
			cfg.LearnPendingFile(),
			cfg.LearnSuppressionFile(),
			learnReload,
		)
		learnerSampler := learn.NewSampler(lokiClient)
		learner := learn.NewLearner(
			transitionCh,
			learnerSampler,
			applier,
			engine.Rules,
			cfg.Learn,
			cfg.LearnSuppressionFile(),
		)
		if cfg.Learn.Enabled {
			go learner.Run(ctx)
		}

		// Delete the state file so the first reconciler tick always regenerates
		// the Vector config. This is necessary because up.go writes an empty
		// include_containers list on startup; without this the reconciler would
		// skip regeneration if the container set hasn't changed since last run.
		_ = os.Remove(cfg.StateDir() + "containers.json")

		// Run the reconciler in a goroutine so we can also handle SIGHUP for
		// live PBR rule reloads (T7.3).
		hupc := listenReloadSignal()
		recErrCh := make(chan error, 1)
		go func() { recErrCh <- reconciler.Run(ctx) }()

		for {
			select {
			case err := <-recErrCh:
				return err
			case <-hupc:
				newCfg, cfgErr := config.Load(cfgFile)
				if cfgErr != nil {
					logger.Error("rule hot-reload: failed to load config", "err", cfgErr)
					continue
				}
				newOverlay, _ := learn.LoadOverlay(newCfg.LearnOverlayFile())
				newMerged := learn.MergeOverlay(newCfg.Rules, newOverlay)
				newRules, rulesErr := pbr.Load(newMerged, newCfg.ContainerOverrides, pbr.BuiltinRules())
				if rulesErr != nil {
					logger.Error("rule hot-reload: invalid rules — keeping old rules", "err", rulesErr)
					continue
				}
				engine.SetRules(newRules)
				reconciler.SetRules(newRules)
				logger.Info("PBR rules hot-reloaded")
			}
		}
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
