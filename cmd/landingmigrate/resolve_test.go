package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ctaDir(t *testing.T, name string) string {
	t.Helper()
	d := t.TempDir()
	for src, dst := range map[string]string{"index.html": "layouts/index.html", "hugo.toml": "hugo.toml", "go.mod.txt": "go.mod"} {
		b, err := os.ReadFile(filepath.Join("testdata", name, src))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(d, dst)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, dst), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// Two real landings whose hero buttons are built from expressions that only read
// hugo.toml (testdata/cta-attest/PROVENANCE.txt). Before, `upgrade` refused them;
// now the title and the params are written as the literals hugo.toml holds.
func TestUpgradeResolvesButtonsThatReadHugoToml(t *testing.T) {
	goCmd = fakeGo("")
	// go-keyring: secondary_url and secondary_label are set
	d := ctaDir(t, "cta-keyring")
	if _, err := upgrade(d, "v0.29.0", nil); err != nil {
		t.Fatal(err)
	}
	conf := readFile(t, filepath.Join(d, "hugo.toml"))
	for _, want := range []string{`href  = "https://github.com/go-keyring"`, `label = "GitHub"`, `href  = "https://pkg.go.dev/github.com/go-keyring/keyring"`, `label = "Go reference"`} {
		if !strings.Contains(conf, want) {
			t.Errorf("go-keyring hugo.toml lacks %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "{{") {
		t.Error("an expression was written into [[params.cta]]")
	}
	// go-attest: secondary_label is not set, so the second button never had words
	// (and linked to the runner's content file): only the GitHub button remains
	d = ctaDir(t, "cta-attest")
	if _, err := upgrade(d, "v0.29.0", nil); err != nil {
		t.Fatal(err)
	}
	conf = readFile(t, filepath.Join(d, "hugo.toml"))
	if !strings.Contains(conf, `href  = "https://github.com/go-attest"`) || strings.Count(conf, "[[params.cta]]") != 1 {
		t.Errorf("go-attest hugo.toml:\n%s", conf)
	}
}

func TestResolveCTAsKeepsWhatItCannotRead(t *testing.T) {
	got := resolveCTAs([]cta{{Href: "{{ .Page.RelPermalink }}", Inner: "Docs"}}, `title = "x"`)
	if len(got) != 1 || !strings.Contains(got[0].Href, "{{") {
		t.Errorf("an expression that is not a read of hugo.toml was rewritten: %+v", got)
	}
}

// go-commonmark's logo is slate and its page cyan: refused unless --follow-logo,
// which takes the logo's palette and says so.
func TestMigrateFollowLogo(t *testing.T) {
	listRepos = func(string) ([]string, error) { return []string{"commonmark"}, nil }
	goCmd = fakeBrand("#0f172a", "#475569")
	d := externalDir(t)
	if _, err := migrate(d, "go-commonmark", "v0.28.0", nil); err == nil {
		t.Fatal("migrated a landing whose brand disagrees")
	}
	followLogo = true
	defer func() { followLogo = false }()
	d = externalDir(t)
	r, err := migrate(d, "go-commonmark", "v0.28.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "replaced by the palette derived from the logo") {
		t.Errorf("notes: %v", r.Notes)
	}
	if _, err := os.Stat(filepath.Join(d, "static/css/brand.css")); err == nil {
		t.Error("brand.css stays")
	}
	if !strings.Contains(readFile(t, filepath.Join(d, "hugo.toml")), "#0f172a") {
		t.Error("the logo's palette is not in hugo.toml")
	}
}
