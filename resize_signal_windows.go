//go:build windows

package main

import "os"

func terminalResizeSignals() []os.Signal {
	return nil
}
