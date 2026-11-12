package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

func drive(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	c := RootCmd()
	var out, errOut bytes.Buffer
	return cli.Drive(c, args, &out, &errOut), out.String(), errOut.String()
}

// write puts one Go file in a fresh directory and returns the directory and
// the file.
func write(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ⛔⛔ THE DIVISION THIS COMMAND EXISTS FOR. A comment is prose and moves; a
// string literal is a VALUE and does not, because renaming it makes the code
// disagree with a filesystem or with bottles that exist.
func TestItRenamesCommentsAndNothingElse(t *testing.T) {
	tick := "`"
	dir, p := write(t, `package a

// pkgx is our runtime, and `+tick+`pkgx ls`+tick+` is how you ask it.
func f() string {
	pkgx := "pkgx"                 // a path on disk
	_ = []string{`+tick+`pkgx`+tick+`}  // the GUARD: a retired name in a raw string
	return pkgx + "/pkgx/lib"
}
`)
	code, out, _ := drive(t, "--retired", "pkgx=zpkgx", "--write", dir)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (it found something); out %q", code, out)
	}
	got := read(t, p)

	// The comments moved.
	if !strings.Contains(got, "// zpkgx is our runtime") {
		t.Error("the doc comment was not renamed")
	}
	if !strings.Contains(got, "zpkgx ls") {
		t.Error("a backticked mention inside a COMMENT was not renamed")
	}
	// The values did not.
	for _, keep := range []string{`pkgx := "pkgx"`, "`pkgx`", `"/pkgx/lib"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("a value was rewritten; %q is gone:\n%s", keep, got)
		}
	}
}

// ⛔⛔ THE GUARD CASE, MEASURED FOR REAL on go-zpkg/bottle before any file was
// written: a line-based rule was one command away from turning the test that
// REFUSES these names into one that looks for their replacements.
func TestARetiredNameInARawStringIsUntouched(t *testing.T) {
	tick := "`"
	dir, p := write(t, `package a

func g() []string {
	// the guard below refuses bk, pkgx and pkgm in a lock header
	return []string{`+tick+`bk`+tick+`, `+tick+`pkgx`+tick+`, `+tick+`pkgm`+tick+`}
}
`)
	if code, out, _ := drive(t, "--retired", "bk=zbk", "--retired", "pkgx=zpkgx",
		"--retired", "pkgm=zpkgm", "--write", dir); code != 1 {
		t.Fatalf("code = %d; out %q", code, out)
	}
	got := read(t, p)
	want := "[]string{" + tick + "bk" + tick + ", " + tick + "pkgx" + tick + ", " + tick + "pkgm" + tick + "}"
	if !strings.Contains(got, want) {
		t.Fatalf("the guard's raw strings were rewritten:\n%s", got)
	}
	if !strings.Contains(got, "refuses zbk, zpkgx and zpkgm") {
		t.Errorf("the comment above it was not renamed:\n%s", got)
	}
}

// ⛔ THE LEFT BOUNDARY: every retired spelling here is a prefix of its
// replacement, so without it every already-correct mention reads as drift.
func TestAnAlreadyCorrectNameIsNotMatched(t *testing.T) {
	dir, p := write(t, `package a

// zbk builds it and zpkgx runs it; zpkgm installs it.
func h() {}
`)
	code, out, _ := drive(t, "--retired", "bk=zbk", "--retired", "pkgx=zpkgx",
		"--retired", "pkgm=zpkgm", "--write", dir)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (nothing to do); out %q", code, out)
	}
	if got := read(t, p); !strings.Contains(got, "zbk builds it and zpkgx runs it; zpkgm installs it") {
		t.Errorf("a correct line was rewritten:\n%s", got)
	}
}

// ⛔⛔ THE ORDERING TRAP, ACROSS PAIRS, WHICH IS HOW IT REALLY HAPPENS.
// Applying the pairs one after another lets the second rewrite the first's
// output: Pkgx->Zpkgx then pkgx->zpkgx turns PkgxBin into ZzpkgxBin, which
// COMPILES. Measured on go-zpkg/zbk, 2026-11-11.
//
// The first version of this test aimed at a single pair doubling its own
// output, and it could not happen: with a left boundary one pass never
// re-reads itself. The trap needs TWO pairs, and the fix is one alternation
// rather than a loop.
func TestTwoPairsCannotRewriteEachOthersOutput(t *testing.T) {
	dir, p := write(t, `package a

// PkgxBin and pkgxDir, both ours.
func i() {}
`)
	code, out, _ := drive(t, "--retired", "Pkgx=Zpkgx", "--retired", "pkgx=zpkgx", "--write", dir)
	if code != 1 {
		t.Fatalf("code = %d, want 1; out %q", code, out)
	}
	got := read(t, p)
	if strings.Contains(got, "Zzpkgx") || strings.Contains(got, "zzpkgx") {
		t.Fatalf("a prefix was doubled:\n%s", got)
	}
	if !strings.Contains(got, "ZpkgxBin and zpkgxDir") {
		t.Errorf("want ZpkgxBin and zpkgxDir:\n%s", got)
	}
}

// ⛔⛔ A PAIR WHOSE SPELLING IS A PREFIX OF ANOTHER'S MUST NOT SHADOW IT. Go's
// regexp alternation is leftmost-FIRST, not leftmost-longest, so `pkg` placed
// before `pkgx` wins on the word "pkgx" and the longer pair never fires.
//
// This test exists because MUTATION FOUND THE GUARD EMPTY: deleting the
// longest-first sort left the whole suite green. The fleet's own five pairs
// (bk, pkgx, pkgs, pkge, pkgm) happen not to collide, so nothing was
// exercising it — and --retired is caller-supplied, so "they happen not to
// collide today" is not a property of the command.
func TestALongerSpellingWinsOverAPrefixOfIt(t *testing.T) {
	dir, p := write(t, "package a\n\n// pkgx and pkg, in that order.\nfunc l() {}\n")
	if code, _, _ := drive(t, "--retired", "pkg=SHORT", "--retired", "pkgx=LONG", "--write", dir); code != 1 {
		t.Fatalf("code = %d", code)
	}
	got := read(t, p)
	if !strings.Contains(got, "// LONG and SHORT, in that order.") {
		t.Errorf("the shorter pair shadowed the longer one:\n%s", got)
	}
}

// And the order the pairs are GIVEN in cannot change the answer, which is the
// tell that the loop is gone: reordering a command line used to decide which
// pair won.
func TestThePairOrderDoesNotChangeTheResult(t *testing.T) {
	body := "package a\n\n// PkgxBin and pkgxDir, both ours.\nfunc i() {}\n"
	var results []string
	for _, args := range [][]string{
		{"--retired", "Pkgx=Zpkgx", "--retired", "pkgx=zpkgx"},
		{"--retired", "pkgx=zpkgx", "--retired", "Pkgx=Zpkgx"},
	} {
		dir, p := write(t, body)
		if code, _, _ := drive(t, append(args, "--write", dir)...); code != 1 {
			t.Fatalf("code = %d", code)
		}
		results = append(results, read(t, p))
	}
	if results[0] != results[1] {
		t.Errorf("the pair order changed the result:\n%s\n---\n%s", results[0], results[1])
	}
}

// Without --write nothing is written, and the status still says something was
// found: a report that exits 0 is one a lane reads as clean.
func TestAReportChangesNothingAndStillFails(t *testing.T) {
	dir, p := write(t, "package a\n\n// pkgx here\nfunc j() {}\n")
	before := read(t, p)
	code, out, errOut := drive(t, "--retired", "pkgx=zpkgx", dir)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if read(t, p) != before {
		t.Error("a report wrote to the file")
	}
	if !strings.Contains(errOut, "nothing was written") {
		t.Errorf("it did not say it wrote nothing: %q %q", out, errOut)
	}
}

// ⛔ A clean tree says so in a way that cannot be read as "and identifiers and
// strings are clean too", because this command never looked at those.
func TestACleanTreeSaysWhatItDidNotLookAt(t *testing.T) {
	dir, _ := write(t, "package a\n\n// nothing retired here\nfunc k() {}\n")
	code, out, _ := drive(t, "--retired", "pkgx=zpkgx", dir)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "identifiers and string literals are NOT this command's business") {
		t.Errorf("the clean message overclaims:\n%s", out)
	}
}

func TestTheArgumentsAreChecked(t *testing.T) {
	dir, _ := write(t, "package a\n")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no directory", []string{"--retired", "pkgx=zpkgx"}},
		{"no pair", []string{dir}},
		{"malformed pair", []string{"--retired", "pkgx", dir}},
		{"empty replacement", []string{"--retired", "pkgx=", dir}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, _, _ := drive(t, tc.args...); code != 2 {
				t.Errorf("code = %d, want 2", code)
			}
		})
	}
}

// A file that does not parse is a question that could not be put, never a
// clean one.
func TestAFileThatDoesNotParseIsUnanswerable(t *testing.T) {
	dir, _ := write(t, "package a\nfunc broken( {\n")
	if code, _, _ := drive(t, "--retired", "pkgx=zpkgx", dir); code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
}

// ⛔⛔ AND IT WRITES NOTHING WHEN IT CANNOT READ EVERYTHING. The earlier
// version rewrote each file as it went, so a parse error in the SECOND file
// exited 2 — "the question could not be put" — over a tree the command had
// already half-renamed. A caller who trusts that status does not even know to
// look.
func TestAParseErrorLateLeavesTheEarlierFilesAlone(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "a_good.go")
	bad := filepath.Join(dir, "b_bad.go")
	body := "package a\n\n// pkgx here\nfunc a() {}\n"
	for p, src := range map[string]string{good: body, bad: "package a\nfunc broken( {\n"} {
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if code, _, _ := drive(t, "--retired", "pkgx=zpkgx", "--write", dir); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if got := read(t, good); got != body {
		t.Errorf("the readable file was rewritten before the unreadable one was reached:\n%s", got)
	}
}

// ⛔⛔ A DIRECTIVE IS A COMMENT TO THE PARSER AND IS NOT PROSE. Its operands
// are paths, identifiers and command lines — the two categories this command
// refuses to touch as string literals, arriving through the one door it
// trusts. //go:embed assets/pkgx.json renamed is a build that fails with "no
// matching files".
func TestADirectiveIsNotProse(t *testing.T) {
	dir, p := write(t, `package a

import _ "embed"

// pkgx holds it.
//
//go:embed pkgx.json
var blob string

//go:generate pkgx gen ./...
//go:linkname pkgxHook runtime.pkgxHook
func m() {}
`)
	code, out, _ := drive(t, "--retired", "pkgx=zpkgx", "--write", dir)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (the prose line); out %q", code, out)
	}
	got := read(t, p)
	for _, keep := range []string{"//go:embed pkgx.json", "//go:generate pkgx gen", "//go:linkname pkgxHook runtime.pkgxHook"} {
		if !strings.Contains(got, keep) {
			t.Errorf("a directive was rewritten; %q is gone:\n%s", keep, got)
		}
	}
	if !strings.Contains(got, "// zpkgx holds it.") {
		t.Errorf("the prose above it was not renamed:\n%s", got)
	}
}

// ⛔ AND THE CGO PREAMBLE IS C SOURCE, not English. The `// #` test catches
// the preprocessor lines; a typedef or a C comment carries no marker of its
// own, so the preamble is excluded as a whole.
func TestTheCgoPreambleIsCSourceAndNotProse(t *testing.T) {
	dir, p := write(t, `package a

// #include "pkgx/shim.h"
// typedef struct pkgx_t pkgx_t;
// /* the pkgx handle */
import "C"

// pkgx wraps it.
func n() {}
`)
	if code, out, _ := drive(t, "--retired", "pkgx=zpkgx", "--write", dir); code != 1 {
		t.Fatalf("code = %d, want 1; out %q", code, out)
	}
	got := read(t, p)
	for _, keep := range []string{`// #include "pkgx/shim.h"`, "// typedef struct pkgx_t pkgx_t;", "// /* the pkgx handle */"} {
		if !strings.Contains(got, keep) {
			t.Errorf("the cgo preamble was rewritten; %q is gone:\n%s", keep, got)
		}
	}
	if !strings.Contains(got, "// zpkgx wraps it.") {
		t.Errorf("the prose below it was not renamed:\n%s", got)
	}
}

// ⛔⛔ AN ADDRESS IN A COMMENT IS STILL AN ADDRESS. ghcr.io/go-pkgx/packages is
// LIVE: GitHub's container packages are org-scoped and did not follow the
// repository transfer. Renaming it in prose names an organisation that does
// not exist, which is how an empty registry shipped in v0.64.0.
func TestAKeptAddressIsNotRenamed(t *testing.T) {
	dir, p := write(t, `package a

// Bottles live at ghcr.io/go-pkgx/packages, and pkgx fetches them.
func o() {}
`)
	if code, out, _ := drive(t, "--retired", "pkgx=zpkgx",
		"--keep", "ghcr.io/go-pkgx/packages", "--write", dir); code != 1 {
		t.Fatalf("code = %d, want 1; out %q", code, out)
	}
	got := read(t, p)
	if !strings.Contains(got, "ghcr.io/go-pkgx/packages") {
		t.Errorf("a live address was rewritten:\n%s", got)
	}
	if !strings.Contains(got, "and zpkgx fetches them") {
		t.Errorf("the mention beside it was not renamed:\n%s", got)
	}
}

// ⛔⛔ THE DOUBLED-FORM COUNT IS NOT DEAD CODE, and this is the measurement
// that says so. One alternation makes doubling unreachable for a WORD-shaped
// pair — but \b falls between a non-word character and a word one, so when the
// replacement's added prefix is punctuation the boundary lands INSIDE the
// already-renamed form and matches it again. Then the guard fires, and the
// file is not written.
func TestADoubledPrefixIsRefusedAndNothingIsWritten(t *testing.T) {
	body := "package a\n\n// -bk is already the flag.\nfunc p() {}\n"
	dir, p := write(t, body)
	code, _, errOut := drive(t, "--retired", "bk=-bk", "--write", dir)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (unanswerable); stderr %q", code, errOut)
	}
	if !strings.Contains(errOut, "doubled") {
		t.Errorf("it did not say what it refused: %q", errOut)
	}
	if got := read(t, p); got != body {
		t.Errorf("a doubled pass was written anyway:\n%s", got)
	}
}

// ⛔ THE SAME SPELLING TWICE IS A QUESTION WITH TWO ANSWERS, and the winner
// would be decided by a length sort that cannot separate them.
func TestASpellingGivenTwiceIsRefused(t *testing.T) {
	dir, _ := write(t, "package a\n")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"two answers", []string{"--retired", "pkgx=zpkgx", "--retired", "pkgx=zpkg", dir}},
		{"the same answer twice", []string{"--retired", "pkgx=zpkgx", "--retired", "pkgx=zpkgx", dir}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, _, _ := drive(t, tc.args...); code != 2 {
				t.Errorf("code = %d, want 2", code)
			}
		})
	}
}
