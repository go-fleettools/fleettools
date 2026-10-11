//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// signalOf names the signal that killed the command, or "" if it exited
// normally.
//
// Separated by platform because WaitStatus is not portable: on Windows there is
// no signal to report, and a build that assumed there was would not compile
// there. The tests this fleet runs include windows-latest.
func signalOf(ee *exec.ExitError) string {
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return ""
	}
	return ws.Signal().String()
}
