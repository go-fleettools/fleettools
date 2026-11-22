// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

// handlayout refuses hand-written UI in a go-widgets application.
//
//	handlayout --diff <base>      findings the range base..HEAD ADDS; exit 1 if any
//	handlayout --hook             PreToolUse hook on Bash: refuse a gitpush that adds one
//	handlayout --repo <clone>     audit the code already there (the backlog sweep)
//
// ⛔ WHY IT EXISTS. The rule is old (2026-07-28): a go-widgets application never
// computes or draws its own UI; what is missing goes into go-widgets/toolkit. It
// was broken again on 2026-11-20/21 in go-agentsync/agentscope (splitter weights
// turned into pane sizes by hand, a panel laid out by hand, wrap widths computed
// as width / average glyph width), and two visible defects followed whose root
// causes were in the toolkit (toolkit#605, #606). A note did not stop it twice;
// this is the gate (fleettools#384).
//
// # What it detects
//
// With go/packages, go/ast and go/types over the non-test Go files — resolved
// types, never a regexp on text:
//
//	painter-primitive   a call of a go-widgets/painter drawing method
//	                    (FillRect, StrokeRect, Text, PutPixel, an image blit ...)
//	layout-arithmetic   + - * / over toolkit/painter geometry or metrics: a Rect
//	                    field (X Y W H), an exported metric such as SplitterHandleW
//	                    or StatusbarH, TextWidth and the like — directly, or through
//	                    a local computed from them
//	hit-test            two or more bound comparisons over such values in one
//	                    && / || chain (a point-in-rect test written by hand)
//
// A plain i+1, arithmetic on the application's own values, a toolkit constant
// that is not a metric, and string concatenation do not fire.
//
// # What it never looks at
//
// go-widgets/toolkit and go-widgets/painter themselves (the module path), any
// _test.go, and any repository that does not depend on them.
//
// # Waiver, and what it cannot read
//
// A line "handlayout-waiver: <reason>" in a commit message of the range turns the
// refusal into an allowance and the reason is printed. Whatever the detector
// cannot read (no go.mod, a package that does not type-check, no remote branch
// to measure from) is ALLOWED and REPORTED: unread is never counted as clean, so
// --diff and --repo exit 2 for it, and the hook allows with the notice.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

// rangeReport is the verdict on a pushed range.
type rangeReport struct {
	Audit    Audit
	Added    []Finding // the findings on lines the range adds
	Waivers  []string  // the reasons of the waiver lines of the range
	Unreadly []string  // what could not be read, from the audit or the range
}

// checkRange audits the tree at dir and keeps what base..HEAD adds.
func checkRange(dir, base string) (rangeReport, error) {
	var r rangeReport
	r.Audit = auditRepo(dir)
	r.Unreadly = append(r.Unreadly, r.Audit.Unread...)
	if !r.Audit.Consumer {
		return r, nil
	}
	added, err := addedLines(dir, base)
	if err != nil {
		return r, err
	}
	prefix, err := git(dir, "rev-parse", "--show-prefix")
	if err != nil {
		return r, err
	}
	prefix = strings.TrimSpace(prefix)
	for _, f := range r.Audit.Findings {
		if findingAdded(f, added, prefix) {
			r.Added = append(r.Added, f)
		}
	}
	if len(r.Added) > 0 {
		if r.Waivers, err = waivers(dir, base); err != nil {
			return r, err
		}
	}
	return r, nil
}

func counts(fs []Finding) string {
	n := map[string]int{}
	for _, f := range fs {
		n[f.Detector]++
	}
	var parts []string
	for _, d := range []string{detPainter, detArith, detHit} {
		parts = append(parts, fmt.Sprintf("%s %d", d, n[d]))
	}
	return strings.Join(parts, ", ")
}

func printFindings(w io.Writer, fs []Finding) {
	seen := map[string]bool{}
	for _, f := range fs {
		fmt.Fprintln(w, f)
		seen[f.Detector] = true
	}
	var ds []string
	for d := range seen {
		ds = append(ds, d)
	}
	sort.Strings(ds)
	for _, d := range ds {
		fmt.Fprintf(w, "  %s: %s\n", d, advice[d])
	}
}

func printUnread(w io.Writer, u []string) {
	for _, x := range u {
		fmt.Fprintf(w, "UNREAD: %s\n", x)
	}
}

func diffMode(out io.Writer, dir, base string) error {
	r, err := checkRange(dir, base)
	if err != nil {
		return cli.Wrap(err)
	}
	switch {
	case r.Audit.Exempt != "":
		fmt.Fprintf(out, "handlayout: exempt: %s\n", r.Audit.Exempt)
		return nil
	case !r.Audit.Consumer && len(r.Unreadly) == 0:
		fmt.Fprintln(out, "handlayout: not a go-widgets consumer: nothing to check")
		return nil
	}
	printUnread(out, r.Unreadly)
	if len(r.Added) > 0 {
		printFindings(out, r.Added)
		if len(r.Waivers) > 0 {
			for _, w := range r.Waivers {
				fmt.Fprintf(out, "handlayout: WAIVED %d finding(s): %s\n", len(r.Added), w)
			}
			return nil
		}
		fmt.Fprintf(out, "handlayout: %d finding(s) added (%s); waive with a commit line \"handlayout-waiver: <reason>\"\n", len(r.Added), counts(r.Added))
		return cli.FromStatus(1)
	}
	if len(r.Unreadly) > 0 {
		return cli.FromStatus(2)
	}
	fmt.Fprintf(out, "handlayout: clean: no finding added by %s..HEAD\n", base)
	return nil
}

func repoMode(out io.Writer, dir string) error {
	a := auditRepo(dir)
	switch {
	case a.Exempt != "":
		fmt.Fprintf(out, "handlayout: exempt: %s\n", a.Exempt)
		return nil
	case !a.Consumer && len(a.Unread) == 0:
		fmt.Fprintln(out, "handlayout: not a go-widgets consumer: nothing to check")
		return nil
	}
	printUnread(out, a.Unread)
	if len(a.Findings) > 0 {
		printFindings(out, a.Findings)
		fmt.Fprintf(out, "handlayout: %d finding(s) (%s)\n", len(a.Findings), counts(a.Findings))
		return cli.FromStatus(1)
	}
	if len(a.Unread) > 0 {
		return cli.FromStatus(2)
	}
	fmt.Fprintln(out, "handlayout: clean: 0 findings")
	return nil
}

var longHelp = `handlayout refuses hand-written UI in a go-widgets application.

  handlayout --diff <base>     the findings that base..HEAD adds (exit 1 if any)
  handlayout --hook            read a PreToolUse payload on stdin; deny a gitpush
                               whose pushed range adds one
  handlayout --repo <clone>    audit the code already there

Run --diff and --repo from the repository, or give --dir.

DETECTORS (type-resolved with go/types, over non-test Go files):
  painter-primitive   a call of a go-widgets/painter drawing method
  layout-arithmetic   + - * / over toolkit/painter geometry or metrics (Rect
                      fields, SplitterHandleW-like constants, TextWidth ...),
                      directly or through a local computed from them
  hit-test            two or more bound comparisons over such values in one
                      && / || chain

EXEMPT: go-widgets/toolkit and go-widgets/painter themselves, any _test.go, a
repository that does not depend on them.

WAIVER: a commit message line of the pushed range, "handlayout-waiver: <reason>";
the reason is required and printed.

UNREAD (no go.mod, a package that does not type-check, no origin branch to
measure from) is allowed by the hook with a notice and exits 2 elsewhere:
never counted as clean.

The hook entry (PreToolUse, matcher Bash):
  {"type": "command", "command": "handlayout --hook"}

Exit status: 0 clean or waived, 1 findings, 2 unread. --hook always exits 0; the
answer is the JSON on stdout.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through SetArgs.
func RootCmd() *cobra.Command {
	var hook bool
	var diff, repo, dir string
	c := cli.New("handlayout --diff <base> | --hook | --repo <clone>",
		"Refuse hand-written layout and drawing in a go-widgets application", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		out := cli.Out(cmd)
		switch {
		case hook:
			return cli.FromStatus(hookMode(out, cli.Err(cmd), cmd.InOrStdin()))
		case cmd.Flags().Changed("diff"):
			if dir == "" {
				dir = "."
			}
			return diffMode(out, dir, diff)
		case cmd.Flags().Changed("repo"):
			return repoMode(out, repo)
		}
		return cli.Unanswerable("nothing to do: give --diff <base>, --hook or --repo <clone>")
	}
	c.Flags().BoolVar(&hook, "hook", false, "read a PreToolUse payload on stdin: deny a gitpush whose range adds hand layout")
	c.Flags().StringVar(&diff, "diff", "", "report the findings that <base>..HEAD adds")
	c.Flags().StringVar(&repo, "repo", "", "audit the clone's existing code")
	c.Flags().StringVar(&dir, "dir", "", "the repository for --diff (default: the current directory)")
	return c
}
