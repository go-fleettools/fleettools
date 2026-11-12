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
	c := newClassifier("go-pkgx", "go-zpkg", []string{"pkgxdev", "pkgx.sh"}, keepPats, nil, vars)
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
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil, nil, nil)
	found, files, err := scanTree(root, c, newAllowList(nil), extSet(nil))
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
	code := run(&out, &errb, opts{old: "go-pkgx", new: "go-zpkg", quiet: true},
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
	if code := run(&out, &errb, opts{old: "go-pkgx", new: "go-zpkg", quiet: true}, []string{root}); code != 1 {
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
	base := opts{old: "go-pkgx", new: "go-zpkg", upstream: []string{"pkgxdev"}}
	if code := run(&out, &errb, base, []string{root}); code != 0 {
		t.Fatalf("exit %d, want 0 — a citation is not drift (%s)", code, out.String())
	}
	if strings.Contains(out.String(), "CITATION pkgxdev") {
		t.Errorf("a citation was listed without --show-citations:\n%s", out.String())
	}
	out.Reset()
	shown := base
	shown.showCitations = true
	run(&out, &errb, shown, []string{root})
	if !strings.Contains(out.String(), "CITATION") {
		t.Errorf("--show-citations listed nothing:\n%s", out.String())
	}
}

// retiredClassify is classify() with the fleet's other retired spellings
// declared, which is how the go-zpkg sweep is actually run.
func retiredClassify(line string) []finding {
	c := newClassifier("go-pkgx", "go-zpkg", []string{"pkgxdev", "pkgx.sh"}, keepPats,
		[]string{"bk=zbk", "pkgm=zpkgm", "pkgs=zpkgs", "pkge=zpkge"}, map[string]bool{})
	return c.classifyLine("README.md", 1, line)
}

// ⛔⛔ TestTheOtherRetiredSpellingsAreSeen is the hole this flag closes.
//
// The 2026-11-09 sweep checked the stem `pkgx` alone and was declared finished.
// The next day `zbk/docs/from-scratch-toolchain.md` still said `bk` 72 times
// and `zpkgx/README.md` still printed a `pkgx <pkg>` usage block: four of the
// five renamed commands were invisible to the check that had passed.
func TestTheOtherRetiredSpellingsAreSeen(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"run `bk build` to make a bottle", "zbk"},
		{"`pkgm install` resolves the closure", "zpkgm"},
		{"ask `pkgs ls` what exists", "zpkgs"},
		{"the generated `pkge` function", "zpkge"},
	} {
		got := retiredClassify(tc.line)
		if len(got) != 1 {
			t.Errorf("%q: %d finding(s), want 1: %+v", tc.line, len(got), got)
			continue
		}
		if got[0].Verdict != unsure {
			t.Errorf("%q: verdict %s, want UNSURE", tc.line, got[0].Verdict)
		}
		if !strings.Contains(got[0].Kind, tc.want) {
			t.Errorf("%q: kind %q does not name the replacement %s", tc.line, got[0].Kind, tc.want)
		}
	}
}

// ⛔⛔ TestARetiredNameDoesNotMatchTheNameThatReplacedIt. Every retired spelling
// in this rename is a PREFIX of its replacement — bk/zbk, pkgs/zpkgs — so
// without a left word boundary the gate fires on every line that is already
// correct, and its report is nothing but the thing it is checking FOR.
func TestARetiredNameDoesNotMatchTheNameThatReplacedIt(t *testing.T) {
	for _, line := range []string{
		"run `zbk build` to make a bottle",
		"`zpkgm install` resolves the closure",
		"ask `zpkgs ls` what exists",
		"the generated `zpkge` function",
		"`zpkgx node@22 --version`",
	} {
		if got := retiredClassify(line); len(got) != 0 {
			t.Errorf("%q: %d finding(s) on a correct line: %+v", line, len(got), got)
		}
	}
}

// TestAnEnvironmentVariableIsDecidedBeforeTheBareName: `$BK_CC` is ONE
// finding about a variable, not a bare `bk` with the rest of its name cut off.
//
// ⛔⛔ TestTheDoubledReplacementIsSeen is the hole ORDER opens, and the one
// every other rule here is blind to by construction.
//
// Measured 2026-11-11 renaming zbk: `Pkgx`→`Zpkgx` ran before `pkgx`→`zpkgx`,
// the second rule fired inside the first's output, and `ZpkgxBin` became
// `ZzpkgxBin`. It COMPILED — a consistently doubled identifier is a valid
// identifier — and every sweep called the file clean, correctly by its own
// rule: `Zzpkgx` contains `zpkgx`, the CORRECT name, and the retired `pkgx` is
// nowhere in it.
//
// ⛔ It states the STEM's replacement (`pkgx=zpkgx`), which retiredClassify
// deliberately does not: without it there is no replacement to double, and the
// stem's is never guessed (TestTheStemsReplacementIsNotGuessed). That is also
// how the fleet invokes this command, and the first version of this test did
// not — it reported the two `zpkgx` cases as clean and was right to.
func TestTheDoubledReplacementIsSeen(t *testing.T) {
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil,
		[]string{"pkgx=zpkgx", "bk=zbk"}, map[string]bool{})
	for _, tc := range []struct{ line, want string }{
		{"dirs = append(dirs, ZzpkgxBin)", "Zpkgx"},
		{"the zzpkgx binary", "zpkgx"},
		{"export ZZBK_TARGET=x86_64", "ZBK"},
		{"zzbk build", "zbk"},
	} {
		got := c.classifyLine("README.md", 1, tc.line)
		if len(got) != 1 {
			t.Errorf("%q: %d finding(s), want 1: %+v", tc.line, len(got), got)
			continue
		}
		if got[0].Verdict != doubled {
			t.Errorf("%q: verdict %s, want DOUBLED", tc.line, got[0].Verdict)
		}
		if got[0].Suggest != tc.want {
			t.Errorf("%q: suggests %q, want %q", tc.line, got[0].Suggest, tc.want)
		}
	}
}

// ⛔ THE CORRECT NAME MUST NOT BE DOUBLED-MATCHED. `zpkgx` is the replacement,
// not the replacement twice; a rule that fired on it would report every
// migrated line as damage — the failure in `a-sweep-that-measured-its-own-regex`,
// one layer further in.
func TestTheCorrectNameIsNotDoubled(t *testing.T) {
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil,
		[]string{"pkgx=zpkgx", "bk=zbk", "pkgm=zpkgm"}, map[string]bool{})
	for _, line := range []string{
		"dirs = append(dirs, ZpkgxBin)",
		"run `zpkgx node@22`",
		"export ZBK_TARGET=x86_64",
		"`zbk build` makes a bottle",
		"the zpkgm closure",
	} {
		for _, f := range c.classifyLine("README.md", 1, line) {
			if f.Verdict == doubled {
				t.Errorf("%q: reported as doubled: %+v", line, f)
			}
		}
	}
}

// ⛔ A REPLACEMENT THAT IS NOT ITS RETIRED SPELLING PLUS A PREFIX HAS NO
// DOUBLED FORM, and one is not invented: the prefix would have to be guessed,
// and a guessed finding is one nobody can act on.
func TestOnlyAPrefixedRenameHasADoubledForm(t *testing.T) {
	for _, tc := range []struct {
		was, now, want string
		ok             bool
	}{
		{"pkgx", "zpkgx", "zzpkgx", true},
		{"bk", "zbk", "zzbk", true},
		{"BK_", "ZBK_", "ZZBK_", true},
		{"pkgx", "", "", false},         // no replacement stated
		{"pkgx", "pkgx", "", false},     // unchanged
		{"foo", "bar", "", false},       // unrelated word
		{"foo", "fo", "", false},        // shorter
		{"", "zpkgx", "", false},        // no retired spelling
		{"pkgx", "zpkgxtra", "", false}, // the old name is not the TAIL
	} {
		got, ok := doubledForm(tc.was, tc.now)
		if ok != tc.ok || got != tc.want {
			t.Errorf("doubledForm(%q, %q) = %q, %v; want %q, %v",
				tc.was, tc.now, got, ok, tc.want, tc.ok)
		}
	}
}

// ⛔⛔ A NAME INSIDE BACKTICKS IS BEING QUOTED, NOT USED. Measured in review:
// run against its own directory this command reported 15 DOUBLED, every one a
// comment or fixture explaining the defect, seven inside code spans — a report
// whose bulk is the thing it checks FOR.
func TestAQuotedDoubledNameIsNotDrift(t *testing.T) {
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil, []string{"pkgx=zpkgx"}, map[string]bool{})
	quoted := c.classifyLine("README.md", 1, "a wrong order produces `ZzpkgxBin`, which compiles")
	for _, f := range quoted {
		if f.Verdict == doubled {
			t.Errorf("a quoted mention was reported: %+v", f)
		}
	}
	// ...and the same name unquoted on the same kind of line still is.
	bare := c.classifyLine("README.md", 2, "a wrong order produces ZzpkgxBin, which compiles")
	if n := countVerdict(bare, doubled); n != 1 {
		t.Errorf("unquoted: %d doubled finding(s), want 1", n)
	}
}

// ⛔⛔ A BACKTICK IN GO OPENS A RAW STRING LITERAL, AND THAT IS WHERE THIS BUG
// LIVES. Skipping code spans in SOURCE blinded the check in exactly the files
// --ext go exists to open: review measured a usage string, a path default and
// a struct tag, all holding the doubled name, reported as 0 DOUBLED, exit 0.
// The fix for a false positive had made a blind spot bigger than the false
// positive.
func TestABacktickInSourceIsNotAQuotation(t *testing.T) {
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil, []string{"pkgx=zpkgx"}, map[string]bool{})
	tick := "`"
	for _, line := range []string{
		"var usage = " + tick + "Usage: ZzpkgxBin [flags]" + tick,
		"const home = " + tick + "$HOME/.config/ZzpkgxBin" + tick,
		"Field string " + tick + `json:"ZzpkgxBin"` + tick,
	} {
		if n := countVerdict(c.classifyLine("main.go", 1, line), doubled); n != 1 {
			t.Errorf("%d doubled finding(s) in Go source, want 1: %s", n, line)
		}
	}
	// ⛔ The file type is what decides, and the control says so: the SAME
	// quoted identifier is a quotation in markup and an identifier in source.
	//
	// (These lines are deliberately not the ones above. Under the adjacency
	// rule a name inside a longer quoted PHRASE — `Usage: ZzpkgxBin [flags]` —
	// is reported even in markup, which is the stated trade for closing the
	// two ways backtick PAIRING hid a name. Asserting the old behaviour here
	// would have been changing a test to match the code.)
	quotedName := "see `ZzpkgxBin` for the shape"
	if n := countVerdict(c.classifyLine("README.md", 1, quotedName), doubled); n != 0 {
		t.Errorf("%d doubled finding(s) for a quoted name in markup, want 0", n)
	}
	if n := countVerdict(c.classifyLine("main.go", 1, quotedName), doubled); n != 1 {
		t.Errorf("%d doubled finding(s) for the same text in source, want 1", n)
	}
}

// ⛔⛔ PAIRING BACKTICKS ACROSS A LINE WAS THE WRONG MODEL, TWICE: by position
// (three backticks paired across the name) and then by parity (two apostrophe
// typos make an EVEN count and pair across it again). Adjacency asks a
// question about the NAME, so nothing between the backticks can fool it.
func TestABacktickElsewhereOnTheLineDoesNotHideAName(t *testing.T) {
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil, []string{"pkgx=zpkgx"}, map[string]bool{})
	tick := "`"
	for _, tc := range []struct {
		name, line string
		want       int
	}{
		{"three backticks", "don" + tick + "t use ZzpkgxBin yet, see " + tick + "--help" + tick, 1},
		{"two apostrophe typos", "don" + tick + "t use ZzpkgxBin, isn" + tick + "t it odd", 1},
		{"properly quoted", "see " + tick + "ZzpkgxBin" + tick + " for the shape", 0},
		{"double backticks", "see " + tick + tick + "ZzpkgxBin" + tick + tick + " there", 0},
		{"quoted phrase containing it", "run " + tick + "ZzpkgxBin [flags]" + tick + " now", 1},
		{"no backticks at all", "dirs = append(dirs, ZzpkgxBin)", 1},
	} {
		if n := countVerdict(c.classifyLine("README.md", 1, tc.line), doubled); n != tc.want {
			t.Errorf("%s: %d doubled finding(s), want %d", tc.name, n, tc.want)
		}
	}
}

// ⛔⛔ DE-DUPLICATION BY EXACT SPAN WAS TOO LITERAL. Two pairs can match the
// same NAME over spans differing by a byte, and a [2]int key sees two things.
func TestOverlappingPairsAreOneFinding(t *testing.T) {
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil,
		[]string{"pkgx=zpkgx", "pkg=zpkg"}, map[string]bool{})
	got := c.classifyLine("main.go", 1, "dirs = append(dirs, ZzpkgxBin)")
	if n := countVerdict(got, doubled); n != 1 {
		t.Fatalf("%d doubled finding(s) for one name: %+v", n, got)
	}
}

// ⛔⛔ AND "FIRST PAIR WINS" PICKED THE WRONG WINNER IN EITHER ORDER, because
// newClassifier puts the stem's pair at index 0. The pair that matches the
// name's capitals is the one that knows how it is spelled.
func TestTheWinningPairIsTheOneThatMatchesTheCase(t *testing.T) {
	for _, order := range [][]string{
		{"PkgX=ZPkgX", "pkgx=zpkgx"},
		{"pkgx=zpkgx", "PkgX=ZPkgX"},
	} {
		c := newClassifier("go-pkgx", "go-zpkg", nil, nil, order, map[string]bool{})
		got := c.classifyLine("main.go", 1, "dirs = append(dirs, ZZPkgXBin)")
		if n := countVerdict(got, doubled); n != 1 {
			t.Fatalf("%v: %d finding(s), want 1: %+v", order, n, got)
		}
		for _, f := range got {
			if f.Verdict == doubled && f.Suggest != "ZPkgX" {
				t.Errorf("%v: suggests %q, want ZPkgX", order, f.Suggest)
			}
		}
	}
}

// ⛔⛔ EVERY EXEMPTED VERDICT COUNTS, NOT JUST DOUBLED. The half-fix left the
// clean-tree line lying about the other four.
func TestAnyExemptedVerdictMakesTheTreeNotClean(t *testing.T) {
	byRoot := map[string][]finding{"zbk": {{
		File: "FILIATION.md", Line: 2, Verdict: citation, Was: stale,
		Kind: "the old name is this page's subject (--allow)", Text: "go-pkgx/zbk",
	}}}
	var b strings.Builder
	report(&b, byRoot, map[string]int{"zbk": 1}, false, true)
	out := b.String()
	if !strings.Contains(out, "NOT a clean tree") {
		t.Errorf("an exempted STALE did not mark the run unclean:\n%s", out)
	}
	if strings.Contains(out, "nothing stale and nothing unsure") {
		t.Errorf("it still claimed a clean tree:\n%s", out)
	}
}

// ⛔⛔ TWO PAIRS, ONE SPAN, TWO CONTRADICTORY REPAIRS. Review measured the
// fleet's real configuration — the pair set whose ORDER causes this defect —
// reporting one occurrence twice.
func TestOneOccurrenceIsOneFinding(t *testing.T) {
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil,
		[]string{"PkgX=ZPkgX", "pkgx=zpkgx"}, map[string]bool{})
	got := c.classifyLine("main.go", 1, "dirs = append(dirs, ZZPkgXBin)")
	if n := countVerdict(got, doubled); n != 1 {
		t.Fatalf("%d doubled finding(s) for one occurrence: %+v", n, got)
	}
}

// ⛔ A dead condition reads as a guard and is not one: len(d) < 4 could never
// fire, because d is prefix+prefix+was.
func TestTheThresholdIsTheOneThatCanFire(t *testing.T) {
	if _, ok := doubledForm("a", "ca"); ok {
		t.Error("a one-letter retired spelling armed the check")
	}
	if d, ok := doubledForm("bk", "zbk"); !ok || d != "zzbk" {
		t.Errorf("doubledForm(bk, zbk) = %q, %v; want zzbk, true", d, ok)
	}
}

// ⛔⛔ THE DOUBLED CHECK IS AN OVERLAY: it must not steal another rule's
// verdict. Measured in review: `https://github.com/go-pkgx/zzpkgx` reported
// 1 DOUBLED and ZERO STALE, so a stale organisation URL lost its verdict and
// its rewrite suggestion. Both facts are true of that line at once.
func TestADoubledNameDoesNotSwallowTheStaleAddressAroundIt(t *testing.T) {
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil, []string{"pkgx=zpkgx"}, map[string]bool{})
	got := c.classifyLine("README.md", 1, "see https://github.com/go-pkgx/zzpkgx for it")
	if n := countVerdict(got, stale); n != 1 {
		t.Errorf("%d STALE, want 1 (the address is still stale): %+v", n, got)
	}
	if n := countVerdict(got, doubled); n != 1 {
		t.Errorf("%d DOUBLED, want 1: %+v", n, got)
	}
	for _, f := range got {
		if f.Verdict == stale && f.Suggest == "" {
			t.Error("the stale address lost its replacement suggestion")
		}
	}
}

// ⛔ --keep STILL WINS. A kept address is one somebody checked and declared
// live, so a doubled-looking name inside it is part of a real address.
func TestAKeptAddressIsNotReportedAsDoubled(t *testing.T) {
	c := newClassifier("go-pkgx", "go-zpkg", nil, []string{"ghcr.io/zzpkgx"},
		[]string{"pkgx=zpkgx"}, map[string]bool{})
	got := c.classifyLine("ci.yml", 1, "image: ghcr.io/zzpkgx/packages:latest")
	if n := countVerdict(got, doubled); n != 0 {
		t.Errorf("%d doubled finding(s) on a kept address: %+v", n, got)
	}
}

// ⛔⛔ "NO WORD CONTAINS THE DOUBLED FORM" WAS TRUE OF ONE PAIR AND WRITTEN AS
// A GENERAL LAW. --retired is caller-supplied: `a=ca` reported "The yucca is a
// plant." as drift.
func TestAShortPairHasNoDoubledForm(t *testing.T) {
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil, []string{"a=ca"}, map[string]bool{})
	got := c.classifyLine("README.md", 1, "The yucca is a plant.")
	if n := countVerdict(got, doubled); n != 0 {
		t.Errorf("%d doubled finding(s) in ordinary prose: %+v", n, got)
	}
}

// ⛔⛔ matchCase MUST NOT LOWER-CASE THE REPLACEMENT'S OWN CAPITALS. Measured
// in review: `--retired PkgX=ZPkgX` on `ZZPkgXBin` suggested `Zpkgx` — the
// exact "a suggestion is a string somebody pastes" failure, one layer down.
func TestTheSuggestionKeepsTheReplacementsOwnCapitals(t *testing.T) {
	for _, tc := range []struct{ src, repl, want string }{
		{"ZZPkgX", "ZPkgX", "ZPkgX"},
		{"Zzpkgx", "zpkgx", "Zpkgx"},
		{"zzpkgx", "zpkgx", "zpkgx"},
		{"ZZBK", "zbk", "ZBK"},
		{"zzBkTool", "zBkTool", "zBkTool"},
	} {
		if got := matchCase(tc.src, tc.repl); got != tc.want {
			t.Errorf("matchCase(%q, %q) = %q, want %q", tc.src, tc.repl, got, tc.want)
		}
	}
}

// ⛔⛔ "0 DOUBLED" FROM A CHECK THAT WAS NEVER ARMED READS LIKE A CLEAN TREE.
// The stem carries no replacement unless one is stated, so a run with no
// --retired scans everything and has not looked.
func TestTheDoubledCheckKnowsWhetherItIsArmed(t *testing.T) {
	stemOnly := newClassifier("go-pkgx", "go-zpkg", nil, nil, nil, map[string]bool{})
	if stemOnly.doubledArmed() {
		t.Error("armed with no --retired pair")
	}
	if n := countVerdict(stemOnly.classifyLine("a.go", 1, "ZzpkgxBin"), doubled); n != 0 {
		t.Error("an unarmed classifier reported a doubled finding")
	}
	stated := newClassifier("go-pkgx", "go-zpkg", nil, nil, []string{"pkgx=zpkgx"}, map[string]bool{})
	if !stated.doubledArmed() {
		t.Error("not armed with --retired pkgx=zpkgx")
	}
}

func countVerdict(fs []finding, v verdict) int {
	n := 0
	for _, f := range fs {
		if f.Verdict == v {
			n++
		}
	}
	return n
}

// ⛔⛔ AN EXEMPTED DOUBLED FINDING IS COUNTED AND SAID. Measured in review:
// `ZzpkgxBin` in an allowed CHANGELOG became a citation and the run reported
// 0 DOUBLED, exit 0 — on exactly the page where a doubled name survives.
func TestAnExemptedDoubledFindingIsStillCounted(t *testing.T) {
	byRoot := map[string][]finding{"zbk": {{
		File: "CHANGELOG.md", Line: 3, Verdict: citation, Was: doubled,
		Kind: "a DOUBLED name on a page whose subject is the rename (--allow)",
		Text: "ZzpkgxBin",
	}}}
	var b strings.Builder
	report(&b, byRoot, map[string]int{"zbk": 1}, false, true)
	if !strings.Contains(b.String(), "exempted by --allow") {
		t.Errorf("the exemption was silent:\n%s", b.String())
	}
}

// ⛔ A DOUBLED FINDING SURVIVES --quiet AND FAILS. Its COUNT is the one number
// that tells a reader nothing: nobody is looking for it, nothing else can see
// it, and it names a file that compiles and passes its tests — so it has to
// arrive as a location.
func TestADoubledFindingSurvivesQuietAndFails(t *testing.T) {
	byRoot := map[string][]finding{"zbk": {{
		File: "cmd/zbk/builder.go", Line: 12, Verdict: doubled,
		Kind: "zpkgx doubled", Text: "ZzpkgxBin", Suggest: "Zpkgx",
	}}}
	var b strings.Builder
	clean := report(&b, byRoot, map[string]int{"zbk": 1}, false, true)
	if clean {
		t.Error("a doubled finding reported clean")
	}
	out := b.String()
	if !strings.Contains(out, "ZzpkgxBin") {
		t.Errorf("--quiet reduced the doubled finding to a count:\n%s", out)
	}
	if !strings.Contains(out, "DOUBLED") {
		t.Errorf("the summary does not name DOUBLED:\n%s", out)
	}
}

// ⛔ Its verdict used to be CITATION here, on the grounds that the code reads
// it. That assertion encoded the mistake DEBT exists for: the code being on
// the old naming is the evidence the migration is unfinished, not a reason to
// accept the name. The property this test is actually about — the whole
// variable is matched, before the bare word — is unchanged.
func TestAnEnvironmentVariableIsDecidedBeforeTheBareName(t *testing.T) {
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil, []string{"bk=zbk"},
		map[string]bool{"BK_CC": true})
	got := c.classifyLine("README.md", 1, "imposed through `$BK_CC`")
	if len(got) != 1 {
		t.Fatalf("%d finding(s), want 1: %+v", len(got), got)
	}
	if got[0].Verdict != debt {
		t.Errorf("verdict %s, want DEBT: %+v", got[0].Verdict, got[0])
	}
	if got[0].Text != "$BK_CC" {
		t.Errorf("matched %q, want the whole variable $BK_CC", got[0].Text)
	}

	// A code-var whose name is NOT retired stays a citation: the flag still
	// does its original job.
	c2 := newClassifier("go-pkgx", "go-zpkg", nil, nil, []string{"bk=zbk"},
		map[string]bool{"PKGX_DIR": true})
	got2 := c2.classifyLine("README.md", 1, "set `$PKGX_DIR`")
	if len(got2) != 1 || got2[0].Verdict != citation {
		t.Errorf("a declared, non-retired name stopped being a citation: %+v", got2)
	}
}

// ⛔⛔ TestAFiliationPageIsNotDrift is what makes the exit status mean
// something. A fork has to be able to say where it came from; without a way to
// name that page, every run ends in hundreds of UNSURE findings and the
// decision is never written down.
func TestAFiliationPageIsNotDrift(t *testing.T) {
	root := tree(t, map[string]string{
		"content/filiation.md": "pkgx has no such object, and could not use one.\n",
		"content/usage.md":     "run `pkgx node@22`\n",
	})
	var out, errb bytes.Buffer
	o := opts{old: "go-pkgx", new: "go-zpkg", allowPats: []string{"filiation.md"}}

	if code := run(&out, &errb, o, []string{root}); code != 1 {
		t.Fatalf("exit %d, want 1 — usage.md is still drift (%s)(%s)", code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "filiation.md") {
		t.Errorf("an allowed page was reported as a finding:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "usage.md") {
		t.Errorf("the drift outside the allowed page went unreported:\n%s", out.String())
	}

	// With the one drifting page fixed, the gate goes green — the property that
	// makes it usable in CI at all.
	if err := os.WriteFile(filepath.Join(root, "content/usage.md"), []byte("run `zpkgx node@22`\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := run(&out, &errb, o, []string{root}); code != 0 {
		t.Fatalf("exit %d once the drift is fixed, want 0:\n%s%s", code, out.String(), errb.String())
	}
}

// TestAnAllowedPageIsStillVisibleWithShowCitations: an exemption nobody can see
// is how the next person re-proposes the rewrite it exists to stop.
func TestAnAllowedPageIsStillVisibleWithShowCitations(t *testing.T) {
	root := tree(t, map[string]string{"filiation.md": "pkgx distributes plain tarballs\n"})
	var out, errb bytes.Buffer
	o := opts{old: "go-pkgx", new: "go-zpkg", allowPats: []string{"filiation.md"}, showCitations: true}
	if code := run(&out, &errb, o, []string{root}); code != 0 {
		t.Fatalf("exit %d, want 0 (%s)(%s)", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "this page's subject") {
		t.Errorf("--show-citations does not show WHY the page is exempt:\n%s", out.String())
	}
}

// ⛔⛔ TestAnAllowPatternThatMatchesNothingIsAnError.
//
// The exemption is meant to be narrow. A pattern naming a page that has since
// been renamed or deleted exempts nothing, and would leave the gate green while
// the thing it documents no longer exists — the vacuous guard recorded in
// `three-ways-a-guard-passes-its-own-mutation`. MUTATION: rename the page the
// pattern was written for, and this must go from 0 to 2.
func TestAnAllowPatternThatMatchesNothingIsAnError(t *testing.T) {
	root := tree(t, map[string]string{"filiation.md": "pkgx distributes plain tarballs\n"})
	o := opts{old: "go-pkgx", new: "go-zpkg", allowPats: []string{"filiation.md"}}

	var out, errb bytes.Buffer
	if code := run(&out, &errb, o, []string{root}); code != 0 {
		t.Fatalf("exit %d before the mutation, want 0 (%s)", code, errb.String())
	}

	if err := os.Rename(filepath.Join(root, "filiation.md"), filepath.Join(root, "heritage.md")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := run(&out, &errb, o, []string{root}); code != 2 {
		t.Fatalf("exit %d after the allowed page was renamed, want 2 — an exemption naming nothing passed silently", code)
	}
	if !strings.Contains(errb.String(), "filiation.md") {
		t.Errorf("the refusal does not name the dead pattern: %s", errb.String())
	}
}

// ⛔ TestTheStemsReplacementIsNotGuessed. `go-pkgx` → `go-zpkg` would derive
// `zpkg` for the command, but the command is `zpkgx`. A suggestion is a string
// somebody pastes into a document, so the stem carries none unless --retired
// states it — and when it does, it REPLACES the stem rather than being shadowed.
func TestTheStemsReplacementIsNotGuessed(t *testing.T) {
	silent := newClassifier("go-pkgx", "go-zpkg", nil, nil, nil, map[string]bool{})
	got := silent.classifyLine("README.md", 1, "run `pkgx node@22`")
	if len(got) != 1 {
		t.Fatalf("%d finding(s), want 1: %+v", len(got), got)
	}
	if strings.Contains(got[0].Kind, "zpkg") {
		t.Errorf("the stem guessed a replacement: %q", got[0].Kind)
	}

	stated := newClassifier("go-pkgx", "go-zpkg", nil, nil, []string{"pkgx=zpkgx"}, map[string]bool{})
	got = stated.classifyLine("README.md", 1, "run `pkgx node@22`")
	if len(got) != 1 {
		t.Fatalf("%d finding(s) with --retired pkgx=zpkgx, want 1: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Kind, "zpkgx") {
		t.Errorf("an explicit --retired for the stem was shadowed: %q", got[0].Kind)
	}
}

// ⛔⛔ TestExtReadsTheSourcesTheDefaultCannot is the hole this flag closes.
//
// On 2026-11-10 the go-zpkg sweep reported zpkgx accounted for while
// `zpkgx --help` printed `ZPKG_DIR  bottle store (default: ~/.pkgx)` and
// `internal/app/environments.go` READ `$HOME/.pkgx/environments`. Neither is
// a documentation file, so no extension in the default set matched them and
// their absence from the report read as cleanliness.
func TestExtReadsTheSourcesTheDefaultCannot(t *testing.T) {
	root := tree(t, map[string]string{
		"README.md": "nothing stale here\n",
		"main.go":   "// help: ZPKG_DIR bottle store (default: ~/.pkgx)\n",
		"run.sh":    "exec pkgx \"$@\"\n",
	})
	o := opts{old: "go-pkgx", new: "go-zpkg", alsoRetired: []string{"pkgx=zpkgx"}}

	// The default set cannot see either file, and says so with a clean run.
	var out, errb bytes.Buffer
	if code := run(&out, &errb, o, []string{root}); code != 0 {
		t.Fatalf("exit %d with the default extensions, want 0:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "1 doc file(s)") {
		t.Errorf("the default set read something other than README.md:\n%s", out.String())
	}

	// Named, they are read and both are found.
	out.Reset()
	errb.Reset()
	o.exts = []string{"go", ".SH"} // bare, dotted and upper-case are one thing
	if code := run(&out, &errb, o, []string{root}); code != 1 {
		t.Fatalf("exit %d with --ext, want 1:\n%s", code, out.String())
	}
	for _, want := range []string{"main.go", "run.sh", "3 doc file(s)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--ext did not reach %s:\n%s", want, out.String())
		}
	}
}

// TestExtNormalisesItsArgument: `go`, `.go` and `.GO` name one extension, so a
// caller cannot half-enable a file set and read the silence as clean.
func TestExtNormalisesItsArgument(t *testing.T) {
	for _, spell := range []string{"go", ".go", ".GO", "  go  "} {
		s := extSet([]string{spell})
		if !s[".go"] {
			t.Errorf("--ext %q did not enable .go", spell)
		}
	}
	if s := extSet([]string{"", "   "}); len(s) != len(docExts) {
		t.Errorf("an empty --ext changed the set: %v", s)
	}
}

// ⛔⛔ TestACodeVarWithARetiredNameIsDebtNotAnExemption.
//
// This is the gate for a mistake made repeatedly on 2026-11-10: `--code-var X`
// was used to excuse the NAME. `BK_CC`, `PKGE_SPECS`, `PKGX_DIR` were each
// declared, each turned from a finding into a silent CITATION, and each time
// the conclusion drawn was "documentation correct, leave it" — when the code
// being on the old naming is precisely the evidence the migration is
// unfinished.
//
// Declaring the variable must no longer make it disappear.
func TestACodeVarWithARetiredNameIsDebtNotAnExemption(t *testing.T) {
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil,
		[]string{"bk=zbk", "pkge=zpkge"}, map[string]bool{"BK_CC": true, "ZBK_CC": true})

	got := c.classifyLine("wrapper.go", 1, `export BK_CC="clang"`)
	if len(got) != 1 {
		t.Fatalf("%d finding(s), want 1: %+v", len(got), got)
	}
	if got[0].Verdict != debt {
		t.Errorf("verdict %s, want DEBT — the code reads it AND the name is retired", got[0].Verdict)
	}
	if !strings.Contains(got[0].Kind, "the CODE is the half to change") {
		t.Errorf("the kind does not say which half to fix: %q", got[0].Kind)
	}

	// ⛔ The prefix test is ANCHORED: ZBK_CC must not match BK_, or the gate
	// fires on the very name the migration produces and nobody can finish.
	if got := c.classifyLine("wrapper.go", 1, `export ZBK_CC="clang"`); len(got) != 0 {
		t.Errorf("ZBK_CC was flagged: %+v", got)
	}
}

// ⛔ TestADebtFindingSurvivesQuietAndFails: --quiet trades findings for
// counts, and the one finding that must not be reducible to a count is the one
// a flag used to make disappear.
func TestADebtFindingSurvivesQuietAndFails(t *testing.T) {
	root := tree(t, map[string]string{"README.md": "set `BK_CC` before building\n"})
	o := opts{
		old: "go-pkgx", new: "go-zpkg",
		alsoRetired: []string{"bk=zbk"},
		codeVar:     []string{"BK_CC"},
		quiet:       true,
	}
	var out, errb bytes.Buffer
	if code := run(&out, &errb, o, []string{root}); code != 1 {
		t.Fatalf("exit %d, want 1 — a declared but retired name must still fail:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "DEBT") || !strings.Contains(out.String(), "BK_CC") {
		t.Errorf("--quiet hid the debt finding:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "has not been migrated") {
		t.Errorf("the summary does not say what a DEBT means:\n%s", out.String())
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
		if code := run(&out, &errb, opts{old: tc.old, new: tc.new}, tc.dirs); code != 2 {
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
	code := run(&out, &errb, opts{old: "go-pkgx", new: "go-zpkg"},
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
	c := newClassifier("go-pkgx", "go-zpkg", nil, nil, nil, nil)
	if _, _, err := scanTree(root, c, newAllowList(nil), extSet(nil)); err == nil {
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
	for _, want := range []string{"--old", "--new", "--upstream", "--code-var", "--quiet", "--retired", "--allow", "--ext"} {
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
