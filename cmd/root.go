// Package cmd contains all Cobra command definitions for errorprobe.
package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/logger"
)

var (
	cfgFile   string
	debugMode bool
	logFmt    string
)

var rootCmd = &cobra.Command{
	Use:          "errorprobe",
	Short:        "Real-time error detection for Docker containers",
	SilenceUsage: true,
	SilenceErrors: true,
	Long: `ErrorProbe monitors your local Docker containers for errors in real time.
It manages Vector, Loki, and Grafana as containers it owns, producing a
semantic health signal — no manual infrastructure setup required.`,
	Version: Version,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if err := config.EnsureDirs(cfg); err != nil {
			return fmt.Errorf("initialising state directories: %w", err)
		}
		if err := logger.Init(cfg.LogsDir()+"errorprobe.log", 10, 5); err != nil {
			return fmt.Errorf("initialising logger: %w", err)
		}
		logger.SetDebug(debugMode)
		switch logFmt {
		case "json":
			logger.SetFormat(logger.FormatJSON)
		default:
			logger.SetFormat(logger.FormatText)
		}
		logger.Info("errorprobe started", "command", cmd.Name())
		if debugMode {
			logger.Debug("debug mode enabled")
		}
		return nil
	},
}

// silentError is returned when the failure has already been displayed to the
// user. PrintError skips it so the message is not printed a second time.
type silentError struct{}

func (silentError) Error() string { return "" }

// Execute runs the root command.
func Execute() error {
	enableListVTP() // enable ANSI on both stdout and stderr before any output
	return rootCmd.Execute()
}

// PrintError prints err to stderr in red. Called by main() after Execute returns an error.
func PrintError(err error) {
	var silent silentError
	if errors.As(err, &silent) {
		return // already displayed
	}
	fmt.Fprintf(os.Stderr, "\033[91mError: %v\033[0m\n", err)
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "path to config file (default: ./errorprobe.yaml or ~/.errorprobe/config.yaml)")
	rootCmd.PersistentFlags().BoolVar(&debugMode, "debug", false, "enable verbose debug logging")
	rootCmd.PersistentFlags().StringVar(&logFmt, "log-format", "text", "log output format: text or json")
	rootCmd.Version = versionString()
	rootCmd.SetVersionTemplate("errorprobe {{.Version}}\n")
	cleanupUpgradeArtifacts()

	rootCmd.AddCommand(
		upCmd,
		downCmd,
		restartCmd,
		reloadCmd,
		updateCmd,
		listCmd,
		statusCmd,
		watchCmd,
		logsCmd,
		checkCmd,
		versionCmd,
		upgradeCmd,
	)
}

func exitIfErr(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
