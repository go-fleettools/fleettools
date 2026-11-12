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
	if !strings.Contains(ledger(t, dir), "lesson — gate: cmd/viaonly") {
		t.Errorf("the ledger should carry it; it says %q", ledger(t, dir))
	}
}

// ⛔ A waiver is made in public: --no-gate takes a REASON, never a bare flag,
// and the reason is written down where the next reader finds it.
func TestAWaiverCarriesItsReasonIntoTheLedger(t *testing.T) {
	dir := mem(t, map[string]string{"lesson.md": "Deuxième fois."})

	code, _, errOut := drive(t, "--memory", dir, "--accept", "lesson")
	if code != 2 || !strings.Contains(errOut, "exactly one of --gate or --no-gate") {
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
