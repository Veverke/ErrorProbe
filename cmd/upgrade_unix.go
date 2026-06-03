//go:build !windows

package cmd

import (
	"fmt"
	"os"
)

// replaceExecutable atomically replaces execPath with newPath via os.Rename.
// On Linux/macOS rename(2) is atomic when src and dst are on the same filesystem,
// which is guaranteed because newPath was created in the same directory as execPath.
func replaceExecutable(execPath, newPath string) error {
	if err := os.Rename(newPath, execPath); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf(
				"upgrade requires write permission to %s; try: sudo errorprobe upgrade",
				execPath,
			)
		}
		return fmt.Errorf("upgrade failed to replace %s: %w", execPath, err)
	}
	return nil
}
