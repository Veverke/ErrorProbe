//go:build integration

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/errorprobe/errorprobe/internal/ingest"
)

// freePort returns an available TCP port on localhost by briefly binding to :0.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// startTransport starts an ingest.HTTPTransport on a free port, wires fn as the
// batch handler, and waits until the listener is accepting connections.
// Cancels the transport and waits for shutdown via t.Cleanup.
// Returns the bound "host:port" address.
func startTransport(t *testing.T, fn func([]ingest.LogEvent)) string {
	t.Helper()
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	transport := ingest.NewHTTPTransport(addr)
	transport.OnBatch(fn)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() { _ = transport.Start(ctx) }()

	// Probe the port until the listener is accepting connections.
	ready := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				select {
				case ready <- struct{}{}:
				default:
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("ingest transport did not become ready within 5 s")
	}
	return addr
}

// postEvents marshals events as a JSON array and POSTs it to addr/ingest.
// Returns the HTTP response status code.
func postEvents(t *testing.T, addr string, events []ingest.LogEvent) int {
	t.Helper()
	data, err := json.Marshal(events)
	require.NoError(t, err)
	resp, err := http.Post("http://"+addr+"/ingest", "application/json", bytes.NewReader(data))
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// postRaw POSTs raw bytes to addr/ingest and returns the HTTP response status code.
func postRaw(t *testing.T, addr string, body []byte) int {
	t.Helper()
	resp, err := http.Post("http://"+addr+"/ingest", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// waitFor polls cond every 50 ms until it returns true or timeout elapses.
// Calls t.Fatalf if the deadline is exceeded.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("condition not satisfied within %s", timeout)
}

// logEvent constructs a minimal LogEvent with the given container, level, and message.
func logEvent(container, level, msg string) ingest.LogEvent {
	return ingest.LogEvent{
		Timestamp: time.Now(),
		Container: container,
		Level:     level,
		Message:   msg,
		Runtime:   "docker",
	}
}

// repeated returns a slice of n copies of msg.
func repeated(msg string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = msg
	}
	return out
}
