//go:build windows

package cmd

import "os"

// listenReloadSignal returns a channel that never receives on Windows, where
// SIGHUP is not available. PBR rule hot-reload is a no-op on this platform.
func listenReloadSignal() <-chan os.Signal {
	return make(chan os.Signal)
}
