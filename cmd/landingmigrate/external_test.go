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

// The vendored real landing's tagline is "Pure-Go CommonMark.": the kit's check
// refuses a hero sub-text that says it again, so the rendered layout must say it
// only without the strip.
func TestTaglineRepeatsTheStrip(t *testing.T) {
	d := externalDir(t)
	l, err := parseExternal(readFile(t, filepath.Join(d, "layouts/index.html")), readFile(t, filepath.Join(d, "static/css/brand.css")))
	if err != nil {
		t.Fatal(err)
	}
	index, _ := render(l, "go-commonmark")
	want := `<p class="sub">{{ if partial "mantra-on.html" . }}Go{{ else }}Pure-Go{{ end }} CommonMark.</p>`
	if !strings.Contains(index, want) {
		t.Errorf("the hero sub-text does not hide its repeat of the strip:\n%s", index)
	}
	for in, want := range map[string]string{
		"reStructuredText in pure Go.": `reStructuredText in {{ if partial "mantra-on.html" . }}Go{{ else }}pure Go{{ end }}.`,
		"A parser, cgo-free.":          `A parser, {{ if not (partial "mantra-on.html" $) }}cgo-free{{ end }}.`,
		"Says {{ .x }} pure Go":        "Says {{ .x }} pure Go", // a Hugo action: left as written
		"Plain words.":                 "Plain words.",
	} {
		if got := wrapTagline(in); got != want {
			t.Errorf("wrapTagline(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

// nano-container-linux's second button is `{{ with .Site.Params.secondary_url }}
// <a class="btn ghost" href="{{ . }}">…{{ $.Site.Params.secondary_label }}</a>{{ end }}`:
// [[params.cta]] cannot hold it, so the migration is refused before any write
// (it once wrote href = "{{ . }}" and label = "", which fails the Hugo build).
func TestMigrateRefusesAButtonBuiltFromAnExpression(t *testing.T) {
	goCmd = fakeGo("")
	d := landingDir(t)
	idx := filepath.Join(d, "layouts/index.html")
	src := strings.Replace(readFile(t, idx), `</div>
    <div class="badges">`, `{{ with .Page.Params.docs }}<a class="btn ghost" href="{{ . }}">{{ $.Page.Title }}</a>{{ end }}</div>
    <div class="badges">`, 1)
	if !strings.Contains(src, "Page.Params.docs") {
		t.Fatal("the fixture did not take the expression button")
	}
	if err := os.WriteFile(idx, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := migrate(d, "acme", "v0.6.0", nil)
	if err == nil || !strings.Contains(err.Error(), "Hugo expression") {
		t.Fatalf("migrated a button built from an expression: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(d, "go.mod")); serr == nil {
		t.Error("a refusal wrote go.mod")
	}
}

func kitDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	root := "testdata/kit-aasm"
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(p) == "PROVENANCE.txt" {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "go.mod.txt" {
			rel = "go.mod"
		}
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
	return d
}

func TestUpgradeARealKitLanding(t *testing.T) {
	goCmd = fakeGo("")
	d := kitDir(t)
	did, err := upgrade(d, "v0.29.0", []string{"amd64", "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	idx := readFile(t, filepath.Join(d, "layouts/index.html"))
	conf := readFile(t, filepath.Join(d, "hugo.toml"))
	for _, want := range []string{`{{ partial "cta.html" . }}`, `{{ if partial "mantra-on.html" . }}Go{{ else }}Pure-Go{{ end }}`} {
		if !strings.Contains(idx, want) {
			t.Errorf("index lacks %q", want)
		}
	}
	if strings.Contains(idx, `<div class="cta"`) {
		t.Error("the hand-written row is still there")
	}
	for _, want := range []string{"[[params.cta]]", `kind  = "docs"`, `kind  = "github"`, `goarch = ["amd64", "arm64"]`, "[params.mantra]"} {
		if !strings.Contains(conf, want) {
			t.Errorf("hugo.toml lacks %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(d, ".github/workflows/landingkit-check.yml")); err != nil {
		t.Errorf("no check workflow: %v (%v)", err, did)
	}
	// twice is the same as once: the row is the kit's now, goarch is there
	if _, err := upgrade(d, "v0.29.0", []string{"amd64", "arm64"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(d, "hugo.toml")); got != conf {
		t.Error("a second upgrade changed hugo.toml")
	}
}

func TestUpgradeDropsAnExplicitCommonsCallAndRefuses(t *testing.T) {
	goCmd = fakeGo("")
	d := kitDir(t)
	idx := filepath.Join(d, "layouts/index.html")
	src := strings.Replace(readFile(t, idx), `{{ partial "fork-ribbon.html" "https://github.com/go-ruby-aasm" }}`, `{{ partial "fork-ribbon.html" "https://github.com/go-ruby-aasm" }}{{ partial "commons-ribbon.html" . }}`, 1)
	if err := os.WriteFile(idx, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := upgrade(d, "v0.29.0", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(t, idx), "commons-ribbon.html") {
		t.Error("the explicit call stays, and would double the band")
	}
	// a button built from an expression is refused before any write
	d = kitDir(t)
	idx = filepath.Join(d, "layouts/index.html")
	src = strings.Replace(readFile(t, idx), `href="/docs/">{{ partial "icons/book.html" . }}Documentation`, `href="{{ .Site.Params.docs }}">{{ partial "icons/book.html" . }}Documentation`, 1)
	if err := os.WriteFile(idx, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, filepath.Join(d, "hugo.toml"))
	if _, err := upgrade(d, "v0.29.0", nil); err == nil {
		t.Fatal("upgraded a button built from an expression")
	}
	if readFile(t, filepath.Join(d, "hugo.toml")) != before {
		t.Error("a refusal changed hugo.toml")
	}
}
