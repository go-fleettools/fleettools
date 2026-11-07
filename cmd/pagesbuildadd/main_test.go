package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	c := RootCmd()
	c.SetArgs(args)
	c.SetOut(&out)
	c.SetErr(&out)
	err := c.Execute()
	return out.String(), err
}

func site(t *testing.T, golden string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "pagesroll", "testdata", golden))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, ".github", "workflows", "docs.yml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEditsThenReportsAlready(t *testing.T) {
	dir := site(t, "before.yml")
	out, err := run(t, "--actionlint", "", dir)
	if err != nil || !strings.Contains(out, "edited") {
		t.Fatalf("%q %v", out, err)
	}
	out, err = run(t, "--actionlint", "", dir)
	if err != nil || !strings.Contains(out, "already") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestRefusesAnUnknownShape(t *testing.T) {
	dir := site(t, "before.yml")
	p := filepath.Join(dir, ".github", "workflows", "docs.yml")
	b, _ := os.ReadFile(p)
	bad := strings.Replace(string(b), "name: Push gh-pages", "name: Push", 1)
	if err := os.WriteFile(p, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "--actionlint", "", dir); err == nil {
		t.Fatal("expected a refusal")
	}
	if b, _ := os.ReadFile(p); string(b) != bad {
		t.Fatal("a refused file was modified")
	}
}

func TestMainExitsOne(t *testing.T) {
	code := -1
	osExit = func(c int) { code = c }
	old := os.Args
	defer func() { os.Args = old }()
	os.Args = []string{"pagesbuildadd", filepath.Join(t.TempDir(), "nothing")}
	main()
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
}
