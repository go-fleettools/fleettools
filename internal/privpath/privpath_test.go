package privpath

import (
	"strings"
	"testing"
)

// ⛔ THE LEAK THIS PACKAGE WAS WRITTEN FOR, in shape. Three issue bodies carried
// a line like this; editing them afterwards does not remove it from GitHub's
// "edited" history.
func TestScanCatchesTheLeakItWasWrittenFor(t *testing.T) {
	const path = "/Users/alicia/Documents/fairshare-b300/fairshare-b300.tex"
	body := "## The measurement\n\n`" + path + "`, 36 pages under `tectonic`.\n"

	got := Scan(body)
	if len(got) != 1 {
		t.Fatalf("Scan found %d finding(s), want 1: %v", len(got), got)
	}
	if got[0].Line != 3 {
		t.Errorf("finding on line %d, want 3", got[0].Line)
	}
	// ⛔ The WHOLE path, not just the home prefix. The rule names three things:
	// "pas de leak de nom ni de chemin/nom de fichier". Matching `/Users/alicia`
	// alone would leave the project and the document published.
	if got[0].Match != path {
		t.Errorf("match = %q, want the whole path %q", got[0].Match, path)
	}
	if got[0].Kind != "home path" {
		t.Errorf("kind = %q", got[0].Kind)
	}
	if !strings.Contains(got[0].Why, "measurement is the content") {
		t.Errorf("why = %q, want the reason the path is not the content", got[0].Why)
	}

	// All three names are learned, the generic directory is not.
	names := Names(body)
	for _, want := range []string{"alicia", "fairshare-b300", "fairshare-b300.tex"} {
		if !contains(names, want) {
			t.Errorf("Names did not learn %q: %v", want, names)
		}
	}
	if contains(names, "Documents") {
		t.Errorf("Names learned a generic directory: %v", names)
	}
}

// ⛔ A refusal is printed where the body was going to go, and that is sometimes
// a public CI log. A gate against publishing a path must not publish the path in
// the course of refusing it.
func TestFindingDoesNotReprintWhatItFound(t *testing.T) {
	f := Scan("see /Users/alicia/thesis/draft.tex")[0]
	if s := f.String(); strings.Contains(s, "alicia") || strings.Contains(s, "draft.tex") {
		t.Errorf("Finding.String() published the path it refused: %q", s)
	}
	if s := f.String(); !strings.Contains(s, "line 1") || !strings.Contains(s, "col 5") {
		t.Errorf("Finding.String() = %q, want the line and column that locate it", s)
	}
	if s := f.Reveal(); !strings.Contains(s, "/Users/alicia/thesis/draft.tex") {
		t.Errorf("Finding.Reveal() = %q, want the match for a local run", s)
	}
}

// ⛔ A home prefix WITHOUT an account is a convention, not a disclosure, and
// refusing it would be the false positive that teaches the reader to waive by
// reflex. go-zpkg/packages#128 legitimately needed the sentence "`/Users/runner`
// is the GitHub runner's home directory".
func TestScanLeavesGenericAndPrefixOnlyPathsAlone(t *testing.T) {
	for _, clean := range []string{
		"`/Users/runner/.pkgx` is the GitHub runner's home directory",
		"the loader tried /home/runner/lib and gave up",
		"set it under /Users/ or /home/ as your platform wants",
		"C:\\Users\\USERNAME\\AppData is the documented location",
		"install to /opt/pkgx, never to a home directory",
		"/home/ubuntu is the image's default account",
	} {
		if got := Scan(clean); len(got) != 0 {
			t.Errorf("Scan(%q) refused a legitimate sentence: %v", clean, got)
		}
	}
}

func TestScanAcrossPlatforms(t *testing.T) {
	for text, want := range map[string]string{
		"see /Users/bob/notes.md":        "/Users/bob/notes.md",
		"see /home/bob/notes.md":         "/home/bob/notes.md",
		`see C:\Users\bob\notes.md`:      `C:\Users\bob\notes.md`,
		`see \\FILESERVER\bob$\notes.md`: `\\FILESERVER\bob$\notes.md`,
	} {
		got := Scan(text)
		if len(got) == 0 {
			t.Errorf("Scan(%q) found nothing", text)
			continue
		}
		if got[0].Match != want {
			t.Errorf("Scan(%q) matched %q, want %q", text, got[0].Match, want)
		}
	}
}

// ⛔ The bare name counts too. A body that writes the path once and the account
// five times has disclosed the account six times — which is how the leak read: a
// path in the measurement section and the same name in a session label below.
func TestScanFindsTheBareAccountOnceItIsKnown(t *testing.T) {
	body := "measured at /Users/carol/x.tex\n" +
		"reported by carol's session\n" +
		"and carol again here\n"
	got := Scan(body)
	if len(got) != 3 {
		t.Fatalf("found %d, want 3 (one path + two bare names): %v", len(got), got)
	}
	bare := 0
	for _, f := range got {
		if f.Kind == "account name" {
			bare++
		}
	}
	if bare != 2 {
		t.Errorf("found %d bare account names, want 2: %v", bare, got)
	}
	// ⛔ The path's own occurrence is not counted twice: the account sits inside
	// it, so a naive second pass reports one disclosure as two.
	for _, f := range got {
		if f.Kind == "account name" && f.Line == 1 {
			t.Errorf("the account inside the path was counted again: %v", f)
		}
	}
}

// ⛔ A SHORT account name is still the person's name. The length floor exists to
// keep the bare hunt off English words, and it applies to directories, not to
// the account — "dave" is four letters and every occurrence of it discloses the
// same thing as the path did.
func TestShortAccountIsStillHunted(t *testing.T) {
	got := Scan("/home/dave/a\ndave ran it\n")
	bare := 0
	for _, f := range got {
		if f.Kind == "account name" {
			bare++
		}
	}
	if bare != 1 {
		t.Errorf("a 4-letter account was not hunted bare: %v", got)
	}
	// While a short directory is NOT hunted bare: "src" in a sentence is prose.
	// Only the path itself is reported; the "src" on line 2 is left alone.
	for _, f := range Scan("/home/dave/src/a\nthe src tree\n") {
		if f.Kind != "home path" {
			t.Errorf("a short directory was hunted bare: %v", f)
		}
	}
}

// ⛔ A name is only looked for once a path has shown it. Without that rule the
// scanner would have to guess which words are people, and a gate that guesses is
// one that fires on prose — and gets switched off.
func TestScanDoesNotGuessAtNames(t *testing.T) {
	if got := Scan("alicia and bob reviewed it, and carol measured it"); len(got) != 0 {
		t.Errorf("Scan invented findings from prose: %v", got)
	}
	if got := Scan("fairshare-b300.tex is the document"); len(got) != 0 {
		t.Errorf("Scan invented a document name from prose: %v", got)
	}
}

// ⛔ The fleet sweep needs ScanKnown: a body can name the account with NO path
// in it at all, and a scanner that only infers names from paths calls such a
// body clean. That is most of what a sweep has to read.
func TestScanKnownFindsWhatHasNoPathToLearnFrom(t *testing.T) {
	body := "filed from localhost-55 by alicia, on fairshare-b300.tex\n"
	if got := Scan(body); len(got) != 0 {
		t.Fatalf("Scan should have nothing to learn from: %v", got)
	}
	got := ScanKnown(body, []string{"alicia", "fairshare-b300.tex"})
	if len(got) != 2 {
		t.Errorf("ScanKnown found %d, want 2: %v", len(got), got)
	}
}

// ⛔ A refusal must not misname what it holds. The first version asked the text
// it was scanning which of its names were accounts — on redacted text, where
// every path is already gone, that answered "none", and it labelled the account
// it had just caught a "document name". A label in a report is not a diagnosis
// unless something made it true.
func TestKindsAreHonest(t *testing.T) {
	got := ScanKnown("`/Users/alicia/p/report.tex`\nby alicia on report.tex\nand alicia again\n", nil)
	kinds := map[string]string{}
	for _, f := range got {
		if f.Kind != "home path" {
			kinds[f.Match] = f.Kind
		}
	}
	if kinds["alicia"] != "account name" {
		t.Errorf("the account was labelled %q", kinds["alicia"])
	}
	if kinds["report.tex"] != "document name" {
		t.Errorf("the document was labelled %q", kinds["report.tex"])
	}
	// Supplied from outside, the scanner does not know which it is and says so
	// rather than guessing.
	for _, f := range ScanKnown("by alicia\n", []string{"alicia"}) {
		if f.Kind != "name" {
			t.Errorf("a supplied name was labelled %q, want the honest %q", f.Kind, "name")
		}
	}
}

// ⛔⛔ THE THREE FALSE POSITIVES A REAL SWEEP MEASURED, 2026-11-09. The first
// run over 60 repositories reported 509 findings; the sample showed the tool
// measuring itself. Each shape below is one of them, and each was costing more
// than it caught — a gate that fires on a package manager's prefix and on TeX is
// one its author switches off, which is the only way a gate truly fails.
func TestTheFalsePositivesASweepMeasured(t *testing.T) {
	// (1) A package manager's own prefix, read as a person called ".zpkg":
	// `/home/.zpkg/<domain>/<pkg>/v<version>/bin/<tool>`. One body produced 38
	// findings this way, every package name along the path hunted as a
	// document. No account name begins with a dot.
	for _, tool := range []string{
		"/home/.zpkg/gnu.org/libtool/v2.6.2/bin/libtoolize",
		"/home/.pkgx/gnu.org/libtool/v2.6.2/bin/libtoolize", // the older spelling its issue bodies still carry
		"/Users/.cache/go-build/ab/abcdef",
		"/home/.local/share/applications/x.desktop",
	} {
		body := tool + " failed, and libtoolize is the wrapper libtool installs\n"
		if got := Scan(body); len(got) != 0 {
			t.Errorf("a tool directory was read as a person: %q → %v", tool, got)
		}
	}

	// (2) TeX. `\\` is a line break in LaTeX and an escaped backslash in every
	// Go string literal, so `\\ + name + \ + name` matched inside ordinary TeX
	// and captured a one-letter "share". In this fleet's TeX engine tracker that
	// is everywhere.
	for _, tex := range []string{
		`a line\\hbox\scriptstyle`,
		`\\par\noindent`,
		`row one\\s`,
		`\\ \hline`,
	} {
		if got := Scan(tex); len(got) != 0 {
			t.Errorf("TeX was read as a Windows share: %q → %v", tex, got)
		}
	}
	// … while a real share is still caught.
	if got := Scan(`\\FILESERVER\alicia$\thesis\main.tex`); len(got) == 0 {
		t.Error("a real UNC path is no longer found")
	}

	// (2b) Shared, not personal: measured in go-tex/engine#715. And the
	// placeholders a package manager's own issues use — the test is whether a
	// name points at a PERSON, not whether some machine has that account.
	for _, clean := range []string{
		"built under /Users/Shared/axc/work by the agent",
		"the key is at /home/user/.ssh/id_rsa",
		"/home/admin/data.db is the default",
		"/home/linuxbrew/.linuxbrew/bin is on PATH",
	} {
		if got := Scan(clean); len(got) != 0 {
			t.Errorf("a path naming nobody was refused: %q → %v", clean, got)
		}
	}

	// (2c) KEYBOARD KEYS. Measured in go-widgets/toolkit#495 and #421: a
	// case-insensitive pattern read `Home/End/Space` as the home directory of a
	// person called End, and then hunted "End" through the body.
	for _, keys := range []string{
		"the widget handles Home/End/Space and the arrows",
		"Home/End move the caret",
		"bind Page Up, Home/End/Tab",
	} {
		if got := Scan(keys); len(got) != 0 {
			t.Errorf("a list of keyboard keys was read as a home directory: %q → %v", keys, got)
		}
	}
	// … while the real spellings still match.
	for _, real := range []string{
		"/Users/alicia/x", "/home/alicia/x", `C:\Users\alicia\x`, `c:\users\alicia\x`,
	} {
		if got := Scan(real); len(got) == 0 {
			t.Errorf("a real home path stopped matching: %q", real)
		}
	}

	// (3) A one- or two-character name is never hunted bare, whatever taught it.
	// One such name produced 276 of the sweep's 509 findings.
	if got := Scan("/home/ab/x\nab initio, absolutely, a b\n"); len(got) != 1 {
		t.Errorf("a two-letter account was hunted through prose: %v", got)
	}
	if got := ScanKnown("s is a letter, so is s, and s", []string{"s"}); len(got) != 0 {
		t.Errorf("a one-letter name supplied by a caller was hunted: %v", got)
	}
}

func TestRedactReplacesAndCounts(t *testing.T) {
	in := "measured on `/Users/dennis/thesis/main.tex` and `/home/dennis/out.pdf`"
	out, n := Redact(in)
	if n < 2 {
		t.Errorf("Redact reported %d replacements, want at least the 2 paths", n)
	}
	if strings.Contains(out, "dennis") {
		t.Errorf("the account survived redaction: %q", out)
	}
	// ⛔ Not an empty string: the sentence must still read, or the next person
	// restores the path to make it legible again.
	if !strings.Contains(out, Placeholder) {
		t.Errorf("redaction left no placeholder: %q", out)
	}
	if !strings.HasPrefix(out, "measured on") {
		t.Errorf("redaction broke the sentence: %q", out)
	}
	keep := "`/Users/runner/.pkgx` is the runner's home"
	if out, n := Redact(keep); n != 0 || out != keep {
		t.Errorf("Redact touched a generic account: %q (%d)", out, n)
	}
	if out, n := Redact("nothing to see"); n != 0 || out != "nothing to see" {
		t.Errorf("Redact changed clean text: %q (%d)", out, n)
	}
}

// ⛔ The leak's own path held both `fairshare-b300` and `fairshare-b300.tex`.
// Replacing the shorter first leaves `<name elided>.tex` — the extension says
// what kind of document it was and the stem is already gone, so this is about
// not producing nonsense as much as disclosure.
func TestRedactReplacesTheLongerNameFirst(t *testing.T) {
	in := "`/Users/alicia/Documents/fairshare-b300/fairshare-b300.tex`\n" +
		"the project fairshare-b300 builds fairshare-b300.tex\n"
	out, _ := Redact(in)
	if strings.Contains(out, "fairshare-b300") {
		t.Errorf("a name survived: %q", out)
	}
	if strings.Contains(out, NamePlaceholder+".tex") {
		t.Errorf("the shorter name was replaced first, leaving an extension: %q", out)
	}
}

// ⛔⛔ THE CONTRACT BETWEEN THE TWO HALVES: whatever Redact produces must hold
// none of the names the text disclosed BEFORE it. Without this the fix and the
// gate disagree — `--fix` rewrites the body, the gate refuses the result over a
// name the fix did not touch, and the author who has just run the fix concludes
// the gate is broken and waives it. That is the failure a gate cannot afford:
// the one that teaches people to switch it off.
//
// ⛔ And it is checked against the names from BEFORE, never by re-scanning the
// output. Measured on 2026-11-09: with the bare-name replacement deliberately
// disabled, a plain `Scan(out)` assertion stayed GREEN on every body here,
// because Scan learns a name from a path and the path is what the fix removed.
func TestRedactLeavesNothingToFind(t *testing.T) {
	bodies := []string{
		"measured at /Users/carol/x.tex\nreported by carol's session\nand carol again\n",
		"`/Users/alicia/Documents/fairshare-b300/fairshare-b300.tex`, 36 pages\nsession localhost-55 (alicia)\n",
		"windows: `C:\\Users\\bob\\out.pdf` and the share `\\\\HOST\\bob$\\out.pdf`, bob's\n",
		"/home/dennis/a and /Users/dennis/b, dennis\n",
		"nothing here at all, and /Users/runner/.pkgx is fine\n",
	}
	for _, body := range bodies {
		was := Names(body)
		out, n := Redact(body)
		for _, name := range was {
			if strings.Contains(out, name) {
				t.Errorf("Redact(%q) left %q behind:\n  → %q", body, name, out)
			}
		}
		if rest := ScanKnown(out, was); len(rest) != 0 {
			t.Errorf("Redact(%q) left %d finding(s) for ScanKnown: %v\n  → %q",
				body, len(rest), rest, out)
		}
		if want := len(Scan(body)) > 0; want != (n > 0) {
			t.Errorf("Redact(%q) changed %d but Scan found %d", body, n, len(Scan(body)))
		}
		// A fixpoint: redacting twice changes nothing more, or a pre-commit hook
		// would rewrite the file on every run.
		if again, m := Redact(out); m != 0 || again != out {
			t.Errorf("Redact is not a fixpoint on %q: %d more, %q", out, m, again)
		}
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
