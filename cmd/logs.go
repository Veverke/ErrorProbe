package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/loki"
	"github.com/errorprobe/errorprobe/internal/stack"
)

var (
	errorsOnly bool
	logsSince  string
)

var logsCmd = &cobra.Command{
	Use:   "logs <container>",
	Short: "Stream log output for a specific watched container",
	Long: `Stream log output for the named container from Loki. Use --errors-only to
restrict output to log lines classified as ERROR or FATAL severity.
Use --since to set the lookback window (e.g. --since 30m). Defaults to 15 minutes.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		containerName := args[0]

		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		cli, err := docker.NewClient()
		if err != nil {
			return fmt.Errorf("connecting to docker: %w", err)
		}
		defer cli.Close()

		running, err := stack.IsStackRunning(context.Background(), cfg, cli)
		if err != nil {
			return fmt.Errorf("checking stack: %w", err)
		}
		if !running {
			fmt.Fprintln(os.Stderr, "errorprobe stack is not running — run 'errorprobe up' first")
			os.Exit(1)
		}

		// Parse --since duration.
		since := 15 * time.Minute
		if logsSince != "" {
			d, err := time.ParseDuration(logsSince)
			if err != nil {
				return fmt.Errorf("invalid --since value %q: %w", logsSince, err)
			}
			since = d
		}

		// Build LogQL query.
		query := fmt.Sprintf(`{container="%s"}`, containerName)
		if errorsOnly {
			patterns := cfg.Detection.SeverityPatterns.Error
			if len(patterns) > 0 {
				query += fmt.Sprintf(` |~ "(?i)(%s)"`, strings.Join(patterns, "|"))
			} else {
				query += ` |~ "(?i)error"`
			}
		}

		lokiBase := fmt.Sprintf("http://127.0.0.1:%d", cfg.Stack.Loki.Port)

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		return loki.NewClient(lokiBase).Tail(ctx, query, since, os.Stdout)
	},
}

func init() {
	logsCmd.Flags().BoolVar(&errorsOnly, "errors-only", false, "show only log lines matching configured error severity patterns")
	logsCmd.Flags().StringVar(&logsSince, "since", "", "lookback window (e.g. 30m, 1h); defaults to 15m")
}
