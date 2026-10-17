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
// there.
//
// ⛔ This comment used to end "the tests this fleet runs include
// windows-latest". That is true of go-crdt, where the lesson was learnt, and
// was never true of THIS repository: every lane in its ci.yml is ubuntu, so
// the Windows half was compiled by nothing at all. cmd/buildscan named the
// file on the day it was written. The lane now cross-compiles for windows and
// darwin, which is what makes the sentence above a statement about this
// repository rather than about another one.
func signalOf(ee *exec.ExitError) string {
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return ""
	}
	return ws.Signal().String()
}
