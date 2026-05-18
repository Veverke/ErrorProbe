package learn

import (
	"regexp"
	"strings"
)

// volatilePattern matches fragments of a log message that carry per-instance
// information (IP addresses, UUIDs, hex addresses, large numbers, version
// paths, ISO-8601 timestamps). These are replaced by generic regex
// placeholders so the resulting pattern stays stable across restarts.
var (
	reIPv4    = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`)
	reIPv6    = regexp.MustCompile(`\b[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{0,4}){2,7}\b`)
	reUUID    = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	reHex     = regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`)
	reTS      = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?\b`)
	reBigNum  = regexp.MustCompile(`\b\d{5,}\b`)
	reVerPath = regexp.MustCompile(`/v\d+(?:\.\d+){0,2}`)
)

// ExtractPattern derives a stable regex pattern from a raw log message.
//
// The algorithm:
//  1. Strip volatile fragments (IPs, UUIDs, hex, timestamps, large numbers,
//     version paths) replacing each with a `\S+` placeholder.
//  2. Compute matchFraction = (stripped chars) / (original chars). When the
//     fraction exceeds 0.60 the message is mostly noise; the returned pattern
//     is empty to signal that the candidate should be discarded.
//  3. Trim leading/trailing whitespace and collapse internal whitespace runs.
//
// The caller is responsible for wrapping the result in the appropriate
// "regex:(?i)…" prefix before storing it in the rule's When map.
func ExtractPattern(msg string) (pattern string, matchFraction float64) {
	if msg == "" {
		return "", 1.0
	}
	orig := msg

	stripped := msg
	stripped = reTS.ReplaceAllString(stripped, `\S+`)
	stripped = reIPv4.ReplaceAllString(stripped, `\S+`)
	stripped = reIPv6.ReplaceAllString(stripped, `\S+`)
	stripped = reUUID.ReplaceAllString(stripped, `\S+`)
	stripped = reHex.ReplaceAllString(stripped, `\S+`)
	stripped = reBigNum.ReplaceAllString(stripped, `\d+`)
	stripped = reVerPath.ReplaceAllString(stripped, `/v\d+`)

	// Count replaced characters to compute the volatility fraction.
	removedChars := len([]rune(orig)) - len([]rune(
		reTS.ReplaceAllString(
			reIPv4.ReplaceAllString(
				reIPv6.ReplaceAllString(
					reUUID.ReplaceAllString(
						reHex.ReplaceAllString(
							reBigNum.ReplaceAllString(
								reVerPath.ReplaceAllString(orig, ""),
							""),
						""),
					""),
				""),
			""),
		""),
	))
	if len([]rune(orig)) > 0 {
		matchFraction = float64(removedChars) / float64(len([]rune(orig)))
	}

	// Normalise whitespace.
	fields := strings.Fields(stripped)
	pattern = strings.Join(fields, " ")

	return pattern, matchFraction
}

// EscapeForRegex escapes characters in s that have special meaning in Go
// regexes, except for the placeholder sequences we intentionally inserted.
// Since our placeholders (\S+ \d+) already use regex syntax, we only escape
// characters in the literal parts.
func EscapeForRegex(pattern string) string {
	// Split on our known placeholders to avoid double-escaping them.
	const ph1 = `\S+`
	const ph2 = `\d+`
	const ph3 = `/v\d+`
	parts := splitOnPlaceholders(pattern, []string{ph3, ph1, ph2})
	var b strings.Builder
	for _, p := range parts {
		switch p {
		case ph1, ph2, ph3:
			b.WriteString(p)
		default:
			b.WriteString(regexp.QuoteMeta(p))
		}
	}
	return b.String()
}

// splitOnPlaceholders splits s around the given literal substrings (which must
// not overlap). Returned tokens alternate between literal segments and
// placeholder matches.
func splitOnPlaceholders(s string, placeholders []string) []string {
	if len(placeholders) == 0 {
		return []string{s}
	}
	ph := placeholders[0]
	rest := placeholders[1:]
	idx := strings.Index(s, ph)
	if idx == -1 {
		return splitOnPlaceholders(s, rest)
	}
	before := s[:idx]
	after := s[idx+len(ph):]
	var result []string
	if before != "" {
		result = append(result, splitOnPlaceholders(before, rest)...)
	}
	result = append(result, ph)
	result = append(result, splitOnPlaceholders(after, placeholders)...)
	return result
}
