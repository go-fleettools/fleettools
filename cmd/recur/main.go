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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"github.com/go-gitsafe/gitsafe/shellcmd"
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

⛔ A lesson is accounted for WITH THE SENTENCES THAT RECORDED IT. Each ledger
line carries a fingerprint of the lines that matched a marker, and a memory
whose recurrence sentences CHANGE is reported again, as a lesson that repeated
after it was accounted for — which is the only thing a gate is for. Without
that, a memory accounted for once could never fire again: it gains a third and
a fourth occurrence in silence, and the ledger reads as if the gate held.
Entries written before fingerprints existed carry none; they are counted and
named in every run, and --seed records their fingerprints without touching the
decision already made about them.

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
	c.Flags().StringVar(&o.pending, "gate-pending", "",
		"the gate is written but has not landed: name it and say what must land (for the case where landing it needs the very command this gate refuses)")
	c.Flags().StringVar(&o.noGate, "no-gate", "",
		"why no gate can exist for this one; the reason is written into the ledger, dated")
	c.Flags().BoolVar(&o.seed, "seed", false,
		"write everything known right now as untriaged backlog, so the gate starts clean")
	c.Flags().BoolVar(&o.hook, "hook", false,
		"read a PreToolUse payload on stdin and answer it: deny a publishing command while a recurrence is unaccounted for")
	return c
}

// publishing is what the gate stands in front of. A recurrence left unaccounted
// for should stop work being SHIPPED, not stop it being done: refusing an edit
// or a test run would make the gate something to disable rather than satisfy.
var publishing = map[string]bool{"gitpush": true, "ghmerge": true, "ghrelease": true}

// envAssign is what may stand between a statement's start and its program.
var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// wrappers run a program named among their OWN arguments, so the search has to
// go past them rather than stop at the first word.
var wrappers = map[string]bool{
	"env": true, "nohup": true, "time": true, "command": true, "exec": true,
	"sudo": true, "builtin": true, "xargs": true, "stdbuf": true,
	"setsid": true, "nice": true, "ionice": true, "timeout": true,
}

// keyword opens a compound command; the program follows it.
var keyword = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "while": true,
	"until": true, "for": true, "do": true, "{": true, "!": true,
}

// redirectBare is a redirection operator whose target is the NEXT word, so that
// word is not the program either.
var redirectBare = regexp.MustCompile(`^[0-9]*(?:<|>|>>|<<<|<&|>&)$`)

// redirectJoined is a redirection with its target attached: >/dev/null, 2>&1.
var redirectJoined = regexp.MustCompile(`^[0-9]*(?:<|>|>>|<&|>&)\S`)

// publishes answers whether this command line RUNS one of the publishing
// programs.
//
// ⛔⛔ IT READS COMMAND POSITIONS, NOT WORDS, and it gets "where does a command
// begin" from [shellcmd] rather than from a regex of its own. Two versions of
// this failed before, in OPPOSITE directions, and both are measured:
//
// The first matched /\b(gitpush|ghmerge|ghrelease)\b/ anywhere in the line, so
// the first command it refused was `recur --accept … --gate-pending "<prose
// naming ghmerge>"` — the gate denying the one command that clears it.
//
// The second split on separators with a regex that had NO NOTION OF QUOTING,
// which fixed the word-anywhere match and left the SAME defect one level along:
// `--gate-pending "mergeable; ghmerge must land it"` was still denied, because
// the `;` inside the quoted reason read as the end of a command. That is the
// FOURTH occurrence of "the gate refuses its own remedy" on this machine, and
// it is why the split is no longer written here: shellcmd.Statements and
// shellcmd.Stages already know both quote characters, heredocs, and that `||`
// is not a pipeline, and gitsafe's guards share that one answer on purpose.
//
// ⚠ WHAT IS STILL NOT SHARED, said rather than left to be discovered: the word
// splitting and the wrapper list below are recur's own, and a review witnessed
// 16 forms that a shell RUNS and that the second version answered false for.
// They are handled here now, but the right home is an exported
// shellcmd.Programs, which has been requested from gitsafe with those 16 forms;
// the recur ledger records this gate as written-and-not-landed until it exists.
func publishes(cmd string) bool { return runs(cmd, 0) }

// maxDepth bounds the descent into `sh -c` strings and substitutions.
//
// ⛔ RUNNING OUT OF BUDGET COUNTS AS PUBLISHING, never as a clean line. This
// answer gates publishing, so a line too deeply nested to read is a line nobody
// vetted, and "I could not tell" must not read as "no".
const maxDepth = 4

func runs(cmd string, depth int) bool {
	if depth > maxDepth {
		return true
	}
	for _, st := range shellcmd.Statements(shellcmd.StripQuotedHeredocs(cmd)) {
		for _, stage := range shellcmd.Stages(st) {
			if stageRuns(stage, depth) {
				return true
			}
		}
		// ⛔ $( … ) and backticks RUN even inside double quotes, which
		// shellcmd.InsideQuotes says in so many words. `echo "$(ghmerge …)"`
		// is a merge.
		for _, m := range substitution.FindAllStringSubmatch(st, -1) {
			if runs(m[1]+m[2], depth+1) {
				return true
			}
		}
	}
	return false
}

// substitution finds the body of a $( … ) or a ` … `.
var substitution = regexp.MustCompile("\\$\\(([^()]*)\\)|`([^`]*)`")

// stageRuns reads one pipeline stage and answers whether its program is one of
// the publishing ones.
func stageRuns(stage string, depth int) bool {
	words := split(stage)
	// wrapped says a wrapper has already been passed, so the program is still
	// ahead and the wrapper's OWN options and operands stand in between.
	//
	// ⛔ `timeout 30 gitpush` was the case that found this: `timeout` was passed
	// as a wrapper and `30` was then read as the program, so the line answered
	// false. `nice -n 5`, `xargs -n1` and `env -i` all have the same shape. An
	// ordinary program still STOPS the scan, which is what keeps
	// `timeout 30 echo "gitpush"` from being read as a push.
	wrapped := false
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "" || envAssign.MatchString(w) || keyword[w] || redirectJoined.MatchString(w):
			continue
		case wrapped && operandish(w):
			continue
		case redirectBare.MatchString(w):
			i++ // its target is the next word, and is not a program either
			continue
		}
		prog := w
		if j := strings.LastIndexByte(prog, '/'); j >= 0 {
			prog = prog[j+1:] // ~/.local/bin/gitpush, and ./gitpush, are gitpush
		}
		switch {
		case publishing[prog]:
			return true
		case shells[prog]:
			// sh -c '<a command line>' runs whatever that string runs.
			for j := i + 1; j < len(words)-1; j++ {
				if words[j] == "-c" {
					return runs(words[j+1], depth+1)
				}
			}
			return false
		case wrappers[prog]:
			wrapped = true
			continue // the program it runs is further along this stage
		}
		return false // an ordinary program: the rest of the stage is arguments
	}
	return false
}

// operandish is the shape of a wrapper's own option or operand: an option, or
// something beginning with a digit (`30`, `30s`, `1m` for timeout, `5` for
// nice). It is only ever consulted AFTER a wrapper, never in first position.
func operandish(w string) bool {
	return strings.HasPrefix(w, "-") || (w != "" && w[0] >= '0' && w[0] <= '9')
}

// shells run a program named in a -c operand.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

// split breaks one pipeline stage into shell words, with the quotes they were
// written in removed and a quoted operand kept WHOLE.
//
// ⛔ strings.Fields CANNOT DO THIS, and that is what the previous version used:
// `sh -c 'gitpush -u origin topic'` came back as six words, so the operand of
// -c was `'gitpush` and nothing could read the command line inside it. Quoting
// does not protect a program name — the shell runs `"gitpush"` — so what keeps
// prose safe is POSITION, which is the whole design above.
func split(stage string) []string {
	var out []string
	cur := &strings.Builder{}
	q := byte(0)
	open := false
	flush := func() {
		if open {
			out = append(out, cur.String())
			cur.Reset()
			open = false
		}
	}
	for i := 0; i < len(stage); i++ {
		c := stage[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			} else {
				cur.WriteByte(c)
			}
			open = true
		case c == '\'' || c == '"':
			q, open = c, true
		case c == '\\' && i+1 < len(stage):
			i++
			cur.WriteByte(stage[i])
			open = true
		case c == ' ' || c == '\t' || c == '(' || c == ')' || c == '}' || c == ';':
			// Grouping is not a word, and `(gitpush …)` is a subshell running
			// gitpush. `{` IS a word, and is in keyword above.
			flush()
		default:
			cur.WriteByte(c)
			open = true
		}
	}
	flush()
	return out
}

// hookMode answers a PreToolUse payload.
//
// ⛔ IT BLOCKS ON ONE CONDITION ONLY: a definite finding. Everything else —
// a payload it cannot read, a memory directory that will not open, its own
// failure — allows and says so on stderr. About 25 sessions share this machine,
// and a detector that halts them all because it could not read a file would be
// a worse defect than the one it is here to catch. Exit 2 means refused to
// decide, and a refusal is not a verdict.
func hookMode(out, errOut io.Writer, o opts, stdin io.Reader) int {
	var payload struct {
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	b, err := io.ReadAll(stdin)
	if err != nil || json.Unmarshal(b, &payload) != nil {
		fmt.Fprintln(errOut, "recur: could not read the hook payload; allowing (a refusal is not a verdict)")
		return 0
	}
	if !publishes(payload.ToolInput.Command) {
		return 0
	}
	open, total, _, err := findings(o)
	if err != nil {
		fmt.Fprintf(errOut, "recur: %v; allowing (a refusal is not a verdict)\n", err)
		return 0
	}
	if len(open) == 0 {
		return 0
	}
	var b2 strings.Builder
	fmt.Fprintf(&b2, "%d of %d recorded recurrence(s) have no gate, and this command publishes:\\n", len(open), total)
	for _, f := range open {
		fmt.Fprintf(&b2, "  %s %s\\n", f.lesson, f.why)
	}
	fmt.Fprintf(&b2, "A lesson that repeated is a tool and a gate that are missing. Build it, then: recur --memory %s --accept <lesson> --gate <what>.\\n", o.memory)
	fmt.Fprint(&b2, "If the gate exists but has not landed — including when landing it needs the very command this refuses — say where it is with --gate-pending; if none can exist, --no-gate '<why>'. Both are published in the ledger.\\n")
	fmt.Fprint(&b2, "⛔ ANY session may record the decision, not only the lesson's author. The ledger keeps what was decided and the words you give it, so say in them what you measured and what you are relaying. Waiting for an author who may have ended is how a lesson nobody can clear blocks every session on this machine.")
	enc := json.NewEncoder(out)
	_ = enc.Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": strings.ReplaceAll(b2.String(), `\n`, "\n"),
		},
	})
	return 0
}

func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

type opts struct {
	memory  string
	accept  string
	gate    string
	pending string
	noGate  string
	seed    bool
	hook    bool
}

// findings is the reading both the report and the hook are built on: which
// recorded recurrences the ledger does not account for, how many were read, and
// how many of the accounted ones are still untriaged backlog.
func findings(o opts) (open []finding, total, backlog int, err error) {
	info, err := os.Stat(o.memory)
	if err != nil || !info.IsDir() {
		return nil, 0, 0, fmt.Errorf("%s is not a readable directory", o.memory)
	}
	known, err := readLedger(filepath.Join(o.memory, ledgerName))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("reading the ledger: %w", err)
	}
	found, err := scan(o.memory)
	if err != nil {
		return nil, 0, 0, err
	}
	open, backlog, _ = triage(known, found)
	return open, len(found), backlog, nil
}

// finding is one lesson the ledger does not account for, and why.
type finding struct {
	lesson string
	why    string
}

// triage is the single reading the report and the hook both use. Two different
// readings of one corpus is how a gate comes to deny what its report called
// clean.
func triage(known map[string]entry, found []recorded) (open []finding, backlog, unmarked int) {
	for _, r := range found {
		e, ok := known[r.lesson]
		if !ok {
			open = append(open, finding{r.lesson, "recorded a recurrence and has no gate"})
			continue
		}
		// ⛔⛔ A STATE WORD NOBODY CHOSE SETTLED A LESSON FOREVER. Measured
		// 2026-11-15 on a hand-edited ledger: a line reading
		// `gatee@<the right fingerprint>` made the report say "1 recurrence(s)
		// read, 1 accounted for", exit 0, and the hook ALLOW — with no warning
		// of any kind, because nothing compared the word against the four that
		// mean something. Any typo, and any state word a later version adds and
		// an older binary does not know, reads as a decision. It is a finding
		// now: a word nobody chose is the one thing this must not take on
		// trust.
		if !states[e.state] {
			open = append(open, finding{r.lesson, fmt.Sprintf(
				"is recorded with %q, which is not one of gate, pending, no-gate or backlog", e.state)})
			continue
		}
		switch {
		case e.mark == "":
			// Written before fingerprints existed: counted and named, never
			// silently treated as current.
			unmarked++
		case e.mark != r.mark:
			open = append(open, finding{r.lesson, fmt.Sprintf(
				"recorded a NEW recurrence since it was accounted for (%s: %s)", e.state, e.detail)})
			continue
		}
		if e.state == "backlog" {
			backlog++
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].lesson < open[j].lesson })
	return open, backlog, unmarked
}

// states are the four decisions a ledger line may record. Anything else is a
// typo or a word written by a newer version, and either way it is not a
// decision this binary can act on.
var states = map[string]bool{"gate": true, "pending": true, "no-gate": true, "backlog": true}

// entry is one ledger line: a lesson, and what was decided about it.
type entry struct {
	lesson string
	state  string // "gate", "pending", "no-gate" or "backlog"
	mark   string // fingerprint of the sentences that recorded it; "" before these existed
	detail string
}

func check(out, errOut io.Writer, o opts) int {
	if o.hook {
		if o.memory == "" {
			fmt.Fprintln(errOut, "recur: --hook needs --memory; allowing")
			return 0
		}
		return hookMode(out, errOut, o, os.Stdin)
	}
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

	found, err := scan(o.memory)
	if err != nil {
		fmt.Fprintln(errOut, "recur:", err)
		return 2
	}

	if o.accept != "" {
		return accept(out, errOut, ledgerPath, known, found, o)
	}

	if o.seed {
		return seed(out, errOut, ledgerPath, known, found)
	}

	open, backlog, unmarked := triage(known, found)
	for _, f := range open {
		fmt.Fprintf(out, "%s %s\n", f.lesson, f.why)
	}
	// ⛔ The backlog is printed on EVERY run, at zero as well, so that a number
	// nobody is looking at cannot quietly become the normal state.
	fmt.Fprintf(out, "%d recurrence(s) read, %d accounted for, %d untriaged backlog\n",
		len(found), len(found)-len(open), backlog)
	if unmarked > 0 {
		fmt.Fprintf(out, "%d accounted entr(ies) carry no fingerprint and cannot fire again; record them: recur --memory %s --seed\n",
			unmarked, o.memory)
	}
	if len(open) > 0 {
		fmt.Fprintf(out, "account for each: recur --memory %s --accept <lesson> --gate <what> | --gate-pending <where it is> | --no-gate '<why>'\n", o.memory)
		return 1
	}
	return 0
}

// accept writes one decision into the ledger.
func accept(out, errOut io.Writer, path string, known map[string]entry, found []recorded, o opts) int {
	given := 0
	for _, v := range []string{o.gate, o.pending, o.noGate} {
		if v != "" {
			given++
		}
	}
	if given != 1 {
		fmt.Fprintln(errOut, "recur: --accept needs exactly one of --gate, --gate-pending or --no-gate: a lesson is enforced now, enforced by something named and not yet landed, or waived with a reason")
		return 2
	}
	// ⛔ A name nothing recorded is a typo, and a typo here accounts for a
	// lesson that does not exist while the real one keeps firing — with the
	// ledger showing a decision was taken. The command refuses instead.
	mark, ok := "", false
	for _, r := range found {
		if r.lesson == o.accept {
			mark, ok = r.mark, true
			break
		}
	}
	if !ok {
		fmt.Fprintf(errOut, "recur: no memory named %s records a recurrence; refusing to account for a lesson that is not there\n", o.accept)
		return 2
	}
	e := entry{lesson: o.accept, state: "gate", mark: mark, detail: o.gate}
	switch {
	case o.pending != "":
		e = entry{lesson: o.accept, state: "pending", mark: mark, detail: o.pending}
	case o.noGate != "":
		e = entry{lesson: o.accept, state: "no-gate", mark: mark, detail: o.noGate}
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
func seed(out, errOut io.Writer, path string, known map[string]entry, found []recorded) int {
	wasEmpty := len(known) == 0
	added, marked := 0, 0
	var buried []string
	for _, r := range found {
		e, ok := known[r.lesson]
		if !ok {
			known[r.lesson] = entry{lesson: r.lesson, state: "backlog", mark: r.mark, detail: "seeded, not triaged"}
			added++
			buried = append(buried, r.lesson)
			continue
		}
		// ⛔ An entry written before fingerprints existed gets one, and KEEPS
		// the decision already taken about it. Re-seeding must never quietly
		// turn somebody's reasoned waiver back into untriaged backlog.
		if e.mark == "" {
			e.mark = r.mark
			known[r.lesson] = e
			marked++
		}
	}
	if err := writeLedger(path, known); err != nil {
		fmt.Fprintln(errOut, "recur: writing the ledger:", err)
		return 2
	}
	if marked > 0 {
		fmt.Fprintf(out, "recorded the fingerprint of %d entr(ies) that had none, decisions unchanged\n", marked)
	}
	// ⛔ Seeding over a ledger that already exists BURIES whatever was a finding
	// a moment ago, as untriaged backlog nobody is reading. That is the right
	// behaviour for the first run and a quiet way to lose a lesson on the
	// twentieth, so each one is named when it is not the first run.
	if !wasEmpty && len(buried) > 0 {
		fmt.Fprintf(out, "⛔ the ledger was not empty, and %d lesson(s) that were findings until now became untriaged backlog:\n", len(buried))
		for _, name := range buried {
			fmt.Fprintf(out, "  %s\n", name)
		}
	}
	fmt.Fprintf(out, "seeded %d backlog entr(ies); %d already accounted for\n", added, len(known)-added)
	fmt.Fprintln(out, "the gate now fires on a lesson that records a recurrence AFTER this point")
	return 0
}

// recorded is one memory that records a recurrence, with the fingerprint of the
// sentences that say so.
type recorded struct {
	lesson string
	mark   string
}

// scan returns the memories whose text records a recurrence, each with its
// fingerprint.
func scan(dir string) ([]recorded, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var found []recorded
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
		if mark, ok := fingerprint(b); ok {
			found = append(found, recorded{lesson: strings.TrimSuffix(name, ".md"), mark: mark})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].lesson < found[j].lesson })
	return found, nil
}

// fingerprint hashes the LINES that matched a marker, not the whole file.
//
// ⛔ The unit is what it has to be: hashing the file would fire on every edit,
// including a typo in an unrelated paragraph, and a gate that cries wolf is a
// gate somebody turns off. Hashing only the lesson's NAME is what the first
// version did, and it meant a memory accounted for once could never fire again
// — measured on ubuntu-latest-moved-and-moved-back, which gained two further
// occurrences on 2026-11-12 while the ledger called it settled.
//
// ⚠ What it costs, said rather than discovered later: REWORDING a recurrence
// sentence fires too, and that is a false positive. It costs one --accept, and
// the alternative is the silence above.
func fingerprint(b []byte) (string, bool) {
	var hit []string
	for _, line := range strings.Split(string(b), "\n") {
		for _, re := range markers {
			if re.MatchString(line) {
				hit = append(hit, strings.TrimSpace(line))
				break
			}
		}
	}
	if len(hit) == 0 {
		return "", false
	}
	sum := sha256.Sum256([]byte(strings.Join(hit, "\n")))
	return hex.EncodeToString(sum[:6]), true
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
		// The fingerprint rides on the state word, which has no "@" of its own:
		// "backlog@a1b2c3d4e5f6". A line written before fingerprints existed
		// carries none, and is reported rather than assumed current.
		state, mark, _ := strings.Cut(state, "@")
		known[lesson] = entry{lesson: lesson, state: state, mark: mark, detail: detail}
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
	b.WriteString("happened about it: `gate` names what refuses it now, `pending` names a gate\n")
	b.WriteString("that is written and has not landed yet, `no-gate` carries the reason none can\n")
	b.WriteString("exist, `backlog` means seeded and not yet triaged.\n\n")
	b.WriteString("The hex after the state is the fingerprint of the sentences that recorded the\n")
	b.WriteString("recurrence. When they change, the lesson is reported again: a memory that\n")
	b.WriteString("gains a third occurrence must not stay settled because of a decision taken\n")
	b.WriteString("about its second.\n\n")
	b.WriteString("Edited by hand at your peril: `recur --accept` keeps the shape its reader\n")
	b.WriteString("expects, and a line it cannot parse is a lesson that silently stops being\n")
	b.WriteString("accounted for.\n\n")
	for _, n := range names {
		e := known[n]
		state := e.state
		if e.mark != "" {
			state += "@" + e.mark
		}
		b.WriteString(fmt.Sprintf("- %s — %s: %s\n", e.lesson, state, e.detail))
	}
	b.WriteString(fmt.Sprintf("\nlast written %s\n", time.Now().Format("2006-01-02")))
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
