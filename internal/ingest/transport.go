package ingest

import "context"

// Transport is the ingest listener interface. Phase 3 ships an HTTP
// implementation; a gRPC slot is reserved for a later phase.
type Transport interface {
	// Start begins listening and blocks until the context is cancelled or Stop
	// is called. It returns any startup or shutdown error.
	Start(ctx context.Context) error

	// Stop initiates a graceful shutdown, draining in-flight requests with a
	// 5-second timeout.
	Stop() error

	// OnBatch registers the handler called for each received batch of log
	// events. The handler is called synchronously (no internal queue in V1).
	// Calling OnBatch after Start may or may not affect in-flight requests;
	// no ordering guarantee is provided. Callers should register the handler
	// before calling Start to ensure consistent behavior.
	OnBatch(handler func([]LogEvent))
}