// Command brief runs a command, shows the end of what it said, and keeps its
// exit status.
//
// It exists because `cmd | head` and `cmd | tail` throw the status away, and no
// amount of knowing that has stopped it happening. A pipeline's status is its
// LAST command's, and head always succeeds:
//
//	$ false | head -3 ; echo $?
//	0
//
// That is not a quirk of one shell. POSIX says it, in XCU 2.9.2 Pipelines: the
// status of a pipeline "shall be the exit status of the last command that is
// executed in the list". The same section notes that an implementation MAY
// provide a way to derive the status "from the exit statuses of all of the
// commands in the pipeline, not just the last" -- which is what bash's
// PIPESTATUS and `set -o pipefail` are, and the word is *may*. So `pipefail` is
// an extension to lean on only where the shell is known, and this works
// wherever the binary runs.
//
// That has cost real answers here. Three times in one day a version tag was
// pushed onto a commit that did not contain the merge, because the chain was
// `ghmerge 21 | tail -2 && git tag …`: the refusal printed, the status vanished,
// the tag went onto the wrong commit, and a published tag does not move. On
// 2026-10-11 it happened three more times in one session while reading tools'
// exit statuses, including twice on this fleet's own binaries -- once reporting
// "exit 0" for a usage error that in fact exits 2, and once reading "actionlint
// is not installed" from a run where actionlint had worked and reported.
//
// So this is not a convenience. It is the convenient path made correct:
//
//	brief -- go test ./...          # the last 10 lines, and `exit: N` on stderr
//	brief -n 3 -- gh pr view 42     # three lines
//	brief -head -n 5 -- mutsweep …  # the FIRST five, for a tool that leads
//
// ⛔ IT EXITS WITH THE CHILD'S STATUS, which is the half a display wrapper would
// forget. `brief -- false && next` does not run next, where `false | head &&
// next` does. A tool that fixed only the printing would leave every `&&` chain
// as wrong as it was.
//
// ⛔ THE STATUS GOES TO STDERR, and the command's own output to stdout, so a
// caller may still capture the output without the status line landing in it.
//
// ⛔ A SIGNAL IS NOT A NUMBER. A command killed by SIGKILL has no exit status,
// and reporting one -- 137, the shell's convention -- invites arithmetic on
// something that is not a count. It is named instead: `killed by signal: killed`.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Runs the command, shows that much of its output, and exits with ITS status.

` + "`cmd | head`" + ` would exit 0 however the command ended — the status read is
the LAST stage's — and that is the whole reason this exists.

    brief -n 20 -- go test ./...
    brief --head -- go build ./...

The default is the LAST lines, because that is where a test suite says what
failed. Flag parsing stops at the first operand, so the command's own flags
reach the command and not this one; ` + "`--`" + ` is accepted and not required.

Exit status: the command's own. 127 when it was not found, 2 when it could not
be run or --n was negative.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var (
		n    int
		head bool
	)
	c := cli.New("brief [flags] [--] command [args...]",
		"Run a command, show part of its output, exit with ITS status", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(summarise(cmd, n, head, args))
	}
	f := c.Flags()
	// Both spellings of the old flag: "n" is registered as the long name AND as
	// the shorthand, so `-n 20` and `--n 20` both reach it. The call sites in
	// this repository's README and scripts use the first.
	f.IntVarP(&n, "n", "n", 10, "how many lines of the command's output to show")
	f.BoolVar(&head, "head", false, "show the FIRST lines rather than the last;\n"+
		"the default is the last, because that is where a test suite says what failed")
	return c
}

// run keeps the signature this command's tests were written against.
func run(argv []string, stdout, stderr io.Writer) int {
	return cli.Drive(RootCmd(), argv, stdout, stderr)
}

func summarise(cobraCmd *cobra.Command, n int, head bool, cmd []string) int {
	stdout, stderr := cli.Out(cobraCmd), cli.Err(cobraCmd)
	if len(cmd) == 0 {
		fmt.Fprintln(stderr, "usage: brief [--n 10] [--head] [--] command [args...]")
		return 2
	}
	if n < 0 {
		fmt.Fprintln(stderr, "brief: --n cannot be negative; 0 shows nothing at all")
		return 2
	}

	var buf bytes.Buffer
	c := exec.Command(cmd[0], cmd[1:]...)
	// One buffer for both, so the interleaving is the one the command wrote and
	// not the one two pipes happened to deliver.
	c.Stdout, c.Stderr = &buf, &buf
	c.Stdin = os.Stdin
	runErr := c.Run()

	fmt.Fprint(stdout, chosen(buf.String(), n, head))

	// What happened, said in the words the thing deserves.
	switch {
	case runErr == nil:
		fmt.Fprintln(stderr, "exit: 0")
		return 0
	case errors.Is(runErr, exec.ErrNotFound):
		fmt.Fprintf(stderr, "brief: %s: not found\n", cmd[0])
		return 127 // the shell's number for it, and the one a caller expects
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		if sig := signalOf(ee); sig != "" {
			// A signal has no exit status. Reporting 128+n would invite
			// arithmetic on something that is not a count.
			fmt.Fprintf(stderr, "killed by signal: %s\n", sig)
			return 2
		}
		fmt.Fprintf(stderr, "exit: %d\n", ee.ExitCode())
		return ee.ExitCode()
	}
	// Could not be started at all: a directory, no permission, a bad binary.
	fmt.Fprintf(stderr, "brief: %s could not run: %v\n", cmd[0], runErr)
	return 2
}

// chosen is the part of the output to show, with a line saying what was left out
// rather than trimming in silence.
func chosen(out string, n int, head bool) string {
	if n == 0 || out == "" {
		return elided(out, countLines(out), 0)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n") + "\n"
	}
	var shown []string
	if head {
		shown = lines[:n]
	} else {
		shown = lines[len(lines)-n:]
	}
	body := strings.Join(shown, "\n") + "\n"
	note := elided(out, len(lines), n)
	if head {
		return body + note
	}
	return note + body
}

// elided says how much was not shown. A tool that quietly truncates teaches its
// reader that the output is complete.
func elided(out string, total, shown int) string {
	hidden := total - shown
	if hidden <= 0 {
		return ""
	}
	return fmt.Sprintf("… %d of %d line(s) not shown\n", hidden, total)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSuffix(s, "\n"), "\n"))
}
