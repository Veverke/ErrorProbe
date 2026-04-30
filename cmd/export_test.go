// export_test.go exposes internal functions for use in package tests.
// It is compiled only when running tests.
package cmd

import (
	"io"

	"github.com/errorprobe/errorprobe/internal/config"
	"github.com/errorprobe/errorprobe/internal/health"
)

// EvalCheck exposes the internal evalCheck function for unit testing.
func EvalCheck(snap health.HealthSnapshot, check config.Check) (bool, []CheckResult) {
	return evalCheck(snap, check)
}

// WriteCheckJSON exposes writeCheckJSON for unit testing.
func WriteCheckJSON(w io.Writer, ok bool, failing []CheckResult) {
	writeCheckJSON(w, ok, failing)
}
