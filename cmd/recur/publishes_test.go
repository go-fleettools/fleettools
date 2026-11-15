package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔⛔ THE ORACLE IS ASKED IN BOTH DIRECTIONS, and that is the point of this
// file. A table of "these must be refused" finds only MISSES; it cannot see an
// OVER-refusal, which is the failure that costs a rule its life — on literaltick
// a doubt stated too broadly produced 5050 refusals over 554 998 commands while
// its suite stayed green. So the two tables below are both mandatory, and
// neither is allowed to be empty.

// Every form here RUNS a publishing program. A reviewer witnessed each one with
// a shimmed binary on PATH that wrote a witness file; what is asserted is that
// publishes() agrees with the shell.
//
// ⛔ The second version of publishes() answered FALSE for all sixteen. It read
// position 0 after environment assignments and nothing else: not quotes, not a
// leading backslash, not grouping, not a leading redirection, not a wrapper
// program, not a shell's -c operand.
// ⛔⛔ THE FLOORS ARE THE GATE, and they exist because a review measured that
// the sentence above was a WRITTEN RULE WITH NOTHING ENFORCING IT: mutating
// each table's lone t.Errorf away left the whole suite green, so a later edit
// could empty either table and the oracle would go one-directional again in
// silence. A minimum row count fails instead. The numbers are the populations
// as measured, and raising a floor is how a reader is told the table grew.
const (
	minRunRows   = 30
	minProseRows = 14
)

func TestEveryFormAShellWouldRunCountsAsPublishing(t *testing.T) {
	rows := 0
	defer func() {
		if rows < minRunRows {
			t.Errorf("%d rows, want at least %d: this table is one half of the oracle, "+
				"and an emptied half cannot see a miss", rows, minRunRows)
		}
	}()
	for _, tc := range []struct{ name, cmd string }{
		{"bare", "gitpush -u origin topic"},
		{"by path", "~/.local/bin/gitpush -u origin topic"},
		{"relative path", "./gitpush -u origin topic"},
		{"double quoted program", `"gitpush" -u origin topic`},
		{"single quoted program", `'gitpush' -u origin topic`},
		{"escaped program", `\gitpush -u origin topic`},
		{"subshell", "(gitpush -u origin topic)"},
		{"brace group", "{ gitpush -u origin topic; }"},
		{"after cd", "cd /x && ghmerge go-crdt/collab 271"},
		{"environment assignment", "GIT_TRACE=1 ghrelease go-crdt/collab 271 v0.1.0"},
		{"env wrapper", "env GIT_TRACE=1 gitpush -u origin topic"},
		{"after a semicolon", "echo hi; ghrelease o/r 1 v0.1.0"},
		{"nohup", "nohup gitpush -u origin topic"},
		{"time", "time gitpush -u origin topic"},
		{"command builtin", "command gitpush -u origin topic"},
		{"exec", "exec gitpush -u origin topic"},
		{"xargs", "echo 1 | xargs ghmerge o/r"},
		{"timeout with an operand", "timeout 30 gitpush -u origin topic"},
		{"timeout with a duration", "timeout 1m gitpush -u origin topic"},
		{"nice with an option and an operand", "nice -n 5 gitpush -u origin topic"},
		{"xargs with an option", "echo 1 | xargs -n1 ghmerge o/r"},
		{"env with an option", "env -i gitpush -u origin topic"},
		{"joined redirection first", ">/dev/null gitpush -u origin topic"},
		{"bare redirection first", "> /dev/null gitpush -u origin topic"},
		{"inside then", "if true; then gitpush -u origin topic; fi"},
		{"inside do", "for x in a; do ghmerge o/r 1; done"},
		{"sh -c", "sh -c 'gitpush -u origin topic'"},
		{"bash -c double quoted", `bash -c "gitpush -u origin topic"`},
		{"command substitution", `echo "$(ghmerge o/r 1)"`},
		{"backtick substitution", "echo `ghmerge o/r 1`"},
		// ⛔ Witnessed by a second review with shimmed publishers on PATH: a
		// publisher really ran and the previous version answered false.
		{"eval", `eval 'gitpush -u origin topic'`},
		{"sh with bundled flags", `sh -ec 'gitpush -u origin topic'`},
		{"bash with a login flag", `bash -lc 'gitpush -u origin topic'`},
		{"after a single ampersand", "echo hi & gitpush -u origin topic"},
		{"a case arm", "case topic in *) gitpush -u origin topic ;; esac"},
		{"nested command substitution", `echo "pushed $(gitpush -u origin $(echo topic))"`},
		{"find -exec", `find . -name x -exec gitpush -u origin topic \;`},
		{"command without -v", "command gitpush -u origin topic"},
		// ⛔ `command gitpush -v` RUNS gitpush, with -v as gitpush's own flag.
		// Found by a full mutsweep: deleting describesOnly's "a non-option
		// means the program, and no -v was seen" check left the suite green,
		// and that deletion makes this line read as a lookup.
		{"command with a program before a v flag", "command gitpush -v"},
		{"a backtick inside a double-quoted body", "ghissue --new --body \"run `ghmerge o/r 1` to land it\""},
	} {
		rows++
		t.Run(tc.name, func(t *testing.T) {
			if !publishes(tc.cmd) {
				t.Errorf("publishes(%q) = false, but a shell runs the publisher", tc.cmd)
			}
		})
	}
}

// ⛔⛔ THE CONTROL, AND THE FOURTH OCCURRENCE IT IS FOR. "The gate refuses its
// own remedy" has now happened four times on this machine, twice inside this
// one command: first a word matched anywhere in the line, then a separator
// regex with no notion of quoting, which denied
//
//	recur --accept l --gate-pending "mergeable; ghmerge must land it"
//
// because the `;` inside a quoted REASON read as the end of a command. A gate
// that cannot be satisfied is a gate somebody removes, so every row below is a
// command no shell runs a publisher for, and each is a separator inside quotes.
func TestProseThatMerelyNamesAPublisherIsNotPublishing(t *testing.T) {
	rows := 0
	defer func() {
		if rows < minProseRows {
			t.Errorf("%d rows, want at least %d: this is the half of the oracle that "+
				"sees an OVER-refusal, and nothing else can", rows, minProseRows)
		}
	}()
	for _, tc := range []struct{ name, cmd string }{
		{"the remedy itself", `recur --accept l --gate-pending "lands when ghmerge stops refusing"`},
		{"a semicolon in the reason", `recur --accept l --gate-pending "mergeable; ghmerge must land it"`},
		{"a pipe in the reason", `recur --accept l --gate-pending "the gate | gitpush is what refuses it"`},
		{"an and-and in the reason", `recur --accept l --no-gate "it holds && ghmerge is the gate"`},
		{"a single ampersand", `recur --accept l --no-gate "ghrelease & nothing else"`},
		{"a commit message", `git commit -m "note: gitpush carries the scope"`},
		{"a commit message with a newline", "git commit -m \"first line\nghmerge is what refuses it\""},
		{"a pipeline whose program is grep", "cat log | grep ghrelease"},
		{"an argument that looks like a path", "cat ~/.local/bin/gitpush"},
		{"a quoted heredoc body", "cat <<'EOF'\nghmerge o/r 1\nEOF"},
		{"nothing at all", ""},
		{"only separators", " ; && || "},
		// ⛔ The control for the wrapper rule: an ordinary program after a
		// wrapper STOPS the scan, or `timeout` would turn every argument of
		// every command it runs into a command position.
		{"a wrapper running something that merely prints the name", `timeout 30 echo "gitpush is the tool"`},
		{"a wrapper running grep", "xargs -n1 grep ghmerge"},
		// ⛔⛔ The FIFTH generation, measured by a second review: this is the
		// install check CLAUDE.md tells every session on this machine to run,
		// and the gate was denying it.
		{"the install check", "command -v gitpush"},
		{"the install check, verbose", "command -V ghmerge"},
		// ⛔⛔ The SIXTH: a substitution inside SINGLE quotes runs nothing, and
		// shellcmd.InsideQuotes already knew that while this did not ask.
		{"a code span in a single-quoted body", "ghissue --new --body 'the gate refuses `ghmerge` until it lands'"},
		{"a dollar-paren inside single quotes", `git commit -m 'run $(ghmerge o/r 1) to land it'`},
		{"a backtick inside single quotes", "git commit -m 'the `gitpush` wrapper carries the scope'"},
		// ⛔⛔ THE TWO FALSE POSITIVES #260 MEASURED, in another session, while
		// trying to land the very gate a lesson here was waiting for. They are
		// that issue's own command lines, kept verbatim: the first has the
		// publisher names as GREP PATTERNS, the second has them in a MESSAGE
		// BODY — and `agentsync tell` writes to a local mailbox, so refusing it
		// made it impossible to tell a peer about the bug with the tool meant
		// for telling peers. Both are allowed now, and both are rows here so
		// nobody has to measure them a third time.
		{"the publisher names as grep patterns", `git ls-tree -r HEAD --name-only; git grep -n "gitpush\|ghmerge\|ghrelease\|publish" -- cmd/recur`},
		{"the publisher names in a peer message", `agentsync tell go-gitsafe/gitsafe "the hook denies gitpush/ghmerge/ghrelease while one lesson is unaccounted for"`},
		// ⛔ A `-c` WITH NOTHING AFTER IT. A boundary sweep found
		// `i+1 < len(args)` surviving its move to `<=`, which indexes one past
		// the end: no row had -c as the last word. It is a broken command line
		// that runs nothing, so it must be allowed AND must not panic — the
		// second half is what the guard is for.
		{"a shell with a dangling -c", "sh -c"},
		{"a shell with flags and a dangling -c", "bash -lc"},
		// ⛔⛔ AN UNBALANCED SUBSTITUTION RUNS NOTHING, and this is the SEVENTH
		// generation of the over-refusal, caught by measuring instead of
		// reasoning. The code leaned toward refusing here on the argument that
		// "a broken command is not a clean one" — but bash does not PARSE
		// either of these:
		//
		//	bash -n -c 'echo "$(someprog -u origin topic'   -> unexpected EOF, exit 2
		//	bash -n -c 'echo `someprog -u origin topic'     -> unexpected EOF, exit 2
		//
		// A line no shell will execute cannot publish, so a doubt about it is
		// not a doubt at all. (Measured with a neutral program name because the
		// gate refuses its own measurement command when a publisher is named in
		// it — which is itself worth relaying, since `bash -n` runs nothing.)
		{"an unbalanced dollar-paren", `echo "$(gitpush -u origin topic`},
		{"an unbalanced backtick", "echo `gitpush -u origin topic"},
	} {
		rows++
		t.Run(tc.name, func(t *testing.T) {
			if publishes(tc.cmd) {
				t.Errorf("publishes(%q) = true, but no shell runs a publisher here", tc.cmd)
			}
		})
	}
}

// ⛔ RUNNING OUT OF BUDGET COUNTS AS PUBLISHING. This answer gates publishing,
// so a line too deeply nested to read is a line nobody vetted, and "I could not
// tell" must never read as "no". Asserted on `runs` directly, because reaching
// the bound through real nesting would need a command no session writes.
func TestALineTooDeepToReadCountsAsPublishing(t *testing.T) {
	if !runs("echo nothing at all", maxDepth+1) {
		t.Error("out of depth answered false; an unread line must count as publishing")
	}
	if runs("echo nothing at all", maxDepth) {
		t.Error("the bound itself must still be read, or the budget is one short")
	}
}

// ⛔⛔ THE DEFECT MEASURED ON A HAND-EDITED LEDGER, 2026-11-15. A state word
// nobody chose settled a lesson forever: `gatee@<the right fingerprint>` made
// the report say "1 accounted for", exit 0, and the hook ALLOW, with no warning
// of any kind. Nothing compared the word against the four that mean something,
// so any typo — and any state a newer version writes that an older binary does
// not know — reads as a decision.
func TestAStateWordThatIsNotADecisionIsAFinding(t *testing.T) {
	for _, state := range []string{"gatee", "GATE", "pendnig", "", "settled"} {
		t.Run("state "+state, func(t *testing.T) {
			dir := mem(t, map[string]string{"came-back.md": "Deuxième fois."})
			// Take the real fingerprint, so the only thing wrong is the word.
			if code, out, _ := drive(t, "--memory", dir, "--accept", "came-back", "--gate", "cmd/x"); code != 0 {
				t.Fatalf("accepting: %d %s", code, out)
			}
			b, err := os.ReadFile(filepath.Join(dir, ledgerName))
			if err != nil {
				t.Fatal(err)
			}
			broken := strings.Replace(string(b), "— gate@", "— "+state+"@", 1)
			if broken == string(b) {
				t.Fatalf("the fixture did not change the state word; the ledger reads %q", string(b))
			}
			if err := os.WriteFile(filepath.Join(dir, ledgerName), []byte(broken), 0o644); err != nil {
				t.Fatal(err)
			}

			code, out, _ := drive(t, "--memory", dir)
			if code != 1 {
				t.Fatalf("code %d, want 1: %q is not a decision\n%s", code, state, out)
			}
			if !strings.Contains(out, "is recorded with") {
				t.Errorf("it must say what it found instead of a decision; it said %q", out)
			}
		})
	}
}

// ⛔ A corpus this could not finish reading is not a clean one. scan refuses
// rather than reporting the memories it managed to open, and nothing asserted
// that until now. A dangling symbolic link is the portable fixture: ReadDir
// lists it and does not call it a directory, and ReadFile fails on it as a
// plain user, on Linux and macOS alike.
func TestAMemoryThatCannotBeReadRefusesTheWholeRun(t *testing.T) {
	dir := mem(t, map[string]string{"came-back.md": "Deuxième fois."})
	if err := os.Symlink(filepath.Join(dir, "nothing-here"), filepath.Join(dir, "dangling.md")); err != nil {
		t.Skip(err) // a file system without symbolic links cannot pose the question
	}
	code, out, errOut := drive(t, "--memory", dir)
	if code != 2 {
		t.Fatalf("code %d, want 2: refused to decide\n%s%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "dangling.md") {
		t.Errorf("the refusal must name the file it could not read; it said %q", errOut)
	}
	if strings.Contains(out, "accounted for") {
		t.Errorf("a corpus it could not read must not be summarised; it said %q", out)
	}
}

// And a ledger it cannot read is the same refusal: a decision it could not look
// up must not read as an absent one. A directory in the ledger's place is the
// portable unreadable file.
func TestALedgerThatCannotBeReadRefusesTheWholeRun(t *testing.T) {
	dir := mem(t, map[string]string{"came-back.md": "Deuxième fois."})
	if err := os.Mkdir(filepath.Join(dir, ledgerName), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := drive(t, "--memory", dir)
	if code != 2 {
		t.Fatalf("code %d, want 2: refused to decide\n%s%s", code, out, errOut)
	}
	if strings.Contains(out, "accounted for") {
		t.Errorf("it must not summarise a ledger it could not read; it said %q", out)
	}
}

// ⛔ --seed over a ledger that already exists BURIES whatever was a finding a
// moment ago. That is right on the first run and a quiet way to lose a lesson
// on the twentieth, so each buried lesson is NAMED — and nothing asserted that
// until now: disabling the naming left the suite green.
func TestSeedingOverAnExistingLedgerNamesWhatItBuried(t *testing.T) {
	dir := mem(t, map[string]string{
		"settled.md":   "Deuxième fois, et réglée.",
		"brand-new.md": "Deuxième fois, celle-ci n'a pas de porte.",
	})
	if code, out, _ := drive(t, "--memory", dir, "--accept", "settled", "--gate", "cmd/x"); code != 0 {
		t.Fatalf("accepting: %d %s", code, out)
	}
	// brand-new is a finding right now.
	if code, _, _ := drive(t, "--memory", dir); code != 1 {
		t.Fatalf("brand-new should be a finding, got %d", code)
	}

	code, out, _ := drive(t, "--memory", dir, "--seed")
	if code != 0 {
		t.Fatalf("seeding: %d\n%s", code, out)
	}
	if !strings.Contains(out, "the ledger was not empty") {
		t.Errorf("it must say that this was not a first run; it said %q", out)
	}
	if !strings.Contains(out, "brand-new") {
		t.Errorf("it must name the lesson it buried; it said %q", out)
	}
	if strings.Contains(out, "  settled\n") {
		t.Errorf("settled was already accounted for and was not buried; it said %q", out)
	}
}
