package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sameLine = "[Repository →](https://github.com/go-fleettools/mutate) Latest tag: **v0.13.0**\n" +
	"(`git ls-remote --tags`, 2026-11-22); this page is written from its README at\n**v0.12.0**.\n"

const nextLine = "Text. [Repository →](https://github.com/go-fleettools/prwait) Latest tag:\n" +
	"**v0.14.0** (`git ls-remote --tags`, 2026-11-22); this page is written.\n"

const startsLine = "# staletools\n\n" +
	"[Repository →](https://github.com/go-fleettools/staletools)\n" +
	"Latest tag: **v0.4.0** (`git ls-remote --tags`, 2026-11-15); this page is written\n" +
	"from its README at **v0.3.0**, and the two are stated separately on purpose.\n"

// ⛔ THE MUTATIONS THESE TESTS ARE HELD BY WERE APPLIED BY HAND, because
// `mutsweep` reaches exactly one condition in cells.go and none of them (see
// the ⛔ above findProse). Re-apply all three after any change to the regexp or
// the pairing, and expect the named test to go red:
//
//	drop "Latest tag:" from proseClaimRE  -> 4 red, among them
//	                                        TestRewriteLeavesTheWrittenFrom...
//	                                        with "the written-from version was
//	                                        touched" (verified 2026-11-22)
//	pair with any link (`if true`)        -> TestAClaimIsNeverPairedWithALink...
//	                                        "paired with a link that follows"
//	                                        (verified 2026-11-22)
//	flush a paragraph on every line       -> 4 red: this test on all three
//	                                        shapes, and the three --min-cells
//	                                        tests, which see 0 cells and exit 2
//	                                        (verified 2026-11-22)
//
// A regexp that stops matching is the failure mode that costs nothing at the
// time and leaves a page unchecked for weeks, which is precisely how this
// function came to be needed.
func TestFindProseReadsTheThreeShapesOfARepositoryPage(t *testing.T) {
	for _, c := range []struct{ name, src, module, tag string }{
		{"one line", sameLine, "mutate", "v0.13.0"},
		{"version on the next line", nextLine, "prwait", "v0.14.0"},
		{"claim starts the line", startsLine, "staletools", "v0.4.0"},
	} {
		cells := Find(c.src, "go-fleettools", false)
		if len(cells) != 1 || cells[0].Module != c.module || cells[0].Tag != c.tag || !cells[0].hasPar || !cells[0].parClosed {
			t.Errorf("%s: %+v", c.name, cells)
			continue
		}
		if c.src[cells[0].start:cells[0].end] != c.tag {
			t.Errorf("%s: offsets point at %q", c.name, c.src[cells[0].start:cells[0].end])
		}
	}
	// ⛔ len BEFORE INDEX, even here: with the paragraph split broken by hand
	// this line panicked with "index out of range", the suite stopped there, and
	// three other tests that had something to say about the same mutation never
	// ran. A test that crashes reports one failure instead of four, and the
	// panic names a test file rather than the guard that is gone.
	cells := Find(startsLine, "go-fleettools", false)
	if len(cells) != 1 {
		t.Fatalf("%d cell(s), want 1: %+v", len(cells), cells)
	}
	if cells[0].Line != 4 {
		t.Fatalf("line %d, want 4", cells[0].Line)
	}
}

func TestRewriteLeavesTheWrittenFromVersionAloneAndMovesTheDate(t *testing.T) {
	for _, src := range []string{startsLine, sameLine} {
		cells := Find(src, "go-fleettools", false)
		// len before index, for the reason spelled out above the first test:
		// the mutations this file is held by are applied by hand, and a test
		// that panics instead of failing takes the rest of the suite with it.
		if len(cells) == 0 {
			t.Fatalf("no claim found at all in:\n%s", src)
		}
		out, refused := Rewrite(src, cells, rel(cells[0].Module, "v0.99.0"), "2026-12-01")
		if len(refused) != 0 {
			t.Fatalf("a claim naming only a command and a date is provable: %+v", refused)
		}
		if !strings.Contains(out, "Latest tag: **v0.99.0**") && !strings.Contains(out, "**v0.99.0**\n(`git") {
			t.Fatalf("the claim was not rewritten:\n%s", out)
		}
		if !strings.Contains(out, "`git ls-remote --tags`, 2026-12-01)") || strings.Contains(out, "2026-11-") {
			t.Fatalf("the date must move with the version:\n%s", out)
		}
		for _, kept := range []string{"README at **v0.3.0**", "README at\n**v0.12.0**"} {
			if strings.Contains(src, kept) && !strings.Contains(out, kept) {
				t.Fatalf("the written-from version was touched:\n%s", out)
			}
		}
	}
	cells := Find(startsLine, "go-fleettools", false)
	if out, _ := Rewrite(startsLine, cells, rel("staletools", "v0.4.0"), "2026-12-01"); out != startsLine {
		t.Fatalf("a current claim keeps its date:\n%s", out)
	}
}

// sourced is the shape that cost this command its credibility on 2026-11-22:
// the claim states the version, the commit, the date it was read AND the command
// that read it, and the command names the tag a second time. `--write` replaced
// the version number alone, leaving a page that said v0.154.0 was commit
// a57b2c0 "read with git ls-remote … refs/tags/v0.141.0, which agree" -- and
// dated it today, so the freshest-looking part of the sentence vouched for the
// stale part.
const sourced = "[Repository →](https://github.com/go-fleettools/fleettools)\n" +
	"Latest tag: **v0.141.0** (`a57b2c0`, read on 2026-11-15 with\n" +
	"`git ls-remote --tags https://github.com/go-fleettools/fleettools refs/tags/v0.141.0`,\n" +
	"which agree); this page is written from its README at **v0.140.0**.\n"

const newCommit = "abcdef0123456789abcdef0123456789abcdef01"

// ⛔ A CLAIM IS MOVED WHOLE OR LEFT ALONE, and these are the mutations that
// hold it, applied BY HAND for the reason given above findProse (mutsweep
// reaches one condition in the whole file). Each line below is the output of
// the run, not a prediction of it -- all four were applied and reverted on
// 2026-11-22, and the message quoted is the one the suite printed:
//
//	`if c.hasPar` -> `if false && ...`   5 red, this test first with
//	                                     `missing "(`abcdef0`," in:` -- the
//	                                     page then reads **v0.154.0** beside
//	                                     (`a57b2c0`, ... refs/tags/v0.141.0)
//	drop the `continue` after a          2 red: "a refused claim must be left
//	refusal                              exactly as it was", and the page it
//	                                     prints is the defect itself: the new
//	                                     tag, the old commit, dated today
//	`moveClaim` never setting `why`      2 red: "0 refusal(s), want 1" and
//	                                     "rc 0, want 2"
//	`parenAfter` returning closed=true   1 red: the unclosed row, "the
//	                                     parentheses never close: 0
//	                                     refusal(s), want 1"
//
// ⛔ AND TWO CHECKS OF THE FIRST VERSION SURVIVED, which is how this file came
// to have one pass instead of two: the rewrite substituted, then a second
// function read the output back and refused what belonged to the old release.
// `mutsweep` reached both of that judge's conditions (they are conditions, not
// regexps) and both SURVIVED -- the date one for want of the row above, the
// leftover-tag one because NO input can redden it: the judge held the writer's
// own regexp, so every version it could see had already been replaced. A
// survivor that cannot be tested is not a missing test, it is a sentence
// pretending to be a guard.
func TestARewrittenClaimCarriesItsCommitDateAndCommand(t *testing.T) {
	cells := Find(sourced, "go-fleettools", false)
	if len(cells) != 1 || cells[0].Tag != "v0.141.0" || !cells[0].hasPar || !cells[0].parClosed {
		t.Fatalf("%+v", cells)
	}
	want := map[string]Release{"fleettools": {Tag: "v0.154.0", Commit: newCommit}}
	out, refused := Rewrite(sourced, cells, want, "2026-11-22")
	if len(refused) != 0 {
		t.Fatalf("every part of this claim is one the command can write: %+v", refused)
	}
	for _, s := range []string{
		"Latest tag: **v0.154.0**",
		"(`abcdef0`,",             // truncated to the SEVEN characters the page chose
		"read on 2026-11-22 with", // the date moves with the version
		"refs/tags/v0.154.0`",     // including inside the command
		"README at **v0.140.0**",  // and the written-from version does not move
	} {
		if !strings.Contains(out, s) {
			t.Fatalf("missing %q in:\n%s", s, out)
		}
	}
	for _, s := range []string{"a57b2c0", "v0.141.0", "2026-11-15"} {
		if strings.Contains(out, s) {
			t.Fatalf("the commit was left pointing at the old release (%q):\n%s", s, out)
		}
	}
	// ⛔ AND THE ABBREVIATION IS NEVER GROWN. A page showing seven characters
	// gets seven, not forty: a longer object id invented from a shorter one is
	// well formed and answers for nothing, which this fleet has paid for.
	if strings.Contains(out, newCommit) {
		t.Fatalf("a 7-character prefix was replaced by a full object id:\n%s", out)
	}
}

func TestAClaimItCannotProveIsLeftExactlyAsItWas(t *testing.T) {
	unclosed := strings.Replace(sourced, "which agree);", "which agree;", 1)
	for _, c := range []struct{ name, src, want, date, why string }{
		{"the commit of the new tag was not read", sourced, "", "2026-11-22", "was not read in full (0 of the 7"},
		{"the parentheses never close", unclosed, newCommit, "2026-11-22", "never closes"},
		// ⛔ A claim that DATES its reading cannot be moved by a run with no
		// date: the date would then state when the OLD tag was read, under the
		// new one. Reachable only through this function -- the command itself
		// always has today -- which is exactly why nothing held it until a
		// mutation of the branch survived.
		{"the run has no date for a dated claim", sourced, newCommit, "", "no date to put in its place"},
	} {
		cells := Find(c.src, "go-fleettools", false)
		if len(cells) != 1 {
			t.Fatalf("%s: %d cell(s)", c.name, len(cells))
		}
		out, refused := Rewrite(c.src, cells, map[string]Release{"fleettools": {Tag: "v0.154.0", Commit: c.want}}, c.date)
		if len(refused) != 1 {
			t.Fatalf("%s: %d refusal(s), want 1", c.name, len(refused))
		}
		if !strings.Contains(refused[0].Why, c.why) {
			t.Fatalf("%s: the refusal must say why: %q", c.name, refused[0].Why)
		}
		if refused[0].Line != 2 || refused[0].Module != "fleettools" {
			t.Fatalf("%s: the refusal must name the claim: %+v", c.name, refused[0].Cell)
		}
		// ⛔ The version does NOT move on its own. A page left stale is a page
		// that still asks to be fixed; a page whose number moved past its own
		// source asks for nothing and is wrong.
		if out != c.src {
			t.Fatalf("%s: a refused claim must be left exactly as it was:\n%s", c.name, out)
		}
	}
}

// A parenthetical that is not a source -- no date, no commit, no tag -- is
// rewritten without being understood, because there is nothing in it to move.
func TestAParentheticalWithNothingToMoveIsNoObstacle(t *testing.T) {
	src := "[R](https://github.com/go-fleettools/alpha) Latest tag: **v0.1.0** (the first one)\n"
	cells := Find(src, "go-fleettools", false)
	out, refused := Rewrite(src, cells, rel("alpha", "v0.2.0"), "2026-11-22")
	if len(refused) != 0 || !strings.Contains(out, "**v0.2.0** (the first one)") {
		t.Fatalf("%+v\n%s", refused, out)
	}
}

func TestRunRefusesToHalfWriteASourcedClaim(t *testing.T) {
	f := filepath.Join(t.TempDir(), "fleettools.md")
	if err := os.WriteFile(f, []byte(sourced), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	// The tag is read, the commit is not: the shape a `git ls-remote` output
	// this command cannot parse produces, and the one that must never be
	// written onto a page.
	tagOnly := func(_, _ string) (Release, error) { return Release{Tag: "v0.154.0"}, nil }
	rc := run(options{org: "go-fleettools", write: true, files: []string{f}, today: "2026-11-22"}, &out, &errb, tagOnly)
	if rc != 2 {
		t.Fatalf("rc %d, want 2: the page could not be made true\n%s%s", rc, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "left alone") || !strings.Contains(errb.String(), "fleettools.md:2") {
		t.Fatalf("the refusal must name the page and the line: %q", errb.String())
	}
	if !strings.Contains(out.String(), "1 left alone") {
		t.Fatalf("the summary must count it: %q", out.String())
	}
	if got, _ := os.ReadFile(f); string(got) != sourced {
		t.Fatalf("the page was touched:\n%s", got)
	}
	// ⛔ And the SAME refusal without --write, because the run that reports the
	// staleness is the one a reader acts on.
	out.Reset()
	errb.Reset()
	if rc := run(options{org: "go-fleettools", files: []string{f}, today: "2026-11-22"}, &out, &errb, tagOnly); rc != 2 {
		t.Fatalf("rc %d, want 2 in report mode too", rc)
	}
}

func TestAClaimIsNeverPairedWithALinkInAnotherParagraph(t *testing.T) {
	// The link of `alpha` ends one paragraph; the claim with no link of its own
	// starts the next. Crossing the blank line would credit it to `alpha`.
	src := "[Repository →](https://github.com/go-fleettools/alpha)\n\n" +
		"Latest tag: **v0.4.0** (`git ls-remote --tags`, 2026-11-15); orphan claim.\n"
	if cells := Find(src, "go-fleettools", false); len(cells) != 0 {
		t.Fatalf("paired across a paragraph: %+v", cells)
	}
	// And a claim split from its version by a blank line is not a claim.
	src = "[Repository →](https://github.com/go-fleettools/alpha) Latest tag:\n\n**v0.4.0** (`git ls-remote --tags`, 2026-11-15)\n"
	if cells := Find(src, "go-fleettools", false); len(cells) != 0 {
		t.Fatalf("a version across a blank line: %+v", cells)
	}
	// A link AFTER the claim in the same paragraph is not the claim's link, and
	// of two links before it the nearest wins.
	src = "Latest tag: **v0.4.0** then [R](https://github.com/go-fleettools/alpha)\n"
	if cells := Find(src, "go-fleettools", false); len(cells) != 0 {
		t.Fatalf("paired with a link that follows: %+v", cells)
	}
	src = "[R](https://github.com/go-fleettools/alpha) and [R](https://github.com/go-fleettools/beta). Latest tag: **v0.4.0**\n"
	if cells := Find(src, "go-fleettools", false); len(cells) != 1 || cells[0].Module != "beta" {
		t.Fatalf("the nearest link is the claim's: %+v", cells)
	}
	// Two modules, each with its own claim: each gets its own.
	src = "[R](https://github.com/go-fleettools/alpha) Latest tag: **v0.1.0**\n\n" +
		"[R](https://github.com/go-fleettools/beta)\nLatest tag: **v0.2.0**\n"
	cells := Find(src, "go-fleettools", false)
	if len(cells) != 2 || cells[0].Module != "alpha" || cells[0].Tag != "v0.1.0" ||
		cells[1].Module != "beta" || cells[1].Tag != "v0.2.0" || cells[0].hasPar {
		t.Fatalf("%+v", cells)
	}
}

func TestAProseClaimOfAnotherOrgIsLeftAlone(t *testing.T) {
	src := "[R](https://github.com/go-opentype/fonts) Latest tag: **v0.1.0** (`git ls-remote --tags`, 2026-11-15)\n"
	if cells := Find(src, "go-fleettools", false); len(cells) != 0 {
		t.Fatalf("%+v", cells)
	}
}

func TestAMarkdownFileHoldsRowsAndProseAtExactOffsets(t *testing.T) {
	src := startsLine + "\n" + md + "\n" + nextLine
	cells := Find(src, "go-widgets", false)
	if len(cells) != 2 || cells[0].Module != "window" || cells[1].Module != "mvvm" {
		t.Fatalf("go-widgets: %+v", cells)
	}
	cells = Find(src, "go-fleettools", false)
	if len(cells) != 2 || cells[0].Module != "staletools" || cells[1].Module != "prwait" {
		t.Fatalf("go-fleettools: %+v", cells)
	}
	all := append(Find(src, "go-widgets", false), cells...)
	for _, c := range all {
		if src[c.start:c.end] != c.Tag {
			t.Fatalf("%s: offsets point at %q, want %q", c.Module, src[c.start:c.end], c.Tag)
		}
	}
	out, _ := Rewrite(src, Find(src, "go-widgets", false), rel("window", "v0.90.0", "mvvm", "v0.16.0"), "2030-01-01")
	if !strings.Contains(out, "| v0.90.0 |") || !strings.Contains(out, "| v0.16.0 |") || strings.Contains(out, "2030") {
		t.Fatal(out)
	}
}

func TestRunWritesAProseClaimWithItsDate(t *testing.T) {
	f := filepath.Join(t.TempDir(), "staletools.md")
	if err := os.WriteFile(f, []byte(startsLine), 0o640); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	latest := func(_, _ string) (Release, error) { return rel("staletools", "v0.5.0")["staletools"], nil }
	o := options{org: "go-fleettools", files: []string{f}, today: "2026-12-01"}
	if rc := run(o, &out, &errb, latest); rc != 1 || !strings.Contains(out.String(), "1 cell(s)") {
		t.Fatalf("rc %d\n%s", rc, out.String())
	}
	o.write = true
	if rc := run(o, &out, &errb, latest); rc != 0 {
		t.Fatalf("-write rc %d", rc)
	}
	got, _ := os.ReadFile(f)
	want := strings.NewReplacer("**v0.4.0**", "**v0.5.0**", "2026-11-15", "2026-12-01").Replace(startsLine)
	if string(got) != want {
		t.Fatalf("got\n%s", got)
	}
	if st, _ := os.Stat(f); st.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", st.Mode())
	}
}

func TestWriteAtomicNeverTruncatesTheOriginalInPlace(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "page.md")
	if err := os.WriteFile(f, []byte("old page\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias.md") // the same inode: an in-place write would show through
	if err := os.Link(f, link); err != nil {
		t.Skip("no hard links:", err)
	}
	if err := writeAtomic(f, []byte("new page\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(f); string(b) != "new page\n" {
		t.Fatalf("%q", b)
	}
	if b, _ := os.ReadFile(link); string(b) != "old page\n" {
		t.Fatalf("the original inode was rewritten in place: %q", b)
	}
	if st, _ := os.Stat(f); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 2 {
		t.Fatalf("a temporary file was left behind: %v", ents)
	}
}

func TestWriteAtomicFailsCleanly(t *testing.T) {
	dir := t.TempDir()
	if err := writeAtomic(filepath.Join(dir, "missing.md"), []byte("x")); err == nil {
		t.Fatal("writing over a missing file must fail, not create it")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("left behind: %v", ents)
	}
}
