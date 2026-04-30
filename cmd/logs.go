package cmd

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
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
			query += ` |= "error"`
		}

		lokiBase := fmt.Sprintf("http://127.0.0.1:%d", cfg.Stack.Loki.Port)
		startNS := fmt.Sprintf("%d", time.Now().Add(-since).UnixNano())

		params := url.Values{}
		params.Set("query", query)
		params.Set("limit", "100")
		params.Set("start", startNS)
		tailURL := fmt.Sprintf("%s/loki/api/v1/tail?%s", lokiBase, params.Encode())

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, tailURL, nil)
		if err != nil {
			return fmt.Errorf("building request: %w", err)
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("connecting to Loki tail endpoint: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("Loki returned status %s", resp.Status)
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			// Context cancellation on Ctrl+C — not an error.
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("reading log stream: %w", err)
		}
		return nil
	},
}

func init() {
	logsCmd.Flags().BoolVar(&errorsOnly, "errors-only", false, "show only log lines containing \"error\"")
	logsCmd.Flags().StringVar(&logsSince, "since", "", "lookback window (e.g. 30m, 1h); defaults to 15m")
}
