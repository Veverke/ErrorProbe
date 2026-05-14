//go:build !windows

package pid

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// SendHUP reads the PID file at path and sends SIGHUP to that process.
// Returns nil if the file does not exist (ep is not running) or the process
// is already gone. Returns an error only on unexpected failures.
func SendHUP(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading pid file: %w", err)
	}

	pidVal, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("invalid pid file content: %w", err)
	}

	proc, err := os.FindProcess(pidVal)
	if err != nil {
		return nil
	}

	if err := proc.Signal(syscall.SIGHUP); err != nil {
		if err.Error() == "os: process already finished" {
			return nil
		}
		return fmt.Errorf("sending SIGHUP to pid %d: %w", pidVal, err)
	}
	return nil
}
