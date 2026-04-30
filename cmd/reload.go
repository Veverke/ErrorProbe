package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/configgen"
	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/stack"
)

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Re-read errorprobe.yaml and apply changes without a full stack restart",
	Long: `Re-read errorprobe.yaml, classify every changed field, and apply the minimum
necessary disruption: soft changes (severity patterns, exclusions, check settings) are
applied via Vector SIGHUP; hard changes (ports, images, ingest transport/bind) recreate
only the affected containers.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		current, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		cli, err := docker.NewClient()
		if err != nil {
			return fmt.Errorf("connecting to docker: %w", err)
		}
		defer cli.Close()

		ctx := context.Background()

		running, err := stack.IsStackRunning(ctx, current, cli)
		if err != nil {
			return fmt.Errorf("checking stack: %w", err)
		}
		if !running {
			return errors.New("errorprobe stack is not running — run 'errorprobe up' first")
		}

		// Load the previously saved config.
		prevCfg, err := loadSavedConfig(current.StateDir(), current)
		if err != nil {
			return fmt.Errorf("loading previous config state: %w", err)
		}

		cs := stack.ClassifyChanges(prevCfg, current)

		if !cs.HasSoft && !cs.HasHard {
			fmt.Println("No configuration changes detected")
			return saveConfig(current)
		}

		configsDir := current.ConfigsDir()

		// Load the persisted watch set to pass the current container list to Vector.
		ws, wsErr := discovery.LoadWatchSet(current.StateDir() + "containers.json")
		if wsErr != nil {
			fmt.Printf("warning: could not load container watch set: %v — regenerating with empty list\n", wsErr)
		}
		containerNames := make([]string, 0, len(ws.Containers))
		for _, c := range ws.Containers {
			containerNames = append(containerNames, c.Name)
		}

		// Apply soft changes first (regenerate Vector config + SIGHUP).
		if cs.HasSoft {
			if err := configgen.GenerateVector(current, configsDir, containerNames); err != nil {
				return fmt.Errorf("regenerating Vector config: %w", err)
			}
			if err := cli.SendSignal(ctx, stack.ContainerVector, "SIGHUP"); err != nil {
				return fmt.Errorf("sending SIGHUP to Vector: %w", err)
			}
			fmt.Printf("Soft changes applied (no restart required): %s\n",
				strings.Join(cs.SoftChanges, "; "))
		}

		// Apply hard changes: stop → remove → regenerate config → start.
		if cs.HasHard {
			fmt.Printf("Hard changes require container recreation: %s\n",
				strings.Join(cs.HardChanges, "; "))

			// Determine which containers are affected.
			affected := affectedContainers(prevCfg, current)

			// Regenerate all configs.
			if err := configgen.GenerateLoki(current, configsDir); err != nil {
				return fmt.Errorf("regenerating Loki config: %w", err)
			}
			if err := configgen.GenerateGrafanaDatasource(current, configsDir); err != nil {
				return fmt.Errorf("regenerating Grafana datasource: %w", err)
			}
			if err := configgen.GenerateVector(current, configsDir, containerNames); err != nil {
				return fmt.Errorf("regenerating Vector config: %w", err)
			}

			for _, name := range affected {
				fmt.Printf("  recreating %s…\n", name)
				if err := cli.StopContainer(ctx, name, 10); err != nil {
					return fmt.Errorf("stopping %s: %w", name, err)
				}
				if err := cli.RemoveContainer(ctx, name, false); err != nil {
					return fmt.Errorf("removing %s: %w", name, err)
				}
			}

			// Restart the full stack via upCore (it skips already-running containers).
			if err := stack.Up(ctx, current, func(msg string) { fmt.Println(" ", msg) }); err != nil {
				return fmt.Errorf("restarting stack: %w", err)
			}

			fmt.Printf("Hard changes applied (containers recreated): %s\n",
				strings.Join(cs.HardChanges, "; "))
		}

		return saveConfig(current)
	},
}

// affectedContainers returns the managed container names that need to be
// recreated given the diff between prev and curr.
func affectedContainers(prev, curr *config.Config) []string {
	var names []string
	if prev.Stack.Loki.Image != curr.Stack.Loki.Image || prev.Stack.Loki.Port != curr.Stack.Loki.Port {
		names = append(names, stack.ContainerLoki)
	}
	if prev.Stack.Grafana.Image != curr.Stack.Grafana.Image || prev.Stack.Grafana.Port != curr.Stack.Grafana.Port {
		names = append(names, stack.ContainerGrafana)
	}
	if prev.Stack.Vector.Image != curr.Stack.Vector.Image ||
		prev.Stack.Ingest.Port != curr.Stack.Ingest.Port ||
		prev.Stack.Ingest.Bind != curr.Stack.Ingest.Bind ||
		prev.Stack.Ingest.Transport != curr.Stack.Ingest.Transport {
		names = append(names, stack.ContainerVector)
	}
	return names
}

const savedConfigFile = "config.json"

func savedConfigPath(stateDir string) string {
	return filepath.Join(stateDir, savedConfigFile)
}

func saveConfig(cfg *config.Config) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	path := savedConfigPath(cfg.StateDir())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing config state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming config state: %w", err)
	}
	return nil
}

func loadSavedConfig(stateDir string, current *config.Config) (*config.Config, error) {
	path := savedConfigPath(stateDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No saved state — use current config as baseline so no changes are reported.
			return current, nil
		}
		return nil, fmt.Errorf("reading saved config: %w", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing saved config: %w", err)
	}
	return &cfg, nil
}
