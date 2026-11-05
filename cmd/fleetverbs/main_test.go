package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// drive runs the command and returns its exit code and both outputs.
func drive(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// write puts a file under dir, making the directories it needs.
func write(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tree is a repository shaped like the fleet's: a library package, a cobra
// command, a test file, a fixture directory and one file that will not parse.
func tree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "convert.go", `package convert

// ArchiveToPDF turns a comic archive into a PDF, one page per picture.
//
// A second paragraph that must not reach the output.
func ArchiveToPDF(src []byte) ([]byte, error) { return nil, nil }

// Options is what a conversion is told.
type Options struct{ DPI float64 }

// Readers names every format this package can read.
func Readers() []string { return formats }

// formats is a table, which is the point: the word psd is in here and in no
// exported name at all.
var formats = []string{"png", "psd"}

func unexported() {}
`)
	write(t, dir, "convert_test.go", `package convert

import "testing"

// ExportedLookingNameInATest must not be read.
func TestSomething(t *testing.T) {}
`)
	write(t, dir, "cmd/pdfconv/main.go", `package main

import "github.com/spf13/cobra"

func root() *cobra.Command {
	c := &cobra.Command{Use: "pdfconv", Short: "turn things into PDFs"}
	c.AddCommand(&cobra.Command{Use: "archive", Short: "a comic archive becomes a PDF"})
	c.AddCommand(&cobra.Command{Use: "noshort"})
	return c
}
`)
	write(t, dir, "testdata/broken.go", "package testdata\nthis is not Go at all\n")
	write(t, dir, "wont_parse.go", "package convert\nfunc (\n")
	return dir
}

func TestItPrintsWhatAPackageExportsWithItsFirstDocLine(t *testing.T) {
	code, out, _ := drive(t, tree(t))
	if code != 0 {
		t.Fatalf("code %d, out %q", code, out)
	}
	for _, want := range []string{
		"func ArchiveToPDF(src []byte) ([]byte, error)",
		"ArchiveToPDF turns a comic archive into a PDF, one page per picture.",
		"type Options",
		"func Readers() []string",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not hold %q:\n%s", want, out)
		}
	}
	// ⛔ The FIRST line only. A paragraph of rationale per symbol would make
	// the answer unreadable, which is the same as not answering.
	if strings.Contains(out, "second paragraph") {
		t.Error("a symbol's whole documentation was printed, not its first line")
	}
	// What is not exported, and what is only in a test, is not the package's
	// surface.
	if strings.Contains(out, "unexported") || strings.Contains(out, "TestSomething") {
		t.Errorf("unexported or test-only names reached the output:\n%s", out)
	}
}

func TestItPrintsTheCobraVerbs(t *testing.T) {
	_, out, _ := drive(t, tree(t))
	for _, want := range []string{
		"verb pdfconv — turn things into PDFs",
		"verb archive — a comic archive becomes a PDF",
		"verb noshort",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not hold %q:\n%s", want, out)
		}
	}
}

// ⛔⛔ THE TALLY IS THE WHOLE POINT. A sweep that cannot read part of its input
// and prints the part it could is indistinguishable from one that found
// nothing there — which is how an inventory comes to be confidently short.
func TestWhatCouldNotBeReadIsCountedAndNamed(t *testing.T) {
	_, out, _ := drive(t, tree(t))
	if !strings.Contains(out, "wont_parse.go") {
		t.Errorf("a file that would not parse is not named:\n%s", out)
	}
	if !strings.Contains(out, "testdata") {
		t.Errorf("the fixture directory is not named as skipped:\n%s", out)
	}
	if !strings.Contains(out, "not read:") {
		t.Errorf("nothing says what was not read:\n%s", out)
	}
	// And the denominator: two packages, and the files that DID parse.
	if !strings.Contains(out, "2 package(s)") {
		t.Errorf("the package count is missing or wrong:\n%s", out)
	}
}

func TestMatchKeepsNamesAndDocumentation(t *testing.T) {
	dir := tree(t)

	// By NAME.
	_, out, _ := drive(t, "--match", "archivetopdf", dir)
	if !strings.Contains(out, "ArchiveToPDF") || strings.Contains(out, "type Options") {
		t.Errorf("matching a name kept the wrong symbols:\n%s", out)
	}
	// By DOCUMENTATION: nothing is named "comic".
	_, out, _ = drive(t, "--match", "comic", dir)
	if !strings.Contains(out, "ArchiveToPDF") {
		t.Errorf("matching a word from the documentation found nothing:\n%s", out)
	}
	// And a word in neither finds nothing, rather than everything.
	_, out, _ = drive(t, "--match", "nothinghasthisword", dir)
	if strings.Contains(out, "ArchiveToPDF") {
		t.Errorf("an unmatched word kept a symbol:\n%s", out)
	}
}

// ⛔ THE CASE THAT MADE THIS TOOL. go-pdfkit/convert reads PSD and says so in
// no exported name: the format is a row in a table. The API answer is honestly
// "no" and the source answer is "codec.go, go and look", and the two must not
// be printed as if they were the same answer.
func TestAWordInTheSourceIsAnsweredApartFromTheAPI(t *testing.T) {
	dir := tree(t)

	code, out, _ := drive(t, "--match", "psd", dir)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if strings.Contains(out, "func ") {
		t.Errorf("the API answer should be empty for psd:\n%s", out)
	}

	code, out, _ = drive(t, "--match", "psd", "--text", dir)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out, "nothing in the API is named for it") {
		t.Errorf("the weaker answer is not labelled as weaker:\n%s", out)
	}
	if !strings.Contains(out, "convert.go:") || !strings.Contains(out, "text ") {
		t.Errorf("the source mentions are not placed:\n%s", out)
	}
	if !strings.Contains(out, "source mention(s)") {
		t.Errorf("the tally does not count the mentions:\n%s", out)
	}
	// ⛔ And --text without --match says nothing rather than everything: a
	// grep for "" is every line of the repository.
	if _, bare, _ := drive(t, "--text", dir); strings.Contains(bare, "text ") {
		t.Errorf("--text with no --match printed source lines:\n%s", bare)
	}
}

func TestTheNamedMentionsAreCappedAndTheRestCounted(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package many\n")
	for i := 0; i < textHits+4; i++ {
		b.WriteString("// the word needle is here\n")
	}
	write(t, dir, "many.go", b.String())

	_, out, _ := drive(t, "--match", "needle", "--text", dir)
	// ⛔ The "… and N more" line also begins with the text marker, so counting
	// the marker counts it too. Count the lines that name a FILE.
	if n := strings.Count(out, "    text many.go:"); n != textHits {
		t.Errorf("%d lines named, want %d:\n%s", n, textHits, out)
	}
	if !strings.Contains(out, "and 4 more") {
		t.Errorf("the lines not named are not counted:\n%s", out)
	}
}

func TestCliPrintsOnlyTheVerbs(t *testing.T) {
	_, out, _ := drive(t, "--cli", tree(t))
	if !strings.Contains(out, "verb pdfconv") {
		t.Errorf("the verbs are missing:\n%s", out)
	}
	if strings.Contains(out, "func ArchiveToPDF") {
		t.Errorf("--cli printed the API as well:\n%s", out)
	}
}

func TestQuietDropsTheTallyAndNothingElse(t *testing.T) {
	_, out, _ := drive(t, "--quiet", tree(t))
	if strings.Contains(out, "package(s)") || strings.Contains(out, "not read:") {
		t.Errorf("--quiet still printed the tally:\n%s", out)
	}
	if !strings.Contains(out, "ArchiveToPDF") {
		t.Errorf("--quiet dropped the answer as well:\n%s", out)
	}
}

func TestADirectoryWithNoGoFilesIsExitOne(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "README.md", "nothing here is Go")
	code, out, errOut := drive(t, dir)
	if code != 1 {
		t.Fatalf("code %d, out %q", code, out)
	}
	if !strings.Contains(errOut, "nothing was read") {
		t.Errorf("it did not say that nothing was read: %q", errOut)
	}
}

func TestADirectoryThatIsNotThereIsNamedRatherThanIgnored(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	code, out, _ := drive(t, missing)
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out, "absent") || !strings.Contains(out, "not read:") {
		t.Errorf("a directory that is not there was passed over in silence:\n%s", out)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"--nope", "."}} {
		if code, _, _ := drive(t, args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}

func TestSeveralDirectoriesAreReadAndCountedTogether(t *testing.T) {
	a, b := tree(t), tree(t)
	_, out, _ := drive(t, a, b)
	if !strings.Contains(out, "2 director(ies)") || !strings.Contains(out, "4 package(s)") {
		t.Errorf("two repositories did not add up:\n%s", out)
	}
}
