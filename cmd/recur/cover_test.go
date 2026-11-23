// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

var _ io.Writer = failWriter{}

// main wires the staleness note and the command to the process.
func TestMainRunsTheCommandAndExitsWithItsStatus(t *testing.T) {
	oldArgs, oldExit := os.Args, cli.OsExit
	t.Cleanup(func() { os.Args, cli.OsExit = oldArgs, oldExit })
	exited := -1
	cli.OsExit = func(code int) { exited = code }
	dir := mem(t, map[string]string{"a.md": "Récidive le 2026-11-01."})
	os.Args = []string{"recur", "--memory", dir}
	main()
	if exited != 1 {
		t.Errorf("an unaccounted recurrence exited %d, want 1", exited)
	}
}

// `command -p` alone asks for neither description nor a program: it is not -v.
func TestDescribesOnlyNeedsAVFlag(t *testing.T) {
	if describesOnly([]string{"-p"}) {
		t.Error("`command -p` describes nothing")
	}
	if !describesOnly([]string{"-p", "-v"}) {
		t.Error("`command -p -v` describes")
	}
}

// Semicolons are words of their own, and `;;` ends a case arm.
func TestSplitEmitsSemicolons(t *testing.T) {
	for in, want := range map[string][]string{
		"a;b":  {"a", ";", "b"},
		"a;;b": {"a", ";;", "b"},
		"a;":   {"a", ";"},
		";x":   {";", "x"},
	} {
		if got := split(in); !reflect.DeepEqual(got, want) {
			t.Errorf("split(%q) = %q, want %q", in, got, want)
		}
	}
}

// A stamp from the future is a clock skew, not extra grace: the grace left is
// the whole grace, never more.
func TestAStampFromTheFutureGivesNoExtraGrace(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	read := func(string, string) (origin, error) {
		return origin{session: "other", modified: now.Add(2 * time.Hour)}, nil
	}
	refuse, warn := scopeByOwner([]finding{{lesson: "x"}}, opts{grace: time.Hour}, "me", now, read)
	if len(refuse) != 0 || len(warn) != 1 || warn[0].left != time.Hour {
		t.Errorf("refuse %v, warn %+v; want one warning with exactly the grace (1h) left", refuse, warn)
	}
}

// The grace is exclusive at its end: a lesson exactly one grace old refuses
// every session, one second younger only warns.
func TestTheGraceEndsAtExactlyItsAge(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	at := func(age time.Duration) func(string, string) (origin, error) {
		return func(string, string) (origin, error) {
			return origin{session: "other", modified: now.Add(-age)}, nil
		}
	}
	o := opts{grace: time.Hour}
	refuse, warn := scopeByOwner([]finding{{lesson: "x"}}, o, "me", now, at(time.Hour))
	if len(refuse) != 1 || len(warn) != 0 {
		t.Errorf("at exactly the grace: refuse %v, warn %v; want a refusal", refuse, warn)
	}
	refuse, warn = scopeByOwner([]finding{{lesson: "x"}}, o, "me", now, at(time.Hour-time.Second))
	if len(refuse) != 0 || len(warn) != 1 || warn[0].left != time.Second {
		t.Errorf("a second inside the grace: refuse %v, warn %+v", refuse, warn)
	}
}

func TestShortIDKeepsEightCharacters(t *testing.T) {
	if got := shortID("abc"); got != "abc" {
		t.Errorf("shortID(abc) = %q", got)
	}
	if got := shortID("123456789"); got != "12345678" {
		t.Errorf("shortID(123456789) = %q", got)
	}
	if got := shortID("12345678"); got != "12345678" {
		t.Errorf("shortID(12345678) = %q", got)
	}
}

func TestReadOriginReportsWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	if _, err := readOrigin(dir, "absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing memory gave %v", err)
	}
	// The read error is the one reported even when a stat would have succeeded.
	old := statFile
	t.Cleanup(func() { statFile = old })
	statFile = func(string) (os.FileInfo, error) { return os.Stat(dir) }
	if _, err := readOrigin(dir, "absent"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing memory with a stat that succeeds gave %v", err)
	}
	// The file is read and then vanishes before its mtime is asked for.
	statFile = func(string) (os.FileInfo, error) { return nil, errors.New("gone") }
	if err := os.WriteFile(filepath.Join(dir, "m.md"), []byte("no frontmatter"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readOrigin(dir, "m"); err == nil || err.Error() != "gone" {
		t.Errorf("a stat that failed gave %v", err)
	}
}

func TestScanRefusesADirectoryItCannotList(t *testing.T) {
	if _, err := scan(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("an absent directory scanned clean")
	}
}

// A ledger line with no " — " is not an entry.
func TestALedgerLineWithNoSeparatorIsSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), ledgerName)
	body := "- no separator here\n- lesson — gate: lands\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	known, err := readLedger(path)
	if err != nil || len(known) != 1 || known["lesson"].state != "gate" {
		t.Errorf("known = %+v, err %v", known, err)
	}
}

// A ledger that exists but cannot be written is a refusal, not a silent loss.
func TestALedgerThatCannotBeWrittenRefusesAccept(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes everything")
	}
	dir := mem(t, map[string]string{"a.md": "Récidive le 2026-11-01.", "b.md": "Récidive le 2026-11-02."})
	account(t, dir, "a")
	path := filepath.Join(dir, ledgerName)
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := drive(t, "--memory", dir, "--accept", "b", "--no-gate", "because")
	if code != 2 || out != "" || !strings.Contains(errOut, "recur: writing the ledger:") {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestALedgerThatCannotBeWrittenRefusesSeed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes everything")
	}
	dir := mem(t, map[string]string{"a.md": "Récidive le 2026-11-01."})
	path := filepath.Join(dir, ledgerName)
	if err := writeLedger(path, map[string]entry{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := drive(t, "--memory", dir, "--seed")
	if code != 2 || out != "" || !strings.Contains(errOut, "recur: writing the ledger:") {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestJSONReportRefusesADirectoryItCannotList(t *testing.T) {
	var out, errOut bytes.Buffer
	code := jsonReport(&out, &errOut, filepath.Join(t.TempDir(), "absent"), map[string]entry{})
	if code != 2 || out.Len() != 0 || !strings.HasPrefix(errOut.String(), "recur: ") {
		t.Errorf("code %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

// A memory whose origin cannot be read is NAMED as unreadable, not dropped and
// not counted a lesson.
func TestJSONNamesAMemoryWhoseOriginCannotBeRead(t *testing.T) {
	dir := mem(t, map[string]string{"a.md": "Récidive le 2026-11-01."})
	old := originOf
	t.Cleanup(func() { originOf = old })
	originOf = func(string, string) (origin, error) { return origin{}, errors.New("no origin") }
	var out, errOut bytes.Buffer
	code := jsonReport(&out, &errOut, dir, map[string]entry{})
	d := decode(t, out.String())
	if code != 2 || len(d.Lessons) != 0 || len(d.Unreadable) != 1 ||
		d.Unreadable[0].File != "a.md" || d.Unreadable[0].Error != "no origin" {
		t.Errorf("code %d, doc %s", code, out.String())
	}
}

// Two unreadable files come out in name order, whatever order the directory gave.
func TestJSONListsUnreadableFilesInNameOrder(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := mem(t, map[string]string{"b.md": "x", "a.md": "x", "c.md": "x"})
	for _, n := range []string{"a.md", "b.md", "c.md"} {
		p := filepath.Join(dir, n)
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(p, 0o644)
	}
	_, out, _ := drive(t, "--memory", dir, "--json")
	d := decode(t, out)
	if len(d.Unreadable) != 3 || d.Unreadable[0].File != "a.md" || d.Unreadable[1].File != "b.md" || d.Unreadable[2].File != "c.md" {
		t.Errorf("unreadable = %+v", d.Unreadable)
	}
}

// A state word the ledger does not know is reported as "unknown", not as one of
// the decisions and not as blank.
func TestJSONReportsAnUnknownDecision(t *testing.T) {
	dir := mem(t, map[string]string{"a.md": "Récidive le 2026-11-01."})
	if err := os.WriteFile(filepath.Join(dir, ledgerName), []byte("- a — weird@abc: whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := drive(t, "--memory", dir, "--json")
	d := decode(t, out)
	if len(d.Lessons) != 1 || d.Lessons[0].Ledger.Decision != "unknown" || d.Totals.Decisions["unknown"] != 1 {
		t.Errorf("doc %s", out)
	}
	if code != 1 {
		t.Errorf("code %d, want 1: an unknown state is not a decision", code)
	}
}

// A report that cannot be written is a refusal.
func TestJSONRefusesAWriterThatFails(t *testing.T) {
	dir := mem(t, map[string]string{"a.md": "nothing"})
	var errOut bytes.Buffer
	code := jsonReport(failWriter{}, &errOut, dir, map[string]entry{})
	if code != 2 || errOut.String() != "recur: disk full\n" {
		t.Errorf("code %d, stderr %q", code, errOut.String())
	}
}

// "Month day, year" states its year.
func TestParseDateMonthDayYear(t *testing.T) {
	d, inferred, ok := parseDate("it happened November 18, 2026 again", time.Time{})
	if d != "2026-11-18" || inferred || !ok {
		t.Errorf("= %q, %v, %v", d, inferred, ok)
	}
}

// A day and month with no frontmatter year to take one from is not a date.
func TestAYearlessDateWithNoStampedYearIsNotGuessed(t *testing.T) {
	if d, inf, ok := parseDate("01-01", time.Time{}); ok || inf || d != "" {
		t.Errorf("parseDate(01-01, zero) = %q, %v, %v; want nothing", d, inf, ok)
	}
}

// The hook says WHY it allows when the memory is not a readable directory, for
// a path that is absent and for one that is a file.
func TestTheHookNamesAMemoryThatIsNotADirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{file, filepath.Join(t.TempDir(), "absent")} {
		var out, errOut bytes.Buffer
		payload := `{"tool_input":{"command":"gitpush -u origin topic"}}`
		code := hookMode(&out, &errOut, opts{memory: p}, strings.NewReader(payload))
		want := "recur: " + p + " is not a readable directory; allowing (a refusal is not a verdict)\n"
		if code != 0 || out.Len() != 0 || errOut.String() != want {
			t.Errorf("%s: code %d, stdout %q, stderr %q", p, code, out.String(), errOut.String())
		}
	}
}

// A link whose fragment holds "://" is not a pointer, and a link that climbs out
// of the memory directory is never read, even when the file it names exists.
func TestAPointerNeverLeavesTheMemoryDirectory(t *testing.T) {
	const q = "Troisième récidive de x."
	body := "---\ndescription: \"récidive ×3 de x\"\n---\n" + q + "\n"
	base := t.TempDir()
	dir := filepath.Join(base, "mem")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"x.md", "sub/x.md", "../outside.md"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, line := range []string{
		"- [t](x.md#a://b): " + q,
		"- [t](../outside.md): " + q,
		"- [t](sub/x.md): " + q,
	} {
		if pointer(line, dir, "z.md") {
			t.Errorf("pointer(%q) = true, want false", line)
		}
	}
	if !pointer("- [t](x.md): "+q, dir, "z.md") {
		t.Error("the control link to a sibling is not a pointer")
	}
}

// Nothing quoted is never a quotation, even of a memory whose description is empty.
func TestNothingQuotedIsNotAQuotation(t *testing.T) {
	body := []byte("---\ndescription:\n---\nRécidive ici.\n")
	if quotes("", body) || quotes("  ", body) {
		t.Error("an empty quotation matched an empty description")
	}
}
