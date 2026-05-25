//go:build windows

package cmd

import (
	"fmt"
	"os"
)

// replaceExecutable replaces execPath with newPath using the deferred-rename
// pattern required on Windows, where a running .exe cannot be overwritten.
//
//  1. Remove any leftover .old from a previous upgrade attempt.
//  2. Rename the current binary to execPath+".old" (frees the filename).
//  3. Rename the new binary into place.
//  4. If step 3 fails, attempt to restore the original binary from .old.
//
// The .old file is cleaned up on the next startup by cleanupUpgradeArtifacts.
func replaceExecutable(execPath, newPath string) error {
	oldPath := execPath + ".old"

	// Remove any .old left from a previous failed upgrade.
	_ = os.Remove(oldPath)

	// Step 2: rename running binary → .old (Windows allows renaming a running exe).
	if err := os.Rename(execPath, oldPath); err != nil {
		return fmt.Errorf("renaming current binary to .old: %w", err)
	}

	// Step 3: rename new binary → final path.
	if err := os.Rename(newPath, execPath); err != nil {
		// Best-effort restore so the user is not left without a working binary.
		_ = os.Rename(oldPath, execPath)
		return fmt.Errorf("placing new binary: %w", err)
	}

	return nil
}
