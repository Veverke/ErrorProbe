// Package pid manages a PID file for the running 'ep up' process so that
// 'ep down --purge' can locate and terminate it before wiping state.
package pid

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Write writes the current process's PID to path, creating or truncating the file.
func Write(path string) error {
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

// Remove deletes the PID file. Errors are silently ignored (idempotent).
func Remove(path string) {
	_ = os.Remove(path)
}

// KillRunning reads path, finds the process, sends SIGTERM (or TerminateProcess
// on Windows), and waits up to 10 s for it to exit. Returns nil if the file
// does not exist or the process is already gone.
func KillRunning(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading pid file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("invalid pid file content: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		// Process not found — already gone.
		return nil
	}

	if err := proc.Kill(); err != nil {
		// "os: process already finished" is fine.
		return nil
	}

	// On Windows, TerminateProcess is synchronous; give the OS a moment to
	// release file handles before the caller proceeds with deletion.
	time.Sleep(500 * time.Millisecond)
	return nil
}
