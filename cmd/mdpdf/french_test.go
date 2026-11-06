package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-pdfkit/extract"
)

func TestFrenchText(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"group":          {"soit 70 000 par mois", "soit 70 000 par mois"},
		"two groups":     {"10 000 000", "10 000 000"},
		"currency":       {"70 000 $", "70 000 $"},
		"euro":           {"11 500 €.", "11 500 €."},
		"percent":        {"20 %", "20 %"},
		"unit":           {"5 kg", "5 kg"},
		"year not group": {"en 2025 100 fois", "en 2025 100 fois"},
		"digit after":    {"70 0001", "70 0001"},
		"article l'":     {"3 l'année", "3 l'année"},
		"word after":     {"3 mois", "3 mois"},
		"colon":          {"stricte : les", "stricte : les"},
		"semicolon":      {"a ; b", "a ; b"},
		"bang, query":    {"Quoi ? Non !", "Quoi ? Non !"},
		"guillemets":     {"« bon » dit", "« bon » dit"},
		"emoji colon":    {"x :smile:", "x :smile:"},
		"plain":          {"rien à faire ici", "rien à faire ici"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := frenchText(tc.in); got != tc.want {
				t.Errorf("frenchText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFrenchTypographySkipsCodeAndAttributes(t *testing.T) {
	in := `<p>70 000 $ : <code>70 000 $ : x</code> <a href="/a b : c" title="70 000 $">70 000</a></p><pre>70 000 : y</pre>`
	got, err := frenchTypography(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{`<code>70 000 $ : x</code>`, `<pre>70 000 : y</pre>`, `href="/a b : c"`, `title="70 000 $"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("output %q altered %q", got, keep)
		}
	}
	if !strings.Contains(got, "<p>70 000 $ : ") {
		t.Errorf("prose not rewritten: %q", got)
	}
}

const frenchRepro = "Le coût mensuel est le nombre de sièges de chaque type multiplié par son prix, soit pour cette population 70 000 $ par mois et 840 000 $ par an : la comparaison n'est pas stricte.\n\n" +
	"| Scénario | Coût |\n| --- | --- |\n| haut (240 000 $) | 11 500 $ par an |\n"

var (
	endsInDigit  = regexp.MustCompile(`[0-9]$`)
	startsGroup  = regexp.MustCompile(`^[0-9]{3}(\s|$|[^0-9])`)
	startsHigh   = regexp.MustCompile(`^[:;!?»$%]`)
	endsOpenQuot = regexp.MustCompile(`«$`)
)

// brokenAt returns a description of every place where a line of the extracted
// text ends inside a number or before a high punctuation mark.
func brokenAt(text string) []string {
	var bad []string
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); i++ {
		a, b := strings.TrimSpace(lines[i]), strings.TrimSpace(lines[i+1])
		if a == "" || b == "" {
			continue
		}
		if endsInDigit.MatchString(a) && startsGroup.MatchString(b) || startsHigh.MatchString(b) || endsOpenQuot.MatchString(a) {
			bad = append(bad, fmt.Sprintf("%q / %q", a, b))
		}
	}
	return bad
}

// sweep renders the repro in columns of 160..460 px and returns the line
// breaks that fall inside a number or before high punctuation.
func sweep(t *testing.T, lang string) []string {
	t.Helper()
	dir := t.TempDir()
	in := write(t, dir, "in.md", frenchRepro)
	var bad []string
	for w := 160; w <= 460; w += 4 {
		css := write(t, dir, "w.css", fmt.Sprintf("body{width:%dpx !important;max-width:%dpx !important}", w, w))
		out := filepath.Join(dir, "o.pdf")
		args := []string{"--in", in, "--out", out, "--css", css}
		if lang != "" {
			args = append(args, "--lang", lang)
		}
		if err, msg := exec(t, args...); err != nil {
			t.Fatalf("%v: %s", err, msg)
		}
		doc := open(t, out)
		for p := 1; p <= doc.PageCount(); p++ {
			s, err := extract.Text(doc, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range brokenAt(s) {
				bad = append(bad, fmt.Sprintf("width %d: %s", w, b))
			}
		}
	}
	return bad
}

// TestFrenchNumbersAndHighPunctuationDoNotBreak is the witness for the
// defect: with --lang fr no line ends inside a digit group or before ":".
// The control (no --lang) must show the same sweep DOES find such breaks,
// otherwise the sweep could not see the defect it guards against.
func TestFrenchNumbersAndHighPunctuationDoNotBreak(t *testing.T) {
	if bad := sweep(t, "fr"); len(bad) > 0 {
		t.Errorf("--lang fr: %d bad breaks, first: %v", len(bad), bad[:min(3, len(bad))])
	}
	if bad := sweep(t, ""); len(bad) == 0 {
		t.Error("control without --lang: no bad break found, the sweep is blind")
	}
}
