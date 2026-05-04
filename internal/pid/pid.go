// Package pid manages a PID file for the running 'ep up' process so that
// 'ep down --purge' can locate and terminate it before wiping state.
package pid

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
// on Windows), and waits for it to exit. Returns nil if the file does not exist
// or the process is already gone. Returns a KillResult describing what happened.
type KillResult struct {
	Found    bool   // PID file existed
	PID      int    // PID that was targeted
	Killed   bool   // Kill() succeeded
	KillErr  error  // Kill() error (nil = success or already gone)
	WaitErr  error  // Wait() error
}

func KillRunning(path string) (KillResult, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return KillResult{}, nil
	}
	if err != nil {
		return KillResult{}, fmt.Errorf("reading pid file: %w", err)
	}

	pidVal, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return KillResult{}, fmt.Errorf("invalid pid file content: %w", err)
	}

	res := KillResult{Found: true, PID: pidVal}

	proc, err := os.FindProcess(pidVal)
	if err != nil {
		// On Unix: process not found. On Windows: FindProcess never errors.
		return res, nil
	}

	res.KillErr = proc.Kill()
	if res.KillErr != nil {
		// "os: process already finished" is fine — it's gone.
		return res, nil
	}
	res.Killed = true

	// proc.Wait() works for child processes. For non-children on Windows it
	// returns immediately; use a brief sleep as a best-effort drain.
	_, res.WaitErr = proc.Wait()
	return res, nil
}
