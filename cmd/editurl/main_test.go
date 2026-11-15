package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// The two bases this tool exists to tell apart, measured on the published
// pages of go-desktop/docs before and after go-desktop/docs#27.
const (
	correctBase = "https://github.com/go-desktop/docs/edit/main/content"
	doubledBase = "https://github.com/go-desktop/docs/edit/main/content/en"
	noRepoBase  = "https://github.com/go-desktop/edit/main/content/en"
)

func TestTheHrefIsJoinedTheWayTheThemeJoinsIt(t *testing.T) {
	// tannevaled/hextra@v0.24.1 layouts/_partials/toc.html:66 --
	// urls.JoinPath base sourceDir path, which together are the page's path
	// relative to content/.
	if got, want := Href(correctBase, "en/finding.md"), correctBase+"/en/finding.md"; got != want {
		t.Errorf("Href = %q, want %q", got, want)
	}
	// The defect, kept as a test so nobody "simplifies" the formula into
	// agreeing with the broken base.
	if got, want := Href(doubledBase, "en/finding.md"), "https://github.com/go-desktop/docs/edit/main/content/en/en/finding.md"; got != want {
		t.Errorf("doubled Href = %q, want %q", got, want)
	}
}

func TestATrailingSlashOnTheBaseIsNotADoubledSeparator(t *testing.T) {
	if got, want := Href(correctBase+"/", "en/finding.md"), correctBase+"/en/finding.md"; got != want {
		t.Errorf("Href = %q, want %q", got, want)
	}
}

func TestTheCorrectBaseNamesThePagesOwnSource(t *testing.T) {
	l := Resolve(correctBase, "en/finding.md", "go-desktop", "docs", "main")
	if l.Reason != ReasonNone || l.Why != "" {
		t.Fatalf("reason = %d, why = %q, want none", l.Reason, l.Why)
	}
	if l.InRepo != "content/en/finding.md" {
		t.Errorf("inRepo = %q, want content/en/finding.md", l.InRepo)
	}
	if !strings.HasSuffix(l.Href, "/content/en/finding.md") {
		t.Errorf("href = %q", l.Href)
	}
}

func TestTheDoubledLanguageIsAPathTheRepositoryDoesNotHave(t *testing.T) {
	// ⛔ Resolve reports NO reason here, deliberately: the base is a perfectly
	// well-formed edit URL for this repository. What is wrong is the path, and
	// only the repository can say so -- which is the whole argument for reading
	// a checkout instead of fetching the href.
	l := Resolve(doubledBase, "en/finding.md", "go-desktop", "docs", "main")
	if l.Reason != ReasonNone {
		t.Fatalf("reason = %d (%q), want none: the shape is valid, the path is not", l.Reason, l.Why)
	}
	if l.InRepo != "content/en/en/finding.md" {
		t.Errorf("inRepo = %q, want content/en/en/finding.md", l.InRepo)
	}
}

func TestAMissingRepositorySegmentIsCaughtByPosition(t *testing.T) {
	l := Resolve(noRepoBase, "en/finding.md", "go-desktop", "docs", "main")
	if l.Reason != ReasonOtherRepo {
		t.Fatalf("reason = %d, want ReasonOtherRepo", l.Reason)
	}
	if !strings.Contains(l.Why, "go-desktop/edit") {
		t.Fatalf("why = %q, want it to name the repository the href invents", l.Why)
	}
}

func TestABaseNamingAnotherRepositoryIsAFinding(t *testing.T) {
	l := Resolve("https://github.com/go-desktop/catalog/edit/main/content", "en/a.md", "go-desktop", "docs", "main")
	if l.Reason != ReasonOtherRepo {
		t.Fatalf("reason = %d, want ReasonOtherRepo", l.Reason)
	}
	if !strings.Contains(l.Why, "go-desktop/catalog") || !strings.Contains(l.Why, "go-desktop/docs") {
		t.Fatalf("why = %q, want both names", l.Why)
	}
}

func TestABaseNamingAnotherBranchIsAFinding(t *testing.T) {
	l := Resolve("https://github.com/go-desktop/docs/edit/master/content", "en/a.md", "go-desktop", "docs", "main")
	if l.Reason != ReasonOtherBranch {
		t.Fatalf("reason = %d, want ReasonOtherBranch", l.Reason)
	}
	if !strings.Contains(l.Why, "master") || !strings.Contains(l.Why, "main") {
		t.Fatalf("why = %q, want both branch names", l.Why)
	}
	// An unknown default branch must not manufacture a finding.
	if l := Resolve("https://github.com/go-desktop/docs/edit/master/content", "en/a.md", "go-desktop", "docs", ""); l.Reason != ReasonNone {
		t.Fatalf("reason = %d (%q), want none when the default branch is unknown", l.Reason, l.Why)
	}
}

func TestABaseThatIsNotAGitHubEditURLIsNotJudgedByPath(t *testing.T) {
	for _, base := range []string{
		"https://gitlab.com/go-desktop/docs/edit/main/content",
		"ftp://github.com/go-desktop/docs/edit/main/content",
		"https://github.com/go-desktop/docs",
	} {
		l := Resolve(base, "en/a.md", "go-desktop", "docs", "main")
		if l.Reason != ReasonNotAnEditURL {
			t.Errorf("%s: reason = %d (%q), want ReasonNotAnEditURL", base, l.Reason, l.Why)
		}
		if l.InRepo != "" {
			t.Errorf("%s: inRepo = %q, want none: nothing may be stat-ed from a base like this", base, l.InRepo)
		}
	}
}

// writeFile writes one file under dir, creating parents.
func writeFile(t *testing.T, dir, rel, body string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfigKeysAreReadWithoutCaseLikeHugo(t *testing.T) {
	// Hugo's keys are case-insensitive; the fleet writes editURL, Hugo's own
	// documentation writes editurl.
	for _, spelling := range []string{"editURL", "editurl", "EditUrl"} {
		dir := t.TempDir()
		writeFile(t, dir, "hugo.yaml", "params:\n  "+spelling+":\n    enable: true\n    base: "+correctBase+"\n")
		cfg, found, why := ReadConfig(dir)
		if !found {
			t.Fatalf("%s: not found (unreadable %q)", spelling, why)
		}
		if cfg.Base != correctBase || !cfg.Enabled {
			t.Errorf("%s: cfg = %+v", spelling, cfg)
		}
		if cfg.Content != "content" {
			t.Errorf("%s: content = %q, want the default", spelling, cfg.Content)
		}
	}
}

func TestAContentDirIsReadWhenTheSiteMovesIt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hugo.yaml", "contentDir: pages\nparams:\n  editURL:\n    enable: true\n    base: "+correctBase+"\n")
	cfg, found, _ := ReadConfig(dir)
	if !found || cfg.Content != "pages" {
		t.Fatalf("cfg = %+v, found = %v", cfg, found)
	}
}

func TestAParamsFileHoldsTheParamsMapItself(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config/_default/params.yaml", "editURL:\n  enable: true\n  base: "+correctBase+"\n")
	cfg, found, why := ReadConfig(dir)
	if !found {
		t.Fatalf("not found (unreadable %q)", why)
	}
	if cfg.Base != correctBase {
		t.Errorf("base = %q", cfg.Base)
	}
}

func TestATomlConfigIsReportedUnreadableRatherThanAbsent(t *testing.T) {
	// ⛔ The failure mode this guards is a count that silently shrinks its
	// population: a site whose configuration this tool cannot parse must not
	// read as a site with no edit link.
	dir := t.TempDir()
	writeFile(t, dir, "hugo.toml", "[params.editURL]\nbase = \""+correctBase+"\"\n")
	_, found, why := ReadConfig(dir)
	if found {
		t.Fatal("found = true, want a refusal to guess")
	}
	if !strings.Contains(why, "hugo.toml") {
		t.Fatalf("why = %q, want it to name the file", why)
	}
}

func TestASiteWithNoEditURLIsNeitherFoundNorUnreadable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hugo.yaml", "title: a site with no edit link\n")
	_, found, why := ReadConfig(dir)
	if found || why != "" {
		t.Fatalf("found = %v, why = %q", found, why)
	}
}

func TestTwoConfigsWithDifferentBasesAreNotPickedBetween(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hugo.yaml", "params:\n  editURL:\n    base: "+correctBase+"\n")
	writeFile(t, dir, "config/_default/hugo.yaml", "params:\n  editURL:\n    base: "+doubledBase+"\n")
	_, found, why := ReadConfig(dir)
	if found {
		t.Fatal("found = true: one of two different bases was picked")
	}
	if !strings.Contains(why, "different bases") {
		t.Fatalf("why = %q", why)
	}
}

func TestTheSameBaseInTwoConfigsIsNotAnAmbiguity(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hugo.yaml", "params:\n  editURL:\n    enable: true\n    base: "+correctBase+"\n")
	writeFile(t, dir, "config/_default/hugo.yaml", "params:\n  editURL:\n    base: "+correctBase+"\n")
	cfg, found, why := ReadConfig(dir)
	if !found {
		t.Fatalf("found = false, why = %q", why)
	}
	if cfg.File != "hugo.yaml" {
		t.Errorf("file = %q, want the first candidate", cfg.File)
	}
}

func TestAPageWithItsOwnEditURLIsNotChargedToTheBase(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "content/en/plain.md", "# plain\n")
	writeFile(t, dir, "content/en/own.md", "---\ntitle: own\neditURL: https://example.invalid/elsewhere\n---\n\n# own\n")
	writeFile(t, dir, "content/en/.hidden/skipped.md", "# hidden\n")
	rel, overridden, err := Pages(filepath.Join(dir, "content"))
	if err != nil {
		t.Fatal(err)
	}
	if overridden != 1 {
		t.Errorf("overridden = %d, want 1", overridden)
	}
	if len(rel) != 1 || rel[0] != "en/plain.md" {
		t.Errorf("pages = %v, want just en/plain.md", rel)
	}
}

// fixtureSite writes a site with the given base and one page, and returns its
// directory.
func fixtureSite(t *testing.T, base string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "hugo.yaml", "params:\n  editURL:\n    enable: true\n    base: "+base+"\n")
	writeFile(t, dir, "content/en/finding.md", "# finding\n")
	return dir
}

func TestInspectNamesThePathThatIsNotInTheRepository(t *testing.T) {
	dir := fixtureSite(t, doubledBase)
	cfg, found, _ := ReadConfig(dir)
	if !found {
		t.Fatal("no config")
	}
	p, pages, err := Inspect(dir, "go-desktop/docs", "go-desktop", "docs", "main", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if pages != 1 {
		t.Fatalf("pages = %d, want 1", pages)
	}
	if p == nil {
		t.Fatal("no problem reported for the doubled base")
	}
	if p.Reason != ReasonNoSuchPath {
		t.Errorf("reason = %d, want ReasonNoSuchPath", p.Reason)
	}
	if !strings.Contains(p.Why, "content/en/en/finding.md") {
		t.Errorf("why = %q, want the path it names", p.Why)
	}
	if p.Broken != 1 || p.Pages != 1 {
		t.Errorf("broken = %d of %d", p.Broken, p.Pages)
	}
	if p.Correct != correctBase {
		t.Errorf("correct = %q, want %q", p.Correct, correctBase)
	}
}

func TestInspectIsSilentWhenEveryHrefResolves(t *testing.T) {
	dir := fixtureSite(t, correctBase)
	cfg, _, _ := ReadConfig(dir)
	p, pages, err := Inspect(dir, "go-desktop/docs", "go-desktop", "docs", "main", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p != nil {
		t.Fatalf("problem = %+v, want none", p)
	}
	if pages != 1 {
		t.Errorf("pages = %d, want 1", pages)
	}
}

func TestARemoteInEitherSpellingIsOneRepository(t *testing.T) {
	for _, remote := range []string{
		"https://github.com/go-desktop/docs.git",
		"https://github.com/go-desktop/docs",
		"git@github.com:go-desktop/docs.git",
		"ssh://git@github.com/go-desktop/docs.git",
	} {
		owner, name, ok := splitRemote(remote)
		if !ok || owner != "go-desktop" || name != "docs" {
			t.Errorf("%s -> %q/%q ok=%v", remote, owner, name, ok)
		}
	}
	if _, _, ok := splitRemote("not a remote"); ok {
		t.Error("a remote that names no repository was accepted")
	}
}

// drive runs the command with args and returns its output and exit code.
func drive(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := cli.Drive(rootCmd(), args, &out, &out)
	return out.String(), code
}

func TestAnEmptyCloneRootIsUnanswerableAndNotClean(t *testing.T) {
	// ⛔ Pointed at the flat <org>-<repo> working farm, Repos finds nothing.
	// "Every edit link resolves" is not what that means.
	out, code := drive(t, "--root", t.TempDir())
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "nothing was read") {
		t.Errorf("output = %q", out)
	}
}

// fixtureRoot builds a clone root holding one site, with a .git directory so
// checkout.Repos sees it, and points the origin and branch seams at it.
func fixtureRoot(t *testing.T, base string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "go-desktop", "docs")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "hugo.yaml", "params:\n  editURL:\n    enable: true\n    base: "+base+"\n")
	writeFile(t, dir, "content/en/finding.md", "# finding\n")
	t.Cleanup(func(o func(string) (string, string, bool), b func(string) string) func() {
		return func() { originRepo, defaultBranch = o, b }
	}(originRepo, defaultBranch))
	originRepo = func(string) (string, string, bool) { return "go-desktop", "docs", true }
	defaultBranch = func(string) string { return "main" }
	return root
}

func TestTheTableNamesTheBaseThatWouldResolve(t *testing.T) {
	out, code := drive(t, "--root", fixtureRoot(t, doubledBase), "--fail-on-broken")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	for _, want := range []string{
		"1 of the 1 render an href that does not resolve",
		"1 name a path that is not in the repository",
		"go-desktop/docs (hugo.yaml)",
		"content/en/en/finding.md",
		"correct " + correctBase,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

func TestTheListNamesEverySiteWithItsContentDirectory(t *testing.T) {
	// ⭐ The list is what reconciled two measurements of this fleet: a grep
	// for bases ending at content/en counted sites whose contentDir IS that
	// directory, for which the same base is correct.
	out, _ := drive(t, "--root", fixtureRoot(t, doubledBase), "--all")
	for _, want := range []string{"every site that renders an edit link:", "go-desktop/docs", "content", "broken 1/1", doubledBase} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

func TestASiteWhoseLinksResolveIsReportedCleanAndExitsZero(t *testing.T) {
	out, code := drive(t, "--root", fixtureRoot(t, correctBase), "--fail-on-broken")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "0 of the 1 render an href that does not resolve") {
		t.Errorf("output = %q", out)
	}
}

func TestADisabledEditLinkIsCountedAndNotJudged(t *testing.T) {
	root := fixtureRoot(t, noRepoBase)
	writeFile(t, filepath.Join(root, "go-desktop", "docs"), "hugo.yaml",
		"params:\n  editURL:\n    enable: false\n    base: "+noRepoBase+"\n")
	out, code := drive(t, "--root", root, "--fail-on-broken")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "0 render an edit link, 1 have one configured but disabled") {
		t.Errorf("output = %q", out)
	}
}

func TestAConfigurationThatCouldNotBeReadIsNamedUnderTheTable(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "go-desktop", "docs")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "hugo.toml", "[params.editURL]\nbase = \""+doubledBase+"\"\n")
	out, code := drive(t, "--root", root)
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, out)
	}
	for _, want := range []string{"could not read", "hugo.toml", "nobody looked"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

// git runs one git command in dir and fails the test if it does not succeed.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=editurl test", "GIT_AUTHOR_EMAIL=test@invalid",
		"GIT_COMMITTER_NAME=editurl test", "GIT_COMMITTER_EMAIL=test@invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// TestAFindingFixedUpstreamIsDroppedBecauseTheRefWasFetched is the test for
// this tool's own first defect.
//
// ⛔ MEASURED, 2026-11-15: the first run reported go-desktop/docs as broken
// hours after go-desktop/docs#27 had merged and published the fix, and printed
// "each was RE-DERIVED against the remote default and still holds" over it.
// checkout.Staleness compares HEAD with the LOCAL copy of origin/main, and
// nothing had fetched that copy since before the merge: the tree the finding
// was re-derived against was the stale one. Remove the checkout.Refresh call
// in run and this test reports the dead finding again.
func TestAFindingFixedUpstreamIsDroppedBecauseTheRefWasFetched(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("no git: %v", err)
	}
	origin := t.TempDir()
	git(t, origin, "init", "-q", "-b", "main")
	writeFile(t, origin, "hugo.yaml", "params:\n  editURL:\n    enable: true\n    base: "+doubledBase+"\n")
	writeFile(t, origin, "content/en/finding.md", "# finding\n")
	git(t, origin, "add", "-A")
	git(t, origin, "commit", "-q", "-m", "a site with the doubled language")

	root := t.TempDir()
	dir := filepath.Join(root, "go-desktop", "docs")
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "clone", "-q", origin, dir)

	defer func(o func(string) (string, string, bool), b func(string) string) {
		originRepo, defaultBranch = o, b
	}(originRepo, defaultBranch)
	originRepo = func(string) (string, string, bool) { return "go-desktop", "docs", true }
	defaultBranch = func(string) string { return "main" }

	// The clone is a faithful copy, so the finding is real and must be reported.
	if out, code := drive(t, "--root", root, "--fail-on-broken"); code != 1 {
		t.Fatalf("exit = %d, want 1 on a site that is broken in both trees\n%s", code, out)
	}

	// The fix lands upstream. The clone's working tree and its local
	// origin/main both still hold the broken base.
	writeFile(t, origin, "hugo.yaml", "params:\n  editURL:\n    enable: true\n    base: "+correctBase+"\n")
	git(t, origin, "commit", "-q", "-a", "-m", "base ends at content")

	out, code := drive(t, "--root", root, "--fail-on-broken")
	if code != 0 {
		t.Fatalf("exit = %d, want 0: the finding was fixed upstream\n%s", code, out)
	}
	if !strings.Contains(out, "0 of the 1 render an href that does not resolve") {
		t.Errorf("output = %q", out)
	}

	// ⛔ AND THE ROW MUST NAME THE BASE THAT WAS JUDGED. The first version of
	// the listing kept the verdict from the materialised tree and the base
	// from the stale working tree, printing "resolves" beside a base the site
	// no longer has -- a row true in neither tree, and a breakdown that
	// disagreed with its own table by one.
	out, _ = drive(t, "--root", root, "--all")
	if !strings.Contains(out, correctBase) {
		t.Errorf("the listing does not name the base it judged:\n%s", out)
	}
	if strings.Contains(out, "resolves      "+doubledBase) {
		t.Errorf("the listing says the stale base resolves:\n%s", out)
	}
	if !strings.Contains(out, "origin/main") {
		t.Errorf("the listing does not say where the base was read:\n%s", out)
	}

	// And with --no-confirm the stale verdict is what you get, said out loud.
	out, code = drive(t, "--root", root, "--fail-on-broken", "--no-confirm")
	if code != 1 {
		t.Fatalf("--no-confirm exit = %d, want 1: it reads the tree as it stands\n%s", code, out)
	}
}
