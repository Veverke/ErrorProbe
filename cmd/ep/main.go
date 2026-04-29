// Package main is the binary entry point for the "ep" alias of errorprobe.
package main

import (
	"fmt"
	"os"

	"github.com/errorprobe/errorprobe/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
