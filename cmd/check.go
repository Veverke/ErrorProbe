package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/docker"
	"github.com/errorprobe/errorprobe/internal/health"
	"github.com/errorprobe/errorprobe/internal/stack"
)

var checkJSONOutput bool
var checkExplain bool

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Exit non-zero if any watched container exceeds the configured fail_on threshold",
	Long: `Read the persisted health snapshot and exit with a non-zero status code if any
watched container has reached or exceeded the health state configured in fail_on
(default: HAS_ERRORS). Designed for use in CI pipelines and test scripts.

fail_on values:
  HAS_ERRORS  exit 1 when any container has state HAS_ERRORS or FAILING (default)
  FAILING     exit 1 only when a container has state FAILING (Tier 2 detection required)`,
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

		if checkExplain {
			printExplain(snap)
			return nil
		}

		if checkJSONOutput {
			if err := writeCheckJSON(os.Stdout, ok, failing); err != nil {
				return fmt.Errorf("writing JSON output: %w", err)
			}
		} else if ok {
			enableListVTP()
			fmt.Println("\033[92m✓\033[0m All containers healthy")
		} else {
			enableListVTP()
			for _, f := range failing {
				var icon string
				if f.State == string(health.StateFailing) {
					icon = "\033[91m✗\033[0m"
				} else {
					icon = "\033[93m⚠\033[0m"
				}
				at := "—"
				if f.LastErrorAt != nil {
					at = f.LastErrorAt.Local().Format("2006-01-02 15:04:05")
				}
				fmt.Fprintf(os.Stderr, "%s  %-30s  %-15s  %s\n", icon, f.Name, f.State, at)
				if msg := checkHumanMsg(f.LastErrorMsg); msg != "" {
					fmt.Fprintf(os.Stderr, "   └─ %s\n", msg)
				}
			}
		}

		if !ok {
			os.Exit(1)
		}
		return nil
	},
}

// healthKeyDisplay returns the human-readable container name from a health key.
// For K8s compound keys ("namespace/container") this is the container part;
// for Docker bare-name keys this is the whole string.
func healthKeyDisplay(key string) string {
	if idx := strings.LastIndex(key, "/"); idx >= 0 {
		return key[idx+1:]
	}
	return key
}

// checkHumanMsg extracts the most readable part of a log message.
// Applies the same extraction strategy as the TUI's humanMsg:
//  1. logfmt:      msg="..." or message="..."
//  2. JSON:        {"error":"..."} / {"message":"..."} / {"reason":"..."} etc.
//  3. Erlang/OTP:  {reason_atom,[stacktrace]} with optional <<"name">> binaries
//  4. Java/Python: ExceptionClassName: message
//  5. fallback:    first ~120 runes
func checkHumanMsg(raw string) string {
	// 1. logfmt
	for _, prefix := range []string{`msg="`, `message="`} {
		if idx := strings.Index(raw, prefix); idx >= 0 {
			rest := raw[idx+len(prefix):]
			if end := strings.Index(rest, `"`); end >= 0 && end > 0 {
				return rest[:end]
			}
		}
	}
	// 2–4: structured detail
	if detail := checkExtractStructuredDetail(raw); detail != "" {
		return detail
	}
	// 5. fallback
	r := []rune(strings.TrimSpace(raw))
	if len(r) > 120 {
		return string(r[:119]) + "…"
	}
	return string(r)
}

// checkExtractStructuredDetail is the check-command equivalent of the TUI's
// extractStructuredDetail. It handles JSON, Erlang/OTP, and Java/Python errors.
func checkExtractStructuredDetail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// JSON object
	if strings.HasPrefix(s, "{") && strings.Contains(s, `"`) {
		for _, field := range []string{"error", "message", "reason", "msg", "err", "cause"} {
			for _, kv := range []string{`"` + field + `":"`, `"` + field + `": "`} {
				if idx := strings.Index(strings.ToLower(s), kv); idx >= 0 {
					rest := s[idx+len(kv):]
					if end := strings.IndexByte(rest, '"'); end > 0 {
						return rest[:end]
					}
				}
			}
		}
	}
	// Erlang/OTP tuple
	if strings.HasPrefix(s, "{") {
		if v := checkExtractErlangReason(s); v != "" {
			return v
		}
	}
	// Java/Python/Go: "identifier.Name: detail"
	if idx := strings.Index(s, ": "); idx > 0 && idx < 80 {
		if checkLooksLikeTypeName(s[:idx]) {
			r := []rune(s)
			if len(r) > 120 {
				return string(r[:119]) + "…"
			}
			return s
		}
	}
	return ""
}

func checkExtractErlangReason(s string) string {
	inner := s[1:]
	end := strings.IndexAny(inner, ",}")
	if end < 0 {
		return ""
	}
	atom := strings.TrimSpace(inner[:end])
	if !checkIsErlangAtom(atom) {
		return ""
	}
	var binaries []string
	rest := s
	for len(binaries) < 2 {
		bStart := strings.Index(rest, `<<"`)
		if bStart < 0 {
			break
		}
		bEnd := strings.Index(rest[bStart+3:], `">>`)
		if bEnd < 0 {
			break
		}
		val := rest[bStart+3 : bStart+3+bEnd]
		if val != "" {
			binaries = append(binaries, val)
		}
		rest = rest[bStart+3+bEnd+3:]
	}
	if len(binaries) == 0 {
		return atom
	}
	return atom + " (" + strings.Join(binaries, ", ") + ")"
}

func checkIsErlangAtom(s string) bool {
	if len(s) == 0 || len(s) > 64 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}

func checkLooksLikeTypeName(s string) bool {
	if len(s) == 0 || len(s) > 120 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_') {
			return false
		}
	}
	return true
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
	for key, ch := range snap.Containers {
		if excluded[key] || excluded[healthKeyDisplay(key)] {
			continue
		}
		switch failOn {
		case "HAS_ERRORS":
			if ch.State == health.StateHasErrors || ch.State == health.StateFailing {
				failing = append(failing, CheckResult{
					Name:         healthKeyDisplay(key),
					State:        string(ch.State),
					LastErrorAt:  ch.LastErrorAt,
					LastErrorMsg: ch.LastErrorMsg,
				})
			}
		case "FAILING":
			if ch.State == health.StateFailing {
				failing = append(failing, CheckResult{
					Name:         healthKeyDisplay(key),
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
	checkCmd.Flags().BoolVar(&checkExplain, "explain", false, "print which PBR rule last set the state for each container")
}

// printExplain prints, for each container in the snapshot, the matched rule name
// that last set its health state or "no rule matched — default applied".
func printExplain(snap health.HealthSnapshot) {
	if len(snap.Containers) == 0 {
		fmt.Println("No containers tracked yet.")
		return
	}
	// Collect and sort keys for deterministic output.
	keys := make([]string, 0, len(snap.Containers))
	for k := range snap.Containers {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, key := range keys {
		ch := snap.Containers[key]
		rule := ch.MatchedRule
		if rule == "" {
			rule = "no rule matched — default applied"
		}
		fmt.Printf("%-40s  %-15s  rule: %s\n", healthKeyDisplay(key), string(ch.State), rule)
	}
}

// sortStrings sorts a string slice in-place (stdlib sort to avoid extra import).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
