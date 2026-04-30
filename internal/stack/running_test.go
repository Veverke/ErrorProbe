package stack_test

import (
	"context"
	"errors"
	"testing"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/stack"
)

// ---------------------------------------------------------------------------
// T4.12 — IsStackRunning tests
// ---------------------------------------------------------------------------

func allRunningMock() *mockDockerAPI {
	m := newMockDocker()
	m.running[stack.ContainerLoki] = true
	m.running[stack.ContainerGrafana] = true
	m.running[stack.ContainerVector] = true
	return m
}

func TestIsStackRunning_AllRunning_True(t *testing.T) {
	m := allRunningMock()
	cfg := &config.Config{}
	got, err := stack.IsStackRunning(context.Background(), cfg, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("expected true when all containers are running")
	}
}

func TestIsStackRunning_OneDown_False(t *testing.T) {
	m := allRunningMock()
	m.running[stack.ContainerLoki] = false // Loki stopped
	cfg := &config.Config{}
	got, err := stack.IsStackRunning(context.Background(), cfg, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("expected false when one container is not running")
	}
}

func TestIsStackRunning_NoneRunning_False(t *testing.T) {
	m := newMockDocker() // no running containers
	cfg := &config.Config{}
	got, err := stack.IsStackRunning(context.Background(), cfg, m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("expected false when no containers are running")
	}
}

func TestIsStackRunning_DockerError_ReturnsError(t *testing.T) {
	m := newMockDocker()
	m.containerRunningErr = errors.New("docker daemon not available")
	cfg := &config.Config{}
	_, err := stack.IsStackRunning(context.Background(), cfg, m)
	if err == nil {
		t.Error("expected error when docker returns error")
	}
}
