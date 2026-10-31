package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// ⛔ cobra answers a help request with a nil error, which is a status of 0. A
// caller that reads the status alone prints the help and then does the work.
//
// The standard `flag` package made this visible as flag.ErrHelp, and a test
// written against that behaviour — modrename's TestParseArgs, asserting
// ["-h"] => (0, false) — is what caught it during the migration. Both halves
// here, because a guard that answered "help was requested" to every run would
// stop every command from doing anything.
func TestAHelpRequestIsAStatusOfZeroAndNotAReasonToProceed(t *testing.T) {
	ran := false
	build := func() *cobra.Command {
		c := New("probe", "a probe", "the long help")
		c.RunE = func(*cobra.Command, []string) error { ran = true; return nil }
		return c
	}

	for _, help := range [][]string{{"-h"}, {"--help"}} {
		ran = false
		c := build()
		var out bytes.Buffer
		code := Drive(c, help, &out, &out)
		if code != 0 {
			t.Errorf("%v exited %d, want 0", help, code)
		}
		if ran {
			t.Errorf("%v ran the command's work as well as printing help", help)
		}
		if !HelpRequested(c) {
			t.Errorf("%v: HelpRequested = false, want true", help)
		}
		if !strings.Contains(out.String(), "the long help") {
			t.Errorf("%v printed %q, want the long help", help, out.String())
		}
	}

	// ⛔ And it must be on STDERR. stdout is the command's answer — a child's
	// output, a CI annotation, a table something downstream counts — so help
	// printed there is a line nobody asked for in the middle of the data.
	for _, help := range [][]string{{"-h"}, {"--help"}} {
		var stdout, stderr bytes.Buffer
		c := build()
		Drive(c, help, &stdout, &stderr)
		if stdout.Len() != 0 {
			t.Errorf("%v wrote %q to stdout; help belongs on stderr", help, stdout.String())
		}
		if !strings.Contains(stderr.String(), "the long help") {
			t.Errorf("%v: stderr = %q, want the long help", help, stderr.String())
		}
		if !strings.Contains(stderr.String(), "usage: probe") {
			t.Errorf("%v: stderr = %q, want a usage line", help, stderr.String())
		}
	}

	// The other half: an ordinary run must not read as a help request, or no
	// command would ever do its work.
	ran = false
	c := build()
	var out bytes.Buffer
	if code := Drive(c, nil, &out, &out); code != 0 {
		t.Errorf("an ordinary run exited %d, want 0", code)
	}
	if !ran {
		t.Error("an ordinary run did not run the command's work")
	}
	if HelpRequested(c) {
		t.Error("an ordinary run reads as a help request")
	}
}

// ⛔ The difference between flag and pflag that changes MEANING: flag stops
// parsing at the first non-flag argument, pflag by default reads flags anywhere.
// A command that takes a whole command line as its operands — brief does — would
// otherwise have the CHILD's flags read as its own.
func TestFlagParsingStopsAtTheFirstOperandAsTheStandardLibraryDid(t *testing.T) {
	var n int
	var seen []string
	c := New("probe", "", "")
	c.Flags().IntVar(&n, "n", 10, "")
	c.RunE = func(_ *cobra.Command, args []string) error { seen = args; return nil }

	// The planted positive: the child's -count=1 must reach the operands, not
	// be read as a flag of the probe.
	if code := Drive(c, []string{"--n", "5", "go", "test", "-count=1", "./..."}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if n != 5 {
		t.Errorf("--n = %d, want 5", n)
	}
	if strings.Join(seen, " ") != "go test -count=1 ./..." {
		t.Errorf("operands = %v, want the child's whole command line", seen)
	}

	// The other half: a flag BEFORE the first operand is still a flag, or the
	// command would take no options at all.
	n = 0
	c2 := New("probe", "", "")
	c2.Flags().IntVar(&n, "n", 10, "")
	c2.RunE = func(_ *cobra.Command, args []string) error { seen = args; return nil }
	if code := Drive(c2, []string{"--n", "7", "file.txt"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if n != 7 || strings.Join(seen, " ") != "file.txt" {
		t.Errorf("--n = %d, operands = %v; want 7 and [file.txt]", n, seen)
	}
}

// FromStatus exists so that a `run(...) int` keeps its body through the
// migration. The mapping has to be exact: turning a 1 into a 2 turns "I looked
// and found something" into "I could not look", which is the confusion these
// tools exist to prevent.
func TestFromStatusKeepsEachStatusItself(t *testing.T) {
	if err := FromStatus(0); err != nil {
		t.Errorf("FromStatus(0) = %v, want nil", err)
	}
	for _, code := range []int{1, 2, 3, 7} {
		err := FromStatus(code)
		var e *Exit
		if !asExit(err, &e) {
			t.Fatalf("FromStatus(%d) = %T, want an *Exit", code, err)
		}
		if e.Code != code {
			t.Errorf("FromStatus(%d) carries %d", code, e.Code)
		}
	}
	// A status of 1 must not print a second line on stderr: the command's own
	// output has already listed what it found.
	c := New("probe", "", "")
	c.RunE = func(cmd *cobra.Command, _ []string) error { return FromStatus(1) }
	var out, errOut bytes.Buffer
	if code := Drive(c, nil, &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing: a finding is not an error on top of itself", errOut.String())
	}

	// And a status of 2 MUST say why, because nothing else has.
	c = New("probe", "", "")
	c.RunE = func(cmd *cobra.Command, _ []string) error { return Unanswerable("the input was unreadable") }
	out.Reset()
	errOut.Reset()
	if code := Drive(c, nil, &out, &errOut); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "the input was unreadable") {
		t.Errorf("stderr = %q, want the reason", errOut.String())
	}
}
