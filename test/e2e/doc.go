// Package e2e contains end-to-end integration tests for ErrorProbe.
//
// These tests require a running Docker daemon. Run them explicitly with:
//
//	go test -tags integration ./test/e2e/ -timeout 10m -v
//
// Tests are split into three groups:
//
//   - pipeline_test.go  — ingest + health engine; fully in-process, no containers needed
//   - discovery_test.go — container discovery; uses testcontainers-go for stub apps
//   - stack_test.go     — full stack lifecycle (up/down); slow, pulls Docker images on first run
package e2e
