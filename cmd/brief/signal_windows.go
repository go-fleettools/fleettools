//go:build windows

package main

import "os/exec"

// signalOf has nothing to report on Windows: a process there ends with a status
// and never with a signal, so every ExitError carries a number.
func signalOf(*exec.ExitError) string { return "" }
