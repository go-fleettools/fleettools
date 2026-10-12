package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const md = "| Module | Tag |\n|---|---|\n" +
	"| [`window`](https://github.com/go-widgets/window) | v0.89.0 | [a native window](x) |\n" +
	"| [`mvvm`](https://github.com/go-widgets/mvvm) | v0.15.0 | observables |\n" +
	"| [`fonts`](https://github.com/go-opentype/fonts) | v0.1.0 | another org's |\n" +
	"| [`alias`](https://github.com/go-widgets/window) | v0.1.0 | name and repo differ |\n"

const toml = `[[params.modules]]
  name = "window"
  layer = "surface"
  tag = "v0.89.0"
[[params.surfaces]]
  name = "Linux"
  tag = "v9.9.9"
[[params.modules]]
  name = "mvvm"
  tag = "v0.15.0"
`

func TestFindMarkdownRowsOfTheOrgOnly(t *testing.T) {
	cells := Find(md, "go-widgets", false)
	if len(cells) != 2 || cells[0].Module != "window" || cells[0].Line != 3 || cells[1].Tag != "v0.15.0" {
		t.Fatalf("%+v", cells)
	}
}

func TestFindTOMLKeepsATagWithItsOwnName(t *testing.T) {
	cells := Find(toml, "go-widgets", true)
	got := []string{}
	for _, c := range cells {
		got = append(got, c.Module+"@"+c.Tag)
	}
	// Linux's tag line belongs to a [[params.surfaces]] block whose name is
	// not a module... and it IS found, because a block with a name and a tag
	// is the shape; the lookup for "Linux" then fails, which run reports.
	if strings.Join(got, " ") != "window@v0.89.0 Linux@v9.9.9 mvvm@v0.15.0" {
		t.Fatalf("%v", got)
	}
}

func TestRewriteChangesOnlyStaleKnownCells(t *testing.T) {
	cells := Find(md, "go-widgets", false)
	out := Rewrite(md, cells, map[string]string{"window": "v0.89.1", "mvvm": "v0.15.0"})
	if !strings.Contains(out, "| [`window`](https://github.com/go-widgets/window) | v0.89.1 |") ||
		!strings.Contains(out, "| [`mvvm`](https://github.com/go-widgets/mvvm) | v0.15.0 |") ||
		!strings.Contains(out, "| [`fonts`](https://github.com/go-opentype/fonts) | v0.1.0 |") ||
		!strings.Contains(out, "| [`alias`](https://github.com/go-widgets/window) | v0.1.0 |") {
		t.Fatal(out)
	}
	tc := Find(toml, "go-widgets", true)
	out = Rewrite(toml, tc, map[string]string{"window": "v0.89.1", "mvvm": "v0.16.0", "Linux": ""})
	if !strings.Contains(out, "tag = \"v0.89.1\"") || !strings.Contains(out, "tag = \"v0.16.0\"") || !strings.Contains(out, "tag = \"v9.9.9\"") {
		t.Fatal(out)
	}
}

func TestHighestIsSemverNotText(t *testing.T) {
	ls := "a\trefs/tags/v0.9.0\nb\trefs/tags/v0.10.0\nc\trefs/tags/v0.11.0-rc1\nd\trefs/tags/rougelex/v0.1.0\ne\trefs/tags/v1\nf\trefs/tags/latest\n"
	if got, err := highest(ls); err != nil || got != "v0.10.0" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := highest("x\trefs/tags/v0.1.0-rc1\n"); err == nil {
		t.Fatal("only a prerelease is no release")
	}
}

func files(t *testing.T) (string, string) {
	dir := t.TempDir()
	m, h := filepath.Join(dir, "modules.md"), filepath.Join(dir, "hugo.toml")
	if err := os.WriteFile(m, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h, []byte("[[params.modules]]\n  name = \"window\"\n  tag = \"v0.89.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return m, h
}

var fake = func(org, repo string) (string, error) {
	return map[string]string{"window": "v0.89.1", "mvvm": "v0.15.0"}[repo], nil
}

func TestRunReportsThenWrites(t *testing.T) {
	m, h := files(t)
	var out, errb bytes.Buffer
	if rc := run(options{org: "go-widgets", files: []string{m, h}}, &out, &errb, fake); rc != 1 {
		t.Fatalf("stale without -write: rc %d\n%s", rc, out.String())
	}
	if !strings.Contains(out.String(), "window: page says v0.89.0, latest is v0.89.1 (stale)") || !strings.Contains(out.String(), "3 cell(s), 2 module(s), 2 stale") {
		t.Fatal(out.String())
	}
	before, _ := os.ReadFile(m)
	if string(before) != md {
		t.Fatal("a report must not write")
	}
	out.Reset()
	if rc := run(options{org: "go-widgets", write: true, files: []string{m, h}}, &out, &errb, fake); rc != 0 {
		t.Fatalf("-write: rc %d", rc)
	}
	after, _ := os.ReadFile(h)
	if !strings.Contains(string(after), `tag = "v0.89.1"`) {
		t.Fatal(string(after))
	}
	out.Reset()
	if rc := run(options{org: "go-widgets", files: []string{m, h}}, &out, &errb, fake); rc != 0 {
		t.Fatalf("after -write everything is current: rc %d\n%s", rc, out.String())
	}
}

func TestAnUnreadTagOrNoCellFailsTheRun(t *testing.T) {
	m, _ := files(t)
	var out, errb bytes.Buffer
	broken := func(_, repo string) (string, error) { return "", errors.New("ls-remote failed") }
	if rc := run(options{org: "go-widgets", files: []string{m}}, &out, &errb, broken); rc != 2 {
		t.Fatalf("unread: rc %d", rc)
	}
	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, []byte("no table here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := run(options{org: "go-widgets", files: []string{empty}}, &out, &errb, fake); rc != 2 {
		t.Fatalf("no cell at all is not up to date: rc %d", rc)
	}
	if rc := run(options{org: "go-widgets", files: []string{"/nope"}}, &out, &errb, fake); rc != 2 {
		t.Fatalf("missing file: rc %d", rc)
	}
}

func TestParseArgs(t *testing.T) {
	for _, c := range []struct {
		args    []string
		code    int
		proceed bool
	}{
		{[]string{"-h"}, 0, false},
		{[]string{"x.md"}, 2, false},
		{[]string{"-org", "go-widgets"}, 2, false},
		{[]string{"-bogus"}, 2, false},
		{[]string{"-org", "go-widgets", "x.md"}, 0, true},
	} {
		var errb bytes.Buffer
		if _, code, proceed := parseArgs(c.args, &errb); code != c.code || proceed != c.proceed {
			t.Errorf("%q: got (%d, %v)", c.args, code, proceed)
		}
	}
}
