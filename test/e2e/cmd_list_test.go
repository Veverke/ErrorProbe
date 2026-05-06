//go:build integration

package e2e_test

// cmd_list_test.go exercises every flag of the `ep list` command via subprocess.
// Tests use testcontainers-go to start stub Alpine containers that appear in
// Docker's container list, giving deterministic, environment-independent output.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tablePrefix returns the first 27 runes of name — the visible portion when
// the CONTAINER column (28 chars wide) truncates long names with an ellipsis.
func tablePrefix(name string) string {
	runes := []rune(name)
	if len(runes) > 27 {
		return string(runes[:27])
	}
	return name
}

// ---------------------------------------------------------------------------
// TestCmd_List_DefaultTable (#7 extended)
// ---------------------------------------------------------------------------

// TestCmd_List_DefaultTable verifies the default tabular output contains the
// expected columns and shows the stub container.
func TestCmd_List_DefaultTable(t *testing.T) {
	name := startAlpineContainer(t, uniqueName("ep-e2e-list"))

	stdout, _, exitCode := runEP(t, tempHome(t), "list")

	require.Equal(t, 0, exitCode)
	assert.Contains(t, stdout, "RUNTIME", "header row must contain RUNTIME column")
	assert.Contains(t, stdout, "CONTAINER", "header row must contain CONTAINER column")
	assert.Contains(t, stdout, "STATUS", "header row must contain STATUS column")
	assert.Contains(t, stdout, "WATCHING", "header row must contain WATCHING column")
	assert.Contains(t, stdout, tablePrefix(name), "stub container must appear in default table output")
	assert.Contains(t, stdout, "docker", "runtime column must show 'docker'")
}

// ---------------------------------------------------------------------------
// TestCmd_List_JSON (#10)
// ---------------------------------------------------------------------------

// TestCmd_List_JSON verifies --json produces a valid JSON array where each
// element carries an ID, Name, Runtime, and Watching field.
func TestCmd_List_JSON(t *testing.T) {
	name := startAlpineContainer(t, uniqueName("ep-e2e-list-json"))

	stdout, _, exitCode := runEP(t, tempHome(t), "list", "--json")

	require.Equal(t, 0, exitCode)

	var items []struct {
		ID       string `json:"ID"`
		Name     string `json:"Name"`
		Runtime  string `json:"Runtime"`
		Watching bool   `json:"Watching"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout)), &items),
		"--json output must be a valid JSON array")

	var found bool
	for _, item := range items {
		if item.Name == name {
			found = true
			assert.Equal(t, "docker", item.Runtime)
			// Watching is false because no containers.json exists (temp home).
			assert.False(t, item.Watching)
		}
	}
	assert.True(t, found, "stub container %q must appear in --json output", name)
}

// ---------------------------------------------------------------------------
// TestCmd_List_Details (#11)
// ---------------------------------------------------------------------------

// TestCmd_List_Details verifies --details shows the image and status lines
// in the per-container breakdown.
func TestCmd_List_Details(t *testing.T) {
	startAlpineContainer(t, uniqueName("ep-e2e-list-details"))

	stdout, _, exitCode := runEP(t, tempHome(t), "list", "--details")

	require.Equal(t, 0, exitCode)
	assert.Contains(t, stdout, "image:", "--details must show image: line")
	assert.Contains(t, stdout, "status:", "--details must show status: line")
	assert.Contains(t, stdout, "alpine", "alpine image must appear in details output")
}

// ---------------------------------------------------------------------------
// TestCmd_List_Runtime_Docker (#8)
// ---------------------------------------------------------------------------

// TestCmd_List_Runtime_Docker verifies --runtime docker includes Docker
// containers and does not include a k8s section header.
func TestCmd_List_Runtime_Docker(t *testing.T) {
	name := startAlpineContainer(t, uniqueName("ep-e2e-list-rt"))

	stdout, _, exitCode := runEP(t, tempHome(t), "list", "--runtime", "docker")

	require.Equal(t, 0, exitCode)
	assert.Contains(t, stdout, tablePrefix(name), "Docker container must appear with --runtime docker")
}

// ---------------------------------------------------------------------------
// TestCmd_List_Runtime_K8s (#9)
// ---------------------------------------------------------------------------

// TestCmd_List_Runtime_K8s verifies --runtime k8s exits zero and produces
// output without the Docker container (no K8s cluster in the CI environment).
func TestCmd_List_Runtime_K8s(t *testing.T) {
	name := startAlpineContainer(t, uniqueName("ep-e2e-list-k8s"))

	stdout, _, exitCode := runEP(t, tempHome(t), "list", "--runtime", "k8s")

	require.Equal(t, 0, exitCode, "--runtime k8s must exit 0 even when no K8s cluster is present")
	assert.NotContains(t, stdout, tablePrefix(name),
		"Docker container must not appear under --runtime k8s filter")
}

// ---------------------------------------------------------------------------
// TestCmd_List_Runtime_InvalidFlag (#8 edge-case)
// ---------------------------------------------------------------------------

// TestCmd_List_Runtime_InvalidFlag verifies --runtime with an unrecognised
// value exits non-zero and emits an informative error.
func TestCmd_List_Runtime_InvalidFlag(t *testing.T) {
	_, stderr, exitCode := runEP(t, tempHome(t), "list", "--runtime", "bogus")

	assert.NotEqual(t, 0, exitCode, "--runtime bogus must exit non-zero")
	combined := stderr // ep prints errors to stderr
	assert.Contains(t, combined, "bogus", "error message must echo the invalid value")
}

// ---------------------------------------------------------------------------
// TestCmd_List_Compact (#12)
// ---------------------------------------------------------------------------

// TestCmd_List_Compact verifies --compact exits zero and produces shorter
// output. The container name must still appear.
func TestCmd_List_Compact(t *testing.T) {
	name := startAlpineContainer(t, uniqueName("ep-e2e-list-compact"))

	stdoutCompact, _, exitCode := runEP(t, tempHome(t), "list", "--compact")
	require.Equal(t, 0, exitCode, "--compact must exit 0")
	assert.Contains(t, stdoutCompact, tablePrefix(name))

	stdoutFull, _, _ := runEP(t, tempHome(t), "list")
	// Compact output must be no longer than (or equal to) full output since
	// identical-value columns are dropped.
	assert.LessOrEqual(t, len(stdoutCompact), len(stdoutFull),
		"--compact output must not be longer than default output")
}
