package ingest

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/errorprobe/errorprobe/internal/logger"
)

const (
	maxRequestBodyBytes = 10 * 1024 * 1024 // 10 MB
	shutdownTimeout     = 5 * time.Second
)

// HTTPTransport implements Transport over HTTP JSON.
type HTTPTransport struct {
	addr    string
	server  *http.Server
	handler func([]LogEvent)
	mu      sync.RWMutex
}

// NewHTTPTransport creates an HTTPTransport that binds on addr (host:port).
func NewHTTPTransport(addr string) *HTTPTransport {
	t := &HTTPTransport{addr: addr}
	mux := http.NewServeMux()
	mux.HandleFunc("/ingest", t.handleIngest)
	t.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return t
}

// OnBatch registers the handler called for each received batch.
func (t *HTTPTransport) OnBatch(handler func([]LogEvent)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handler = handler
}

// Start begins listening. It blocks until ctx is cancelled or Stop is called.
func (t *HTTPTransport) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", t.addr)
	if err != nil {
		return fmt.Errorf("ingest: listen %s: %w", t.addr, err)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := t.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		} else {
			errCh <- nil
		}
	}()

	select {
	case <-ctx.Done():
		return t.Stop()
	case err := <-errCh:
		return err
	}
}

// Stop initiates graceful shutdown with a 5-second drain timeout.
func (t *HTTPTransport) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := t.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("ingest: shutdown: %w", err)
	}
	return nil
}

func (t *HTTPTransport) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		// MaxBytesReader returns an error with "http: request body too large"
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	events, err := ParseBatch(data)
	if err != nil {
		logger.Error("ingest: invalid JSON batch", "error", err)
		http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	t.mu.RLock()
	h := t.handler
	t.mu.RUnlock()

	if h != nil {
		h(events)
	}

	w.WriteHeader(http.StatusNoContent)
}
