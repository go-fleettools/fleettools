// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mem(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func drive(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func ledger(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ledgerName))
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	return string(b)
}

// The words a memory already uses when the same mistake happens again. These
// are the sentences this command exists to read, in the languages the corpus is
// written in.
func TestTheWordsThatRecordARecurrenceAreRead(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"recidive", "Troisième récidive, et la note était sous la main."},
		{"ordinal-fr", "C'est la deuxième fois aujourd'hui."},
		{"ordinal-en", "4th occurrence the same day, with the note already written."},
		{"pour-la-nieme", "Pour la troisième fois, j'ai annoncé avant de lire."},
		// ⛔ The first run against the real corpus missed a memory headed
		// "RÉCURRENCE" because the list read "récidive". A vocabulary is not a
		// synonym for a concept, and 2349 memories do not share one author.
		{"recurrence", "## ⛔⛔ RÉCURRENCE le 2026-11-07 au soir, en retrait et non en ajout"},
		{"repeated-itself", "Puisque la leçon s'est répétée, elle devient une porte."},
		{"did-it-again", "J'avais la note sous les yeux et je l'ai refait."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := mem(t, map[string]string{"lesson.md": tc.body})
			code, out, _ := drive(t, "--memory", dir)
			if code != 1 {
				t.Fatalf("code %d, want 1\n%s", code, out)
			}
			if !strings.Contains(out, "lesson recorded a recurrence and has no gate") {
				t.Errorf("it should name the lesson; it said %q", out)
			}
		})
	}
}

// A note about somebody else's mistake is not a recurrence of this one. The
// weak phrasings are deliberately not markers.
func TestProseThatIsNotARecurrenceIsNotAFinding(t *testing.T) {
	dir := mem(t, map[string]string{
		"a.md": "The same mistake can be made by anyone; this is how to avoid it.",
		"b.md": "Une erreur fréquente dans la flotte, jamais commise ici.",
		// ⛔ Deliberately not a marker: rhetorical about as often as factual.
		// Adding it would turn an argument into a finding.
		"c.md": "Encore une fois, la mesure prime sur le raisonnement.",
	})
	code, out, _ := drive(t, "--memory", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0 — a guard that cries wolf gets turned off\n%s", code, out)
	}
	if !strings.Contains(out, "0 recurrence(s) read") {
		t.Errorf("it should say it read none; it said %q", out)
	}
}

// Accounting for one: a gate names what refuses the mistake now.
func TestAGateAccountsForALesson(t *testing.T) {
	dir := mem(t, map[string]string{"lesson.md": "Troisième récidive."})
	if code, _, _ := drive(t, "--memory", dir); code != 1 {
		t.Fatalf("it should start as a finding, got %d", code)
	}
	code, out, _ := drive(t, "--memory", dir, "--accept", "lesson", "--gate", "cmd/viaonly")
	if code != 0 {
		t.Fatalf("accepting: code %d\n%s", code, out)
	}
	code, out, _ = drive(t, "--memory", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0 once accounted for\n%s", code, out)
	}
	// The state carries the fingerprint of the sentences it was decided about.
	if !strings.Contains(ledger(t, dir), "lesson — gate@") || !strings.Contains(ledger(t, dir), ": cmd/viaonly") {
		t.Errorf("the ledger should carry it; it says %q", ledger(t, dir))
	}
}

// ⛔ A waiver is made in public: --no-gate takes a REASON, never a bare flag,
// and the reason is written down where the next reader finds it.
func TestAWaiverCarriesItsReasonIntoTheLedger(t *testing.T) {
	dir := mem(t, map[string]string{"lesson.md": "Deuxième fois."})

	code, _, errOut := drive(t, "--memory", dir, "--accept", "lesson")
	if code != 2 || !strings.Contains(errOut, "exactly one of --gate, --gate-pending or --no-gate") {
		t.Errorf("neither flag must be refused, got %d %q", code, errOut)
	}
	code, _, errOut = drive(t, "--memory", dir, "--accept", "lesson",
		"--gate", "cmd/x", "--no-gate", "because")
	if code != 2 {
		t.Errorf("both flags must be refused, got %d %q", code, errOut)
	}

	why := "the oracle is a sentence in a doc comment; no analyser reads one"
	if code, _, _ := drive(t, "--memory", dir, "--accept", "lesson", "--no-gate", why); code != 0 {
		t.Fatalf("a reasoned waiver should be accepted, got %d", code)
	}
	if !strings.Contains(ledger(t, dir), why) {
		t.Errorf("the reason must be in the ledger; it says %q", ledger(t, dir))
	}
	if code, _, _ := drive(t, "--memory", dir); code != 0 {
		t.Errorf("a waived lesson is accounted for, got %d", code)
	}
}

// ⛔ The point of the ledger: seeding makes the gate fire on what happens NEXT,
// not on a backlog of 120 findings nobody will read.
func TestSeedingStartsTheGateCleanAndStillCountsTheBacklog(t *testing.T) {
	dir := mem(t, map[string]string{
		"old-a.md": "Troisième récidive.",
		"old-b.md": "Deuxième occurrence.",
	})
	code, out, _ := drive(t, "--memory", dir, "--seed")
	if code != 0 {
		t.Fatalf("seeding: code %d\n%s", code, out)
	}
	if !strings.Contains(out, "seeded 2 backlog entr(ies)") {
		t.Errorf("it should say how many; it said %q", out)
	}

	code, out, _ = drive(t, "--memory", dir)
	if code != 0 {
		t.Fatalf("after seeding the gate is clean, got %d\n%s", code, out)
	}
	// The backlog is printed on every run, so it cannot quietly become normal.
	if !strings.Contains(out, "2 untriaged backlog") {
		t.Errorf("the backlog must be counted every time; it said %q", out)
	}

	// And the gate fires on a lesson that records a recurrence AFTER the seed.
	if err := os.WriteFile(filepath.Join(dir, "new.md"), []byte("Second time."), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = drive(t, "--memory", dir)
	if code != 1 {
		t.Fatalf("a new recurrence must fire the gate, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "new recorded a recurrence") {
		t.Errorf("it should name the new one; it said %q", out)
	}
}

// ⛔ 2 is never an absence: a directory that would not open is not a clean one,
// and neither is a missing declaration.
func TestItRefusesRatherThanReportingACleanCorpus(t *testing.T) {
	for _, tc := range []struct {
		name, says string
		args       []string
	}{
		{"no memory directory given", "--memory is required", []string{}},
		{"the directory is not there", "not a readable directory", []string{"--memory", filepath.Join(t.TempDir(), "nope")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := drive(t, tc.args...)
			if code != 2 {
				t.Fatalf("code %d, want 2\nout %q err %q", code, out, errOut)
			}
			if !strings.Contains(errOut, tc.says) {
				t.Errorf("it should say %q; it said %q", tc.says, errOut)
			}
			if strings.Contains(out, "recurrence(s) read") {
				t.Error("a refusal must not also report a reading")
			}
		})
	}
}

// badLedger is a memory directory whose ledger cannot be read: a directory
// stands where the file belongs, which fails on Linux and macOS alike and needs
// no permission fixture.
func badLedger(t *testing.T) string {
	t.Helper()
	dir := mem(t, map[string]string{"came-back.md": "Troisième récidive."})
	if err := os.Mkdir(filepath.Join(dir, ledgerName), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// badMemory is a directory holding a .md that cannot be read: a dangling
// symbolic link, which ReadDir lists and does not call a directory, and which
// ReadFile fails on as a plain user.
func badMemory(t *testing.T) string {
	t.Helper()
	dir := mem(t, map[string]string{"came-back.md": "Troisième récidive."})
	if err := os.Symlink(filepath.Join(dir, "nothing-here"), filepath.Join(dir, "dangling.md")); err != nil {
		t.Skip(err)
	}
	return dir
}

// ⛔⛔ AND IT ALLOWS A CORPUS WITH NOTHING LEFT OPEN, which is the state the
// machine is in most of the time and which NO test reached: deleting
// `len(open) == 0` from the hook left the suite green, and that deletion makes
// the gate deny every gitpush, ghmerge and ghrelease on this machine over a
// clean ledger. The row above for a non-publishing command cannot reach it,
// because publishes() answers first and returns early.
func TestTheHookAllowsWhenEveryRecurrenceIsAccountedFor(t *testing.T) {
	dir := mem(t, map[string]string{"came-back.md": "Troisième récidive."})
	if code, out, _ := drive(t, "--memory", dir, "--accept", "came-back", "--gate", "cmd/x"); code != 0 {
		t.Fatalf("accepting: %d %s", code, out)
	}
	var out, errOut bytes.Buffer
	code := hookMode(&out, &errOut, opts{memory: dir},
		strings.NewReader(`{"tool_input":{"command":"gitpush -u origin topic"}}`))
	if code != 0 {
		t.Fatalf("code %d, want 0", code)
	}
	if strings.Contains(out.String(), `"permissionDecision":"deny"`) {
		t.Errorf("nothing is open, so a publishing command must pass; it said %q", out.String())
	}
}

// ⛔ --hook WITH NO --memory ALLOWS, and the report mode with no --memory
// REFUSES. The two look like one check written twice and they are opposite
// contracts: a hook that cannot be configured must not block every session on
// this machine, while a report asked for nothing must not answer about a
// directory it guessed. A mutsweep showed the hook-side check surviving its own
// deletion — the outcome is the same either way, because hookMode then fails to
// Stat "" and allows — so without this the gate allowed by ACCIDENT rather than
// on purpose, which is the distinction this whole command is about.
func TestWithNoMemoryTheHookAllowsAndTheReportRefuses(t *testing.T) {
	code, _, errOut := drive(t, "--hook")
	if code != 0 {
		t.Fatalf("the hook must allow when it is misconfigured, got %d", code)
	}
	if !strings.Contains(errOut, "--hook needs --memory") {
		t.Errorf("it must say what is missing; stderr = %q", errOut)
	}
	code, _, errOut = drive(t)
	if code != 2 {
		t.Fatalf("the report must refuse rather than guess a directory, got %d", code)
	}
	if !strings.Contains(errOut, "--memory is required") {
		t.Errorf("it must say what is missing; stderr = %q", errOut)
	}
}

// ⛔ And --hook REACHES that mode. Deleting the dispatch left the suite green,
// because every hook test called hookMode directly: the flag could have stopped
// being wired and the gate would have become a silent no-op that printed a
// report nobody reads to a hook that ignores stdout.
//
// The discriminator is the stderr line: with no payload on stdin, hookMode says
// it could not read one and allows, while the report prints a count.
func TestTheHookFlagIsWiredToTheHookMode(t *testing.T) {
	dir := mem(t, map[string]string{"came-back.md": "Troisième récidive."})
	_, out, errOut := drive(t, "--hook", "--memory", dir)
	if !strings.Contains(errOut, "hook payload") {
		t.Errorf("--hook must reach the hook mode; stderr = %q", errOut)
	}
	if strings.Contains(out, "recurrence(s) read") {
		t.Errorf("--hook printed the report instead; stdout = %q", out)
	}
}

// ⛔ THE GATE BLOCKS ON ONE CONDITION ONLY. About 25 sessions share this
// machine; a detector that halted them all because it could not read a file
// would be a worse defect than the one it is here to catch. Only a definite
// finding on a publishing command denies — everything else allows and says so.
func TestTheHookDeniesOnlyADefiniteFinding(t *testing.T) {
	dir := mem(t, map[string]string{"came-back.md": "Troisième récidive."})
	const push = `{"tool_input":{"command":"cd /x && ~/.local/bin/gitpush -u origin topic"}}`

	for _, tc := range []struct {
		name, memory, payload string
		deny                  bool
		warns                 string
	}{
		{name: "a publishing command with an unaccounted recurrence", memory: dir, payload: push, deny: true},
		{name: "a command that publishes nothing", memory: dir,
			payload: `{"tool_input":{"command":"ls -la"}}`},
		{name: "a memory directory that will not open", memory: filepath.Join(dir, "nope"),
			payload: push, warns: "allowing"},
		{name: "a payload it cannot read", memory: dir, payload: "not json at all", warns: "allowing"},
		// ⛔⛔ THE TWO READ FAILURES THE TABLE DID NOT REACH, found by a full
		// mutsweep of the file rather than by the mutations I thought of:
		// deleting the error check after readLedger, and the one after scan,
		// both left the suite GREEN. The row above only reaches the Stat on
		// the directory. "It allows whenever it cannot read" was written in
		// the doc comment and held at one read of three — this package's own
		// recurring defect, in the contract that protects every session here.
		{name: "a ledger it cannot read", memory: badLedger(t), payload: push, warns: "allowing"},
		{name: "a memory file it cannot read", memory: badMemory(t), payload: push, warns: "allowing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := hookMode(&out, &errOut, opts{memory: tc.memory}, strings.NewReader(tc.payload))
			// Always 0: the decision travels in the JSON, never in the status,
			// so a non-zero exit cannot be mistaken for a block.
			if code != 0 {
				t.Fatalf("code %d, want 0", code)
			}
			denied := strings.Contains(out.String(), `"permissionDecision":"deny"`)
			if denied != tc.deny {
				t.Fatalf("denied=%v, want %v\nout: %s\nerr: %s", denied, tc.deny, out.String(), errOut.String())
			}
			if tc.deny && !strings.Contains(out.String(), "came-back") {
				t.Errorf("a refusal must name the lesson; it said %q", out.String())
			}
			if tc.warns != "" && !strings.Contains(errOut.String(), tc.warns) {
				t.Errorf("it should warn on stderr; it said %q", errOut.String())
			}
		})
	}
}

// ⛔⛔ THE DEFECT THIS FINGERPRINT IS FOR, measured on the real corpus.
//
// ubuntu-latest-moved-and-moved-back was seeded as backlog on 2026-11-11. On
// 2026-11-12 it gained two further occurrences, written into the memory in so
// many words — and `recur` said nothing, because the lesson's NAME was already
// in the ledger. A gate that can fire once per memory and never again is a gate
// that goes quiet exactly as a lesson gets worse.
func TestALessonThatRecursAGAINFiresAgainThoughItWasAccountedFor(t *testing.T) {
	dir := mem(t, map[string]string{"came-back.md": "Deuxième fois, et la note était là."})
	if code, out, _ := drive(t, "--memory", dir, "--accept", "came-back", "--gate", "cmd/x"); code != 0 {
		t.Fatalf("accepting: %d %s", code, out)
	}
	if code, _, _ := drive(t, "--memory", dir); code != 0 {
		t.Fatalf("it should be settled, got %d", code)
	}

	// The same memory, one more occurrence recorded in it.
	path := filepath.Join(dir, "came-back.md")
	if err := os.WriteFile(path, []byte(
		"Deuxième fois, et la note était là.\n\n## RÉCURRENCE 2026-11-12\n\nTroisième fois.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := drive(t, "--memory", dir)
	if code != 1 {
		t.Fatalf("a NEW recurrence must fire although the lesson was accounted for; got %d\n%s", code, out)
	}
	if !strings.Contains(out, "came-back recorded a NEW recurrence since it was accounted for") {
		t.Errorf("it should say why it is back; it said %q", out)
	}
	// And the decision already taken is named, so the reader knows what failed.
	if !strings.Contains(out, "cmd/x") {
		t.Errorf("it should name the gate that did not hold; it said %q", out)
	}
}

// ⛔ The control for the test above: a gate that fires on ANY edit is a gate
// somebody turns off. Only the sentences that record the recurrence count.
func TestAnEditThatRecordsNoNewRecurrenceIsSilent(t *testing.T) {
	dir := mem(t, map[string]string{"came-back.md": "Deuxième fois.\n\nWhy: the note was there.\n"})
	if code, _, _ := drive(t, "--memory", dir, "--accept", "came-back", "--gate", "cmd/x"); code != 0 {
		t.Fatal("accepting")
	}
	path := filepath.Join(dir, "came-back.md")
	if err := os.WriteFile(path, []byte(
		"Deuxième fois.\n\nWhy: the note was there, and here is a paragraph added today\nthat says nothing about it happening again.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := drive(t, "--memory", dir); code != 0 {
		t.Fatalf("an unrelated edit must not fire; got %d\n%s", code, out)
	}
}

// ⛔⛔ THE DEADLOCK, reported by a peer session on 2026-11-12: the only gate for
// a lesson was written and MERGEABLE, and landing it needed ghmerge — which
// this gate refuses. The gate blocked the merge that would have satisfied it.
// --gate-pending is the honest third answer: the gate is named, where it is
// waiting is published, and nobody has to write a false "--gate <thing>" for a
// binary that is not installed.
func TestAGateThatIsWrittenButHasNotLandedIsRecordedAsPending(t *testing.T) {
	dir := mem(t, map[string]string{"locked.md": "Troisième récidive."})
	why := "gitsafe#91 (literaltick), MERGEABLE and not merged; landing it needs ghmerge, which this gate refuses"
	code, out, _ := drive(t, "--memory", dir, "--accept", "locked", "--gate-pending", why)
	if code != 0 {
		t.Fatalf("a pending gate must be accepted, got %d\n%s", code, out)
	}
	if !strings.Contains(ledger(t, dir), "pending") || !strings.Contains(ledger(t, dir), why) {
		t.Errorf("the ledger must carry the state and where it waits; it says %q", ledger(t, dir))
	}
	if code, _, _ := drive(t, "--memory", dir); code != 0 {
		t.Errorf("a pending gate accounts for the lesson, got %d", code)
	}
	// And it is one answer, never two.
	if code, _, errOut := drive(t, "--memory", dir, "--accept", "locked",
		"--gate-pending", why, "--no-gate", "also this"); code != 2 {
		t.Errorf("two answers must be refused, got %d %q", code, errOut)
	}
}

// ⛔ A name nothing recorded is a typo, and a typo accounts for a lesson that
// does not exist while the real one keeps firing — with the ledger showing that
// a decision was taken.
func TestAcceptingALessonThatRecordsNothingIsRefused(t *testing.T) {
	dir := mem(t, map[string]string{"came-back.md": "Deuxième fois."})
	code, out, errOut := drive(t, "--memory", dir, "--accept", "came-bak", "--gate", "cmd/x")
	if code != 2 {
		t.Fatalf("code %d, want 2\n%s", code, out)
	}
	if !strings.Contains(errOut, "no memory named came-bak records a recurrence") {
		t.Errorf("it should name what it could not find; it said %q", errOut)
	}
}

// An entry written before fingerprints existed is counted and named, never
// assumed current; and --seed records its fingerprint WITHOUT undoing the
// decision somebody already took about it.
func TestAnEntryWithNoFingerprintIsNamedAndThenRecorded(t *testing.T) {
	dir := mem(t, map[string]string{
		"came-back.md": "Deuxième fois.",
		ledgerName:     "- came-back — no-gate: measured, nothing can refuse it\n",
	})
	code, out, _ := drive(t, "--memory", dir)
	if code != 0 {
		t.Fatalf("an old entry is not a finding, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "1 accounted entr(ies) carry no fingerprint") {
		t.Errorf("it must say so every run; it said %q", out)
	}
	if code, out, _ = drive(t, "--memory", dir, "--seed"); code != 0 {
		t.Fatalf("seeding: %d\n%s", code, out)
	}
	if !strings.Contains(out, "recorded the fingerprint of 1 entr(ies)") {
		t.Errorf("it should say what it recorded; it said %q", out)
	}
	if got := ledger(t, dir); !strings.Contains(got, "no-gate@") || !strings.Contains(got, "nothing can refuse it") {
		t.Errorf("the decision must survive re-seeding; the ledger says %q", got)
	}
}

// ⛔⛔ THE GATE REFUSED THE COMMAND THAT CLEARS IT. Measured 2026-11-13: the
// word match fired on `recur --accept … --gate-pending "<reason naming
// ghmerge>"`, because the reason said what the gate would be. A guard that
// fires on prose about itself cannot be satisfied, only removed.
func TestOnlyAProgramInCommandPositionCounts(t *testing.T) {
	for _, tc := range []struct {
		name, cmd string
		want      bool
	}{
		{"a bare publish", "gitpush -u origin topic", true},
		{"by path", "~/.local/bin/gitpush -u origin topic", true},
		{"after cd", "cd /x && ghmerge go-crdt/collab 271", true},
		{"behind an environment assignment", "GIT_TRACE=1 ghrelease go-crdt/collab 271 v0.1.0", true},
		{"after a semicolon", "echo hi; ghrelease o/r 1 v0.1.0", true},
		// ⛔ The case that provoked this: the remedy names the gate in prose.
		{"recur accounting for a lesson whose gate is ghmerge", `recur --accept l --gate-pending "lands when ghmerge stops refusing"`, false},
		{"a commit message naming a publisher", `git commit -m "note: gitpush carries the scope"`, false},
		{"a pipeline whose program is grep", "cat log | grep ghrelease", false},
		{"nothing at all", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := publishes(tc.cmd); got != tc.want {
				t.Errorf("publishes(%q) = %v, want %v", tc.cmd, got, tc.want)
			}
		})
	}
}

// The ledger and the index are not lessons, and must not be scanned as ones.
func TestTheLedgerAndTheIndexAreNotScanned(t *testing.T) {
	dir := mem(t, map[string]string{
		"MEMORY.md": "Troisième récidive mentioned in the index line.",
		ledgerName:  "- something — gate: récidive in a description",
		"real.md":   "nothing here",
	})
	code, out, _ := drive(t, "--memory", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "0 recurrence(s) read") {
		t.Errorf("neither file is a lesson; it said %q", out)
	}
}

// ⛔⛔ THE LEDGER KEEPS ONE ENTRY PER LESSON AND `--accept` REPLACES IT, so the
// entry a session read a minute ago is not the entry that is there now.
// Measured 2026-11-15: a careful relay replaced another session's LANDED,
// mutation-confirmed `gate` with a `pending` thirty seconds after it was
// written, and the ledger then said "nothing landed" about a gate that was
// already green. It had happened once that day already, to a third session.
//
// What is printed, and WHEN, is the remedy the overwritten entry's own author
// argued for: a warning naming what is there, BEFORE the write, because a diff
// printed afterwards is exactly as invisible.
func TestReplacingAnEntryPrintsTheOneThatIsThere(t *testing.T) {
	dir := mem(t, map[string]string{"lesson.md": "Troisième récidive."})
	if code, out, _ := drive(t, "--memory", dir, "--accept", "lesson", "--gate", "cmd/first"); code != 0 {
		t.Fatalf("first accept: %d\n%s", code, out)
	}
	code, out, _ := drive(t, "--memory", dir, "--accept", "lesson", "--gate", "cmd/second")
	if code != 0 {
		t.Fatalf("a gate replaced by another gate must be allowed: %d\n%s", code, out)
	}
	for _, want := range []string{"replacing the entry that is there", "gate", "cmd/first", "written "} {
		if !strings.Contains(out, want) {
			t.Errorf("the replacement did not say what it replaced (%q missing):\n%s", want, out)
		}
	}
	if !strings.Contains(ledger(t, dir), "cmd/second") {
		t.Errorf("the new decision is not in the ledger: %q", ledger(t, dir))
	}
}

// The ONE refusal: a landed gate becoming something weaker is a strict loss of
// information, and needs a reason written in public.
func TestALandedGateIsNotSilentlyDowngraded(t *testing.T) {
	dir := mem(t, map[string]string{"lesson.md": "Deuxième fois."})
	if code, out, _ := drive(t, "--memory", dir, "--accept", "lesson", "--gate", "cmd/landed"); code != 0 {
		t.Fatalf("first accept: %d\n%s", code, out)
	}
	for _, weaker := range [][]string{{"--gate-pending", "somewhere"}, {"--no-gate", "cannot"}} {
		args := append([]string{"--memory", dir, "--accept", "lesson"}, weaker...)
		code, _, errOut := drive(t, args...)
		if code != 2 {
			t.Errorf("%s over a landed gate: code %d, want 2", weaker[0], code)
		}
		if !strings.Contains(errOut, "--downgrade") {
			t.Errorf("the refusal does not name the way through: %q", errOut)
		}
		// ⛔ And the ledger still holds the gate: a refusal that had already
		// written would be the defect with a message on top.
		if !strings.Contains(ledger(t, dir), "cmd/landed") || strings.Contains(ledger(t, dir), "somewhere") {
			t.Errorf("the refused write changed the ledger: %q", ledger(t, dir))
		}
	}
}

func TestADowngradeWithItsReasonIsAllowedAndStamped(t *testing.T) {
	dir := mem(t, map[string]string{"lesson.md": "Troisième récidive."})
	drive(t, "--memory", dir, "--accept", "lesson", "--gate", "cmd/landed")
	code, out, errOut := drive(t, "--memory", dir, "--accept", "lesson",
		"--gate-pending", "rewritten, not landed", "--downgrade", "the gate passed on a race")
	if code != 0 {
		t.Fatalf("a stated downgrade must be allowed: %d\n%s\n%s", code, out, errOut)
	}
	l := ledger(t, dir)
	if !strings.Contains(l, "DOWNGRADED from gate: the gate passed on a race") {
		t.Errorf("the reason is not published beside the decision: %q", l)
	}
	if !strings.Contains(l, "rewritten, not landed") {
		t.Errorf("the new decision is missing: %q", l)
	}
}

// ⛔ `pending` and `no-gate` are NOT ordered against each other: "written, not
// landed" and "none can exist" are different decisions, not different
// strengths. Ordering them would refuse replacements this command cannot
// justify refusing.
func TestPendingAndNoGateReplaceEachOtherFreely(t *testing.T) {
	dir := mem(t, map[string]string{"lesson.md": "Deuxième occurrence."})
	drive(t, "--memory", dir, "--accept", "lesson", "--gate-pending", "over there")
	if code, _, errOut := drive(t, "--memory", dir, "--accept", "lesson", "--no-gate", "none can exist"); code != 0 {
		t.Errorf("pending → no-gate: code %d, %q", code, errOut)
	}
	if code, _, errOut := drive(t, "--memory", dir, "--accept", "lesson", "--gate-pending", "back again"); code != 0 {
		t.Errorf("no-gate → pending: code %d, %q", code, errOut)
	}
}

func TestADowngradeReasonWithNothingToReplaceIsRefused(t *testing.T) {
	dir := mem(t, map[string]string{"lesson.md": "Troisième récidive."})
	code, _, errOut := drive(t, "--memory", dir, "--accept", "lesson",
		"--gate", "cmd/x", "--downgrade", "why")
	if code != 2 || !strings.Contains(errOut, "no entry to replace") {
		t.Errorf("code %d, %q", code, errOut)
	}
}

// ⛔⛔ THE STAMP MUST NOT RIDE INSIDE THE `state@mark` TOKEN. readLedger cuts
// that segment at its FIRST "@", so a `gate@mark@time` would hand an older
// binary `mark = "mark@time"`: every entry would read as a fingerprint that
// had changed, and every lesson would be reported again as having repeated
// after it was accounted for. This is the control on the format choice.
func TestTheWriteInstantDoesNotPolluteTheFingerprint(t *testing.T) {
	dir := mem(t, map[string]string{"lesson.md": "Troisième récidive."})
	drive(t, "--memory", dir, "--accept", "lesson", "--gate", "cmd/x")
	l := ledger(t, dir)
	if !strings.Contains(l, "[written ") {
		t.Fatalf("the instant was not recorded: %q", l)
	}
	// Exactly one "@" in the whole entry line: the fingerprint's own.
	var line string
	for _, ln := range strings.Split(l, "\n") {
		if strings.HasPrefix(ln, "- lesson — ") {
			line = ln
		}
	}
	if line == "" {
		t.Fatalf("no entry line in %q", l)
	}
	if n := strings.Count(line, "@"); n != 1 {
		t.Errorf("the line carries %d '@', want exactly the fingerprint's: %q", n, line)
	}
	// And the report still reads it as accounted for, which is what a polluted
	// fingerprint would break.
	if code, out, _ := drive(t, "--memory", dir); code != 0 {
		t.Errorf("accounted-for lesson reported again: %d\n%s", code, out)
	}
}

// An entry from before the stamp existed says so rather than being given a
// time it does not have.
func TestAnEntryWrittenBeforeTheStampSaysSo(t *testing.T) {
	dir := mem(t, map[string]string{"lesson.md": "Troisième récidive."})
	// Hand-written, in the shape an older binary left behind: no [written …].
	if err := os.WriteFile(filepath.Join(dir, ledgerName),
		[]byte("- lesson — gate@deadbeef: cmd/old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := drive(t, "--memory", dir, "--accept", "lesson", "--gate", "cmd/new")
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	if !strings.Contains(out, "not recorded") {
		t.Errorf("it should say the instant is unknown rather than invent one: %q", out)
	}
}

// A rewrite of the whole file keeps every other entry's instant: writeLedger
// re-emits all of them, so a stamp that did not survive the round trip would
// be lost the first time any other lesson was accounted for.
func TestOneAcceptDoesNotEraseAnotherEntrysInstant(t *testing.T) {
	dir := mem(t, map[string]string{
		"first.md":  "Troisième récidive.",
		"second.md": "Deuxième fois.",
	})
	drive(t, "--memory", dir, "--accept", "first", "--gate", "cmd/one")
	before := ledger(t, dir)
	drive(t, "--memory", dir, "--accept", "second", "--gate", "cmd/two")
	after := ledger(t, dir)

	stamp := func(l, lesson string) string {
		for _, ln := range strings.Split(l, "\n") {
			if strings.HasPrefix(ln, "- "+lesson+" — ") {
				if m := whenTag.FindStringSubmatch(ln); m != nil {
					return m[1]
				}
			}
		}
		return ""
	}
	if s := stamp(before, "first"); s == "" || s != stamp(after, "first") {
		t.Errorf("first's instant was %q before and %q after another accept", s, stamp(after, "first"))
	}
	if stamp(after, "second") == "" {
		t.Errorf("second got no instant: %q", after)
	}
}

// fixture copies testdata/<name> into a temp directory, so a test can account
// for a lesson there without ever writing under testdata.
func fixture(t *testing.T, name string) string {
	t.Helper()
	files := map[string]string{}
	ents, err := os.ReadDir(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join("testdata", name, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(b)
	}
	return mem(t, files)
}

func account(t *testing.T, dir, lesson string) {
	t.Helper()
	if code, _, e := drive(t, "--memory", dir, "--accept", lesson, "--no-gate", "because"); code != 0 {
		t.Fatalf("accepting %s: %d %s", lesson, code, e)
	}
}

// ⛔ #303: an INDEX lists one line per child. Once the child is accounted for
// under its own name, a line that points at it is not a recurrence of the index,
// and folding another accounted lesson into the index must not re-fire the gate.
func TestAnIndexLinePointingAtAnAccountedLessonIsNotRead(t *testing.T) {
	dir := fixture(t, "hub")
	account(t, dir, "child")
	code, out, _ := drive(t, "--memory", dir)
	if code != 0 || strings.Contains(out, "hub recorded") {
		t.Fatalf("code %d; an index of accounted pointers is not a lesson\n%s", code, out)
	}
	// The fold: one more accounted lesson, one more pointer line.
	if err := os.WriteFile(filepath.Join(dir, "second.md"), []byte("Quatrième récidive, ailleurs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	account(t, dir, "second")
	f, err := os.OpenFile(filepath.Join(dir, "hub.md"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("- [second](second.md): 4e fois, replié\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := drive(t, "--memory", dir); code != 0 {
		t.Fatalf("a fold of an accounted lesson re-fired the gate: code %d\n%s", code, out)
	}
}

// Controls: the same index fires while its target is NOT accounted for (the
// child is reported under its own name and the line counts for the index).
func TestAnIndexLineFiresWhileItsTargetIsNotAccounted(t *testing.T) {
	_, out, _ := drive(t, "--memory", "testdata/hub")
	if !strings.Contains(out, "hub recorded a recurrence") {
		t.Errorf("an unaccounted target must not hide the line; got %q", out)
	}
}

// The required control: a recurrence in the index's OWN words fires even when
// every pointer is skipped.
func TestAnIndexWithASentenceOfItsOwnStillFires(t *testing.T) {
	dir := fixture(t, "hub-own")
	account(t, dir, "child")
	code, out, _ := drive(t, "--memory", dir)
	if code != 1 || !strings.Contains(out, "hub recorded a recurrence") {
		t.Fatalf("the index's own sentence must count; code %d\n%s", code, out)
	}
}

// A pointer to nothing is prose, even in an index.
func TestAnIndexLinkToAMissingFileIsNotAPointer(t *testing.T) {
	code, out, _ := drive(t, "--memory", "testdata/hub-dangling")
	if code != 1 || !strings.Contains(out, "hub recorded a recurrence") {
		t.Fatalf("code %d; a dangling index line must fire\n%s", code, out)
	}
}

// ⛔ The three cases the review of #322 found. In an ORDINARY lesson no line is
// ever skipped, even when its target is accounted for, so each must fire; and
// the same lines inside an INDEX whose target is not accounted for fire too.
func TestNothingIsSkippedOutsideAnIndexOrForAnUnaccountedTarget(t *testing.T) {
	lines := map[string]string{
		"sibling": "- [[sibling]]: récidive ×3 ici, même erreur que là-bas.\n",
		"self":    "- [[lesson]]: deuxième fois aujourd'hui.\n",
	}
	const index = "---\nmetadata:\n  node_type: index\n---\n"
	for name, line := range lines {
		for _, tc := range []struct {
			label, body string
			account     bool
		}{
			{"ordinary lesson, target accounted", line, true},
			{"index, target not accounted", index + line, false},
		} {
			t.Run(name+"/"+tc.label, func(t *testing.T) {
				dir := mem(t, map[string]string{"lesson.md": tc.body, "sibling.md": "Troisième récidive de x.\n"})
				if tc.account {
					account(t, dir, "sibling")
				}
				_, out, _ := drive(t, "--memory", dir)
				if !strings.Contains(out, "lesson recorded a recurrence") {
					t.Errorf("must fire; got %q", out)
				}
			})
		}
	}
	// The third case: appending a related-lesson line to an accounted ordinary
	// lesson must change its fingerprint.
	dir := mem(t, map[string]string{"lesson.md": "Deuxième fois, déjà.\n", "other.md": "Troisième récidive de x.\n"})
	account(t, dir, "other")
	account(t, dir, "lesson")
	if err := os.WriteFile(filepath.Join(dir, "lesson.md"),
		[]byte("Deuxième fois, déjà.\n- [o](other.md): 3e fois, encore, ici même.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := drive(t, "--memory", dir)
	if code != 1 || !strings.Contains(out, "lesson recorded a NEW recurrence") {
		t.Errorf("an appended related-lesson line must re-fire; code %d, %q", code, out)
	}
}

func TestIsIndexReadsOnlyTheFrontmatterKey(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{"---\nmetadata:\n  node_type: index\n---\nx", true},
		{"---\nname: n\nmetadata:\n  type: reference\n  node_type: \"index\"\n---\nx", true},
		{"---\nmetadata:\n\tnode_type: index\n---\nx", true},
		{"---\nmetadata:\n  node_type: memory\n---\nx", false},
		{"---\nnode_type: index\n---\nx", false},                              // not under metadata
		{"---\nmetadata:\n  a: b\nother:\n  node_type: index\n---\nx", false}, // under another key
		{"---\nmetadata:\n  node_type: memory\n---\nmetadata:\n  node_type: index\n", false},
		{"# no frontmatter\nmetadata:\n  node_type: index\n", false},
		{"", false},
		{"---\nmetadata:\n  a: b\n", false}, // frontmatter never closed
	} {
		if got := isIndex([]byte(tc.body)); got != tc.want {
			t.Errorf("isIndex(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

// Each shape on its own, so a mutation of one guard is caught by name.
func TestPointerShapes(t *testing.T) {
	dir := mem(t, map[string]string{
		"x.md": "x", "b.md": "x", "p.md": "x", "n.md": "x", "u.md": "x", "w.md": "x",
		ledgerName: "x", "MEMORY.md": "x",
	})
	if err := os.Mkdir(filepath.Join(dir, "d.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	known := map[string]entry{
		"x": {state: "gate"}, "p": {state: "pending"}, "n": {state: "no-gate"},
		"b": {state: "backlog"}, "w": {state: "gatee"}, "d": {state: "gate"},
		"missing": {state: "gate"}, "MEMORY": {state: "gate"}, "": {state: "gate"},
		strings.TrimSuffix(ledgerName, ".md"): {state: "gate"},
	}
	const q = "résumé"
	for _, tc := range []struct {
		line string
		self string
		want bool
	}{
		{"- [t](x.md): " + q, "z.md", true},
		{"[t](x.md) : " + q, "z.md", true},
		{"* [t](x.md#top); " + q, "z.md", true},
		{"> [[x]]: " + q, "z.md", true},
		{"[[x|alias]]: " + q, "z.md", true},
		{"[[x#h]]: " + q, "z.md", true},
		{"- [t](p.md): " + q, "z.md", true},  // pending counts
		{"- [t](n.md): " + q, "z.md", true},  // no-gate counts
		{"- [t](b.md): " + q, "z.md", false}, // backlog is not a decision
		{"- [t](w.md): " + q, "z.md", false}, // a state nobody chose is not one
		{"- [t](u.md): " + q, "z.md", false}, // no ledger entry
		{"- [[x]]: " + q, "x.md", false},     // a self-link
		{"- [t](" + ledgerName + "): " + q, "z.md", false},
		{"- [t](MEMORY.md): " + q, "z.md", false},
		// ⛔ A prose sentence that merely OPENS with a link is the memory's own
		// recurrence, not a pointer: measured on the real corpus, a-sweep-that-
		// cannot-see-dot-github says "[[x]] — c'est la deuxième fois" at column 0.
		{"[[x]] — c'est la deuxième fois", "z.md", false},
		{"   [[x]] — récidive le même jour.", "z.md", false},
		{"- [t](missing.md): " + q, "z.md", false},
		{"- [[missing]]: " + q, "z.md", false},
		{"- [t](d.md): " + q, "z.md", false},
		{"- [t](https://e.org/x.md): " + q, "z.md", false},
		{"- [t](../x.md): " + q, "z.md", false},
		{"- [t](sub/x.md): " + q, "z.md", false},
		{"- [t](x.txt): " + q, "z.md", false},
		{"- [t](#): " + q, "z.md", false},
		{"see [t](x.md): " + q, "z.md", false},
		{"récidive", "z.md", false},
	} {
		if got := pointer(tc.line, dir, tc.self, known); got != tc.want {
			t.Errorf("pointer(%q, self %s) = %v, want %v", tc.line, tc.self, got, tc.want)
		}
	}
}
