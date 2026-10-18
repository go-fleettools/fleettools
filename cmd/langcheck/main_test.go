package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⛔ THE FIRST CASE IS THE ONE THAT MOTIVATED THIS: a real file in a public
// repository, go-gitsafe/gitsafe's pipestatus_test.go, whose case names and
// comments are French.
func TestItReportsFrenchProseWeWrote(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "pipestatus_test.go", `package pipestatus

// ⛔ Les trois premiers cas sont les formes RÉELLEMENT tapées le 2026-10-11, dans
// la même après-midi, par quelqu'un qui avait relu sa propre note entre deux.
func TestItRefuses(t *testing.T) {
	for _, c := range []struct{ name, cmd string }{
		{"après go build | tail (tel quel)", "a | b"},
		{"le tuyau est deux statements plus haut, séparés par du vide", "a | b"},
	} {
		_ = c
	}
}
`)
	out, code := langcheck(t, "-v", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1; output:\n%s", code, out)
	}
	if !strings.Contains(out, "pipestatus_test.go") {
		t.Errorf("the file is not named:\n%s", out)
	}
	// Both halves: the comment AND the case names in string literals.
	if n := strings.Count(out, "pipestatus_test.go:"); n < 2 {
		t.Errorf("only %d block(s) reported; a comment and a case name are two: \n%s", n, out)
	}
}

// ⭐ The half that keeps the tool from being ignored. Every case here would be
// named by a word-list grep, and every one of them is correct work.
func TestItLeavesAloneWhatIsNotOurProse(t *testing.T) {
	dir := t.TempDir()

	// A French sentence as DATA in a non-test file: a fixture, a message being
	// parsed, a corpus string. Not a comment, so not ours to translate.
	write(t, dir, "parse.go", `package p

// Parse reads a line of the input.
func Parse(s string) string {
	const sample = "Le petit chat dort dans la maison avec les enfants, qui ne font rien."
	return sample + s
}
`)
	// A French document under test, which is the SUBJECT.
	writeIn(t, dir, filepath.Join("testdata", "lettre.md"), `# Une lettre

Cette lettre est écrite en français pour que le moteur puisse la composer, avec
des accents et tout ce qui va avec.
`)
	// Markdown prose in English that shows a French transcript in a fence.
	writeIn(t, dir, "README.md", "# Thing\n\nIt reads a document and typesets it.\n\n```\nqui que dans pour avec cette donc alors\n```\n")
	// A worktree copy of the same repository.
	writeIn(t, dir, filepath.Join("thing-worktrees", "a.go"), `package p

// Ceci est un commentaire en français qui ne doit pas être compté deux fois.
func A() {}
`)
	// ⛔ And the spelling that was MISSED: per-session agent worktrees live
	// under `.claude/worktrees/<name>/`, so the directory is called `worktrees`
	// and not `*-worktrees`. The first reading of go-tex/engine counted the
	// same file at the same line ten times because of exactly this.
	writeIn(t, dir, filepath.Join(".claude", "worktrees", "branch-a", "b.go"), `package p

// Le même commentaire français, dans un worktree de session, qui ne doit pas
// gonfler le compte une fois par branche.
func B() {}
`)
	writeIn(t, dir, filepath.Join("worktrees", "branch-b", "c.go"), `package p

// Et la même chose avec un répertoire nommé worktrees tout court, parce que
// les deux orthographes existent dans cette flotte.
func C() {}
`)
	// ⛔ A DELIBERATE TRANSLATION. Measured on go-fileshare/docs: 510 of 884
	// findings were this, which is a tool nobody runs twice.
	writeIn(t, dir, filepath.Join("content", "fr", "guide.md"), `# Guide

Cette page est la traduction française du guide, avec tout ce qui va avec, et
elle ne doit pas être signalée parce qu'elle est voulue.
`)
	writeIn(t, dir, filepath.Join("content", "pt-br", "guia.md"), `# Guia

Esta pagina é uma traducao que nao deve ser reportada, com cette forme de code
de langue qui porte une region.
`)

	out, code := langcheck(t, "-v", dir)
	if code != 0 {
		t.Errorf("exit %d, want 0 — something was wrongly reported:\n%s", code, out)
	}
	if !strings.Contains(out, "every block reads as English") {
		t.Errorf("expected a clean verdict:\n%s", out)
	}
}

// ⭐ English under `content/en` is the prose this tool exists to read, and a
// language-code directory anywhere ELSE is code. Both directions, because the
// skip rule is structural and a structural rule can over-reach.
func TestOnlyATranslationDirectoryIsSkipped(t *testing.T) {
	dir := t.TempDir()
	// content/en is READ: its French would be a real finding.
	writeIn(t, dir, filepath.Join("content", "en", "guide.md"), `# Guide

Cette page est en français alors qu'elle est dans le répertoire anglais, ce qui
est exactement le défaut que cet outil doit nommer.
`)
	// pkg/fr is CODE, not a translation: `fr` is only a language here when it
	// sits directly under content, i18n, locales or lang.
	writeIn(t, dir, filepath.Join("pkg", "fr", "a.go"), `package fr

// Ce commentaire est du français dans du code, qui doit être signalé comme
// tout le reste du code de la flotte.
func A() {}
`)
	out, code := langcheck(t, "-v", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	for _, want := range []string{filepath.Join("content", "en", "guide.md"), filepath.Join("pkg", "fr", "a.go")} {
		if !strings.Contains(out, want) {
			t.Errorf("%s was skipped but is not a translation:\n%s", want, out)
		}
	}
}

// ⛔ A check that read nothing passes everything, so it has to say what it read.
func TestItSaysWhatItRead(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.go", "package p\n\n// A does a thing.\nfunc A() {}\n")
	out, code := langcheck(t, dir)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "read 1 file(s)") {
		t.Errorf("it does not say how many files it read:\n%s", out)
	}
	// And an empty tree must say ZERO rather than passing quietly.
	out, code = langcheck(t, t.TempDir())
	if code != 0 {
		t.Fatalf("exit %d on an empty tree:\n%s", code, out)
	}
	if !strings.Contains(out, "read 0 file(s)") {
		t.Errorf("an empty tree must report zero files, not pass silently:\n%s", out)
	}
}

// The threshold is what separates French prose from an English block that
// quotes a French word in passing.
func TestTheThresholdSeparatesAQuoteFromProse(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.go", `package p

// A is named after the French "que", which is one word and not a sentence.
func A() {}
`)
	if out, code := langcheck(t, dir); code != 0 {
		t.Errorf("one French word is not French prose: exit %d\n%s", code, out)
	}
	// Two is the default threshold, and this has four.
	write(t, dir, "b.go", `package p

// B fait ce que les autres ne font pas, dans le cas qui nous occupe.
func B() {}
`)
	out, code := langcheck(t, dir)
	if code != 1 {
		t.Errorf("four French words in one comment is prose: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "b.go") {
		t.Errorf("b.go is not named:\n%s", out)
	}
	// And -min is honoured: raising it past the count clears the report.
	if out, code := langcheck(t, "-min", "30", dir); code != 0 {
		t.Errorf("-min 30 should clear it: exit %d\n%s", code, out)
	}
}

// ⭐ An accent counts as ONE signal, which is what makes a short block
// decidable — and one signal on its own is not a report, or every `café` in an
// English comment would be named.
func TestAnAccentIsOneSignalAndNotAVerdict(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.go", `package p

// A writes the café order to the résumé, which is English prose with loanwords.
func A() {}
`)
	if out, code := langcheck(t, "-v", dir); code != 0 {
		t.Errorf("loanwords alone are not French prose: exit %d\n%s", code, out)
	}
	// One French word AND an accent reaches the threshold, which is the case a
	// six-word test name gives you.
	write(t, dir, "b_test.go", `package p

func TestX(t *testing.T) {
	cases := []string{"après le build, tel quel"}
	_ = cases
}
`)
	out, code := langcheck(t, "-v", dir)
	if code != 1 {
		t.Errorf("a French case name must be reported: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "·accent") {
		t.Errorf("the accent signal is not shown, so a reader cannot disagree with it:\n%s", out)
	}
}

// ⛔ -min 0 would report every block, which is the defect this tool exists
// against rather than a setting.
func TestItRefusesAThresholdOfZero(t *testing.T) {
	if _, code := langcheck(t, "-min", "0", t.TempDir()); code != 2 {
		t.Errorf("exit %d, want 2 for -min 0", code)
	}
}

// ⭐ The ambiguous words are left out ON PURPOSE, and this says so in a test so
// that adding one is a decision rather than an accident.
func TestEnglishThatLooksFrenchIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.go", `package p

// A signal on a page: note the mode and the format of the image, plus a point
// about an important patient record, a par score, and his son.
func A() {}
`)
	if out, code := langcheck(t, "-v", dir); code != 0 {
		t.Errorf("English words that are also French must not be counted: exit %d\n%s", code, out)
	}
}

func langcheck(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return out.String() + errb.String(), code
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	writeIn(t, dir, name, body)
}

func writeIn(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
