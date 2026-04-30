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
	if lines[0].Line != "first log line" {
		t.Errorf("expected %q, got %q", "first log line", lines[0].Line)
	}
	if lines[1].Line != "second log line" {
		t.Errorf("expected %q, got %q", "second log line", lines[1].Line)
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
	err := client.Tail(ctx, `{container="myapp"}`, 15*time.Minute, func(l loki.LogLine) string { return l.Line }, &buf)
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
	_ = client.Tail(ctx, `{container="myapp"}`, 15*time.Minute, func(l loki.LogLine) string { return l.Line }, &buf)

	if callCount < 2 {
		t.Fatalf("expected at least 2 polls, got %d", callCount)
	}
	// Second start must be later than first.
	if len(receivedStarts) >= 2 && receivedStarts[1] <= receivedStarts[0] {
		t.Errorf("start did not advance: first=%s second=%s", receivedStarts[0], receivedStarts[1])
	}
}
