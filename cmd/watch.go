package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/tui"
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Stream real-time health events in an interactive terminal UI",
	Long: `Open an interactive Bubbletea terminal UI that streams health state changes
for all watched containers in real time, updating as new log events arrive.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		snapshotPath := cfg.StateDir() + "health.json"
		watchSetPath := cfg.StateDir() + "containers.json"

		// Require the stack to be running: health.json must exist.
		if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
			return fmt.Errorf("health snapshot not found — is `errorprobe up` running?\nExpected: %s", snapshotPath)
		}

		snap, err := health.LoadSnapshot(snapshotPath)
		if err != nil {
			return fmt.Errorf("loading health snapshot: %w", err)
		}

		ws, err := discovery.LoadWatchSet(watchSetPath)
		if err != nil {
			return fmt.Errorf("loading watch set: %w", err)
		}

		model := tui.NewModel(snapshotPath, watchSetPath, snap, ws)
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("watch TUI: %w", err)
		}
		return nil
	},
}
