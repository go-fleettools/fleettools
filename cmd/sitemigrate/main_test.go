package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, d, rel, s string) {
	t.Helper()
	p := filepath.Join(d, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, d, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(d, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := RootCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func site(t *testing.T, yaml string) string {
	d := t.TempDir()
	put(t, d, "layouts/_partials/custom/version-select.html", "x")
	put(t, d, "hugo.yaml", yaml)
	put(t, d, ".github/workflows/docs.yml", "run: sh themes/hextra/scripts/page-history.sh > data/pagehistory.json\n")
	put(t, d, "assets/css/custom.css", "a{}\n.fs-version-select .x { left: auto; }\nb{}\n")
	put(t, d, "README.md", "| `layouts/_partials/custom/version-select.html` | sel |\nkeep\n")
	put(t, d, ".gitignore", "# Written by themes/hextra/scripts/page-history.sh\n")
	return d
}

func TestMigrate(t *testing.T) {
	d := site(t, "partial: custom/version-select.html\nversions:\n  development: main\n# themes/hextra/scripts/page-history.sh\n")
	out, err := run(t, "--tag", "v0.21.0", d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "removed layouts/_partials/custom/version-select.html") {
		t.Errorf("output: %s", out)
	}
	for rel, want := range map[string]string{
		"hugo.yaml":                  "partial: components/version-select.html",
		".github/workflows/docs.yml": "go run github.com/tannevaled/hextra/cmd/pagehistory@v0.21.0 > data/pagehistory.json",
		"assets/css/custom.css":      "a{}\nb{}",
		"README.md":                  "keep",
		".gitignore":                 "the theme's cmd/pagehistory",
	} {
		if got := get(t, d, rel); !strings.Contains(got, want) {
			t.Errorf("%s lacks %q:\n%s", rel, want, got)
		}
	}
	if _, err := os.Stat(filepath.Join(d, "layouts/_partials/custom/version-select.html")); err == nil {
		t.Error("local copy kept")
	}
	if strings.Contains(get(t, d, "README.md"), "custom/version-select.html") {
		t.Error("README row kept")
	}
}

func TestAddsDevelopmentDefault(t *testing.T) {
	d := site(t, "title: t\n\nparams:\n  a: 1\n# see layouts/_partials/custom/version-select.html\npartial: custom/version-select.html\n")
	if _, err := run(t, "--tag", "v0.21.0", d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(get(t, d, "hugo.yaml"), `development: "dev"`) {
		t.Error("development not added")
	}
}

func TestRefusals(t *testing.T) {
	// no params: block to hold versions.development
	if _, err := run(t, "--tag", "v0.21.0", site(t, "partial: custom/version-select.html\n")); err == nil {
		t.Error("accepted a site without a params: block")
	}
	// two menu items
	if _, err := run(t, "--tag", "v0.21.0", site(t, "partial: custom/version-select.html\npartial: custom/version-select.html\ndevelopment: x\n")); err == nil {
		t.Error("accepted two menu items")
	}
	// a multi-line custom.css rule
	d := site(t, "partial: custom/version-select.html\ndevelopment: x\n")
	put(t, d, "assets/css/custom.css", ".fs-version-select .x {\n left: 0;\n}\n")
	if _, err := run(t, "--tag", "v0.21.0", d); err == nil {
		t.Error("accepted a multi-line rule")
	}
	// tag shape and arguments
	if _, err := run(t, "--tag", "latest", t.TempDir()); err == nil {
		t.Error("accepted a floating tag")
	}
	if _, err := run(t, t.TempDir()); err == nil {
		t.Error("accepted a missing --tag")
	}
	if _, err := run(t, "--tag", "v0.21.0"); err == nil {
		t.Error("accepted a missing dir")
	}
}

func TestIdempotentRefusesMigratedTree(t *testing.T) {
	d := site(t, "partial: custom/version-select.html\ndevelopment: x\n")
	if _, err := run(t, "--tag", "v0.21.0", d); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "--tag", "v0.21.0", d); err == nil {
		t.Error("second run did not refuse")
	}
}
