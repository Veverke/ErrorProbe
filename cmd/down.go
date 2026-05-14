package cmd

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/k8s"
	"github.com/errorprobe/errorprobe/internal/logger"
	"github.com/errorprobe/errorprobe/internal/pid"
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
			return err
		}
		prog := newCmdProgress()
		err = runDown(cmd.Context(), cfg, purgeFlag, prog)
		prog.DoneErr(err)
		return err
	},
}

// runDown executes the full down sequence (K8s daemonset + Docker stack) using
// the given progress printer. The caller must call prog.Done() after this returns.
func runDown(ctx context.Context, cfg *config.Config, purge bool, prog *cmdProgress) error {
	onStatus := prog.OnStatus()

	// Kill any running 'ep up' process FIRST — before touching Docker or K8s.
	// ep up holds the Docker named-pipe connection and the log file handle.
	// Killing it frees both before we start issuing API calls.
	pidPath := cfg.StateDir() + "ep.pid"
	res, _ := pid.KillRunning(pidPath)
	// Always sweep by name so that other ep subcommands (e.g. 'ep watch', 'ep logs')
	// that hold the log file open are also terminated, not just the ep up daemon
	// tracked by the pid file.
	_ = pid.KillByName("ep")
	logger.Debug("pre-down ep up kill", "pid_file_found", res.Found, "killed", res.Killed)

	// Best-effort: remove Vector DaemonSet from K8s cluster if available.
	if k8cCli, k8sErr := k8s.NewClient(""); k8sErr == nil {
		onStatus("removing vector daemonset…")
		k8sCtx, k8sCancel := context.WithTimeout(ctx, 60*time.Second)
		defer k8sCancel()
		if dsErr := k8cCli.DeleteVectorDaemonSet(k8sCtx); dsErr != nil {
			logger.Error("could not delete vector daemonset", "err", dsErr)
		}
	}

	return stack.Down(ctx, cfg, purge, onStatus)
}

func init() {
	downCmd.Flags().BoolVar(&purgeFlag, "purge", false, "also remove data volumes and ~/.errorprobe/ (full uninstall)")
}
