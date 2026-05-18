package loki_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/loki"
)

// buildStreamsPayload wraps timestamp+line pairs into the Loki query_range JSON shape.
func buildStreamsPayload(entries [][2]string) map[string]interface{} {
	return map[string]interface{}{
		"data": map[string]interface{}{
			"resultType": "streams",
			"result": []interface{}{
				map[string]interface{}{
					"stream": map[string]string{"container": "myapp"},
					"values": entries,
				},
			},
		},
	}
}

func TestQueryRange_ParsesLokiStreamsResponse(t *testing.T) {
	ts1 := time.Now().Add(-5 * time.Second)
	ts2 := time.Now().Add(-2 * time.Second)

	payload := buildStreamsPayload([][2]string{
		{fmt.Sprintf("%d", ts1.UnixNano()), "first log line"},
		{fmt.Sprintf("%d", ts2.UnixNano()), "second log line"},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	lines, err := client.QueryRange(
		context.Background(),
		`{container="myapp"}`,
		time.Now().Add(-1*time.Minute),
		time.Now(),
		100,
		"forward",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0].Message != "first log line" {
		t.Errorf("expected %q, got %q", "first log line", lines[0].Message)
	}
	if lines[1].Message != "second log line" {
		t.Errorf("expected %q, got %q", "second log line", lines[1].Message)
	}
	if lines[0].Timestamp.IsZero() {
		t.Error("expected non-zero timestamp on first line")
	}
}

func TestQueryRange_EmptyResult(t *testing.T) {
	payload := map[string]interface{}{
		"data": map[string]interface{}{
			"result": []interface{}{},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	lines, err := client.QueryRange(context.Background(), `{container="x"}`, time.Now().Add(-time.Minute), time.Now(), 10, "forward")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("expected 0 lines, got %d", len(lines))
	}
}

func TestTail_StopsOnContextCancellation(t *testing.T) {
	ts := time.Now().Add(-1 * time.Second)
	payload := buildStreamsPayload([][2]string{
		{fmt.Sprintf("%d", ts.UnixNano()), "log line from server"},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately after the first poll completes.
	cancel()

	client := loki.NewClient(srv.URL)
	var buf bytes.Buffer
	err := client.Tail(ctx, `{container="myapp"}`, 15*time.Minute, func(l loki.LogLine) string { return l.Message }, &buf)
	if err != nil {
		t.Fatalf("expected nil on context cancellation, got: %v", err)
	}
}

func TestTail_AdvancesStartTimestamp(t *testing.T) {
	// Each server call returns a new line with a later timestamp.
	// We verify the 'start' query param advances between calls.
	callCount := 0
	var receivedStarts []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedStarts = append(receivedStarts, r.URL.Query().Get("start"))
		callCount++

		ts := time.Now().Add(time.Duration(callCount) * time.Millisecond)
		payload := buildStreamsPayload([][2]string{
			{fmt.Sprintf("%d", ts.UnixNano()), fmt.Sprintf("line %d", callCount)},
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	// Allow exactly 2 polls by cancelling after a short time.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client := loki.NewClient(srv.URL)
	var buf bytes.Buffer
	_ = client.Tail(ctx, `{container="myapp"}`, 15*time.Minute, func(l loki.LogLine) string { return l.Message }, &buf)

	if callCount < 2 {
		t.Fatalf("expected at least 2 polls, got %d", callCount)
	}
	// Second start must be later than first.
	if len(receivedStarts) >= 2 && receivedStarts[1] <= receivedStarts[0] {
		t.Errorf("start did not advance: first=%s second=%s", receivedStarts[0], receivedStarts[1])
	}
}

// ---------------------------------------------------------------------------
// T6.10 — CountErrors tests
// ---------------------------------------------------------------------------

// buildInstantQueryPayload constructs the Loki /query (instant) JSON response for
// a vector result with a single entry.  count is the count_over_time value.
func buildInstantQueryPayload(count int) map[string]interface{} {
	return map[string]interface{}{
		"data": map[string]interface{}{
			"resultType": "vector",
			"result": []interface{}{
				map[string]interface{}{
					"metric": map[string]string{},
					"value":  []interface{}{1.0, fmt.Sprintf("%d", count)},
				},
			},
		},
	}
}

func buildInstantQueryEmpty() map[string]interface{} {
	return map[string]interface{}{
		"data": map[string]interface{}{
			"resultType": "vector",
			"result":     []interface{}{},
		},
	}
}

func TestCountErrors_ReturnsCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buildInstantQueryPayload(47))
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	n, err := client.CountErrors(context.Background(), "myapp", 3*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 47 {
		t.Errorf("expected 47, got %d", n)
	}
}

func TestCountErrors_ZeroCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buildInstantQueryEmpty())
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	n, err := client.CountErrors(context.Background(), "myapp", 3*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

func TestQueryRange_ReturnsLogLines(t *testing.T) {
	ts1 := time.Now().Add(-5 * time.Second)
	payload := buildStreamsPayload([][2]string{
		{fmt.Sprintf("%d", ts1.UnixNano()), "first error line"},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	lines, err := client.QueryRange(context.Background(), `{container="myapp"}`,
		time.Now().Add(-time.Minute), time.Now(), 100, "forward")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0].Message != "first error line" {
		t.Errorf("expected %q, got %q", "first error line", lines[0].Message)
	}
}

func TestQueryRange_EmptyResult_NoError(t *testing.T) {
	payload := map[string]interface{}{
		"data": map[string]interface{}{
			"result": []interface{}{},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	lines, err := client.QueryRange(context.Background(), `{container="x"}`,
		time.Now().Add(-time.Minute), time.Now(), 10, "forward")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("expected 0 lines, got %d", len(lines))
	}
}

func TestQueryRange_Timeout_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the client context times out.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	client := loki.NewClientWithTimeout(srv.URL, 50*time.Millisecond)
	_, err := client.QueryRange(ctx, `{container="x"}`,
		time.Now().Add(-time.Minute), time.Now(), 10, "forward")
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

func TestQueryRange_NonOKStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	_, err := client.QueryRange(context.Background(), `{container="x"}`,
		time.Now().Add(-time.Minute), time.Now(), 10, "forward")
	if err == nil {
		t.Error("expected error for non-200 status, got nil")
	}
}

func TestCountErrors_NonOKStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	_, err := client.CountErrors(context.Background(), "myapp", 3*time.Minute)
	if err == nil {
		t.Error("expected error for non-200 status, got nil")
	}
}

func TestCountErrors_MultipleResults_SumsCount(t *testing.T) {
	payload := map[string]interface{}{
		"data": map[string]interface{}{
			"resultType": "vector",
			"result": []interface{}{
				map[string]interface{}{"metric": map[string]string{}, "value": []interface{}{1.0, "10"}},
				map[string]interface{}{"metric": map[string]string{}, "value": []interface{}{2.0, "5"}},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	n, err := client.CountErrors(context.Background(), "myapp", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 15 {
		t.Errorf("expected 15, got %d", n)
	}
}

func TestCountErrors_SecondsDuration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if q == "" {
			t.Errorf("expected query param")
		}
		_ = json.NewEncoder(w).Encode(buildInstantQueryPayload(3))
	}))
	defer srv.Close()

	// 90 seconds — not an exact number of minutes → should use seconds form
	client := loki.NewClient(srv.URL)
	n, err := client.CountErrors(context.Background(), "svc", 90*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3, got %d", n)
	}
}

func TestQueryRange_ParsesStreamLabels(t *testing.T) {
	ts := time.Now().Add(-time.Second)
	payload := map[string]interface{}{
		"data": map[string]interface{}{
			"result": []interface{}{
				map[string]interface{}{
					"stream": map[string]string{"container": "myapp", "level": "error"},
					"values": [][2]string{{fmt.Sprintf("%d", ts.UnixNano()), "an error occurred"}},
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	lines, err := client.QueryRange(context.Background(), `{container="myapp"}`,
		time.Now().Add(-time.Minute), time.Now(), 10, "forward")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0].Container != "myapp" {
		t.Errorf("expected Container=myapp, got %q", lines[0].Container)
	}
	if lines[0].Level != "error" {
		t.Errorf("expected Level=error, got %q", lines[0].Level)
	}
}

func TestTail_ServerError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	var buf bytes.Buffer
	err := client.Tail(context.Background(), `{container="x"}`, time.Minute,
		func(l loki.LogLine) string { return l.Message }, &buf)
	if err == nil {
		t.Error("expected error for 500 status, got nil")
	}
}

func TestCountErrors_NumericValue_SkipsEntry(t *testing.T) {
	// When Loki returns value[1] as a JSON number (not string), CountErrors
	// should skip that entry gracefully and return 0.
	payload := map[string]interface{}{
		"data": map[string]interface{}{
			"resultType": "vector",
			"result": []interface{}{
				map[string]interface{}{
					"metric": map[string]string{},
					"value":  []interface{}{1.0, 42.0}, // count as float64, not string
				},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	n, err := client.CountErrors(context.Background(), "myapp", 3*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 for untyped count, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// QueryErrorMessages
// ---------------------------------------------------------------------------

func TestCountErrors_K8sNamespacedKey_BuildsNamespaceQuery(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query().Get("query")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{"resultType": "vector", "result": []interface{}{}},
		})
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	n, err := client.CountErrors(context.Background(), "production/payment-svc", 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Contains(t, capturedQuery, "namespace")
	assert.Contains(t, capturedQuery, "production")
}

func TestCountErrors_HTTPError_ReturnsError(t *testing.T) {
	// Use a closed server to provoke a transport error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // close before use

	client := loki.NewClient(srv.URL)
	_, err := client.CountErrors(context.Background(), "myapp", time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "querying Loki count")
}

func TestCountErrors_BadJSON_ReturnsDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	_, err := client.CountErrors(context.Background(), "myapp", time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding Loki count response")
}

func buildQueryRangeStreamsPayload(container string, messages []string) map[string]interface{} {
	now := time.Now()
	var values [][2]string
	for i, msg := range messages {
		ts := now.Add(time.Duration(i) * time.Second)
		values = append(values, [2]string{fmt.Sprintf("%d", ts.UnixNano()), msg})
	}

	entries := make([]interface{}, len(values))
	for i, v := range values {
		entries[i] = []interface{}{v[0], v[1]}
	}

	return map[string]interface{}{
		"data": map[string]interface{}{
			"resultType": "streams",
			"result": []interface{}{
				map[string]interface{}{
					"stream": map[string]string{"container": container, "level": "error"},
					"values": entries,
				},
			},
		},
	}
}

func TestQueryErrorMessages_DockerContainer_ReturnsMessages(t *testing.T) {
	want := []string{"db connection refused", "timeout dialing postgres"}
	payload := buildQueryRangeStreamsPayload("api", want)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	msgs, err := client.QueryErrorMessages(context.Background(), "api", 3*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0] != want[0] {
		t.Errorf("first message: got %q want %q", msgs[0], want[0])
	}
}

func TestQueryErrorMessages_K8sNamespacedKey_BuildsCorrectQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if q == "" {
			t.Errorf("expected non-empty query param")
		}
		// K8s key format should include namespace in the stream selector.
		if !strings.Contains(q, `namespace`) {
			t.Errorf("expected namespace in K8s query, got: %s", q)
		}
		payload := buildQueryRangeStreamsPayload("myapp", []string{"err msg"})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	msgs, err := client.QueryErrorMessages(context.Background(), "production/myapp", time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
}

func TestQueryErrorMessages_LokiError_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	_, err := client.QueryErrorMessages(context.Background(), "api", time.Minute)
	if err == nil {
		t.Fatal("expected an error for non-OK status")
	}
}

func TestQueryErrorMessages_EmptyResult_ReturnsEmptySlice(t *testing.T) {
	payload := map[string]interface{}{
		"data": map[string]interface{}{
			"resultType": "streams",
			"result":     []interface{}{},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	client := loki.NewClient(srv.URL)
	msgs, err := client.QueryErrorMessages(context.Background(), "api", time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty slice, got %d messages", len(msgs))
	}
}


