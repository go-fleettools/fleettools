package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// externalDir copies the vendored real landing (testdata/external-commonmark,
// see PROVENANCE.txt) into a temp dir.
func externalDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	root := "testdata/external-commonmark"
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(p) == "PROVENANCE.txt" {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(d, rel)), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(d, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(d, "static"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "static/favicon.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseExternal(t *testing.T) {
	d := externalDir(t)
	src := readFile(t, filepath.Join(d, "layouts/index.html"))
	css := readFile(t, filepath.Join(d, "static/css/brand.css"))
	if !isExternal(src) {
		t.Fatal("not recognised as external")
	}
	l, err := parseExternal(src, css)
	if err != nil {
		t.Fatal(err)
	}
	if l.H1 != "go-commonmark" || l.Tagline != "Pure-Go CommonMark." || len(l.CTAs) != 2 {
		t.Fatalf("hero read as %+v", l)
	}
	if !strings.Contains(l.Main, "What is go-commonmark?") || !strings.Contains(l.Main, `class="grid"`) || strings.Contains(l.Main, `class="wrap"`) {
		t.Errorf("main: %s", l.Main)
	}
	if !strings.Contains(l.Footer, "go-commonmark/brand") {
		t.Errorf("footer: %q", l.Footer)
	}
	for _, gone := range []string{".hero", ".btns", ".theme", "body"} {
		if strings.Contains(l.Style, gone+"{") || strings.Contains(l.Style, gone+",") {
			t.Errorf("style still has %s rules", gone)
		}
	}
	if !strings.Contains(l.Style, ".card") {
		t.Error("the card rules are lost")
	}
	// something the tool does not know is a refusal, never dropped
	if _, err := parseExternal(strings.Replace(src, "</body>", "<aside>x</aside></body>", 1), css); err == nil {
		t.Error("an unknown element in the body was dropped")
	}
}

func TestStylesheetBrand(t *testing.T) {
	css := readFile(t, "testdata/external-commonmark/static/css/brand.css")
	if f, to := stylesheetBrand(css); f != "#22cce2" || to != "#0079a8" {
		t.Errorf("from %s to %s", f, to)
	}
	if err := checkBrand("[params.brand]\n  base = \"#0079a8\"\n  bright = \"#22cce2\"\n", css); err != nil {
		t.Error(err)
	}
	// go-commonmark's own logo is slate: the real disagreement
	err := checkBrand("[params.brand]\n  base = \"#0f172a\"\n  bright = \"#475569\"\n", css)
	if err == nil || !strings.Contains(err.Error(), "disagrees") {
		t.Errorf("slate logo against cyan page passed: %v", err)
	}
}

func fakeBrand(base, bright string) func(string, ...string) ([]byte, error) {
	return func(dir string, args ...string) ([]byte, error) {
		if args[0] == "mod" {
			return nil, os.WriteFile(filepath.Join(dir, "go.sum"), []byte("sum\n"), 0o644)
		}
		if args[0] == "get" {
			return nil, nil
		}
		if args[0] == "run" {
			return []byte("[params.brand]\n  base = \"" + base + "\"\n  bright = \"" + bright + "\"\n"), nil
		}
		return nil, errors.New("unexpected go " + strings.Join(args, " "))
	}
}

func TestMigrateExternal(t *testing.T) {
	listRepos = func(string) ([]string, error) { return []string{"commonmark"}, nil }
	// the logo and the page disagree: refused before any write
	goCmd = fakeBrand("#0f172a", "#475569")
	d := externalDir(t)
	if _, err := migrate(d, "go-commonmark", "v0.28.0", nil); err == nil || !strings.Contains(err.Error(), "disagrees") {
		t.Fatalf("migrated a landing whose brand disagrees: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d, "static/css/brand.css")); err != nil {
		t.Error("a refusal changed the checkout")
	}
	if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
		t.Error("a refusal wrote go.mod")
	}
	// they agree
	goCmd = fakeBrand("#0079a8", "#22cce2")
	d = externalDir(t)
	r, err := migrate(d, "go-commonmark", "v0.28.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Shape != "external-stylesheet" {
		t.Errorf("shape %q", r.Shape)
	}
	for _, gone := range []string{"static/css/brand.css", "static/js/theme.js"} {
		if _, err := os.Stat(filepath.Join(d, gone)); err == nil {
			t.Errorf("%s kept", gone)
		}
	}
	idx := readFile(t, filepath.Join(d, "layouts/index.html"))
	for _, want := range []string{`partial "styles.html"`, `class="legacy"`, "What is go-commonmark?", "{{ len site.Data.pkgs.packages }}", `partial "fork-ribbon.html"`} {
		if !strings.Contains(idx, want) {
			t.Errorf("index lacks %q", want)
		}
	}
	for _, not := range []string{"brand.css", "theme.js", "themebtn"} {
		if strings.Contains(idx, not) {
			t.Errorf("index still has %q", not)
		}
	}
	conf := readFile(t, filepath.Join(d, "hugo.toml"))
	if !strings.Contains(conf, "[params.brand]") || !strings.Contains(conf, "[module]") {
		t.Errorf("hugo.toml: %s", conf)
	}
}

func TestCtaToml(t *testing.T) {
	got := ctaToml([]cta{
		{Href: "https://go-commonmark.github.io/docs/", Inner: `{{ partial "icons/book.html" . }}Documentation`},
		{Href: "https://acme.github.io/playground/", Inner: "Try it"},
		{Href: "https://github.com/acme", Inner: `{{ partial "icons/mark-github.html" . }}GitHub`},
	})
	for _, want := range []string{"kind  = \"docs\"", "kind  = \"try\"", "kind  = \"github\"", "label = \"Documentation\"", "label = \"GitHub\""} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q\n%s", want, got)
		}
	}
}
