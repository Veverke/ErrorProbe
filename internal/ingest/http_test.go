package ingest

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freeAddr returns an available localhost TCP address.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func newStartedTransport(t *testing.T) (*HTTPTransport, context.CancelFunc, string) {
	t.Helper()
	addr := freeAddr(t)
	tr := NewHTTPTransport(addr)
	ctx, cancel := context.WithCancel(context.Background())
	go tr.Start(ctx) //nolint:errcheck
	// Wait until the server is ready to accept connections.
	connected := false
	for i := 0; i < 50; i++ {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			conn.Close()
			connected = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !connected {
		cancel()
		t.Fatal("server never became reachable within the retry window")
	}
	return tr, cancel, addr
}

func TestHTTPTransport_ValidBatch_CallsHandler(t *testing.T) {
	_, cancel, addr := newStartedTransport(t)
	defer cancel()

	tr := NewHTTPTransport(addr) // reuse for OnBatch registration test via httptest below

	var called atomic.Bool
	var received []LogEvent

	// Use httptest directly to test the handler in isolation (faster + no port conflict).
	tr2 := NewHTTPTransport("127.0.0.1:0")
	tr2.OnBatch(func(events []LogEvent) {
		called.Store(true)
		received = events
	})

	body := `[{"timestamp":"2024-01-01T12:00:00Z","container":"api","level":"error","message":"boom","raw":"ERROR boom"}]`
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(body))
	rr := httptest.NewRecorder()
	tr2.server.Handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
	assert.True(t, called.Load())
	require.Len(t, received, 1)
	assert.Equal(t, "api", received[0].Container)
	assert.Equal(t, "error", received[0].Level)
	assert.Equal(t, "boom", received[0].Message)
	_ = tr // silence unused warning
}

func TestHTTPTransport_InvalidJSON_Returns400(t *testing.T) {
	var called atomic.Bool
	tr := NewHTTPTransport("127.0.0.1:0")
	tr.OnBatch(func(_ []LogEvent) { called.Store(true) })

	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader("{not valid json}"))
	rr := httptest.NewRecorder()
	tr.server.Handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.False(t, called.Load(), "handler must not be called on bad JSON")
}

func TestHTTPTransport_EmptyBatch_NoOp(t *testing.T) {
	var called atomic.Bool
	var received []LogEvent

	tr := NewHTTPTransport("127.0.0.1:0")
	tr.OnBatch(func(events []LogEvent) {
		called.Store(true)
		received = events
	})

	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader("[]"))
	rr := httptest.NewRecorder()
	tr.server.Handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
	assert.True(t, called.Load())
	assert.Empty(t, received)
}

func TestHTTPTransport_OversizeRequest_Rejected(t *testing.T) {
	tr := NewHTTPTransport("127.0.0.1:0")

	// Build a body slightly over 10 MB.
	big := bytes.Repeat([]byte("x"), maxRequestBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewReader(big))
	rr := httptest.NewRecorder()
	tr.server.Handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
}

func TestHTTPTransport_GracefulShutdown(t *testing.T) {
	addr := freeAddr(t)
	tr := NewHTTPTransport(addr)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- tr.Start(ctx)
	}()

	// Wait for server to be up.
	for i := 0; i < 50; i++ {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Cancel the context (triggers Stop).
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("graceful shutdown timed out")
	}
}

func TestHTTPTransport_MethodNotAllowed(t *testing.T) {
	tr := NewHTTPTransport("127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/ingest", nil)
	rr := httptest.NewRecorder()
	tr.server.Handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHTTPTransport_NoHandler_NoError(t *testing.T) {
	// With no OnBatch handler registered, a valid batch should still return 204.
	tr := NewHTTPTransport("127.0.0.1:0")

	body := `[{"container":"api","level":"info","message":"hello"}]`
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(body))
	rr := httptest.NewRecorder()
	tr.server.Handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestHTTPTransport_Start_ListenError(t *testing.T) {
	// Bind a listener so the address is already taken.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	tr := NewHTTPTransport(ln.Addr().String())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = tr.Start(ctx)
	assert.Error(t, err)
}
