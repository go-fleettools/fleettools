// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

// stdin is the seam the tests feed a hook payload through.
var stdin io.Reader = os.Stdin

// The fleet's three statuses, named here so the tests read as the rules they
// assert: 0 the question was put and nothing was found, 1 a finding
// (cli.Found), 2 the question could not be put (cli.Unanswerable).
//
// ⛔ 2 covers BOTH "nothing was read" and "no file was named", and that is the
// point: an empty file must not read as a clean run, and neither must an empty
// command line. cli.Unanswerable's own doc comment says the same — *it is NOT
// the same as a clean result, and that is the whole reason it has its own
// status*. A fourth status for "unusable arguments" was dropped on the way in:
// it said nothing 2 does not, and a bespoke status is one more thing every
// caller has to learn.
const (
	exitNone     = 0
	exitDeclines = 1
	exitNothing  = 2
)

func main() {
	if dispatch(os.Args[1:], os.Stdout, os.Stderr, func(w io.Writer) { fleet.WarnIfStale(w) }) {
		return
	}
	cli.Run(RootCmd())
}

// dispatch handles hook mode and reports whether it did. warn is the staleness
// warning, which is a `gh api` call: ⛔ HOOK MODE NEVER CALLS IT. This hook runs
// after EVERY Bash call, in about twenty-five sessions on one machine, so a
// network round trip there is paid thousands of times an hour — and it would
// spend the shared REST budget on behalf of every other tool. It is a parameter
// so a test can assert it. The shape is `replystamp`'s, for the same reasons.
func dispatch(args []string, stdout, stderr io.Writer, warn func(io.Writer)) bool {
	if !hookMode(args) {
		warn(stderr)
		return false
	}
	// ⛔⛔ UNDER --hook THE STATUS BELONGS TO THE PROTOCOL, and cobra never
	// reaches RunE when a flag is malformed: a typo in the settings file would
	// otherwise make this command exit non-zero after every Bash call. A hook
	// must fail quietly, so the whole program does, before cobra parses
	// anything.
	driveHook(args, stdout, stderr)
	return true
}

// driveHook runs the command in hook mode and ALWAYS leaves the process with
// status 0.
func driveHook(args []string, stdout, stderr io.Writer) {
	if code := cli.Drive(RootCmd(), args, stdout, stderr); code != 0 {
		// On stderr, where a hook's output is logged rather than read as an
		// answer — and saying it, because a hook that failed silently is this
		// tool's own subject.
		fmt.Fprintf(stderr, "declines: NOT read: the command itself failed (status %d), so nothing was judged\n", code)
	}
}

// hookMode reports whether this run answers the hook protocol, read from the
// raw arguments because the answer has to be known BEFORE cobra parses them.
// It accepts every spelling pflag does, because pflag parses a boolean flag's
// value with strconv.ParseBool and a hand-written list of three covers four
// cases out of seven.
func hookMode(args []string) bool {
	for _, a := range args {
		name, value, _ := strings.Cut(a, "=")
		if name != "--hook" && name != "-hook" {
			continue
		}
		// ⛔ There is no `if !hasValue { return true }` branch, and the third
		// result of Cut is dropped on purpose: a bare --hook leaves value
		// empty, ParseBool("") fails, and the error branch below already
		// answers true. That branch was written first and a mutation sweep
		// reported it SURVIVED — no input could tell it from its absence,
		// because both paths give the same answer. The rule it stated lives in
		// this comment instead, where it cannot rot into a second code path.
		// An unparseable value means a hook: whoever wrote --hook=yes meant
		// one, and only an explicit false is not one.
		b, err := strconv.ParseBool(value)
		if err != nil {
			return true
		}
		return b
	}
	return false
}

const long = `declines reads an output file a tool wrote and prints the lines in which it
declined to do something -- "no record", "skipped", "not counted", "NOT read",
"allowing", "could not read".

It exists for one measured mistake: buildscan --vet exited 0, its last line
said "go vet passed under all 2", and line 2 of the same output said "no
record: the working tree has uncommitted changes". A refusal is printed where
the decision is TAKEN, near the top; the summary at the bottom is about what
the run FOUND; so a tail of the log holds the one and never the other.

  declines <file>...   print the declining lines of each file
  declines --hook      read a PostToolUse payload on stdin

--hook reports on the files a Bash command redirected its output to, and its
rule is narrow on purpose: the dangerous combination is a command that exited
without an error while its own output says it declined. A command that visibly
failed is already being read.

⛔ What it could not read is SAID, never counted as clean: an empty file, a
name holding $VAR or a glob, a file that would not open, a payload it could not
parse, a line past the bound. A target the command never created is skipped,
because the harness already showed that failure.

Exit status: 0 nothing declined, 1 declining lines found, 2 the question could
not be put -- nothing was read, because no file was named or because none could
be read. An empty file is not a clean run. Under --hook the status is always 0:
a PostToolUse hook cannot undo the command it is reading, and one that fails
reports nothing at all.`

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var hook bool
	root := cli.New("declines [file...]",
		"The lines where a tool said it did NOT do something, out of the file it wrote",
		long)
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if hook {
			return hookRun(cmd)
		}
		return fileMode(cmd, args)
	}
	root.Flags().BoolVar(&hook, "hook", false, "read a PostToolUse payload on stdin and report on the files the command redirected its output to")
	return root
}

// fileMode reports on files named on the command line.
func fileMode(cmd *cobra.Command, args []string) error {
	out := cli.Out(cmd)
	if len(args) == 0 {
		return cli.Unanswerable("name at least one output file to read: nothing was read, which is not a clean run")
	}
	var all []hit
	read, unread := 0, 0
	for _, path := range args {
		hits, lines, err := scanFile(path, declineWords)
		if err != nil {
			// ⛔ An unreadable file is reported, never counted as clean.
			fmt.Fprintf(cli.Err(cmd), "declines: %v — NOT read, so not counted as clean\n", err)
			unread++
			continue
		}
		read += lines
		all = append(all, hits...)
	}
	for _, h := range all {
		fmt.Fprintf(out, "%s:%d: %s\n", h.File, h.Line, h.Text)
	}
	switch {
	case len(all) > 0:
		fmt.Fprintf(out, "declines: %d declining line(s) in %d line(s) read\n", len(all), read)
		return cli.Found("%d declining line(s)", len(all))
	case read == 0:
		fmt.Fprintf(out, "declines: NOTHING READ (%d file(s) unreadable, %d line(s)) — this is not a clean run\n", unread, read)
		return cli.Unanswerable("nothing was read")
	default:
		fmt.Fprintf(out, "declines: no declining line in %d line(s) read\n", read)
		return nil
	}
}
