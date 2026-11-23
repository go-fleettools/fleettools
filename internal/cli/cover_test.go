package cli

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestAnExitWithoutAReasonNamesItsStatus(t *testing.T) {
	if got := (&Exit{Code: 7}).Error(); got != "exit status 7" {
		t.Errorf("Error() = %q", got)
	}
}

// A command with only a Short description still prints it as its help.
func TestHelpFallsBackToTheShortDescription(t *testing.T) {
	c := New("probe", "the short line", "")
	var out bytes.Buffer
	Drive(c, []string{"-h"}, &out, &out)
	if !strings.HasPrefix(out.String(), "the short line\n") {
		t.Errorf("help = %q", out.String())
	}
}

// Run exits through OsExit with the command's status, and does not exit at all
// for a status of zero.
func TestRunExitsWithTheStatusOfTheCommand(t *testing.T) {
	oldArgs, oldExit := os.Args, OsExit
	t.Cleanup(func() { os.Args, OsExit = oldArgs, oldExit })
	exited := -1
	OsExit = func(code int) { exited = code }

	os.Args = []string{"probe"}
	c := New("probe", "s", "l")
	c.RunE = func(*cobra.Command, []string) error { return Unanswerable("no") }
	Run(c)
	if exited != 2 {
		t.Errorf("exit status %d, want 2", exited)
	}

	exited = -1
	c = New("probe", "s", "l")
	c.RunE = func(*cobra.Command, []string) error { return nil }
	Run(c)
	if exited != -1 {
		t.Errorf("a clean run exited with %d", exited)
	}
}

// Exec hands back the error itself, applies the shim, and refuses an ambiguous
// single-dash flag before running anything.
func TestExecReturnsTheErrorAndAppliesTheShim(t *testing.T) {
	var out, errOut bytes.Buffer
	ran := false
	build := func() *cobra.Command {
		c := New("probe", "s", "l")
		c.Flags().Bool("max", false, "")
		c.Flags().BoolP("a", "a", false, "")
		c.Flags().BoolP("b", "b", false, "")
		c.Flags().Bool("ab", false, "")
		c.RunE = func(*cobra.Command, []string) error { ran = true; return errors.New("the sentence") }
		return c
	}
	if err := Exec(build(), []string{"-max"}, &out, &errOut); err == nil || err.Error() != "the sentence" || !ran {
		t.Errorf("Exec returned %v (ran %v)", err, ran)
	}
	ran = false
	err := Exec(build(), []string{"-ab"}, &out, &errOut)
	var e *Exit
	if !errors.As(err, &e) || e.Code != 2 || ran {
		t.Errorf("an ambiguous flag gave %v (ran %v)", err, ran)
	}
}

// Drive turns the shim's refusal into a status and a printed reason.
func TestDriveReportsAnAmbiguousFlag(t *testing.T) {
	c := New("probe", "s", "l")
	c.Flags().BoolP("a", "a", false, "")
	c.Flags().BoolP("b", "b", false, "")
	c.Flags().Bool("ab", false, "")
	c.RunE = func(*cobra.Command, []string) error { t.Fatal("ran"); return nil }
	var out, errOut bytes.Buffer
	if code := Drive(c, []string{"-ab"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "ambiguous") {
		t.Errorf("status %d, stderr %q", code, errOut.String())
	}
}

// Status prints a plain error (one that carries no status) and maps it to 2; a
// message that opens with the command's name is not prefixed again.
func TestStatusPrintsAPlainErrorAndPrefixesItOnce(t *testing.T) {
	for _, c := range []struct{ msg, want string }{
		{"disk full", "⛔ probe: disk full\n"},
		{"probe: usage: probe [flags]", "⛔ probe: usage: probe [flags]\n"},
	} {
		cmd := New("probe", "s", "l")
		msg := c.msg
		cmd.RunE = func(*cobra.Command, []string) error { return errors.New(msg) }
		var out, errOut bytes.Buffer
		if code := Drive(cmd, nil, &out, &errOut); code != 2 || errOut.String() != c.want {
			t.Errorf("%q: status %d, stderr %q, want %q", c.msg, code, errOut.String(), c.want)
		}
	}
}

// A status of 1 is a finding the command already printed: nothing more is
// written. Any other status that carries a reason prints it.
func TestStatusOfOnePrintsNothingMore(t *testing.T) {
	cmd := New("probe", "s", "l")
	cmd.RunE = func(*cobra.Command, []string) error { return Found("found it") }
	var out, errOut bytes.Buffer
	if code := Drive(cmd, nil, &out, &errOut); code != 1 || errOut.Len() != 0 {
		t.Errorf("status %d, stderr %q", code, errOut.String())
	}
}

func TestOutAndErrAreTheCommandsWriters(t *testing.T) {
	c := New("probe", "s", "l")
	var out, errOut bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errOut)
	if Out(c) != &out || Err(c) != &errOut {
		t.Error("Out/Err did not return the command's writers")
	}
}

// A negative number is an operand, not a single-dash flag.
func TestANegativeNumberIsNotASingleDashFlag(t *testing.T) {
	if name, ok := singleDashLong("-10"); ok {
		t.Errorf("singleDashLong(-10) = %q, true", name)
	}
}
