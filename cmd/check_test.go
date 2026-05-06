package cmd_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/errorprobe/errorprobe/cmd"
	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/health"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func snapWithState(name string, state health.FunctionalState, errMsg string) health.HealthSnapshot {
	snap := health.HealthSnapshot{
		Containers: map[string]health.ContainerHealth{},
		SnapshotAt: time.Now(),
	}
	ch := health.ContainerHealth{
		Name:  name,
		State: state,
	}
	if state == health.StateHasErrors {
		ch.ErrorCount = 1
		ch.LastErrorMsg = errMsg
		now := time.Now()
		ch.LastErrorAt = &now
		ch.FirstErrorAt = &now
	}
	snap.Containers[name] = ch
	return snap
}

// ---------------------------------------------------------------------------
// T4.11 — check command logic tests
// ---------------------------------------------------------------------------

func TestCheck_AllOK_ExitsZero(t *testing.T) {
	snap := snapWithState("myapp", health.StateOK, "")
	check := config.Check{FailOn: "HAS_ERRORS"}
	ok, failing, err := cmd.EvalCheck(snap, check)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected ok=true, got failing: %v", failing)
	}
	if len(failing) != 0 {
		t.Errorf("expected no failing containers, got: %v", failing)
	}
}

func TestCheck_HasErrors_FailOnHasErrors_ExitsOne(t *testing.T) {
	snap := snapWithState("broken", health.StateHasErrors, "connection refused")
	check := config.Check{FailOn: "HAS_ERRORS"}
	ok, failing, err := cmd.EvalCheck(snap, check)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false")
	}
	if len(failing) != 1 || failing[0].Name != "broken" {
		t.Errorf("expected failing=[broken], got: %v", failing)
	}
}

func TestCheck_HasErrors_FailOnFailing_ExitsZero(t *testing.T) {
	// HAS_ERRORS should NOT trigger failure under fail_on=FAILING.
	snap := snapWithState("broken", health.StateHasErrors, "connection refused")
	check := config.Check{FailOn: "FAILING"}
	ok, failing, err := cmd.EvalCheck(snap, check)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected ok=true under FAILING threshold, got failing: %v", failing)
	}
}

func TestCheck_ExcludedContainer_NotEvaluated(t *testing.T) {
	snap := snapWithState("noisy", health.StateHasErrors, "lots of errors")
	check := config.Check{FailOn: "HAS_ERRORS", Exclude: []string{"noisy"}}
	ok, failing, err := cmd.EvalCheck(snap, check)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("excluded container should not trigger failure, got: %v", failing)
	}
}

func TestCheck_DefaultFailOn_IsHasErrors(t *testing.T) {
	// Empty FailOn defaults to HAS_ERRORS.
	snap := snapWithState("broken", health.StateHasErrors, "oops")
	check := config.Check{} // FailOn empty → default HAS_ERRORS
	ok, _, err := cmd.EvalCheck(snap, check)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false when FailOn defaults to HAS_ERRORS")
	}
}

func TestCheck_JSON_Output(t *testing.T) {
	snap := snapWithState("broken", health.StateHasErrors, "timeout")
	check := config.Check{FailOn: "HAS_ERRORS"}
	ok, failing, err := cmd.EvalCheck(snap, check)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	if err := cmd.WriteCheckJSON(&buf, ok, failing); err != nil {
		t.Fatalf("WriteCheckJSON returned error: %v", err)
	}

	var out struct {
		OK      bool `json:"ok"`
		Failing []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"failing"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON output: %v\nbuf: %s", err, buf.String())
	}
	if out.OK {
		t.Error("expected ok=false in JSON")
	}
	if len(out.Failing) != 1 || out.Failing[0].Name != "broken" {
		t.Errorf("expected failing=[broken] in JSON, got: %+v", out.Failing)
	}
}

func TestCheck_JSON_Output_AllOK(t *testing.T) {
	snap := health.HealthSnapshot{Containers: map[string]health.ContainerHealth{}}
	check := config.Check{FailOn: "HAS_ERRORS"}
	ok, failing, err := cmd.EvalCheck(snap, check)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var buf bytes.Buffer
	if err := cmd.WriteCheckJSON(&buf, ok, failing); err != nil {
		t.Fatalf("WriteCheckJSON returned error: %v", err)
	}

	var out struct {
		OK      bool          `json:"ok"`
		Failing []interface{} `json:"failing"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v\nbuf: %s", err, buf.String())
	}
	if !out.OK {
		t.Error("expected ok=true in JSON")
	}
	if out.Failing == nil {
		t.Error("expected failing to be [] not null")
	}
}

func TestCheck_UnknownFailOn_ReturnsError(t *testing.T) {
	snap := snapWithState("myapp", health.StateOK, "")
	check := config.Check{FailOn: "HAS_ERROR"} // typo — should fail
	_, _, err := cmd.EvalCheck(snap, check)
	if err == nil {
		t.Error("expected error for unsupported fail_on value, got nil")
	}
}

// ---------------------------------------------------------------------------
// T6.14 — check command tests for FAILING state
// ---------------------------------------------------------------------------

func snapWithFailing(name string) health.HealthSnapshot {
	snap := health.HealthSnapshot{
		Containers: map[string]health.ContainerHealth{},
		SnapshotAt: time.Now(),
	}
	now := time.Now()
	snap.Containers[name] = health.ContainerHealth{
		Name:                     name,
		State:                    health.StateFailing,
		ErrorCount:               20,
		LastErrorMsg:             "connection refused to db",
		LastErrorAt:              &now,
		FirstErrorAt:             &now,
		DominantFingerprintCount: 20,
	}
	return snap
}

func TestCheck_FAILING_FailOnFailing_ExitsOne(t *testing.T) {
	snap := snapWithFailing("broken")
	check := config.Check{FailOn: "FAILING"}
	ok, failing, err := cmd.EvalCheck(snap, check)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false for FAILING container with fail_on=FAILING")
	}
	if len(failing) != 1 || failing[0].Name != "broken" {
		t.Errorf("expected failing=[broken], got: %v", failing)
	}
}

func TestCheck_HAS_ERRORS_FailOnFailing_ExitsZero(t *testing.T) {
	snap := snapWithState("noisy", health.StateHasErrors, "minor error")
	check := config.Check{FailOn: "FAILING"}
	ok, failing, err := cmd.EvalCheck(snap, check)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected ok=true for HAS_ERRORS under fail_on=FAILING, got failing: %v", failing)
	}
}

func TestCheck_FAILING_FailOnHasErrors_ExitsOne(t *testing.T) {
	// FAILING is a superset of HAS_ERRORS: fail_on=HAS_ERRORS must still exit 1.
	snap := snapWithFailing("broken")
	check := config.Check{FailOn: "HAS_ERRORS"}
	ok, failing, err := cmd.EvalCheck(snap, check)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false: FAILING should trigger fail_on=HAS_ERRORS")
	}
	if len(failing) != 1 {
		t.Errorf("expected 1 failing entry, got: %v", failing)
	}
}

