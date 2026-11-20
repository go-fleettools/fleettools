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

⛔⛔ THE LEDGER KEEPS ONE ENTRY PER LESSON, AND --accept REPLACES IT. About
twenty-five sessions share a memory directory, so the entry you read a minute
ago may not be the entry that is there now. Every replacement therefore PRINTS
the entry it is about to overwrite — its decision, its fingerprint, the instant
it was written and its reason — before writing, not after.

And a landed --gate may not become --gate-pending or --no-gate without
--downgrade '<why>', which is written into the ledger beside the new decision.
That one direction is a strict loss of information and needs no judgement about
which text is better; a gate replaced by another gate may be an improvement or
a regression, and deciding that is nobody's business here, so it is announced
and allowed. --gate-pending and --no-gate are NOT ordered against each other:
"written, not landed" and "none can exist" are different decisions, not
different strengths.

It exists because it happened twice in one day, 2026-11-15: a careful relay
from one session replaced another's landed, mutation-confirmed gate with a
pending thirty seconds after it was written, and nothing told either of them.

⛔ --hook SCOPES ITS REFUSAL. A recurrence is refused immediately to the session
that wrote it (the payload's session_id equals the memory's originSessionId),
and to every other session only once it has gone unaccounted for longer than
--grace (default 60m, measured from the memory's modified metadata, else its
mtime); until then the others get a warning naming the lesson and its owner. One
session's unaccounted lesson once refused gitpush for all ~25 sessions on the
machine. What the hook cannot read refuses everyone, as before: a memory with no
originSessionId or no readable age, a payload with no session_id, --grace 0. And
a ledger or directory it cannot read at all still allows.

    recur --memory <dir> --json

prints ONE JSON document instead of the report, for a program to chart whether
the gates work: "lessons" (one per memory that records a recurrence, with its
recurrence sentences and their dates, its ledger decision, and
fingerprintMatches — false when it repeated after it was accounted for),
"totals", and "unreadable", the files it could not read, so a blind scan is never
a zero. A recurrence's date is read from its sentence or its section heading and
is null when neither states one; it is never guessed. A day and month with no
year take the year of the frontmatter "modified" stamp and are marked
yearInferred. Exit status as below, and 2 when a file was unreadable.

Exit status: 0 accounted for, 1 at least one is not, 2 refused to decide — the
memory directory or the ledger could not be read, or a landed gate would have
been downgraded without a reason. A directory that would not open is never
reported as a clean one.`

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
	c.Flags().StringVar(&o.downgrade, "downgrade", "",
		"why a landed `gate` may be replaced by a weaker decision; the reason is written into the ledger beside it (required to replace a gate with --gate-pending or --no-gate)")
	c.Flags().BoolVar(&o.seed, "seed", false,
		"write everything known right now as untriaged backlog, so the gate starts clean")
	c.Flags().BoolVar(&o.hook, "hook", false,
		"read a PreToolUse payload on stdin and answer it: deny a publishing command while a recurrence is unaccounted for")
	c.Flags().BoolVar(&o.json, "json", false,
		"print one JSON document instead of the text report: every lesson that records a recurrence, with its dated recurrence sentences, its ledger decision and whether it repeated after being accounted for, plus totals and the files that could not be read. Exit status as for the text report, and 2 when a file was unreadable")
	c.Flags().DurationVar(&o.grace, "grace", defaultGrace,
		"with --hook: a recurrence blocks only the session that wrote it (the payload's session_id == the memory's originSessionId) until it has gone unaccounted for this long, then every session; other sessions are warned meanwhile. 0 refuses everyone at once. A memory with no originSessionId or no readable age refuses everyone, as before")
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

// evals take a command LINE as an argument and run it, so what follows has to
// be read as a command line of its own rather than as arguments.
var evals = map[string]bool{"eval": true}

// boundary is a word after which the next word is a COMMAND POSITION again,
// even though an ordinary program has already been seen in this stage.
//
// ⛔ `echo hi & gitpush -u origin topic` is the case that made this a state
// rather than a loop over the first few words: shellcmd.Statements does not
// split on a lone `&`, so the stage still held two commands and the scan had
// already stopped at `echo`. `case topic in *) gitpush … ;; esac` and
// `find . -exec gitpush … \;` are the same shape through different words.
var boundary = map[string]bool{
	"&": true, ";": true, ";;": true, "|": true, "&&": true, "||": true,
	"(": true, ")": true, "{": true, "}": true,
	"if": true, "then": true, "else": true, "elif": true, "while": true,
	"until": true, "for": true, "do": true, "done": true, "fi": true,
	"case": true, "in": true, "esac": true, "!": true,
	"-exec": true, "-execdir": true,
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
		for _, body := range substitutions(st) {
			if runs(body, depth+1) {
				return true
			}
		}
	}
	return false
}

// substitutions returns the body of every command substitution in a statement
// that the shell would actually RUN.
//
// ⛔ IT ASKS shellcmd.InsideQuotes, which the first version did not, and that
// cost the fifth and sixth generation of "the gate refuses its own remedy":
// `ghissue --new --body 'the gate refuses `ghmerge` until it lands'` was
// DENIED over a markdown code span in a single-quoted body, as was
// `git commit -m 'run $(ghmerge o/r 1) to land it'`. Single quotes stop a
// substitution; double quotes do NOT, and InsideQuotes already encodes exactly
// that distinction. It was sitting unused one import away.
//
// ⛔ AND THE PARENTHESES ARE BALANCED, not matched by a regex. The regex was
// `\$\(([^()]*)\)`, which cannot match a body containing a nested
// substitution, so `echo "pushed $(gitpush -u origin $(echo topic))"` was a
// reachable MISS — both bash and zsh ran the push, and the stage scan saw only
// `echo`. A character class is not a parser.
func substitutions(st string) []string {
	var out []string
	for i := 0; i < len(st); i++ {
		switch {
		case st[i] == '$' && i+2 <= len(st)-1 && st[i+1] == '(':
			// ⛔ THE POSITION ASKED ABOUT IS THE ONE INSIDE THE PARENTHESIS, not
			// the `$`. Measured: for `echo "$(ghmerge o/r 1)"` InsideQuotes
			// answers TRUE at the `$` — which is correct, the dollar really is
			// inside double quotes — and FALSE one character into the body,
			// which is the question being asked: does the shell interpret what
			// is in there. Asking at the `$` made every double-quoted
			// substitution invisible, turning a fix for an over-refusal into a
			// miss of the plainest form there is.
			if shellcmd.InsideQuotes(st, i+2) {
				continue // single-quoted: the shell runs nothing here
			}
			depth, j := 0, i+1
			for ; j < len(st); j++ {
				if st[j] == '(' {
					depth++
				} else if st[j] == ')' {
					if depth--; depth == 0 {
						break
					}
				}
			}
			// ⛔⛔ AN UNBALANCED `$(` CONTRIBUTES NOTHING, and the sentence that
			// stood here said the opposite. It read: "a broken command is not a
			// clean one when the answer gates publishing, so the rest of the
			// line is read as the body — I cannot tell leans toward refusing."
			// That reasoning is wrong, and MEASURING it is what showed how:
			//
			//	$ bash -n -c 'echo "$(someprog -u origin topic'
			//	unexpected EOF while looking for matching `)'   (exit 2)
			//
			// bash does not PARSE it, so no shell runs anything, so there is
			// nothing to lean about — and reading the tail as a body makes the
			// gate refuse a line no shell executes. That is the seventh
			// generation of "the gate refuses its own remedy", written by me in
			// the comment where I claimed to be leaning the right way. A doubt
			// is only a doubt where something might actually run.
			if j >= len(st) {
				return out
			}
			out = append(out, st[i+2:j])
			i = j
		case st[i] == '`':
			if i+1 < len(st) && shellcmd.InsideQuotes(st, i+1) {
				continue
			}
			if j := strings.IndexByte(st[i+1:], '`'); j >= 0 {
				out = append(out, st[i+1:i+1+j])
				i = i + 1 + j
			}
		}
	}
	return out
}

// stageRuns reads one pipeline stage and answers whether its program is one of
// the publishing ones.
func stageRuns(stage string, depth int) bool {
	words := split(stage)
	// atCommand says the next word is read as a PROGRAM. It starts true, goes
	// false once an ordinary program has been seen — its arguments follow — and
	// comes back on a boundary word.
	//
	// ⛔ It is a state rather than "the first few words" because of
	// `echo hi & gitpush -u origin topic`: Statements does not split on a lone
	// `&`, so one stage held two commands and the scan had already stopped.
	//
	// wrapped says a wrapper has been passed, so the program is still ahead and
	// the wrapper's OWN options and operands stand in between. `timeout 30
	// gitpush` found that: `30` was being read as the program.
	atCommand, wrapped := true, false
	for i := 0; i < len(words); i++ {
		w := words[i]
		if boundary[w] {
			atCommand, wrapped = true, false
			continue
		}
		if !atCommand {
			continue // an argument of something already identified
		}
		switch {
		case w == "" || envAssign.MatchString(w) || redirectJoined.MatchString(w):
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
		case prog == "command" && describesOnly(words[i+1:]):
			// ⛔⛔ `command -v gitpush` DOES NOT RUN IT: -v and -V ask where it
			// is and print a line. And this is the install check CLAUDE.md
			// tells every session on this machine to run, so denying it was
			// the FIFTH generation of "the gate refuses its own remedy" —
			// refusing the command a session runs to find out whether the
			// fleet tool it is being told to use is even installed.
			return false
		case shells[prog]:
			// sh -c '<a command line>' runs whatever that string runs, and the
			// flag may be bundled: `sh -ec`, `bash -lc`.
			if body, ok := dashC(words[i+1:]); ok {
				return runs(body, depth+1)
			}
			return false
		case evals[prog]:
			// eval takes a command line and runs it; its arguments are joined
			// with a space, which is what the shell does before parsing them.
			return runs(strings.Join(words[i+1:], " "), depth+1)
		case wrappers[prog]:
			wrapped = true
			continue // the program it runs is further along this stage
		}
		atCommand = false // an ordinary program: what follows are its arguments
	}
	return false
}

// describesOnly reports whether `command`'s arguments ask it to DESCRIBE rather
// than run: -v or -V, possibly bundled with -p.
func describesOnly(args []string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return false // the program itself, reached with no -v seen
		}
		if strings.ContainsAny(a, "vV") {
			return true
		}
	}
	return false
}

// dashC returns the operand of a shell's -c, which may be bundled with other
// single-letter flags.
//
// ⛔ `sh -ec '…'` and `bash -lc '…'` were MISSES because only the exact word
// `-c` was looked for, and both are ordinary spellings a script uses.
var dashCFlag = regexp.MustCompile(`^-[A-Za-z]*c$`)

func dashC(args []string) (string, bool) {
	for i, a := range args {
		if dashCFlag.MatchString(a) && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
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
		case c == ' ' || c == '\t':
			flush()
		case c == '(' || c == ')' || c == '}' || c == '&':
			// ⛔ EMITTED AS WORDS, not dropped. The first version swallowed
			// them, which cost two misses: `(gitpush …)` was fine by accident
			// but `echo hi & gitpush …` lost the only thing saying a second
			// command had begun, and a `case` pattern's `)` likewise.
			flush()
			out = append(out, string(c))
		case c == ';':
			flush()
			if i+1 < len(stage) && stage[i+1] == ';' {
				i++
				out = append(out, ";;") // the end of a case arm
				continue
			}
			out = append(out, ";")
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
		SessionID string `json:"session_id"`
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
	open, warn := scopeByOwner(open, o, payload.SessionID, clock(), readOrigin)
	for _, w := range warn {
		fmt.Fprintf(errOut, "recur: warning: %s %s; owned by session %s, who is refused now. Other sessions are refused after %s unaccounted (%s left).\n",
			w.lesson, w.why, shortID(w.owner), o.grace, w.left.Round(time.Minute))
	}
	if len(open) == 0 {
		if len(warn) > 0 {
			var w strings.Builder
			for _, x := range warn {
				fmt.Fprintf(&w, "recur: %s %s. It is owned by session %s, who is refused now; other sessions are refused once it has gone unaccounted for %s (%s left). Account for it: recur --memory %s --accept %s --gate <what>.\n",
					x.lesson, x.why, shortID(x.owner), o.grace, x.left.Round(time.Minute), o.memory, x.lesson)
			}
			_ = json.NewEncoder(out).Encode(map[string]any{
				"hookSpecificOutput": map[string]any{
					"hookEventName":     "PreToolUse",
					"additionalContext": w.String(),
				},
			})
		}
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
	memory    string
	accept    string
	gate      string
	pending   string
	noGate    string
	downgrade string
	seed      bool
	hook      bool
	json      bool
	grace     time.Duration // --hook: how long a lesson may stay unaccounted before it refuses sessions other than its owner; 0 refuses everyone at once
}

// defaultGrace is how long another session's unaccounted recurrence leaves the
// rest of the machine alone. One session's lesson, `grep-c-counts-lines-not-
// occurrences` (cae751b5), refused gitpush for every one of ~25 sessions until
// somebody else accounted for it.
const defaultGrace = 60 * time.Minute

// scoped is a finding that is a warning for this session, not a refusal.
type scoped struct {
	finding
	owner string
	left  time.Duration // grace remaining
}

// origin is what a memory says about who wrote it and when.
type origin struct {
	session  string
	modified time.Time
	stated   bool // modified came from the frontmatter, not from the file's mtime
}

// scopeByOwner splits the open findings into those that refuse THIS session and
// those that only warn it.
//
// A finding refuses when this session is the one that wrote the memory
// (payload session == originSessionId), or when it has gone unaccounted for
// longer than o.grace. ⛔ ANYTHING IT CANNOT READ REFUSES, as before: no
// originSessionId, an age that cannot be read, a payload with no session id, a
// grace of 0 or less. A gate that cannot read must not turn quietly
// permissive about what it CAN read; the only allowance is for a finding it read
// completely and found young and foreign.
func scopeByOwner(open []finding, o opts, session string, now time.Time, read func(dir, lesson string) (origin, error)) (refuse []finding, warn []scoped) {
	for _, f := range open {
		if o.grace <= 0 || session == "" {
			refuse = append(refuse, f)
			continue
		}
		org, err := read(o.memory, f.lesson)
		if err != nil || org.session == "" || org.modified.IsZero() || org.session == session {
			refuse = append(refuse, f)
			continue
		}
		age := now.Sub(org.modified)
		if age < 0 {
			age = 0 // a modified stamp from the future is a clock skew, not extra grace
		}
		if age >= o.grace {
			refuse = append(refuse, f)
			continue
		}
		warn = append(warn, scoped{f, org.session, o.grace - age})
	}
	return refuse, warn
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// readOrigin reads originSessionId and modified from a memory's frontmatter; when
// `modified` is missing or not a timestamp the file's mtime stands in for it.
func readOrigin(dir, lesson string) (origin, error) {
	path := filepath.Join(dir, lesson+".md")
	b, err := os.ReadFile(path)
	if err != nil {
		return origin{}, err
	}
	var org origin
	var stamp string
	lines := strings.Split(string(b), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for _, l := range lines[1:] {
			t := strings.TrimSpace(l)
			if t == "---" {
				break
			}
			if v, ok := strings.CutPrefix(t, "originSessionId:"); ok {
				org.session = strings.Trim(strings.TrimSpace(v), `"'`)
			} else if v, ok := strings.CutPrefix(t, "modified:"); ok {
				stamp = strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
	}
	if t, err := time.Parse(time.RFC3339, stamp); err == nil {
		org.modified, org.stated = t, true
		return org, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return origin{}, err
	}
	org.modified = info.ModTime()
	return org, nil
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
	when   string // RFC3339 instant this decision was written; "" for lines older than the stamp
}

// whenTag is how the write instant rides on a ledger line: appended to the
// DETAIL, in brackets, at the end.
//
// ⛔⛔ NOT INSIDE THE `state@mark` TOKEN, and that is the whole reason it is
// here. `readLedger` cuts the pre-colon segment at its FIRST "@", so a
// `gate@mark@time` or a `gate@mark time` would hand an OLDER binary
// `mark = "mark@time"` — every entry would then read as a fingerprint that had
// changed, and every lesson would be reported again as having repeated after
// it was accounted for. At the end of the detail an old reader simply prints it
// as part of the reason, which is true and readable.
var whenTag = regexp.MustCompile(`\s*\[written ([^\]]+)\]\s*$`)

// clock is the seam a test drives, so the stamp a test asserts is a value and
// not whatever second the suite ran in.
var clock = time.Now

// theRank of a decision, for the ONE comparison this command makes: a landed
// `gate` may not be replaced by anything weaker without a reason.
//
// ⛔ `pending` and `no-gate` are NOT ordered against each other, deliberately.
// "written, not landed" and "none can exist" are different decisions, not
// different strengths, and inventing an order between them would make the
// command refuse replacements it cannot justify refusing. The only strict loss
// of information is a gate that stops being one — which is detectable without
// any model of which text is better.
func theRank(state string) int {
	if state == "gate" {
		return 1
	}
	return 0
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

	if o.json {
		if o.accept != "" || o.seed {
			fmt.Fprintln(errOut, "recur: --json only reads; it cannot be combined with --accept or --seed")
			return 2
		}
		return jsonReport(out, errOut, o.memory, known)
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

	// ⛔⛔ THE LEDGER KEEPS ONE ENTRY PER LESSON AND THIS REPLACES IT. Said out
	// loud, BEFORE the write, because ~25 sessions share this file and the
	// entry read a minute ago is not the entry that is there now.
	//
	// Measured 2026-11-15: a careful relay from one session replaced another's
	// LANDED, mutation-confirmed `gate` with a `pending` thirty seconds after
	// it was written, so the ledger then said "nothing landed" about a gate
	// that was already green. Neither session was told, and the only reason it
	// was repaired is that the two happened to be talking. It had already
	// happened once that day, to a third session's entry.
	//
	// ⛔ PRINTED BEFORE, NOT AFTER, and that is the author of the overwritten
	// entry's own point: a diff printed afterwards would have been exactly as
	// invisible. The useful form shows the operator what is there while they
	// are still deciding.
	if prev, had := known[o.accept]; had {
		when := prev.when
		if when == "" {
			when = "not recorded — written before recur stamped the instant"
		}
		fmt.Fprintf(out, "replacing the entry that is there:\n  %s %s\n  written %s\n  %s\n",
			prev.state, prev.mark, when, prev.detail)

		// The ONE refusal. A `gate` becoming anything else is a strict loss of
		// information, which needs no judgement about which text is better —
		// unlike a gate replaced by another gate, which may be an improvement
		// or a regression and is nobody's to decide here. So that one is
		// announced and allowed, and this one is refused until the operator
		// says why in public.
		if theRank(prev.state) > theRank(e.state) && o.downgrade == "" {
			fmt.Fprintf(errOut, "recur: %s already names a landed gate, and %s is weaker. Replacing it needs --downgrade \"<why>\" — a reason, never a bare flag, and it is written into the ledger beside the new decision.\nIf you are relaying somebody else's work, ask them first: the entry above may be newer than what you read.\n",
				o.accept, e.state)
			return 2
		}
		if o.downgrade != "" {
			e.detail += " ⛔ DOWNGRADED from " + prev.state + ": " + o.downgrade
		}
	} else if o.downgrade != "" {
		// A reason for replacing something that is not there describes nothing.
		fmt.Fprintf(errOut, "recur: --downgrade was given but %s has no entry to replace\n", o.accept)
		return 2
	}
	e.when = clock().UTC().Format(time.RFC3339)
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
		if mark, ok := fingerprint(b, dir, name); ok {
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
//
// ⛔ A POINTER LINE IS NOT A RECURRENCE OF THE MEMORY THAT HOLDS IT (#303). An
// index hub is one line per child — "[title](child.md): ⛔⛔ récidive …" — quoting
// the child's own sentence. The child is judged under its own name; counting the
// quotation as well made every fold into the hub change the hub's fingerprint,
// and refused gitpush, ghmerge and ghrelease for every session although the
// child was accounted for. So a line that is only a pointer to a memory that
// EXISTS in dir is not read. A recurrence sentence of the hub's OWN still is,
// and so is a "pointer" whose target is not there: it points at nothing, so it
// is prose.
func fingerprint(b []byte, dir, self string) (string, bool) {
	hits := hitLines(b, dir, self)
	if len(hits) == 0 {
		return "", false
	}
	return markOf(hits), true
}

// hit is one line that matched a marker: its 0-based index and trimmed text.
type hit struct {
	idx  int
	text string
}

// hitLines is the ONE reading of which lines record a recurrence; the
// fingerprint and --json are both built on it.
func hitLines(b []byte, dir, self string) []hit {
	var hits []hit
	for i, line := range strings.Split(string(b), "\n") {
		for _, re := range markers {
			if re.MatchString(line) {
				if !pointer(line, dir, self) {
					hits = append(hits, hit{i, strings.TrimSpace(line)})
				}
				break
			}
		}
	}
	return hits
}

func markOf(hits []hit) string {
	texts := make([]string, len(hits))
	for i, h := range hits {
		texts[i] = h.text
	}
	sum := sha256.Sum256([]byte(strings.Join(texts, "\n")))
	return hex.EncodeToString(sum[:6])
}

// pointerRe finds a line that OPENS, at column 0, with a link to another memory
// — after an optional list bullet or quote mark, "[t](x.md)" or "[[x]]" — and
// that the link is followed by ':' or ';', the way an index quotes its child.
// ⛔ Both constraints are measured, not guessed: a prose sentence that merely
// begins with a wikilink ("[[x]] — c'est la deuxième fois", indented or not) is
// the memory's OWN recurrence, and an earlier, looser form of this pattern
// dropped it from a-sweep-that-cannot-see-dot-github and from two others.
var pointerRe = regexp.MustCompile(`^(?:[-*+>]\s+)?(?:\[[^\]]*\]\(([^)\s]+)\)|\[\[([^\]|#]+)(?:[|#][^\]]*)?\]\])\s*[:;]`)

// pointer reports whether line opens with a link to a memory that exists in
// dir. A URL, a link to a file that is not there, or a line that does not open
// with a link is not a pointer.
func pointer(line, dir, self string) bool {
	m := pointerRe.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	target := m[1]
	if target == "" {
		target = m[2] + ".md"
	}
	if strings.Contains(target, "://") {
		return false
	}
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i]
	}
	if target == "" || target != filepath.Base(target) || !strings.HasSuffix(target, ".md") {
		return false
	}
	// ⛔ A link is not a quotation. The target must be ANOTHER memory that scan
	// fingerprints (never this file, never the ledger or MEMORY.md), and what
	// follows the separator must QUOTE it; "- [[sibling]]: récidive ×3 ici" is
	// the memory's own sentence about its sibling, and dropping it would let a
	// lesson gain occurrences in silence.
	if target == self || target == ledgerName || target == "MEMORY.md" {
		return false
	}
	tb, err := os.ReadFile(filepath.Join(dir, target))
	if err != nil {
		return false
	}
	return quotes(line[len(m[0]):], tb)
}

// norm collapses whitespace and strips the quotation marks a description is
// wrapped in, so a quotation and its source compare equal.
func norm(s string) string {
	return strings.Trim(strings.Join(strings.Fields(s), " "), "\"'`«»“”")
}

// quotes reports whether text is a quotation of the memory body: the whole of
// its frontmatter description (what mem-add writes into an index), or the whole
// of one of its recurrence lines. ⛔ A memory with no recurrence line has
// nothing a hub could be quoting, so the words are the hub's own.
// Equality, not prefix: measured on the real hub, no pointer line is a prefix of
// its target's description or recurrence lines either, and a prefix rule would
// let any short stub like "3e fois" through.
func quotes(text string, body []byte) bool {
	want := norm(text)
	if want == "" {
		return false
	}
	var cands []string
	recurs := false
	for _, l := range strings.Split(string(body), "\n") {
		if v, ok := strings.CutPrefix(l, "description:"); ok {
			cands = append(cands, norm(v))
		}
		for _, re := range markers {
			if re.MatchString(l) {
				recurs = true
				cands = append(cands, norm(strings.TrimLeft(strings.TrimSpace(l), "-*+># ")))
				break
			}
		}
	}
	if !recurs {
		return false
	}
	for _, c := range cands {
		if c == want {
			return true
		}
	}
	return false
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
		when := ""
		if m := whenTag.FindStringSubmatch(detail); m != nil {
			when = m[1]
			detail = whenTag.ReplaceAllString(detail, "")
		}
		known[lesson] = entry{lesson: lesson, state: state, mark: mark, detail: detail, when: when}
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
		detail := e.detail
		if e.when != "" {
			detail += " [written " + e.when + "]"
		}
		b.WriteString(fmt.Sprintf("- %s — %s: %s\n", e.lesson, state, detail))
	}
	b.WriteString(fmt.Sprintf("\nlast written %s\n", time.Now().Format("2006-01-02")))
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
