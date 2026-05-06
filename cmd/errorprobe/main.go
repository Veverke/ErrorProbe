// Package main is the binary entry point for errorprobe.
package main

import (
	"os"

	"github.com/errorprobe/errorprobe/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		cmd.PrintError(err)
		os.Exit(1)
	}
}
