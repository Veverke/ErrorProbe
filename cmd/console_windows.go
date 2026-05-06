//go:build windows

package cmd

import "golang.org/x/sys/windows"

// enableListVTP enables ANSI/VTP colour processing on Windows for ep list output.
// Called explicitly from printTable only — never from init() to avoid interfering
// with BubbleTea's own terminal initialisation in ep watch.
func enableListVTP() {
	for _, handle := range []windows.Handle{windows.Handle(windows.Stdout), windows.Handle(windows.Stderr)} {
		var mode uint32
		if err := windows.GetConsoleMode(handle, &mode); err != nil {
			continue
		}
		_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}
