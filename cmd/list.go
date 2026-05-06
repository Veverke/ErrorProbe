package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/k8s"
)

var (
	listJSONFlag    bool
	listDetailsFlag bool
	listCompactFlag bool
	listRuntimeFlag string
)

type tableCol struct {
	header string
	width  int
}

type tableRow struct {
	cells []string
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all containers currently watched by ErrorProbe",
	Long: `List all Docker and/or Kubernetes containers that match the current watch
policy defined in errorprobe.yaml, showing runtime, names, images, infra status,
and watch status.

Use --runtime docker or --runtime k8s to filter by runtime.`,
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

		dockerContainers, err := discovery.ListRunning(cmd.Context(), cli)
		if err != nil {
			return fmt.Errorf("listing docker containers: %w", err)
		}

		// Attempt K8s discovery; silently skip if unavailable.
		var k8sContainers []discovery.ContainerMeta
		if k8cCli, k8sErr := k8s.NewClient(""); k8sErr == nil {
			var k8sDiscErr error
			k8sContainers, k8sDiscErr = discovery.ListRunningK8s(cmd.Context(), k8cCli, cfg)
			if k8sDiscErr != nil {
				fmt.Fprintf(os.Stderr, "warning: K8s discovery failed: %v\n", k8sDiscErr)
			}
		}

		merged := discovery.MergeContainers(dockerContainers, k8sContainers)
		approved := discovery.ApplyPolicy(merged, cfg)

		// Validate and apply --runtime filter.
		if listRuntimeFlag != "" {
			if listRuntimeFlag != "docker" && listRuntimeFlag != "k8s" {
				return fmt.Errorf("unknown --runtime %q: must be \"docker\" or \"k8s\"", listRuntimeFlag)
			}
			filtered := approved[:0]
			for _, c := range approved {
				if c.Runtime == listRuntimeFlag {
					filtered = append(filtered, c)
				}
			}
			approved = filtered
		}

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

		if listDetailsFlag {
			return printListDetails(approved, watched)
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

		cols := []tableCol{
			{"RUNTIME", 7},
			{"CONTAINER", 28},
			{"POD", 28},
			{"NAMESPACE", 16},
			{"STATUS", 11},
			{"WATCHING", 8},
		}

		rows := make([]tableRow, 0, len(approved))
		for _, c := range approved {
			watching := "no"
			if watched[c.ID] {
				watching = "yes"
			}
			name := c.Name
			pod := c.Pod
			ns := c.Namespace
			if listCompactFlag {
				name = compactContainerName(name)
				pod = compactPodName(pod)
			}
			rows = append(rows, tableRow{cells: []string{
				c.Runtime, name, pod, ns, c.InfraStatus, watching,
			}})
		}

		if listCompactFlag {
			// Drop columns where all non-empty values are identical (except STATUS and WATCHING).
			const alwaysKeep = "STATUS|WATCHING"
			keptCols := cols[:0:len(cols)]
			keptIdxs := make([]int, 0, len(cols))
			for ci, col := range cols {
				if strings.Contains(alwaysKeep, col.header) {
					keptCols = append(keptCols, col)
					keptIdxs = append(keptIdxs, ci)
					continue
				}
				distinct := map[string]struct{}{}
				for _, row := range rows {
					if v := row.cells[ci]; v != "" {
						distinct[v] = struct{}{}
					}
				}
				if len(distinct) > 1 {
					keptCols = append(keptCols, col)
					keptIdxs = append(keptIdxs, ci)
				}
			}
			// Rebuild rows with only kept columns.
			for ri := range rows {
				newCells := make([]string, len(keptIdxs))
				for ni, ci := range keptIdxs {
					newCells[ni] = rows[ri].cells[ci]
				}
				rows[ri].cells = newCells
			}
			cols = keptCols
		}

		printTable(cols, rows)
		return nil
	},
}

// printListDetails prints the container → image → volume breakdown.
func printListDetails(containers []discovery.ContainerMeta, watched map[string]bool) error {
	const indent = "  "
	const rule = "────────────────────────────────────────────────────"

	for i, c := range containers {
		watchMark := "watching"
		if !watched[c.ID] {
			watchMark = "not watching"
		}
		fmt.Printf("%s  [%s]  runtime=%s\n", c.Name, watchMark, c.Runtime)
		fmt.Printf("%simage:   %s\n", indent, c.Image)
		fmt.Printf("%sstatus:  %s\n", indent, c.InfraStatus)
		if c.Runtime == "k8s" {
			fmt.Printf("%spod:       %s\n", indent, c.Pod)
			fmt.Printf("%snamespace: %s\n", indent, c.Namespace)
			fmt.Printf("%snode:      %s\n", indent, c.Node)
		} else {
			if len(c.Mounts) == 0 {
				fmt.Printf("%svolumes: (none)\n", indent)
			} else {
				fmt.Printf("%svolumes:\n", indent)
				for _, m := range c.Mounts {
					fmt.Printf("%s%s  %s\n", indent+indent, mountLabel(m), mountArrow(m))
				}
			}
		}

		if i < len(containers)-1 {
			fmt.Println(rule)
		}
	}
	return nil
}

// mountLabel returns the human-readable type tag for a mount.
func mountLabel(m discovery.MountInfo) string {
	switch m.Type {
	case "volume":
		if m.Name != "" {
			return fmt.Sprintf("[volume: %s]", m.Name)
		}
		return "[volume: anonymous]"
	case "bind":
		return "[bind]"
	case "tmpfs":
		return "[tmpfs]"
	default:
		return fmt.Sprintf("[%s]", m.Type)
	}
}

// mountArrow returns the "source → destination (ro/rw)" string for a mount.
func mountArrow(m discovery.MountInfo) string {
	src := m.Source
	if src == "" {
		src = "(managed by docker)"
	}
	rw := "rw"
	if m.ReadOnly {
		rw = "ro"
	}
	return strings.Join([]string{src, "→", m.Destination}, " ") + "  (" + rw + ")"
}

func init() {
	listCmd.Flags().BoolVar(&listJSONFlag, "json", false, "output as JSON array")
	listCmd.Flags().BoolVar(&listDetailsFlag, "details", false, "show image and volume breakdown per container")
	listCmd.Flags().BoolVar(&listCompactFlag, "compact", false, "compact output: shorten names and drop uniform columns")
	listCmd.Flags().StringVar(&listRuntimeFlag, "runtime", "", "filter by runtime: docker or k8s")
	_ = listCmd.RegisterFlagCompletionFunc("runtime", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"docker", "k8s"}, cobra.ShellCompDirectiveNoFileComp
	})
}

// colorStatus wraps s with ANSI escape codes for the given infra status.
// enableListVTP must be called before output reaches the terminal.
func colorStatus(s string) string {
	const reset = "\033[0m"
	switch s {
	case "running":
		return "\033[92m" + s + reset // bright green
	case "restarting", "pending", "waiting":
		return "\033[93m" + s + reset // bright yellow
	case "unknown":
		return "\033[2m" + s + reset // dim
	default:
		return "\033[91m" + s + reset // bright red
	}
}

// padCell pads or truncates s to exactly w visible runes, adding … on overflow.
func padCell(s string, w int) string {
	runes := []rune(s)
	if len(runes) > w {
		return string(runes[:w-1]) + "…"
	}
	return s + strings.Repeat(" ", w-utf8.RuneCountInString(s))
}

// printTable prints a bordered ASCII table to stdout.
func printTable(cols []tableCol, rows []tableRow) {
	enableListVTP()

	sep := func(left, mid, right, fill string) string {
		parts := make([]string, len(cols))
		for i, c := range cols {
			parts[i] = strings.Repeat(fill, c.width+2)
		}
		return left + strings.Join(parts, mid) + right
	}

	fmt.Println(sep("+", "+", "+", "-"))
	headerCells := make([]string, len(cols))
	for i, c := range cols {
		headerCells[i] = " " + padCell(c.header, c.width) + " "
	}
	fmt.Println("|" + strings.Join(headerCells, "|") + "|")
	fmt.Println(sep("+", "+", "+", "="))

	for _, row := range rows {
		cells := make([]string, len(cols))
		for i, col := range cols {
			v := ""
			if i < len(row.cells) {
				v = row.cells[i]
			}
			if col.header == "STATUS" {
				// Color the raw value, then pad to column width using plain-text length.
				colored := colorStatus(v)
				pad := col.width - utf8.RuneCountInString(v)
				if pad < 0 {
					pad = 0
				}
				cells[i] = " " + colored + strings.Repeat(" ", pad) + " "
			} else {
				cells[i] = " " + padCell(v, col.width) + " "
			}
		}
		fmt.Println("|" + strings.Join(cells, "|") + "|")
	}
	fmt.Println(sep("+", "+", "+", "-"))
}

var reContainerSuffix = regexp.MustCompile(`^(.*)-[a-z0-9]{5}$`)
var rePodHash = regexp.MustCompile(`^(.*)-[a-z0-9]{5,10}-[a-z0-9]{5}$`)

// compactContainerName strips a trailing 5-char operator suffix (e.g. "-abcde").
func compactContainerName(name string) string {
	if m := reContainerSuffix.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return name
}

// compactPodName strips the deployment hash from a pod name.
func compactPodName(pod string) string {
	if m := rePodHash.FindStringSubmatch(pod); m != nil {
		return m[1]
	}
	return pod
}