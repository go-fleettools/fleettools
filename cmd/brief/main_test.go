package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The whole point: the status survives, where a pipe to head loses it.
//
// The control is the pipe itself, run for real rather than described, because
// the claim this tool rests on is about the shell and not about Go: a
// pipeline's status is its LAST command's, and head always succeeds.
func TestTheStatusSurvivesWhereAPipeToHeadLosesIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the control runs a POSIX shell pipeline")
	}
	// The control, first: this is what goes wrong without this tool.
	sh := exec.Command("sh", "-c", "false | head -3")
	if err := sh.Run(); err != nil {
		t.Fatalf("`false | head -3` failed, so the premise of this tool does not hold here: %v", err)
	}

	var out, errOut bytes.Buffer
	if code := run([]string{"--", "false"}, &out, &errOut); code == 0 {
		t.Error("brief exited 0 for a command that failed")
	}
	if !strings.Contains(errOut.String(), "exit: 1") {
		t.Errorf("it did not report the status: %q", errOut.String())
	}
}

// It exits with the child's status, not merely a non-zero one, so a caller can
// tell 2 from 127 from 1.
func TestItExitsWithTheChildsOwnStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh to choose an exit status")
	}
	for _, want := range []int{0, 1, 2, 42} {
		var out, errOut bytes.Buffer
		got := run([]string{"--", "sh", "-c", fmt.Sprintf("exit %d", want)}, &out, &errOut)
		if got != want {
			t.Errorf("a command exiting %d gave %d", want, got)
		}
		if !strings.Contains(errOut.String(), fmt.Sprintf("exit: %d", want)) {
			t.Errorf("for %d it reported %q", want, errOut.String())
		}
	}
}

// The status goes to stderr and the output to stdout, so a caller capturing the
// output does not find the status line inside it.
func TestTheStatusDoesNotLandInTheOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses echo through sh")
	}
	var out, errOut bytes.Buffer
	run([]string{"--", "sh", "-c", "echo hello"}, &out, &errOut)
	if got := strings.TrimSpace(out.String()); got != "hello" {
		t.Errorf("stdout = %q, want just the command's own output", got)
	}
	if !strings.Contains(errOut.String(), "exit: 0") {
		t.Errorf("stderr = %q, want the status", errOut.String())
	}
}

// What is not shown is counted, because a tool that truncates in silence
// teaches its reader that the output was complete.
func TestWhatIsNotShownIsCounted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell loop to print lines")
	}
	script := "for i in 1 2 3 4 5 6 7 8 9 10 11 12; do echo line$i; done"

	var out, errOut bytes.Buffer
	run([]string{"-n", "3", "--", "sh", "-c", script}, &out, &errOut)
	got := out.String()
	for _, want := range []string{"line10", "line11", "line12", "9 of 12 line(s) not shown"} {
		if !strings.Contains(got, want) {
			t.Errorf("the tail of 3 does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "line1\n") {
		t.Errorf("the tail of 3 showed the first line:\n%s", got)
	}

	out.Reset()
	run([]string{"-head", "-n", "3", "--", "sh", "-c", script}, &out, &errOut)
	got = out.String()
	for _, want := range []string{"line1", "line2", "line3", "9 of 12 line(s) not shown"} {
		if !strings.Contains(got, want) {
			t.Errorf("the head of 3 does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "line12") {
		t.Errorf("the head of 3 showed the last line:\n%s", got)
	}

	// And output shorter than the window is shown whole, with nothing elided.
	out.Reset()
	run([]string{"-n", "50", "--", "sh", "-c", script}, &out, &errOut)
	if strings.Contains(out.String(), "not shown") {
		t.Errorf("twelve lines under a window of fifty claimed something was hidden:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "line1\n") || !strings.Contains(out.String(), "line12") {
		t.Errorf("the whole output was not shown:\n%s", out.String())
	}
}

// A command that is not there is said so, and gets the number a caller expects.
func TestACommandThatIsNotThereIsNamed(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--", "no-such-command-anywhere-xyzzy"}, &out, &errOut)
	if code != 127 {
		t.Errorf("exit %d, want 127", code)
	}
	if !strings.Contains(errOut.String(), "not found") {
		t.Errorf("it did not say what was wrong: %q", errOut.String())
	}
}

// A signal is named rather than turned into a number.
//
// 128+n is the shell's convention, and reporting it invites arithmetic on
// something that is not a count: a command "exiting 137" reads like a status
// its author chose.
func TestASignalIsNamedAndNotNumbered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("there are no signals to report on Windows")
	}
	var out, errOut bytes.Buffer
	run([]string{"--", "sh", "-c", "kill -9 $$"}, &out, &errOut)
	got := errOut.String()
	if !strings.Contains(got, "killed by signal") {
		t.Errorf("a killed command reported %q", got)
	}
	if strings.Contains(got, "137") || strings.Contains(got, "exit: ") {
		t.Errorf("it gave a signal a status number: %q", got)
	}
}

// It reads its arguments: the lesson redscan#77 learned, where -h was a full
// fleet sweep.
func TestItReadsItsArguments(t *testing.T) {
	for _, c := range []struct {
		name string
		argv []string
		want int
	}{
		{"no command at all", []string{}, 2},
		{"only a separator", []string{"--"}, 2},
		{"an unknown flag", []string{"-bogus", "--", "true"}, 2},
		{"a negative window", []string{"-n", "-1", "--", "true"}, 2},
		{"help", []string{"-h"}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if got := run(c.argv, &out, &errOut); got != c.want {
				t.Errorf("exit %d, want %d (stderr: %q)", got, c.want, errOut.String())
			}
			if out.Len() != 0 {
				t.Errorf("it wrote to stdout before running anything: %q", out.String())
			}
		})
	}
}

// Nothing is run before the arguments are understood, which is what makes the
// test above more than a status check.
func TestNothingRunsBeforeTheArgumentsAreRead(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "it-ran")
	script := filepath.Join(dir, "touch-marker")
	body := "#!/bin/sh\ntouch " + marker + "\n"
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the command")
	}
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	// The control: given properly, it does run and the marker appears.
	var out, errOut bytes.Buffer
	if code := run([]string{"--", script}, &out, &errOut); code != 0 {
		t.Fatalf("the control did not run: %d %q", code, errOut.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the control left no marker, so this test cannot see a run: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	// And with a bad flag in front of it, nothing runs.
	run([]string{"-bogus", "--", script}, &out, &errOut)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a command ran although its arguments were not understood")
	}
}
