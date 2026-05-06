//go:build integration

package e2e_test

// cmd_status_test.go exercises every flag of the `ep status` command via
// subprocess with a redirected home dir so state files go to a temp location.
// No running EP stack is required — the command reads from the health snapshot
// and containers.json files directly.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/discovery"
	"github.com/errorprobe/errorprobe/internal/health"
)

// ---------------------------------------------------------------------------
// TestCmd_Status_AllOK (#28)
// ---------------------------------------------------------------------------

// TestCmd_Status_AllOK verifies the default table output shows "✓ OK" for a
// container whose snapshot state is OK.
func TestCmd_Status_AllOK(t *testing.T) {
	home := tempHome(t)
	writeSnapshot(t, home, snapOK("payments-api"))

	stdout, _, exitCode := runEP(t, home, "status")

	require.Equal(t, 0, exitCode)
	assert.Contains(t, stdout, "payments-api")
	assert.Contains(t, stdout, "OK", "table must show OK state")
}

// ---------------------------------------------------------------------------
// TestCmd_Status_HAS_ERRORS (#29)
// ---------------------------------------------------------------------------

// TestCmd_Status_HAS_ERRORS verifies the table row shows "HAS ERRORS" and the
// error count and message excerpt for a container in the HAS_ERRORS state.
func TestCmd_Status_HAS_ERRORS(t *testing.T) {
	home := tempHome(t)
	writeSnapshot(t, home, snapErrors("user-service", "timeout connecting to db", 3))

	stdout, _, exitCode := runEP(t, home, "status")

	require.Equal(t, 0, exitCode)
	assert.Contains(t, stdout, "user-service")
	assert.Contains(t, stdout, "HAS ERRORS", "table must show HAS ERRORS state")
	assert.Contains(t, stdout, "3", "error count must appear in the row")
}

// ---------------------------------------------------------------------------
// TestCmd_Status_FAILING (#30)
// ---------------------------------------------------------------------------

// TestCmd_Status_FAILING verifies the table shows "FAILING" and the dominant
// fingerprint repeat-count line below the container row.
func TestCmd_Status_FAILING(t *testing.T) {
	home := tempHome(t)
	writeSnapshot(t, home, snapFailing("auth-svc", "connection refused", 15))

	stdout, _, exitCode := runEP(t, home, "status")

	require.Equal(t, 0, exitCode)
	assert.Contains(t, stdout, "auth-svc")
	assert.Contains(t, stdout, "FAILING", "table must show FAILING state")
	assert.Contains(t, stdout, "same pattern", "fingerprint repeat line must be printed below FAILING row")
}

// ---------------------------------------------------------------------------
// TestCmd_Status_JSON (#31)
// ---------------------------------------------------------------------------

// TestCmd_Status_JSON verifies --json produces a JSON document that round-trips
// correctly through the HealthSnapshot schema.
func TestCmd_Status_JSON(t *testing.T) {
	home := tempHome(t)
	snap := snapErrors("worker", "out of memory", 7)
	writeSnapshot(t, home, snap)

	stdout, _, exitCode := runEP(t, home, "status", "--json")

	require.Equal(t, 0, exitCode)

	var parsed health.HealthSnapshot
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &parsed),
		"--json output must be parseable as a HealthSnapshot")
	require.Contains(t, parsed.Containers, "worker")
	assert.Equal(t, health.StateHasErrors, parsed.Containers["worker"].State)
	assert.Equal(t, 7, parsed.Containers["worker"].ErrorCount)
}

// ---------------------------------------------------------------------------
// TestCmd_Status_Reset (#32)
// ---------------------------------------------------------------------------

// TestCmd_Status_Reset verifies --reset <name> clears the container's health
// state in the persisted snapshot and prints a confirmation message.
func TestCmd_Status_Reset(t *testing.T) {
	home := tempHome(t)
	writeSnapshot(t, home, snapErrors("broken-svc", "crash", 2))

	stdout, _, exitCode := runEP(t, home, "status", "--reset", "broken-svc")

	require.Equal(t, 0, exitCode)
	assert.Contains(t, stdout, "Reset", "output must confirm the reset")
	assert.Contains(t, stdout, "broken-svc")

	// Verify the snapshot on disk was updated.
	updated, err := health.LoadSnapshot(
		home + "/.errorprobe/state/health.json",
	)
	require.NoError(t, err)
	assert.Equal(t, health.StateOK, updated.Containers["broken-svc"].State,
		"snapshot on disk must reflect the reset state")
}

// ---------------------------------------------------------------------------
// TestCmd_Status_GrafanaLinks (#33)
// ---------------------------------------------------------------------------

// TestCmd_Status_GrafanaLinks verifies the "Grafana Explore:" section and
// deep-link URLs are printed for every container in the snapshot.
func TestCmd_Status_GrafanaLinks(t *testing.T) {
	home := tempHome(t)
	// Pre-populate both health.json and containers.json.
	writeSnapshot(t, home, snapOK("my-svc"))
	writeWatchSet(t, home, discovery.WatchSet{
		Containers:  []discovery.ContainerMeta{{ID: "abc", Name: "my-svc", Runtime: "docker"}},
		GeneratedAt: time.Now(),
	})

	stdout, _, exitCode := runEP(t, home, "status")

	require.Equal(t, 0, exitCode)
	assert.Contains(t, stdout, "Grafana Explore:", "Grafana section header must be printed")
	assert.Contains(t, stdout, "explore", "deep-link URL must be printed")
	assert.Contains(t, stdout, "my-svc", "container name must appear in the Grafana link section")
}
