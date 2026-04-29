package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/health"
)

var (
	statusJSON  bool
	statusReset string
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the current health status of all watched containers",
	Long: `Display the current health state (OK / HAS_ERRORS / FAILING) for each
container watched by ErrorProbe, along with the last seen error timestamp.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		snapshotPath := cfg.StateDir() + "health.json"
		snap, err := health.LoadSnapshot(snapshotPath)
		if err != nil {
			return fmt.Errorf("loading health snapshot: %w", err)
		}

		// Handle --reset flag: modify persisted snapshot directly, no IPC needed.
		if statusReset != "" {
			snap.Reset(statusReset)
			if err := health.SaveSnapshot(snapshotPath, snap); err != nil {
				return fmt.Errorf("saving health snapshot after reset: %w", err)
			}
			fmt.Printf("Reset health state for %q to OK\n", statusReset)
			return nil
		}

		// Load infra state from watch set.
		watchSetPath := cfg.StateDir() + "containers.json"
		ws, err := discovery.LoadWatchSet(watchSetPath)
		if err != nil {
			return fmt.Errorf("loading watch set: %w", err)
		}

		infraState := make(map[string]string, len(ws.Containers))
		for _, c := range ws.Containers {
			infraState[c.Name] = c.InfraStatus
		}

		if statusJSON {
			out, err := json.MarshalIndent(snap, "", "  ")
			if err != nil {
				return fmt.Errorf("marshalling snapshot: %w", err)
			}
			fmt.Println(string(out))
			return nil
		}

		// Build the full container list: union of health snapshot + watch set.
		names := make(map[string]struct{})
		for n := range snap.Containers {
			names[n] = struct{}{}
		}
		for _, c := range ws.Containers {
			names[c.Name] = struct{}{}
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "CONTAINER\tFUNCTIONAL\tINFRA\tERRORS\tLAST ERROR")

		for name := range names {
			ch := snap.Containers[name]
			funcState := formatFunctionalState(ch)
			infra := infraState[name]
			if infra == "" {
				infra = "unknown"
			}
			errors := "0"
			lastErr := "—"
			if ch.State == health.StateHasErrors {
				errors = fmt.Sprintf("%d", ch.ErrorCount)
				if ch.LastErrorAt != nil {
					lastErr = ch.LastErrorAt.Format("15:04") + " " + truncate(ch.LastErrorMsg, 30)
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", name, funcState, infra, errors, lastErr)
		}
		return w.Flush()
	},
}

func formatFunctionalState(ch health.ContainerHealth) string {
	switch ch.State {
	case health.StateHasErrors:
		return fmt.Sprintf("⚠ HAS ERRORS %d", ch.ErrorCount)
	default:
		return "✓ OK"
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "output full health snapshot as JSON")
	statusCmd.Flags().StringVar(&statusReset, "reset", "", "reset health state for the named container")
}
