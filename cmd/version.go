package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version, Commit, and BuildDate are injected at build time via -ldflags:
//
//	-X github.com/errorprobe/errorprobe/cmd.Version=1.0.0
//	-X github.com/errorprobe/errorprobe/cmd.Commit=$(git rev-parse --short HEAD)
//	-X github.com/errorprobe/errorprobe/cmd.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// versionString returns the full version string printed by --version and the version subcommand.
func versionString() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, BuildDate)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, commit, and build date",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("errorprobe %s\n", versionString())
	},
}
