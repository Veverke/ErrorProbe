package health

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
)

var (
	// ISO8601 / RFC3339 timestamps, e.g. 2024-01-15T10:30:00Z or 2024-01-15 10:30:00.123
	reTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)

	// Memory addresses, e.g. 0x7f3a2c01b940
	reHexAddr = regexp.MustCompile(`0x[0-9a-fA-F]+`)

	// UUIDs, e.g. 550e8400-e29b-41d4-a716-446655440000
	reUUID = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

	// Line numbers: "line 42", ":42" (after a colon at a non-space boundary)
	reLineNum = regexp.MustCompile(`(?i)\bline\s+\d+|:\d+`)

	// Standalone numeric IDs of 4+ digits that look like auto-incremented values.
	// This runs after UUID/timestamp/addr so those are already replaced.
	reNumericID = regexp.MustCompile(`\b\d{4,}\b`)

	reWhitespace = regexp.MustCompile(`\s+`)
)

// Fingerprint strips volatile parts of a log message and returns the first 16
// hex characters of its SHA-256 hash.  The function is pure (no I/O) and
// deterministic: identical normalised messages always produce identical hashes.
func Fingerprint(message string) string {
	s := message
	s = reTimestamp.ReplaceAllString(s, "<ts>")
	s = reHexAddr.ReplaceAllString(s, "<addr>")
	s = reUUID.ReplaceAllString(s, "<uuid>")
	s = reLineNum.ReplaceAllString(s, "<line>")
	s = reNumericID.ReplaceAllString(s, "<id>")
	s = strings.TrimSpace(s)
	s = reWhitespace.ReplaceAllString(s, " ")

	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)[:16]
}
