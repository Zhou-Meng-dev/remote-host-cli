//go:build !windows

package main

import (
	"os"
	"syscall"
)

func terminalResizeSignals() []os.Signal {
	return []os.Signal{syscall.SIGWINCH}
}
