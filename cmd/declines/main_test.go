// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// mainRole is the environment variable that turns this test binary into the
// program itself. ⛔ It exists because main()'s own line -- `if dispatch(...) {
// return }` -- is the wiring that makes a malformed flag harmless, and a test
// that drives dispatch() directly leaves that line held by nothing: a mutation
// sweep reported it SURVIVED while every rule it implements was green. The only
// test that can hold it is one that RUNS the program, so this binary runs it.
const mainRole = "DECLINES_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(mainRole) == "1" {
		// os.Args here are the ones the test below passed, because TestMain
		// runs before testing parses its own flags.
		main()
		return
	}
	os.Exit(m.Run())
}

// runMain runs the program in a child process and returns its status and
// output. The child never reaches the staleness warning: that is a `gh api`
// call, and these arguments all take the hook path, which must never make one.
func runMain(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), mainRole+"=1")
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	var code int
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("the child did not run at all: %v", err)
		}
		code = ee.ExitCode()
	}
	return code, out.String(), errOut.String()
}

// ⛔⛔ THE RULE THIS HOLDS: under --hook the status belongs to the protocol, and
// cobra never reaches RunE when a flag is malformed. Without main()'s dispatch
// line the program falls through to cli.Run, which exits non-zero -- so one
// typo in settings.json would report a failing hook after EVERY Bash call, in
// about twenty-five sessions on this machine. The child must exit 0 AND say on
// stderr that it read nothing, because a hook that fails silently is this
// tool's own subject.
func TestTheProgramItselfKeepsStatusZeroOnAMalformedFlagUnderHook(t *testing.T) {
	code, out, errOut := runMain(t, "", "--hook", "--no-such-flag")
	if code != 0 {
		t.Fatalf("exited %d, stderr:\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "the command itself failed") {
		t.Fatalf("the failure was not said out loud; stderr:\n%s", errOut)
	}
	if out != "" {
		t.Fatalf("a hook that judged nothing printed context:\n%s", out)
	}
}

// And the same program, given a payload it can read, reports the decline: the
// test above would also pass on a program that answered nothing at all.
func TestTheProgramItselfReportsADeclineThroughTheHook(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/out.txt", []byte("ok\nbuildscan: no record: the working tree has uncommitted changes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := `{"hook_event_name":"PostToolUse","tool_name":"Bash","cwd":"` + dir + `","tool_input":{"command":"buildscan --vet > out.txt 2>&1"}}`
	code, out, errOut := runMain(t, payload, "--hook")
	if code != 0 {
		t.Fatalf("exited %d, stderr:\n%s", code, errOut)
	}
	if !strings.Contains(out, "no record") {
		t.Fatalf("the decline was not reported; stdout:\n%s\nstderr:\n%s", out, errOut)
	}
}
