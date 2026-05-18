//go:build ignore

// check_coverage parses a Go coverage profile and verifies that the aggregate
// statement coverage of non-excluded packages meets the required threshold.
//
// Usage:
//
//	go run ./scripts/check_coverage.go <coverage.out> <threshold>
//
// Excluded packages (legitimately untestable without live infrastructure):
//   - cmd/ep, cmd/errorprobe  — main entry points; zero application logic
//   - internal/pid            — OS-level pid-file helper; no useful unit path
//   - internal/tui            — terminal UI renderer; requires a real terminal
//   - internal/k8s            — requires a live cluster or envtest setup
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// excludedSuffixes lists package path suffixes that are skipped when computing
// the adjusted coverage total. Add entries here only for packages that cannot
// be meaningfully unit-tested (entry points, live-infra dependencies, UI).
var excludedSuffixes = []string{
	"cmd/ep",
	"cmd/errorprobe",
	// The root cmd package wires cobra commands to live Docker/Loki/Grafana
	// infrastructure; it is excluded like the TUI for the same reason.
	"/cmd",
	"internal/pid",
	"internal/tui",
	"internal/k8s",
}

func isExcluded(pkg string) bool {
	for _, suffix := range excludedSuffixes {
		if strings.HasSuffix(pkg, suffix) || strings.Contains(pkg, suffix+"/") {
			return true
		}
	}
	return false
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: go run ./scripts/check_coverage.go <coverage.out> <threshold>\n")
		os.Exit(2)
	}
	profilePath := os.Args[1]
	threshold, err := strconv.ParseFloat(os.Args[2], 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid threshold %q: %v\n", os.Args[2], err)
		os.Exit(2)
	}

	f, err := os.Open(profilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "opening %s: %v\n", profilePath, err)
		os.Exit(1)
	}
	defer f.Close()

	// Coverage profile format (after the mode header):
	//   <file>:<startLine>.<startCol>,<endLine>.<endCol> <numStmt> <count>
	//
	// We accumulate (stmts, covered) per package, excluding excluded ones.
	type stats struct{ stmts, covered int }
	pkgStats := make(map[string]*stats)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}
		// Extract the file path (everything before the first colon that is
		// followed by a digit — the position part).
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		filePath := line[:colon]
		rest := line[colon+1:]

		// rest is "<start>,<end> <numStmt> <count>"
		// We only need numStmt and count.
		parts := strings.Fields(rest)
		if len(parts) < 2 {
			continue
		}
		// parts[0] is "<startLine>.<col>,<endLine>.<col>"
		numStmt, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		var count int
		if len(parts) >= 3 {
			count, _ = strconv.Atoi(parts[2])
		}

		// Derive the package path: everything before the last "/" component.
		slash := strings.LastIndex(filePath, "/")
		var pkg string
		if slash >= 0 {
			pkg = filePath[:slash]
		} else {
			pkg = filePath
		}

		if isExcluded(pkg) {
			continue
		}

		if pkgStats[pkg] == nil {
			pkgStats[pkg] = &stats{}
		}
		pkgStats[pkg].stmts += numStmt
		if count > 0 {
			pkgStats[pkg].covered += numStmt
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "reading profile: %v\n", err)
		os.Exit(1)
	}

	totalStmts, totalCovered := 0, 0
	for _, s := range pkgStats {
		totalStmts += s.stmts
		totalCovered += s.covered
	}

	if totalStmts == 0 {
		fmt.Fprintln(os.Stderr, "coverage profile contains no statements")
		os.Exit(1)
	}

	pct := float64(totalCovered) / float64(totalStmts) * 100
	fmt.Printf("Coverage (excluding infra packages): %.1f%%  (threshold: %.0f%%)\n", pct, threshold)

	if pct < threshold {
		fmt.Fprintf(os.Stderr,
			"\nERROR: coverage %.1f%% is below the required %.0f%% threshold.\n"+
				"Run 'go test -coverprofile=coverage.out -covermode=atomic ./internal/... ./cmd/...'\n"+
				"to see which packages need more tests.\n",
			pct, threshold)
		os.Exit(1)
	}
}
