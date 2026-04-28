package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupLogger(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	if err := Init(path, 10, 3); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	SetDebug(false)
	t.Cleanup(Close)
	return dir, path
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	return string(data)
}

func TestInit_CreatesLogFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "test.log")
	if err := Init(path, 10, 3); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	t.Cleanup(Close)
	if _, err := os.Stat(path); err != nil {
		// The file is created lazily by lumberjack on first write.
		Info("probe")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("log file not created: %v", err)
		}
	}
}

func TestInit_Idempotent(t *testing.T) {
	_, path := setupLogger(t)
	if err := Init(path, 10, 3); err != nil {
		t.Fatalf("second Init returned error: %v", err)
	}
}
func TestInfo_WritesToFile(t *testing.T) {
	_, path := setupLogger(t)
	Info("hello world")
	content := readLog(t, path)
	if !strings.Contains(content, "INFO") {
		t.Errorf("expected INFO in log, got: %s", content)
	}
	if !strings.Contains(content, "hello world") {
		t.Errorf("expected message in log, got: %s", content)
	}
}

func TestError_WritesToFile(t *testing.T) {
	_, path := setupLogger(t)
	Error("something failed")
	content := readLog(t, path)
	if !strings.Contains(content, "ERROR") {
		t.Errorf("expected ERROR level in log, got: %s", content)
	}
	if !strings.Contains(content, "something failed") {
		t.Errorf("expected message in log, got: %s", content)
	}
}

func TestDebug_SuppressedByDefault(t *testing.T) {
	_, path := setupLogger(t)
	SetDebug(false)
	Debug("secret debug message")
	// File may not exist yet if nothing was written; that's fine.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return // nothing written at all — suppressed as expected
	}
	content := readLog(t, path)
	if strings.Contains(content, "secret debug message") {
		t.Errorf("debug message should be suppressed, but found in log: %s", content)
	}
}

func TestDebug_WritesWhenEnabled(t *testing.T) {
	_, path := setupLogger(t)
	SetDebug(true)
	t.Cleanup(func() { SetDebug(false) })
	Debug("debug enabled message")
	content := readLog(t, path)
	if !strings.Contains(content, "DEBUG") {
		t.Errorf("expected DEBUG in log when debug enabled, got: %s", content)
	}
	if !strings.Contains(content, "debug enabled message") {
		t.Errorf("expected message in log when debug enabled, got: %s", content)
	}
}

func TestInfo_WithKeyValueFields(t *testing.T) {
	_, path := setupLogger(t)
	Info("request complete", "status", 200, "latency", "5ms")
	content := readLog(t, path)
	if !strings.Contains(content, "status=200") {
		t.Errorf("expected key=value pair in log, got: %s", content)
	}
	if !strings.Contains(content, "latency=5ms") {
		t.Errorf("expected key=value pair in log, got: %s", content)
	}
}

func TestInfo_WithOddFields(t *testing.T) {
	// Odd number of fields: last one is printed as bare value.
	_, path := setupLogger(t)
	Info("odd", "key1", "val1", "orphan")
	content := readLog(t, path)
	if !strings.Contains(content, "key1=val1") {
		t.Errorf("expected key1=val1 in log, got: %s", content)
	}
	if !strings.Contains(content, "orphan") {
		t.Errorf("expected orphan value in log, got: %s", content)
	}
}

func TestError_WithKeyValueFields(t *testing.T) {
	_, path := setupLogger(t)
	Error("connection failed", "host", "localhost", "port", 5432)
	content := readLog(t, path)
	if !strings.Contains(content, "ERROR") {
		t.Errorf("expected ERROR level, got: %s", content)
	}
	if !strings.Contains(content, "host=localhost") {
		t.Errorf("expected host=localhost in log, got: %s", content)
	}
}
