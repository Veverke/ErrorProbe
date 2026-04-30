package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
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

		// Use LoadSnapshot which treats a missing file as an empty snapshot,
		// so `watch` works on a fresh run before the first state change is persisted.
		snap, err := health.LoadSnapshot(snapshotPath)
		if err != nil {
			return fmt.Errorf("loading health snapshot: %w", err)
		}

		ws, err := discovery.LoadWatchSet(watchSetPath)
		if err != nil {
			return fmt.Errorf("loading watch set: %w", err)
		}

		// Force TrueColor so ANSI styles render in VS Code's integrated terminal,
		// which fails the isatty check and causes lipgloss to auto-detect NoTTY.
		lipgloss.SetColorProfile(termenv.TrueColor)

		model := tui.NewModel(snapshotPath, watchSetPath, snap, ws)
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("watch TUI: %w", err)
		}
		return nil
	},
}