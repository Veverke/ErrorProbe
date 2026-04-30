package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/stack"
)

var checkJSONOutput bool

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Exit non-zero if any watched container exceeds the configured fail_on threshold",
	Long: `Read the persisted health snapshot and exit with a non-zero status code if any
watched container has reached or exceeded the health state configured in fail_on
(default: HAS_ERRORS). Designed for use in CI pipelines and test scripts.

fail_on values:
  HAS_ERRORS  exit 1 when any container has state HAS_ERRORS or FAILING (default)
  FAILING     exit 1 only when a container has state FAILING

NOTE: FAILING state requires V2 Tier 2 detection and is not reachable in V1.
Under fail_on=FAILING, containers in HAS_ERRORS state will pass the check.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		cli, err := docker.NewClient()
		if err != nil {
			return fmt.Errorf("connecting to docker: %w", err)
		}
		defer cli.Close()

		running, err := stack.IsStackRunning(context.Background(), cfg, cli)
		if err != nil {
			return fmt.Errorf("checking stack: %w", err)
		}
		if !running {
			fmt.Fprintln(os.Stderr, "errorprobe stack is not running — run 'errorprobe up' first")
			os.Exit(1)
		}

		snap, err := health.LoadSnapshot(cfg.StateDir() + "health.json")
		if err != nil {
			return fmt.Errorf("loading health snapshot: %w", err)
		}

		ok, failing, err := evalCheck(snap, cfg.Check)
		if err != nil {
			return fmt.Errorf("evaluating check: %w", err)
		}

		if checkJSONOutput {
			if err := writeCheckJSON(os.Stdout, ok, failing); err != nil {
				return fmt.Errorf("writing JSON output: %w", err)
			}
		} else if ok {
			fmt.Println("All containers healthy")
		} else {
			for _, f := range failing {
				at := ""
				if f.LastErrorAt != nil {
					at = "  at=" + f.LastErrorAt.Local().Format("2006-01-02 15:04:05")
				}
				fmt.Fprintf(os.Stderr, "  %s  state=%s  last_error=%q%s\n", f.Name, f.State, f.LastErrorMsg, at)
			}
		}

		if !ok {
			os.Exit(1)
		}
		return nil
	},
}

// CheckResult is a single failing container entry.
type CheckResult struct {
	Name         string     `json:"name"`
	State        string     `json:"state"`
	LastErrorAt  *time.Time `json:"last_error_at,omitempty"`
	LastErrorMsg string     `json:"last_error_msg"`
}

// evalCheck evaluates the health snapshot against check settings and returns
// (ok bool, failing []CheckResult, err error).  It is a pure function — no I/O.
func evalCheck(snap health.HealthSnapshot, check config.Check) (bool, []CheckResult, error) {
	excluded := make(map[string]bool, len(check.Exclude))
	for _, name := range check.Exclude {
		excluded[name] = true
	}

	failOn := check.FailOn
	if failOn == "" {
		failOn = "HAS_ERRORS"
	}

	var failing []CheckResult
	for name, ch := range snap.Containers {
		if excluded[name] {
			continue
		}
		switch failOn {
		case "HAS_ERRORS":
			if ch.State == health.StateHasErrors || ch.State == health.StateFailing {
				failing = append(failing, CheckResult{
					Name:         name,
					State:        string(ch.State),
					LastErrorAt:  ch.LastErrorAt,
					LastErrorMsg: ch.LastErrorMsg,
				})
			}
		case "FAILING":
			if ch.State == health.StateFailing {
				failing = append(failing, CheckResult{
					Name:         name,
					State:        string(ch.State),
					LastErrorAt:  ch.LastErrorAt,
					LastErrorMsg: ch.LastErrorMsg,
				})
			}
		default:
			return false, nil, fmt.Errorf("unsupported fail_on value %q", failOn)
		}
	}
	return len(failing) == 0, failing, nil
}

func writeCheckJSON(w io.Writer, ok bool, failing []CheckResult) error {
	out := struct {
		OK      bool          `json:"ok"`
		Failing []CheckResult `json:"failing"`
	}{OK: ok, Failing: failing}
	if out.Failing == nil {
		out.Failing = []CheckResult{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func init() {
	checkCmd.Flags().BoolVar(&checkJSONOutput, "json", false, "output result as JSON")
}
