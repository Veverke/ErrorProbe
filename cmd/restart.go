package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/logger"
)

var restartPurgeFlag bool

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Stop the stack then bring it back up",
	Long: `Stop all ErrorProbe-managed containers (equivalent to 'ep down'), then
immediately start them again (equivalent to 'ep up').

If 'down' encounters an error you are prompted whether to proceed with 'up'
anyway; answering no exits with an error so the failure is visible in scripts.

Use --purge to wipe volumes and config before restarting (full clean restart).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return err
		}

		// ── phase 1: down ────────────────────────────────────────────────────
		fmt.Println()
		fmt.Println("  ── down ────────────────────────────────────────────────")
		prog := newCmdProgress()
		downErr := runDown(cmd.Context(), cfg, restartPurgeFlag, prog)
		prog.DoneErr(downErr)

		if downErr != nil {
			fmt.Printf("\n  \033[91m✗\033[0m down failed: %v\n\n", downErr)
			if !confirmContinue("  Continue with 'up' anyway? [y/N] ") {
				fmt.Println("  restart aborted")
				return silentError{}
			}
		}

		// ── phase 2: up ──────────────────────────────────────────────────────
		// runDown deletes ~/.errorprobe/ entirely, which removes the log
		// directory and leaves the lumberjack instance pointing at a gone path.
		// Re-create dirs and re-initialise the logger so that all log lines
		// from the up phase are written to the new file, not silently dropped.
		if err := config.EnsureDirs(cfg); err == nil {
			_ = logger.Init(cfg.LogsDir()+"errorprobe.log", 10, 5)
		}

		fmt.Println()
		fmt.Println("  ── up ──────────────────────────────────────────────────")
		return upCmd.RunE(cmd, args)
	},
}

// confirmContinue prints prompt, reads a line, and returns true only when the
// user explicitly types "y" or "Y". Any other input (including blank / Enter)
// is treated as "no".
func confirmContinue(prompt string) bool {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.TrimSpace(scanner.Text())
		return strings.EqualFold(answer, "y")
	}
	return false
}

func init() {
	restartCmd.Flags().BoolVar(&restartPurgeFlag, "purge", false, "also remove data volumes and config before restarting (full clean restart)")
}
