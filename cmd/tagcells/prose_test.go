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
		if len(cells) != 1 || cells[0].Module != c.module || cells[0].Tag != c.tag || !cells[0].hasDate {
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
		out := Rewrite(src, cells, map[string]string{cells[0].Module: "v0.99.0"}, "2026-12-01")
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
	if out := Rewrite(startsLine, cells, map[string]string{"staletools": "v0.4.0"}, "2026-12-01"); out != startsLine {
		t.Fatalf("a current claim keeps its date:\n%s", out)
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
		cells[1].Module != "beta" || cells[1].Tag != "v0.2.0" || cells[0].hasDate {
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
	out := Rewrite(src, Find(src, "go-widgets", false), map[string]string{"window": "v0.90.0", "mvvm": "v0.16.0"}, "2030-01-01")
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
	latest := func(_, _ string) (string, error) { return "v0.5.0", nil }
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
