// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

// recur says which lessons recorded a RECURRENCE and have no gate.
//
//	recur --memory ~/.claude/projects/<slug>/memory
//	recur --memory <dir> --accept <lesson> --gate 'go-fleettools/fleettools/cmd/viaonly'
//	recur --memory <dir> --accept <lesson> --no-gate 'why a gate cannot exist here'
//
// ⛔ WHY IT EXISTS. The user had to say it twice, and the second time like this:
// "une erreur qui se reproduit c'est un outil et une gate qui manque."
//
// A memory that records the same mistake a second time is a note that did not
// work. The file already says so — "Troisième récidive", "la deuxième fois
// aujourd'hui", "4th occurrence the same day" — and nothing reads those words,
// so the lesson keeps being filed and keeps being repeated. In one session three
// separate commands (gotest, prwait, ghrelease) each caught a mistake its author
// had already written a memory about. The memories did not hold; the commands
// did.
//
// # It does not cry wolf, and that is a design constraint
//
// Measured over 2349 memories on 2026-11-11: 31 say "récidive", 84 carry an
// ordinal with "fois" or "occurrence", 27 the English equivalent — about 5% of
// the corpus, essentially none of them with a gate. Reporting all of those on
// day one would be a wall of 120 findings, and a guard that produces a wall is
// a guard somebody turns off: this fleet has that measured twice.
//
// So the finding is not "a lesson has repeated". It is "a lesson has repeated
// AND NOBODY HAS DECIDED WHAT TO DO ABOUT IT" — which is what the LEDGER
// records. Everything known at the moment the ledger is seeded is a backlog
// entry, honestly marked as untriaged; after that the gate fires only on a
// lesson that NEWLY records a recurrence. That is the same shape as the ledger
// gating `ghissue --new` (#233).
//
// # The three conditions a gate has to meet here
//
//	a mandatory flag with no default   --memory, and --accept needs --gate or --no-gate
//	a refusal BEFORE the expense       it runs before gitpush/ghmerge/ghrelease
//	a waiver that STAMPS               --no-gate writes its reason, dated, into the ledger
//
// Exit status, following assetpath's and priorart's:
//
//	0  every recorded recurrence is in the ledger
//	1  at least one is not; each is named
//	2  REFUSED TO DECIDE: the memory directory or the ledger could not be read
//
// ⛔ 2 is never an absence. A directory that would not open is not a clean one,
// and this fleet has three memories of a sweep that could not read reporting
// zero.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Says which lessons recorded a RECURRENCE and have no gate.

A memory that records the same mistake twice is a note that did not work. The
file already says so in its own words; nothing reads them.

    recur --memory <dir>

reports every lesson whose text records a recurrence and that the ledger does
not account for. The ledger lives beside the memories as ` + ledgerName + `.

    recur --memory <dir> --accept <lesson> --gate <what enforces it now>
    recur --memory <dir> --accept <lesson> --no-gate '<why none can exist>'

account for one. --gate names the command, hook or test that now refuses the
mistake. --no-gate takes a REASON, never a bare flag, and the reason is written
into the ledger with the date: a waiver is made in public or not at all.

    recur --memory <dir> --seed

writes every recurrence known RIGHT NOW as an untriaged backlog entry, so the
gate starts clean and fires on what happens next rather than on history. The
backlog is counted in every later run, so it cannot be forgotten quietly.

Exit status: 0 accounted for, 1 at least one is not, 2 refused to decide — the
memory directory or the ledger could not be read. A directory that would not
open is never reported as a clean one.`

const ledgerName = ".recur-ledger.md"

// Strong markers: a sentence that says, in so many words, that this happened
// again. Weak ones are deliberately absent — "la même erreur" appears in prose
// about somebody else's mistake as often as about a recurrence, and "encore une
// fois" is rhetorical ("encore une fois, la mesure prime") about as often as it
// is factual. A marker that needs a judgement is not a marker.
//
// ⛔ The first run against the real corpus found the gap this list is for. It
// missed a-mechanism-wired-at-only-some-of-its-doors, whose body carries a
// section headed "⛔⛔ RÉCURRENCE le 2026-11-07" — because the list read
// "récidive" and not "récurrence". A detector that matches a vocabulary matches
// the words its author happened to use that day; 2349 memories were written by
// many sessions and do not share one. Measured when it was added: 11 files say
// récurrence, 8 say it repeated itself, 8 say the author did it again.
var markers = []*regexp.Regexp{
	regexp.MustCompile(`(?i)récidiv`),
	regexp.MustCompile(`(?i)récurrence|recurrence`),
	regexp.MustCompile(`(?i)\b(2e|3e|4e|5e|deuxième|troisième|quatrième|seconde)\s+(fois|occurrence)`),
	regexp.MustCompile(`(?i)\b(second|third|fourth|fifth|2nd|3rd|4th|5th)\s+(time|occurrence)`),
	regexp.MustCompile(`(?i)\bpour la (n|\d)\S*\s*fois`),
	regexp.MustCompile(`(?i)\b(twice|trois fois|deux fois) (in|en|dans|the same)`),
	regexp.MustCompile(`(?i)s'est répét|se répète|repeated itself|happened again`),
	regexp.MustCompile(`(?i)je l'ai refait|je le refais|did it again`),
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var o opts
	c := cli.New("recur --memory <dir> [flags]",
		"Which lessons recorded a recurrence and have no gate", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(check(cli.Out(cmd), cli.Err(cmd), o))
	}
	c.Flags().StringVar(&o.memory, "memory", "",
		"the memory directory to read (required, no default)")
	c.Flags().StringVar(&o.accept, "accept", "",
		"account for one lesson by name, with --gate or --no-gate")
	c.Flags().StringVar(&o.gate, "gate", "",
		"what now refuses this mistake: a command, a hook or a test")
	c.Flags().StringVar(&o.noGate, "no-gate", "",
		"why no gate can exist for this one; the reason is written into the ledger, dated")
	c.Flags().BoolVar(&o.seed, "seed", false,
		"write everything known right now as untriaged backlog, so the gate starts clean")
	return c
}

func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

type opts struct {
	memory string
	accept string
	gate   string
	noGate string
	seed   bool
}

// entry is one ledger line: a lesson, and what was decided about it.
type entry struct {
	lesson string
	state  string // "gate", "no-gate" or "backlog"
	detail string
}

func check(out, errOut io.Writer, o opts) int {
	if o.memory == "" {
		fmt.Fprintln(errOut, "recur: --memory is required: there is no default memory directory, and guessing one would read somebody else's")
		return 2
	}
	info, err := os.Stat(o.memory)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(errOut, "recur: %s is not a readable directory; refusing to call a corpus clean that could not be opened\n", o.memory)
		return 2
	}
	ledgerPath := filepath.Join(o.memory, ledgerName)
	known, err := readLedger(ledgerPath)
	if err != nil {
		fmt.Fprintln(errOut, "recur: reading the ledger:", err)
		return 2
	}

	if o.accept != "" {
		return accept(out, errOut, ledgerPath, known, o)
	}

	found, err := scan(o.memory)
	if err != nil {
		fmt.Fprintln(errOut, "recur:", err)
		return 2
	}

	if o.seed {
		return seed(out, errOut, ledgerPath, known, found)
	}

	backlog := 0
	var open []string
	for _, lesson := range found {
		e, ok := known[lesson]
		if !ok {
			open = append(open, lesson)
			continue
		}
		if e.state == "backlog" {
			backlog++
		}
	}
	sort.Strings(open)
	for _, lesson := range open {
		fmt.Fprintf(out, "%s recorded a recurrence and has no gate\n", lesson)
	}
	// ⛔ The backlog is printed on EVERY run, at zero as well, so that a number
	// nobody is looking at cannot quietly become the normal state.
	fmt.Fprintf(out, "%d recurrence(s) read, %d accounted for, %d untriaged backlog\n",
		len(found), len(found)-len(open), backlog)
	if len(open) > 0 {
		fmt.Fprintf(out, "account for each: recur --memory %s --accept <lesson> --gate <what> | --no-gate '<why>'\n", o.memory)
		return 1
	}
	return 0
}

// accept writes one decision into the ledger.
func accept(out, errOut io.Writer, path string, known map[string]entry, o opts) int {
	if (o.gate == "") == (o.noGate == "") {
		fmt.Fprintln(errOut, "recur: --accept needs exactly one of --gate or --no-gate: a lesson is either enforced now or waived with a reason")
		return 2
	}
	e := entry{lesson: o.accept, state: "gate", detail: o.gate}
	if o.noGate != "" {
		e = entry{lesson: o.accept, state: "no-gate", detail: o.noGate}
	}
	known[o.accept] = e
	if err := writeLedger(path, known); err != nil {
		fmt.Fprintln(errOut, "recur: writing the ledger:", err)
		return 2
	}
	fmt.Fprintf(out, "accounted for %s: %s %s\n", e.lesson, e.state, e.detail)
	return 0
}

// seed records everything known right now as untriaged, so the gate starts
// clean and fires on what happens next.
func seed(out, errOut io.Writer, path string, known map[string]entry, found []string) int {
	added := 0
	for _, lesson := range found {
		if _, ok := known[lesson]; ok {
			continue
		}
		known[lesson] = entry{lesson: lesson, state: "backlog", detail: "seeded, not triaged"}
		added++
	}
	if err := writeLedger(path, known); err != nil {
		fmt.Fprintln(errOut, "recur: writing the ledger:", err)
		return 2
	}
	fmt.Fprintf(out, "seeded %d backlog entr(ies); %d already accounted for\n", added, len(known)-added)
	fmt.Fprintln(out, "the gate now fires on a lesson that records a recurrence AFTER this point")
	return 0
}

// scan returns the names of the memories whose text records a recurrence.
func scan(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, de := range ents {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".md") || name == ledgerName || name == "MEMORY.md" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			// ⛔ One unreadable file is not "no recurrence in it": the whole run
			// refuses rather than reporting a corpus it could not finish.
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		for _, re := range markers {
			if re.Match(b) {
				found = append(found, strings.TrimSuffix(name, ".md"))
				break
			}
		}
	}
	sort.Strings(found)
	return found, nil
}

func readLedger(path string) (map[string]entry, error) {
	known := map[string]entry{}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return known, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		rest := strings.TrimPrefix(line, "- ")
		lesson, tail, ok := strings.Cut(rest, " — ")
		if !ok {
			continue
		}
		state, detail, _ := strings.Cut(tail, ": ")
		known[lesson] = entry{lesson: lesson, state: state, detail: detail}
	}
	return known, nil
}

func writeLedger(path string, known map[string]entry) error {
	names := make([]string, 0, len(known))
	for n := range known {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# recur ledger — what was decided about each repeated lesson\n\n")
	b.WriteString("Written by `recur`. A line is a lesson that recorded a recurrence and what\n")
	b.WriteString("happened about it: `gate` names what refuses it now, `no-gate` carries the\n")
	b.WriteString("reason none can exist, `backlog` means seeded and not yet triaged.\n\n")
	b.WriteString("Edited by hand at your peril: `recur --accept` keeps the shape its reader\n")
	b.WriteString("expects, and a line it cannot parse is a lesson that silently stops being\n")
	b.WriteString("accounted for.\n\n")
	for _, n := range names {
		e := known[n]
		b.WriteString(fmt.Sprintf("- %s — %s: %s\n", e.lesson, e.state, e.detail))
	}
	b.WriteString(fmt.Sprintf("\nlast written %s\n", time.Now().Format("2006-01-02")))
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
