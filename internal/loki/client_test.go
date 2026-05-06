package loki_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

