package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/docker"
)

var listJSONFlag bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all containers currently watched by ErrorProbe",
	Long: `List all Docker containers that match the current watch policy defined in
errorprobe.yaml, showing their names, images, infra status, and watch status.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		cli, err := docker.NewClient()
		if err != nil {
			return fmt.Errorf("errorprobe stack is not running or Docker is unreachable: %w", err)
		}
		defer cli.Close()

		containers, err := discovery.ListRunning(cmd.Context(), cli)
		if err != nil {
			return fmt.Errorf("listing containers: %w", err)
		}

		approved := discovery.ApplyPolicy(containers, cfg)

		// Load persisted watch set to determine watch status.
		stateFile := cfg.StateDir() + "containers.json"
		ws, err := discovery.LoadWatchSet(stateFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load watch state from %s: %v\n", stateFile, err)
			ws = discovery.WatchSet{}
		}
		watched := make(map[string]bool, len(ws.Containers))
		for _, c := range ws.Containers {
			watched[c.ID] = true
		}

		if listJSONFlag {
			type jsonContainer struct {
				discovery.ContainerMeta
				Watching bool `json:"Watching"`
			}
			out := make([]jsonContainer, len(approved))
			for i, c := range approved {
				out[i] = jsonContainer{ContainerMeta: c, Watching: watched[c.ID]}
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "CONTAINER\tIMAGE\tINFRA STATUS\tWATCHING")
		for _, c := range approved {
			watching := "no"
			if watched[c.ID] {
				watching = "yes"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Name, c.Image, c.InfraStatus, watching)
		}
		return tw.Flush()
	},
}

func init() {
	listCmd.Flags().BoolVar(&listJSONFlag, "json", false, "output as JSON array")
}