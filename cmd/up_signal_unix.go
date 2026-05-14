//go:build !windows

package cmd

import (
	"os"
	"os/signal"
	"syscall"
)

// listenReloadSignal returns a channel that receives whenever the process is
// sent SIGHUP, which ep reload uses to trigger a live PBR rule swap.
func listenReloadSignal() <-chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	return ch
}
