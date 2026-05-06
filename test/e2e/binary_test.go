//go:build integration

package e2e_test

// binary_test.go provides TestMain (builds the ep binary once for all command tests),
// and helpers shared by all subprocess-based command tests.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/health"
)

// epBinaryPath is the path to the compiled ep binary, set once in TestMain.
var epBinaryPath string

// TestMain builds the ep binary once and runs all tests in the package.
// Tests tagged //go:build integration in this package share the binary.
func TestMain(m *testing.M) {
	bin, err := buildEPBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: could not build ep binary: %v\n", err)
		os.Exit(2)
	}
	epBinaryPath = bin
	code := m.Run()
	_ = os.Remove(bin)
	os.Exit(code)
}

// buildEPBinary compiles cmd/ep into a temp file and returns its path.
func buildEPBinary() (string, error) {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	tmp, err := os.CreateTemp("", "ep-e2e-bin-*"+ext)
	if err != nil {
		return "", err
	}
	_ = tmp.Close()

	// go test runs with cwd = package dir (test/e2e); module root is ../../
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root := filepath.Join(cwd, "..", "..")

	cmd := exec.Command("go", "build", "-o", tmp.Name(), "./cmd/ep")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("go build failed: %w\noutput:\n%s", err, out)
	}
	return tmp.Name(), nil
}

// ---------------------------------------------------------------------------
// Subprocess helpers
// ---------------------------------------------------------------------------

// tempHome creates a temporary directory that acts as the user home dir for an
// ep subprocess. Pre-creates .errorprobe/{state,configs,logs}/ subdirectories.
// Registered t.Cleanup removes the whole tree after the test.
func tempHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "ep-e2e-home-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	for _, sub := range []string{"state", "configs", "logs"} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, ".errorprobe", sub), 0o755))
	}
	return home
}

// runEP invokes the compiled ep binary with the given args.
// home redirects HOME/USERPROFILE so ~/.errorprobe/ resolves to a temp location;
// pass "" to use the real home dir (required for full-stack tests).
// Returns stdout, stderr, and the process exit code.
func runEP(t *testing.T, home string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(epBinaryPath, args...)
	if home != "" {
		env := make([]string, 0, len(os.Environ())+2)
		env = append(env, os.Environ()...)
		// HOME for Unix, USERPROFILE for Windows (Go's os.UserHomeDir checks both).
		env = append(env, "HOME="+home, "USERPROFILE="+home)
		cmd.Env = env
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode()
		}
	}
	return outBuf.String(), errBuf.String(), 0
}

// runEPWithTimeout like runEP but kills the process after timeout.
// Designed for streaming commands (logs, watch) that run until interrupted.
func runEPWithTimeout(t *testing.T, home string, timeout time.Duration, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, epBinaryPath, args...)
	if home != "" {
		env := make([]string, 0, len(os.Environ())+2)
		env = append(env, os.Environ()...)
		env = append(env, "HOME="+home, "USERPROFILE="+home)
		cmd.Env = env
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return outBuf.String(), errBuf.String(), exitErr.ExitCode()
		}
	}
	return outBuf.String(), errBuf.String(), 0
}

// ---------------------------------------------------------------------------
// Snapshot / watch-set file helpers
// ---------------------------------------------------------------------------

// writeSnapshot marshals snap and writes it to <home>/.errorprobe/state/health.json.
func writeSnapshot(t *testing.T, home string, snap health.HealthSnapshot) {
	t.Helper()
	path := filepath.Join(home, ".errorprobe", "state", "health.json")
	data, err := json.Marshal(snap)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

// writeWatchSet marshals ws and writes it to <home>/.errorprobe/state/containers.json.
func writeWatchSet(t *testing.T, home string, ws discovery.WatchSet) {
	t.Helper()
	path := filepath.Join(home, ".errorprobe", "state", "containers.json")
	data, err := json.Marshal(ws)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

// snapOK returns a HealthSnapshot with a single container in the OK state.
func snapOK(name string) health.HealthSnapshot {
	return health.HealthSnapshot{
		Containers: map[string]health.ContainerHealth{
			name: {Name: name, State: health.StateOK},
		},
		SnapshotAt: time.Now(),
	}
}

// snapErrors returns a HealthSnapshot with a single HAS_ERRORS container.
func snapErrors(name, msg string, count int) health.HealthSnapshot {
	now := time.Now()
	ch := health.ContainerHealth{
		Name:         name,
		State:        health.StateHasErrors,
		ErrorCount:   count,
		LastErrorMsg: msg,
		FirstErrorAt: &now,
		LastErrorAt:  &now,
	}
	return health.HealthSnapshot{
		Containers: map[string]health.ContainerHealth{name: ch},
		SnapshotAt: now,
	}
}

// snapFailing returns a HealthSnapshot with a single FAILING container.
func snapFailing(name, fingerprint string, count int) health.HealthSnapshot {
	now := time.Now()
	ch := health.ContainerHealth{
		Name:                     name,
		State:                    health.StateFailing,
		ErrorCount:               count,
		LastErrorMsg:             fingerprint,
		DominantFingerprint:      fingerprint,
		DominantFingerprintCount: count,
		FirstErrorAt:             &now,
		LastErrorAt:              &now,
	}
	return health.HealthSnapshot{
		Containers: map[string]health.ContainerHealth{name: ch},
		SnapshotAt: now,
	}
}
