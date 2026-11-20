// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"
)

var update = flag.Bool("update", false, "rewrite testdata/json.golden")

var nov20 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// Both sides of each date form: the form is read, and a near-miss is not.
func TestParseDate(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		mod      time.Time
		want     string
		inferred bool
	}{
		{"iso", "RÉCURRENCE le 2026-11-18 au soir", time.Time{}, "2026-11-18", false},
		{"iso-needs-no-modified", "2026-01-02", time.Time{}, "2026-01-02", false},
		{"french-day-month", "récidive le 18-11", nov20, "2026-11-18", true},
		{"french-slash", "récidive le 18/11", nov20, "2026-11-18", true},
		{"day-month-year", "le 18-11-2026", time.Time{}, "2026-11-18", false},
		{"named-fr", "le 18 novembre", nov20, "2026-11-18", true},
		{"named-fr-year", "le 18 novembre 2025", nov20, "2025-11-18", false},
		{"named-fr-1er", "le 1er février 2026", time.Time{}, "2026-02-01", false},
		{"named-fr-accentless", "le 3 aout 2026", time.Time{}, "2026-08-03", false},
		{"named-en", "happened again on November 12th", nov20, "2026-11-12", true},
		{"named-en-year", "18 November 2026", time.Time{}, "2026-11-18", false},
		{"previous-year", "le 28-12", time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), "2025-12-28", true},
		{"earliest-wins", "le 18 novembre (voir 2026-11-07)", nov20, "2026-11-18", true},

		// The other side: nothing stated, nothing returned.
		{"no-date", "C'est la deuxième fois.", nov20, "", false},
		{"yearless-without-modified", "récidive le 18-11", time.Time{}, "", false},
		{"yearless-after-modified", "récidive le 25-12", time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC), "2025-12-25", true},
		{"impossible-iso", "2026-02-30", nov20, "", false},
		{"impossible-day-month", "le 31-02", nov20, "", false},
		{"range-is-not-a-date", "2-3 fois", nov20, "", false},
		{"iso-tail-is-not-day-month", "2026-11", nov20, "", false},
		{"version-is-not-a-date", "v0.12-34", nov20, "", false},
		{"month-without-day", "en novembre", nov20, "", false},
		{"year-only-month", "mars 2026", nov20, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, inferred, ok := parseDate(tc.in, tc.mod)
			if ok != (tc.want != "") || got != tc.want || inferred != tc.inferred {
				t.Errorf("parseDate(%q) = %q inferred=%v ok=%v; want %q inferred=%v", tc.in, got, inferred, ok, tc.want, tc.inferred)
			}
		})
	}
}

// A sentence with no date of its own takes the heading it sits under, and a
// sentence under a dateless heading has none.
func TestHeadingDate(t *testing.T) {
	lines := []string{"## Le 2026-11-03", "", "Troisième récidive.", "## Sans date", "Quatrième récidive."}
	if d, _, ok := dateOfHit(lines, 2, time.Time{}); !ok || d != "2026-11-03" {
		t.Errorf("under a dated heading: %q %v", d, ok)
	}
	if d, _, ok := dateOfHit(lines, 4, time.Time{}); ok {
		t.Errorf("under a dateless heading it took %q; the nearest heading governs", d)
	}
}

// Properties that must hold for any input: no panic, and whatever comes back is
// a real calendar date. Its committed corpus lives in testdata/fuzz.
func FuzzParseDate(f *testing.F) {
	for _, s := range []string{"2026-11-18", "le 18-11", "le 18 novembre 2026", "November 12th", "31-02", "0000-00-00", "99/99/9999", "1er mai"} {
		f.Add(s, int64(1795000000))
	}
	f.Fuzz(func(t *testing.T, s string, unix int64) {
		var mod time.Time
		if unix != 0 {
			mod = time.Unix(unix%1e11, 0).UTC()
		}
		d, inferred, ok := parseDate(s, mod)
		if !ok {
			if d != "" || inferred {
				t.Fatalf("not ok but returned %q %v", d, inferred)
			}
			return
		}
		tm, err := time.Parse("2006-01-02", d)
		if err != nil || tm.Format("2006-01-02") != d {
			t.Fatalf("parseDate(%q) = %q, not a calendar date: %v", s, d, err)
		}
		if inferred && (mod.IsZero() || tm.After(mod)) {
			t.Fatalf("parseDate(%q) inferred %q from %v", s, d, mod)
		}
		_ = utf8.ValidString(s)
	})
}

// fixture copies testdata/json and writes a ledger with the fingerprints the
// current sentences have, plus one that no longer matches.
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ents, err := os.ReadDir("testdata/json")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join("testdata/json", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "accounted.md"))
	mark, _ := fingerprint(b, dir, "accounted.md")
	b, _ = os.ReadFile(filepath.Join(dir, "french.md"))
	fmark, _ := fingerprint(b, dir, "french.md")
	known := map[string]entry{
		"accounted": {lesson: "accounted", state: "gate", mark: mark, detail: "refused by the pre-push hook", when: "2026-11-19T09:00:00Z"},
		"repeated":  {lesson: "repeated", state: "no-gate", mark: "000000000000", detail: "none can exist ⛔ DOWNGRADED from gate: the tool was removed", when: "2026-11-10T08:00:00Z"},
		"french":    {lesson: "french", state: "backlog", mark: fmark, detail: "seeded, not triaged"},
	}
	if err := writeLedger(filepath.Join(dir, ledgerName), known); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestJSONGolden(t *testing.T) {
	dir := fixture(t)
	code, out, errOut := drive(t, "--memory", dir, "--json")
	if code != 1 { // `repeated` and the unaccounted ones are findings
		t.Fatalf("code %d, want 1\n%s", code, errOut)
	}
	golden := "testdata/json.golden"
	if *update {
		if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Errorf("--json differs from %s (run with -update to rewrite it):\n%s", golden, out)
	}
}

func decode(t *testing.T, out string) jsonDoc {
	t.Helper()
	var d jsonDoc
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	return d
}

// A lesson accounted for and then repeated is told apart from one that is
// accounted for and quiet, and from one with no fingerprint to compare.
func TestJSONRepeatedAfterAccounted(t *testing.T) {
	_, out, _ := drive(t, "--memory", fixture(t), "--json")
	d := decode(t, out)
	by := map[string]jsonLesson{}
	for _, l := range d.Lessons {
		by[l.Name] = l
	}
	if m := by["accounted"].FingerprintMatches; m == nil || !*m {
		t.Errorf("accounted: fingerprintMatches = %v, want true", m)
	}
	if m := by["repeated"].FingerprintMatches; m == nil || *m {
		t.Errorf("repeated: fingerprintMatches = %v, want false", m)
	}
	if by["undated"].FingerprintMatches != nil {
		t.Errorf("no ledger entry: fingerprintMatches should be null")
	}
	if g := by["repeated"].Ledger.Downgrade; g == nil || g.From != "gate" || g.Reason != "the tool was removed" {
		t.Errorf("downgrade = %+v", g)
	}
	if d.Totals.RepeatedAfterAccepted != 1 {
		t.Errorf("repeatedAfterAccounted = %d, want 1", d.Totals.RepeatedAfterAccepted)
	}
	if _, ok := by["quiet"]; ok {
		t.Error("a memory with no recurrence is not a lesson")
	}
}

func TestJSONTotals(t *testing.T) {
	_, out, _ := drive(t, "--memory", fixture(t), "--json")
	d := decode(t, out)
	tt := d.Totals
	if tt.Lessons != 6 || tt.Decisions["gate"] != 1 || tt.Decisions["no-gate"] != 1 || tt.Decisions["untriaged"] != 1 || tt.Decisions["none"] != 3 {
		t.Errorf("totals: %+v", tt)
	}
	if tt.Recurrences != tt.RecurrencesDated+tt.RecurrencesUndated || tt.RecurrencesUndated != 1 {
		t.Errorf("dated %d undated %d of %d; only undated.md states no date", tt.RecurrencesDated, tt.RecurrencesUndated, tt.Recurrences)
	}
}

// A file that cannot be read is NAMED, never counted as a lesson with no
// recurrence, and the run refuses to call the corpus clean.
func TestJSONUnreadable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := mem(t, map[string]string{"ok.md": "Récidive le 2026-11-01.", "blind.md": "Récidive."})
	if err := os.Chmod(filepath.Join(dir, "blind.md"), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Join(dir, "blind.md"), 0o644)
	code, out, _ := drive(t, "--memory", dir, "--json")
	d := decode(t, out)
	if len(d.Unreadable) != 1 || d.Unreadable[0].File != "blind.md" || d.Totals.Unreadable != 1 {
		t.Errorf("unreadable = %+v", d.Unreadable)
	}
	if code != 2 {
		t.Errorf("code %d, want 2: a blind scan is not a clean one", code)
	}
}

func TestJSONRefusesToWrite(t *testing.T) {
	code, _, errOut := drive(t, "--memory", mem(t, nil), "--json", "--seed")
	if code != 2 || errOut == "" {
		t.Errorf("code %d %q", code, errOut)
	}
}

// The text report and its exit codes are untouched by the flag existing.
func TestJSONCleanCorpusExitsZero(t *testing.T) {
	code, out, _ := drive(t, "--memory", mem(t, map[string]string{"a.md": "nothing"}), "--json")
	d := decode(t, out)
	if code != 0 || d.Lessons == nil || len(d.Lessons) != 0 || d.Unreadable == nil {
		t.Errorf("code %d, doc %s", code, out)
	}
}
