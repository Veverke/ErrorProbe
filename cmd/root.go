// Package cmd contains all Cobra command definitions for errorprobe.
package cmd

import (
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

// Version is injected at build time via -ldflags.
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "errorprobe",
	Short: "Real-time error detection for Docker containers",
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

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "path to config file (default: ./errorprobe.yaml or ~/.errorprobe/config.yaml)")
	rootCmd.PersistentFlags().BoolVar(&debugMode, "debug", false, "enable verbose debug logging")
	rootCmd.PersistentFlags().StringVar(&logFmt, "log-format", "text", "log output format: text or json")
	rootCmd.SetVersionTemplate("errorprobe {{.Version}}\n")

	rootCmd.AddCommand(
		upCmd,
		downCmd,
		reloadCmd,
		updateCmd,
		listCmd,
		statusCmd,
		watchCmd,
		logsCmd,
		checkCmd,
	)
}

func exitIfErr(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
