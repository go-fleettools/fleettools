package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// keepPats is the live-address exemption every classify() call carries, so the
// default test classifier matches how the real sweep is run.
var keepPats = []string{"ghcr.io/go-pkgx"}

func classify(line string, codeVars ...string) []finding {
	vars := map[string]bool{}
	for _, v := range codeVars {
		vars[v] = true
	}
	c := newClassifier("go-pkgx", "go-zpkg", []string{"pkgxdev", "pkgx.sh"}, keepPats, vars)
	return c.classifyLine("README.md", 1, line)
}

// TestACitationIsNotDrift is the property the whole command exists for.
//
// `pkgxdev/pantry` contains the stem, so a check that asked "does this line
// mention pkgx" would propose rewriting the citations — including the ones
// that exist to say what this project deliberately does NOT copy.
func TestACitationIsNotDrift(t *testing.T) {
	got := classify("`PantryBase` is still `pkgxdev/pantry` compiled in, which is the recipe we may not copy.")
	if len(got) != 1 {
		t.Fatalf("%d finding(s), want 1: %+v", len(got), got)
	}
	if got[0].Verdict != citation {
		t.Errorf("verdict %s, want CITATION: %+v", got[0].Verdict, got[0])
	}
	if got[0].Text != "pkgxdev" {
		t.Errorf("matched %q, want pkgxdev", got[0].Text)
	}
}

// TestUpstreamIsMatchedBeforeTheBareName pins the ORDER, which is the rule.
//
// Without it the stem inside "pkgxdev" is also a bare-name match, and the same
// seven characters are reported twice with opposite verdicts.
func TestUpstreamIsMatchedBeforeTheBareName(t *testing.T) {
	for _, line := range []string{
		"see pkgxdev/pantry",
		"https://pkgx.sh/ is upstream",
		"pkgxdev and pkgx.sh both",
	} {
		for _, f := range classify(line) {
			if f.Verdict != citation {
				t.Errorf("%q: a %s finding %+v — upstream must cover its own span", line, f.Verdict, f)
			}
		}
	}
}

// TestAnOrgPathIsStaleAndCarriesItsReplacement: the one class that can be
// rewritten mechanically is also the one that silently keeps working, because
// GitHub redirects a transferred repository's old URL.
func TestAnOrgPathIsStaleAndCarriesItsReplacement(t *testing.T) {
	got := classify("built from github.com/go-pkgx/bk/cmd/bk@main")
	if len(got) != 1 {
		t.Fatalf("%d finding(s), want 1: %+v", len(got), got)
	}
	if got[0].Verdict != stale {
		t.Errorf("verdict %s, want STALE", got[0].Verdict)
	}
	if got[0].Suggest != "github.com/go-zpkg/bk" {
		t.Errorf("suggests %q, want github.com/go-zpkg/bk", got[0].Suggest)
	}
}

// TestTheCodeDecidesAboutAnEnvironmentVariable.
//
// ⛔ A variable is not renamed on sight. `BREWKIT_TARGET` is what zbk reads, so
// a document naming it is CORRECT and one naming `ZBK_TARGET` would be the
// drift. Getting this backwards would have had the sweep "fix" the docs into
// disagreeing with the code — which is the opposite of the job.
func TestTheCodeDecidesAboutAnEnvironmentVariable(t *testing.T) {
	got := classify("honours `PKGX_DIR` and `PKGX_TARGET`", "PKGX_TARGET")
	if len(got) != 2 {
		t.Fatalf("%d finding(s), want 2: %+v", len(got), got)
	}
	byText := map[string]finding{}
	for _, f := range got {
		byText[strings.Trim(f.Text, "`$")] = f
	}
	if v := byText["PKGX_DIR"].Verdict; v != stale {
		t.Errorf("PKGX_DIR is %s, want STALE — nothing reads it", v)
	}
	if v := byText["PKGX_TARGET"].Verdict; v != citation {
		t.Errorf("PKGX_TARGET is %s, want CITATION — the code still reads it", v)
	}
}

// TestALiveAddressCarryingTheOldNameIsKept is the regression for a rewrite that
// would have been DESTRUCTIVE.
//
// ⛔⛔ Every repository followed the `go-pkgx` → `go-zpkg` transfer; GitHub's
// container packages are org-scoped and did NOT. Every bottle the project has
// published still lives at `ghcr.io/go-pkgx/packages`. Renaming
// `bottle.DistBase` on the assumption that packages follow a transfer is what
// shipped an EMPTY registry in v0.64.0 through v0.68.0 — so an address can
// carry the old name and be the live one, and nothing in its spelling says
// which.
//
// It is reported as a CITATION with its own kind rather than dropped: an
// exemption nobody can see is how the next person re-proposes the same rewrite.
func TestALiveAddressCarryingTheOldNameIsKept(t *testing.T) {
	for _, line := range []string{
		`ARG DIST=oci://ghcr.io/go-pkgx/packages`,
		`  ZPKG_DIST: oci://ghcr.io/go-pkgx/packages`,
		`installed from <code>oci://ghcr.io/go-pkgx/packages</code>`,
	} {
		got := classify(line)
		if len(got) == 0 {
			t.Fatalf("%q: no finding at all", line)
		}
		for _, f := range got {
			if f.Verdict != citation {
				t.Errorf("%q: %s %+v — rewriting this empties the registry", line, f.Verdict, f)
			}
		}
		if !strings.Contains(got[0].Kind, "--keep") {
			t.Errorf("%q: the exemption is not visible in the kind: %q", line, got[0].Kind)
		}
	}
}

// TestKeepDoesNotSwallowAStaleAddressBesideIt: the exemption is a substring
// match, so it must cover only its own span.
func TestKeepDoesNotSwallowAStaleAddressBesideIt(t *testing.T) {
	line := `pull from oci://ghcr.io/go-pkgx/packages, source at github.com/go-pkgx/zbk`
	var sawKeep, sawStale bool
	for _, f := range classify(line) {
		if f.Verdict == citation && strings.Contains(f.Kind, "--keep") {
			sawKeep = true
		}
		if f.Verdict == stale && f.Suggest == "github.com/go-zpkg/zbk" {
			sawStale = true
		}
	}
	if !sawKeep || !sawStale {
		t.Errorf("keep=%v stale=%v — the live registry and the dead source link must be told apart on one line: %+v",
			sawKeep, sawStale, classify(line))
	}
}

// TestAnAddressIsStaleWhateverTheSentenceSays.
//
// ⛔ The first real run added this, and the second corrected it. A host, a forge
// shorthand, a landing host or a repository path must be updated whatever the
// surrounding prose claims — GitHub redirects a transferred repository, so a
// stale link keeps working and stays invisible, and a retired landing host does
// not.
func TestAnAddressIsStaleWhateverTheSentenceSays(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{`<a href="https://github.com/go-pkgx">the org</a>`, "github.com/go-zpkg"},
		{`the landing tree at <https://go-pkgx.github.io/packages/>`, "go-zpkg.github.io/packages"},
		// Renovate's `github>org/repo` shorthand carries no host, so the
		// suggestion carries none either.
		{`"extends": ["github>go-pkgx/.github"]`, "github>go-zpkg/.github"},
		{`built from go-pkgx/bk at main`, "go-zpkg/bk"},
	} {
		var sawStale bool
		for _, f := range classify(tc.line) {
			if f.Verdict == stale && strings.Contains(f.Suggest, tc.want) {
				sawStale = true
			}
		}
		if !sawStale {
			t.Errorf("%q: no STALE finding suggesting %q: %+v", tc.line, tc.want, classify(tc.line))
		}
	}
}

// TestTheNameInProseIsNotRewritten is the regression for two confident wrong
// answers.
//
// ⛔⛔ Making the repository suffix optional decided 24 more findings and put
// TWO of zbk's own README sentences under STALE **with a suggested
// replacement** — sentences that are ABOUT the rename and whose meaning the
// replacement destroys. A net gain had hidden two examples that went red
// (`a-net-gain-hides-an-example-that-went-red`), and the fix is the distinction
// between an address and a name in prose.
func TestTheNameInProseIsNotRewritten(t *testing.T) {
	for _, line := range []string{
		"every repository went from `go-pkgx` to `go-zpkg` and every bottle stayed",
		"Nothing in go-pkgx carries a build identity — it predates them.",
		"<h1>go-pkgx registry viewer</h1>",
	} {
		for _, f := range classify(line) {
			if f.Verdict == stale {
				t.Errorf("%q: called an address (%+v) — prose is a question, not a rewrite", line, f)
			}
		}
	}
}

// TestTheNewNameIsNotTheOldOne, which is not as obvious as it sounds.
//
// ⛔⛔ `pkgx` became `zpkgx`: the new name CONTAINS the old stem. Without a word
// boundary the check fires on every mention of the current tool, and that was
// most of the first sweep's UNSURE findings — 137 in the zpkgx repository, 124
// in zbk's README, 38 in zpkgm, nearly all of them lines that are already
// right. A report whose bulk is the thing it is checking FOR is one nobody
// reads.
func TestTheNewNameIsNotTheOldOne(t *testing.T) {
	for _, line := range []string{
		"`zpkgm` **installs**, `zpkgx` **runs** without installing",
		"run `zpkgx node@22`, or `zpkgx +git +bash -- …`",
		"curl …/install.sh | sh -s -- zpkgx v0.39.0",
		"the bottle protocol with `zpkgm` and `zpkgx`",
	} {
		if got := classify(line); len(got) != 0 {
			t.Errorf("%q: %d finding(s) about a line that names only the CURRENT tools: %+v",
				line, len(got), got)
		}
	}
}

// TestABareNameIsUnsureAndCarriesItsSentence.
//
// The honest verdict. "pkgx" alone is our old command or their live project
// depending on the sentence, so the sentence is printed — which is what makes
// deciding cheap instead of requiring the file to be opened.
func TestABareNameIsUnsureAndCarriesItsSentence(t *testing.T) {
	got := classify("Run `pkgx +node` to get a shell with node on PATH.")
	if len(got) != 1 {
		t.Fatalf("%d finding(s), want 1: %+v", len(got), got)
	}
	if got[0].Verdict != unsure {
		t.Errorf("verdict %s, want UNSURE", got[0].Verdict)
	}
	if !strings.Contains(got[0].Text, "+node") {
		t.Errorf("the finding does not carry enough of the sentence to decide by: %q", got[0].Text)
	}
}

// TestOnlyDocumentationIsRead: source files are modrename's business, and .git
// holds every old name forever.
func TestOnlyDocumentationIsRead(t *testing.T) {
	root := tree(t, map[string]string{
		"README.md":      "go-pkgx/zbk",
		"main.go":        "// go-pkgx/zbk",
		".git/config":    "url = github.com/go-pkgx/zbk",
		"docs/page.html": "<p>go-pkgx/zbk</p>",
	})
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil, nil)
	found, files, err := scanTree(root, c)
	if err != nil {
		t.Fatal(err)
	}
	if files != 2 {
		t.Errorf("%d doc file(s) read, want 2 (README.md and docs/page.html)", files)
	}
	for _, f := range found {
		if strings.HasPrefix(f.File, ".git") || strings.HasSuffix(f.File, ".go") {
			t.Errorf("read %s, which is not documentation", f.File)
		}
	}
}

// TestATreeWithNoDocumentationSaysSo, because zero findings from an unread tree
// looks exactly like zero findings from a clean one — which happened: a clone
// that failed left an empty directory and the first sweep reported it clean.
func TestATreeWithNoDocumentationSaysSo(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(&out, &errb, "go-pkgx", "go-zpkg", nil, nil, nil, false, true,
		[]string{t.TempDir()})
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(out.String(), "0 doc file(s)") {
		t.Errorf("the file count is not in the output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "a tree that was not read reports the same") {
		t.Errorf("nothing warns that a zero may be an unread tree:\n%s", out.String())
	}
}

// TestQuietPrintsCountsAndStillFails: --quiet changes what is printed, never
// the verdict.
func TestQuietPrintsCountsAndStillFails(t *testing.T) {
	root := tree(t, map[string]string{"README.md": "github.com/go-pkgx/zbk\n"})
	var out, errb bytes.Buffer
	if code := run(&out, &errb, "go-pkgx", "go-zpkg", nil, nil, nil, false, true, []string{root}); code != 1 {
		t.Fatalf("exit %d with --quiet, want 1", code)
	}
	if strings.Contains(out.String(), "STALE  ") {
		t.Errorf("--quiet printed a finding:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "1 STALE") {
		t.Errorf("--quiet did not print the count:\n%s", out.String())
	}
}

// TestCitationsAreCountedButNotListedUnlessAsked keeps a report about drift
// from being 90% things that are fine.
func TestCitationsAreCountedButNotListedUnlessAsked(t *testing.T) {
	root := tree(t, map[string]string{"README.md": "pkgxdev/pantry\n"})
	var out, errb bytes.Buffer
	if code := run(&out, &errb, "go-pkgx", "go-zpkg", []string{"pkgxdev"}, nil, nil, false, false, []string{root}); code != 0 {
		t.Fatalf("exit %d, want 0 — a citation is not drift (%s)", code, out.String())
	}
	if strings.Contains(out.String(), "CITATION pkgxdev") {
		t.Errorf("a citation was listed without --show-citations:\n%s", out.String())
	}
	out.Reset()
	run(&out, &errb, "go-pkgx", "go-zpkg", []string{"pkgxdev"}, nil, nil, true, false, []string{root})
	if !strings.Contains(out.String(), "CITATION") {
		t.Errorf("--show-citations listed nothing:\n%s", out.String())
	}
}

// TestEveryRequiredFlagIsNamed.
func TestEveryRequiredFlagIsNamed(t *testing.T) {
	for _, tc := range []struct {
		old, new string
		dirs     []string
		want     string
	}{
		{"", "go-zpkg", []string{"."}, "--old"},
		{"go-pkgx", "", []string{"."}, "--new"},
		{"go-pkgx", "go-zpkg", nil, "at least one directory"},
	} {
		var out, errb bytes.Buffer
		if code := run(&out, &errb, tc.old, tc.new, nil, nil, nil, false, false, tc.dirs); code != 2 {
			t.Errorf("%+v: exit %d, want 2", tc, code)
		}
		if !strings.Contains(errb.String(), tc.want) {
			t.Errorf("%+v: does not name %s: %s", tc, tc.want, errb.String())
		}
	}
}

// TestAnUnreadableTreeIsAnError, not an empty result.
func TestAnUnreadableTreeIsAnError(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(&out, &errb, "go-pkgx", "go-zpkg", nil, nil, nil, false, false,
		[]string{filepath.Join(t.TempDir(), "no-such-tree")})
	if code != 2 {
		t.Errorf("exit %d, want 2 (%s)", code, errb.String())
	}
}

// TestAnUnreadableFileIsAnError: a document nobody could read must not be
// counted among the clean ones.
func TestAnUnreadableFileIsAnError(t *testing.T) {
	root := tree(t, map[string]string{"README.md": "x"})
	p := filepath.Join(root, "README.md")
	if err := os.Chmod(p, 0); err != nil {
		t.Skipf("cannot make a file unreadable here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	if _, err := os.ReadFile(p); err == nil {
		t.Skip("running as a user that can read a 0000 file")
	}
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil, nil)
	if _, _, err := scanTree(root, c); err == nil {
		t.Error("an unreadable document passed as a clean read")
	}
}

// TestTheRootCommandAnswersHelpWithZero, which every command here must.
func TestTheRootCommandAnswersHelpWithZero(t *testing.T) {
	c := RootCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"--help"})
	if err := c.Execute(); err != nil {
		t.Fatalf("--help returned %v", err)
	}
	for _, want := range []string{"--old", "--new", "--upstream", "--code-var", "--quiet"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--help does not mention %s", want)
		}
	}
}

// TestItRunsThroughTheRootCommand, so the wiring is covered and not just the
// function underneath it.
func TestItRunsThroughTheRootCommand(t *testing.T) {
	root := tree(t, map[string]string{"README.md": "github.com/go-pkgx/zbk\n"})
	c := RootCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"--old", "go-pkgx", "--new", "go-zpkg", "-q", root})
	err := c.Execute()
	if err == nil {
		t.Fatal("a stale reference did not make the command fail")
	}
	if !strings.Contains(out.String(), "1 STALE") {
		t.Errorf("output does not count the finding:\n%s", out.String())
	}
}
