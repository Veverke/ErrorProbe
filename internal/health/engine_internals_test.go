package health

// White-box tests for unexported helpers in engine.go.
// Lives in the same package so it can access splitHealthKey,
// hasNotableKeyword, extractNotableLines, and logEventKey directly.

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/ingest"
)

// ---------------------------------------------------------------------------
// splitHealthKey
// ---------------------------------------------------------------------------

func TestSplitHealthKey_DockerBareName(t *testing.T) {
	ns, container := splitHealthKey("my-api")
	assert.Equal(t, "", ns)
	assert.Equal(t, "my-api", container)
}

func TestSplitHealthKey_K8sNamespacedKey(t *testing.T) {
	ns, container := splitHealthKey("production/payment-svc")
	assert.Equal(t, "production", ns)
	assert.Equal(t, "payment-svc", container)
}

func TestSplitHealthKey_EmptyString(t *testing.T) {
	ns, container := splitHealthKey("")
	assert.Equal(t, "", ns)
	assert.Equal(t, "", container)
}

func TestSplitHealthKey_MultipleSlashes_FirstIsNamespace(t *testing.T) {
	// Only the first slash is the delimiter; the rest belongs to the container.
	ns, container := splitHealthKey("ns/container/extra")
	assert.Equal(t, "ns", ns)
	assert.Equal(t, "container/extra", container)
}

// ---------------------------------------------------------------------------
// logEventKey
// ---------------------------------------------------------------------------

func TestLogEventKey_DockerEvent_BareContainerName(t *testing.T) {
	ev := ingest.LogEvent{Container: "my-svc", Namespace: ""}
	assert.Equal(t, "my-svc", logEventKey(ev))
}

func TestLogEventKey_K8sEvent_NamespacePrefixed(t *testing.T) {
	ev := ingest.LogEvent{Container: "api", Namespace: "staging"}
	assert.Equal(t, "staging/api", logEventKey(ev))
}

// ---------------------------------------------------------------------------
// hasNotableKeyword
// ---------------------------------------------------------------------------

func TestHasNotableKeyword_ErrorLower(t *testing.T) {
	assert.True(t, hasNotableKeyword("database error: connection refused"))
}

func TestHasNotableKeyword_WarnUpper(t *testing.T) {
	assert.True(t, hasNotableKeyword("WARN: disk usage high"))
}

func TestHasNotableKeyword_Fatal(t *testing.T) {
	assert.True(t, hasNotableKeyword("fatal: out of memory"))
}

func TestHasNotableKeyword_Panic(t *testing.T) {
	assert.True(t, hasNotableKeyword("panic: nil pointer dereference"))
}

func TestHasNotableKeyword_Exception(t *testing.T) {
	assert.True(t, hasNotableKeyword("java.lang.NullPointerException"))
}

func TestHasNotableKeyword_PlainLine_NoMatch(t *testing.T) {
	assert.False(t, hasNotableKeyword("starting HTTP server on port 8080"))
}

func TestHasNotableKeyword_EmptyString(t *testing.T) {
	assert.False(t, hasNotableKeyword(""))
}

// ---------------------------------------------------------------------------
// extractNotableLines
// ---------------------------------------------------------------------------

func TestExtractNotableLines_SingleLine_ReturnedUnchanged(t *testing.T) {
	msg := "error: connection refused"
	assert.Equal(t, msg, extractNotableLines(msg))
}

func TestExtractNotableLines_MultiLine_OnlyNotableLinesKept(t *testing.T) {
	msg := "starting init sequence\nerror: disk full\ninitialising tables\nwarn: low memory\n"
	result := extractNotableLines(msg)
	assert.Contains(t, result, "error: disk full")
	assert.Contains(t, result, "warn: low memory")
	assert.NotContains(t, result, "starting init sequence")
	assert.NotContains(t, result, "initialising tables")
}

func TestExtractNotableLines_MultiLine_JoinedWithPipe(t *testing.T) {
	msg := "line one\nerror: bad state\nwarn: retrying\nline four"
	result := extractNotableLines(msg)
	assert.Contains(t, result, " | ")
}

func TestExtractNotableLines_MultiLine_NoNotableLines_ReturnsOriginal(t *testing.T) {
	// No error/warn/fatal/panic/exception → original returned unchanged.
	msg := "step 1 complete\nstep 2 complete\nstep 3 complete"
	assert.Equal(t, msg, extractNotableLines(msg))
}

func TestExtractNotableLines_TrimmedLeadingWhitespace(t *testing.T) {
	msg := "  error: leading spaces\nnormal line"
	result := extractNotableLines(msg)
	// The notable line should be trimmed.
	assert.Equal(t, "error: leading spaces", result)
}

// ---------------------------------------------------------------------------
// SetTransitionEvents + Engine.SetFailing / SetRecovered fire onChange
// ---------------------------------------------------------------------------

func TestEngine_SetFailing_FiresOnChange(t *testing.T) {
	dir := t.TempDir()
	var callCount atomic.Int32
	e := NewEngine(dir+"/h.json", nil, func(_ HealthSnapshot) {
		callCount.Add(1)
	})

	require.NoError(t, e.SetFailing("svc", "fp-abc123", 12))
	assert.Equal(t, int32(1), callCount.Load())
	assert.Equal(t, StateFailing, e.Snapshot().Containers["svc"].State)
}

func TestEngine_SetRecovered_TransitionsToHasErrors(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(dir+"/h.json", nil, nil)

	require.NoError(t, e.SetFailing("svc", "fp-abc123", 12))
	require.NoError(t, e.SetRecovered("svc"))

	assert.Equal(t, StateHasErrors, e.Snapshot().Containers["svc"].State)
	ch := e.Snapshot().Containers["svc"]
	assert.Equal(t, "", ch.DominantFingerprint)
}

func TestEngine_SetTransitionEvents_ChannelReceivesEvents(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(dir+"/h.json", nil, nil)

	ch := make(chan StateTransitionEvent, 4)
	e.SetTransitionEvents(ch)

	e.ProcessBatch([]ingest.LogEvent{
		{Timestamp: time.Now(), Container: "svc", Level: "error", Message: "boom"},
	})

	// At least one transition event (OK → HAS_ERRORS) must have been emitted.
	select {
	case ev := <-ch:
		assert.Equal(t, "svc", ev.Container)
		assert.Equal(t, StateHasErrors, ev.NewState)
	default:
		t.Fatal("expected at least one transition event")
	}
}

func TestSetFailing_NilContainersMap_InitialisedOnDemand(t *testing.T) {
	// e.snapshot.Containers is explicitly set to nil before the call.
	dir := t.TempDir()
	e := NewEngine(dir+"/snap.json", nil, nil)
	// Force nil to exercise the defensive branch.
	e.snapshot.Containers = nil
	err := e.SetFailing("svc", "fp1", 5)
	require.NoError(t, err)
	assert.Equal(t, StateFailing, e.Snapshot().Containers["svc"].State)
}

func TestRecordFingerprint_NilContainersMap_InitialisedOnDemand(t *testing.T) {
	// RecordFingerprint initialises the Containers map when nil — white-box test.
	snap := HealthSnapshot{}
	snap.RecordFingerprint("api", "fp-abc")
	assert.Equal(t, 1, snap.Containers["api"].Fingerprints["fp-abc"])
}

func TestEngine_Reset_FiresOnChange(t *testing.T) {
	// Verifies that Reset calls the onChange callback registered via NewEngine.
	dir := t.TempDir()
	var callCount atomic.Int32
	e := NewEngine(dir+"/h.json", nil, func(_ HealthSnapshot) {
		callCount.Add(1)
	})
	// Seed a failing state so Reset has something meaningful to do.
	require.NoError(t, e.SetFailing("svc", "fp-xyz", 3))
	callCount.Store(0) // reset counter so we only count the Reset call

	require.NoError(t, e.Reset("svc"))

	assert.Equal(t, int32(1), callCount.Load())
	assert.Equal(t, StateOK, e.Snapshot().Containers["svc"].State)
}
