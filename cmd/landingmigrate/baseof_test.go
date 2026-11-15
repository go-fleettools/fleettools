package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func baseofDir(t *testing.T, name string) string {
	t.Helper()
	d := t.TempDir()
	root := filepath.Join("testdata", name)
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
	return d
}

// Two real landings of the baseof shape (testdata/baseof-*/PROVENANCE.txt): they
// were refused ("no <head> or no <body>"). The hero's title, tagline, intro and
// pills stay as the landing wrote them; the top bar, the band, the button row,
// the strip and the footer are the kit's.
func TestMigrateBaseof(t *testing.T) {
	goCmd = fakeBrand("#0891b2", "#22cce2")
	listRepos = func(string) ([]string, error) { return nil, nil }
	for name, org := range map[string]string{"baseof-gfx": "go-gfx", "baseof-typeset": "go-typeset"} {
		t.Run(name, func(t *testing.T) {
			d := baseofDir(t, name)
			r, err := migrate(d, org, "v0.32.0", nil)
			if err != nil {
				t.Fatal(err)
			}
			if r.Shape != "baseof" || len(r.Notes) < 1 || !strings.Contains(r.Notes[0], "replaced by the palette derived from the logo") {
				t.Fatalf("shape %q notes %v", r.Shape, r.Notes)
			}
			idx := readFile(t, filepath.Join(d, "layouts/index.html"))
			for _, want := range []string{`partial "styles.html"`, `partial "fork-ribbon.html"`, `partial "cta.html"`, `<main id="repos" class="legacy">`, `<h1 class="head">`} {
				if !strings.Contains(idx, want) {
					t.Errorf("index lacks %q", want)
				}
			}
			for _, not := range []string{`define "main"`, "css/main.css", `class="hero-logo"`, `id="theme-toggle"`} {
				if strings.Contains(idx, not) {
					t.Errorf("index still has %q", not)
				}
			}
			for _, gone := range baseofFiles {
				if _, err := os.Stat(filepath.Join(d, gone)); err == nil {
					t.Errorf("%s kept", gone)
				}
			}
			conf := readFile(t, filepath.Join(d, "hugo.toml"))
			if !strings.Contains(conf, "[params.brand]") || !strings.Contains(conf, "[module]") {
				t.Errorf("hugo.toml:\n%s", clip(conf))
			}
		})
	}
	// the hero's data is kept: go-gfx's heroAccent and intro paragraphs
	d := baseofDir(t, "baseof-gfx")
	if _, err := migrate(d, "go-gfx", "v0.32.0", nil); err != nil {
		t.Fatal(err)
	}
	idx := readFile(t, filepath.Join(d, "layouts/index.html"))
	if !strings.Contains(idx, ".Site.Params.heroAccent") || !strings.Contains(idx, ".Site.Params.heroIntro") {
		t.Error("the hero's params were dropped")
	}
	// go-gfx's string param `brand` would be a key of the kit's [params.brand] table
	conf := readFile(t, filepath.Join(d, "hugo.toml"))
	if !strings.Contains(conf, "brandName") || strings.Contains(idx, ".Site.Params.brand ") {
		t.Errorf("the string param brand collides with [params.brand]:\n%s", clip(conf))
	}
}
