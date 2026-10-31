// Package cli carries the three things every command in this repository does
// the same way, so that they are decided once instead of thirty-two times.
//
// It exists because the migration from the standard `flag` package to cobra
// touched every command at the same place, and that was the moment to notice
// that the place was copied thirty-two times: the usage text, the exit-status
// convention, and the staleness note.
//
// # The exit-status convention
//
// Every command here answers with 0, 1 or 2, and the three are deliberately not
// collapsed into pass/fail:
//
//	0  the question was answered and the answer is clean
//	1  the question was answered and something was found
//	2  the question could NOT be answered
//
// A lane that cannot tell 1 from 2 eventually reads a scan that could not run
// as a fleet with nothing wrong, which is the failure these tools exist to
// prevent. cobra returns an error and nothing else, so the status travels
// inside the error, as an *Exit.
//
// # Flag spelling
//
// pflag accepts `--long` and `-s`; the standard `flag` package also accepted
// `-long`, and this fleet's call sites used that spelling everywhere. The
// migration therefore BREAKS them, loudly — `unknown shorthand flag: 'g' in
// -go` — which is the good case: the program's variable keeps its zero value
// and the command fails instead of quietly ignoring an argument.
//
// One case is silent and worth knowing: when every letter of a single-dash name
// is itself a registered one-letter shorthand, pflag reads a cluster and
// succeeds. `-ab` with `-a` and `-b` registered sets both. So do not register a
// one-letter shorthand whose letters can spell another flag's long name.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// An Exit carries the status a command should end with, and the reason.
//
// Err may be nil for a status that the command's own output has already
// explained: a table that lists what was found does not need the error repeated
// under it.
type Exit struct {
	Code int
	Err  error
}

func (e *Exit) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

func (e *Exit) Unwrap() error { return e.Err }

// Found returns the status for a question answered with a finding: the command
// worked, and what it looked for is there.
func Found(format string, a ...any) error {
	return &Exit{Code: 1, Err: fmt.Errorf(format, a...)}
}

// Unanswerable returns the status for a question that could not be put: an
// unreadable input, a missing credential, an empty population. It is NOT the
// same as a clean result, and that is the whole reason it has its own status.
func Unanswerable(format string, a ...any) error {
	return &Exit{Code: 2, Err: fmt.Errorf(format, a...)}
}

// Wrap turns any error into an unanswerable one, for the common case of a
// failure on the way to the measurement.
func Wrap(err error) error {
	if err == nil {
		return nil
	}
	var e *Exit
	if errors.As(err, &e) {
		return err // already carries a status; do not relabel it
	}
	return &Exit{Code: 2, Err: err}
}

// HelpRequested reports whether the run was a request for help rather than a
// request to do the work.
//
// cobra prints the help and returns a nil error, which is a status of 0 — and a
// command whose caller reads "0, carry on" would then print its help AND do its
// work. The standard `flag` package made this visible as flag.ErrHelp, and the
// tests written against that behaviour are what caught it here.
func HelpRequested(c *cobra.Command) bool {
	f := c.Flags().Lookup("help")
	return f != nil && f.Changed
}

// FromStatus turns a command's old integer status into the error cobra expects,
// so that a `run(...) int` written before this migration keeps its body exactly
// as it was.
//
// That matters more than it looks. The alternative is editing the inside of
// thirty-two functions to change every `return 1` and `return 2`, in files of up
// to eight hundred lines, where the statuses are chosen deliberately and are
// documented one defect at a time. Each such edit is a chance to turn a 1 into a
// 2 — to turn "I looked and found something" into "I could not look" — and that
// is precisely the confusion this repository's tools exist to prevent.
//
// A status this does not know becomes 2, because an unrecognised status is not
// evidence of a clean result.
func FromStatus(code int) error {
	switch code {
	case 0:
		return nil
	case 1:
		// The command has already printed what it found; the message is here
		// for a caller that wants a reason, and Status does not print it.
		return &Exit{Code: 1, Err: errFound}
	default:
		return &Exit{Code: code, Err: nil}
	}
}

var errFound = errors.New("the command reported a finding; its own output says which")

// New returns a command with the settings every command here wants.
//
// SilenceUsage: a measurement that failed must not bury its reason under forty
// lines of help. SilenceErrors: Run prints the error itself, once, with the
// mark the rest of the fleet uses.
func New(use, short, long string) *cobra.Command {
	c := &cobra.Command{
		Use:           use,
		Short:         short,
		Long:          long,
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// ⛔ THE OTHER DIFFERENCE BETWEEN flag AND pflag, and the one that changes
	// what a command MEANS rather than how it is spelled.
	//
	// The standard library: "Flag parsing stops just before the first non-flag
	// argument." pflag's default is the opposite — it reads flags anywhere in
	// the line, interspersed with the operands. So
	//
	//	brief -n 5 go test -count=1 ./...
	//
	// parsed under flag as: -n 5, then the operands `go test -count=1 ./...`.
	// Under pflag's default, -count=1 is read as a flag OF BRIEF, which it is
	// not, and the command fails — or worse, for a command that happens to have
	// a flag of that name, silently does something else.
	//
	// Every command in this repository takes operands, and three of them take a
	// whole command line as operands. So the old rule is restored here, once,
	// rather than remembered thirty-two times.
	c.Flags().SetInterspersed(false)

	// ⛔ HELP GOES TO STDERR, as the standard library's flag.Usage did.
	//
	// cobra writes help to stdout, and stdout is these commands' ANSWER: brief
	// prints the child command's output there, vulngate prints annotations a CI
	// parser reads, and every scan prints a table something downstream counts.
	// Help on that stream is a line nobody asked for in the middle of the data.
	// A repository-wide test asserts that no command writes to stdout before it
	// has run anything, and it is what caught this.
	c.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		w := cmd.ErrOrStderr()
		if cmd.Long != "" {
			fmt.Fprintln(w, cmd.Long)
		} else if cmd.Short != "" {
			fmt.Fprintln(w, cmd.Short)
		}
		fmt.Fprintf(w, "\nusage: %s\n", cmd.UseLine())
		if usages := cmd.Flags().FlagUsages(); usages != "" {
			fmt.Fprintf(w, "\nflags:\n%s", usages)
		}
	})
	return c
}

// OsExit is a variable so that a test can observe a status without ending the
// test binary.
var OsExit = os.Exit

// Run executes a command and exits with the status its error carries.
//
// It is the whole of every command's main(), so that no command can invent its
// own mapping from an error to a status — and so that the single-dash
// compatibility shim is applied in exactly one place rather than thirty-two.
func Run(c *cobra.Command) {
	// One line, and it goes through Drive, so that what a test drives and what
	// a user runs cannot diverge.
	if code := Drive(c, os.Args[1:], os.Stdout, os.Stderr); code != 0 {
		OsExit(code)
	}
}

// Exec is Drive for a caller that wants the ERROR rather than a status: it
// applies the argument shim, runs the command, and returns whatever came back
// without printing it.
//
// Some commands here were written as `run(args, out) error` rather than
// `run(...) int`, because their failures are sentences and not categories -- a
// missing index.html, a directory that is not one, a version that is neither
// MAJOR.MINOR nor main. Their tests read the returned error, so handing those
// tests a status would throw away precisely what they assert.
func Exec(c *cobra.Command, args []string, out, errOut io.Writer) error {
	c.SetOut(out)
	c.SetErr(errOut)
	shimmed, err := Compat(c, args, errOut)
	if err != nil {
		return err
	}
	c.SetArgs(shimmed)
	return c.Execute()
}

// Status executes a command, prints whatever its error has to say, and reports
// the status it would exit with.
//
// A status of 1 prints nothing extra: the command's own output has already
// listed what was found, and repeating it on stderr makes a lane's log read as
// if something went wrong on top of the finding.
func Status(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	var e *Exit
	if errors.As(err, &e) {
		if e.Code != 1 && e.Err != nil {
			complain(c, e.Err)
		}
		return e.Code
	}
	complain(c, err)
	return 2
}

// complain writes an error the way this repository's commands always have:
// the command's name, then what went wrong.
//
// Centralised rather than repeated, because the name is what a reader of a CI
// log has to see first when four tools write to the same stream — and because
// leaving it to each call site means thirty-two chances to forget it. The
// prefix is skipped when the message already opens with the name, so a usage
// line does not come out as "covblocks: usage: covblocks ...".
func complain(c *cobra.Command, err error) {
	msg := err.Error()
	name := c.Name()
	if strings.HasPrefix(msg, name) {
		fmt.Fprintln(c.ErrOrStderr(), "⛔", msg)
		return
	}
	fmt.Fprintf(c.ErrOrStderr(), "⛔ %s: %s\n", name, msg)
}

// Drive runs a command exactly as Run does — argument shim included — and
// returns the status instead of exiting.
//
// It is what every command's `run(args, out, errOut) int` now is, which does two
// things at once. The tests written against that signature keep working
// unchanged through the migration, so a conversion cannot quietly change what a
// command does while its tests still pass. And what those tests exercise
// becomes the REAL surface: pflag parsing, the compatibility shim, the exit
// status mapping. Before this they drove a hand-rolled FlagSet that no user
// ever reached.
func Drive(c *cobra.Command, args []string, out, errOut io.Writer) int {
	c.SetOut(out)
	c.SetErr(errOut)
	shimmed, err := Compat(c, args, errOut)
	if err != nil {
		fmt.Fprintln(errOut, "⛔", err)
		var e *Exit
		if errors.As(err, &e) {
			return e.Code
		}
		return 2
	}
	c.SetArgs(shimmed)
	return Status(c)
}

// Out and Err are the writers a command must print through, so that a test can
// read what it wrote. Reaching for os.Stdout directly is what made the previous
// generation of these commands testable only by running them.
func Out(c *cobra.Command) io.Writer { return c.OutOrStdout() }
func Err(c *cobra.Command) io.Writer { return c.ErrOrStderr() }
