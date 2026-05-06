//go:build !windows

package cmd

// enableListVTP is a no-op on non-Windows platforms.
func enableListVTP() {}
