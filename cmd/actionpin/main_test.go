package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sha = "3d3c42e5aac5ba805825da76410c181273ba90b1"

func TestJudge(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	cases := []struct {
		name, in string
		bad      string // substring of the reason, "" = accepted
	}{
		{"sha+comment", "actions/checkout@" + sha + " # v7", ""},
		{"sha+comment quoted", `"actions/checkout@` + sha + `" # v7`, ""},
		{"subpath", "github/codeql-action/init@" + sha + " # v3", ""},
		{"reusable workflow", "o/r/.github/workflows/x.yml@" + sha + " # v0.4.2", ""},
		{"local", "./.github/actions/x", ""},
		{"docker digest", "docker://alpine@" + digest, ""},
		{"docker tag", "docker://alpine:3.20", "docker image"},
		{"major tag", "actions/checkout@v7", "not a full 40-hex"},
		{"branch", "o/r/.github/workflows/x.yml@main", "not a full 40-hex"},
		{"short sha", "actions/checkout@3d3c42e", "not a full 40-hex"},
		{"41 hex", "actions/checkout@" + sha + "0 # v7", "not a full 40-hex"},
		{"uppercase sha", "actions/checkout@" + strings.ToUpper(sha) + " # v7", "not a full 40-hex"},
		{"no ref", "actions/checkout", "no @ref"},
		{"sha without comment", "actions/checkout@" + sha, "no trailing"},
		{"sha with empty comment", "actions/checkout@" + sha + " #", "no trailing"},
		{"empty", "", "empty"},
	}
	for _, c := range cases {
		_, reason := judge(c.in)
		if c.bad == "" && reason != "" {
			t.Errorf("%s: refused: %s", c.name, reason)
		}
		if c.bad != "" && !strings.Contains(reason, c.bad) {
			t.Errorf("%s: reason %q, want it to contain %q", c.name, reason, c.bad)
		}
	}
}

func TestScanIgnoresCommentsAndFindsListDash(t *testing.T) {
	text := "steps:\n" +
		"  # uses: actions/checkout@v7 is quoted in a comment\n" +
		"  - uses: actions/checkout@v7\n" +
		"    with:\n      x: 1\n" +
		"  - name: n\n    uses: actions/setup-go@" + sha + " # v7\n"
	got := scan("w.yml", text)
	if len(got) != 1 || got[0].Line != 3 || got[0].Uses != "actions/checkout@v7" {
		t.Fatalf("got %+v, want exactly line 3", got)
	}
	if !strings.Contains(got[0].String(), "w.yml:3:") {
		t.Errorf("String() = %q", got[0].String())
	}
}

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := RootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestCommand(t *testing.T) {
	good := repo(t, map[string]string{
		".github/workflows/ci.yml":                "- uses: actions/checkout@" + sha + " # v7\n",
		".github/workflows/b.yaml":                "uses: ./local\n",
		".github/actions/mine/action.yml":         "- uses: actions/setup-go@" + sha + " # v7\n",
		".github/workflows/notes.txt":             "uses: ignored@v1\n",
		".github/actions/mine/helper/action.yaml": "uses: ignored@v1\n",
	})
	out, err := run(t, good)
	if err != nil || !strings.Contains(out, "ok:") {
		t.Fatalf("good repo: err=%v out=%q", err, out)
	}

	bad := repo(t, map[string]string{
		".github/workflows/ci.yml":        "- uses: actions/checkout@v7\n",
		".github/actions/mine/action.yml": "- uses: o/r@main\n",
	})
	out, err = run(t, bad)
	if err == nil || !strings.Contains(err.Error(), "2 ") {
		t.Fatalf("bad repo: err=%v out=%q", err, out)
	}
	if !strings.Contains(out, "ci.yml:1:") || !strings.Contains(out, "action.yml:1:") {
		t.Errorf("both files must be named: %q", out)
	}
}

func TestNoWorkflowIsNotAPass(t *testing.T) {
	empty := repo(t, map[string]string{"README.md": "x"})
	if _, err := run(t, empty); err == nil || !strings.Contains(err.Error(), "read nothing") {
		t.Fatalf("a directory with no workflow must be an error, got %v", err)
	}
}

func TestDefaultsToCurrentDirectory(t *testing.T) {
	dir := repo(t, map[string]string{".github/workflows/ci.yml": "uses: a/b@v1\n"})
	t.Chdir(dir)
	if _, err := run(t); err == nil {
		t.Fatal("no argument must scan the current directory")
	}
}

func TestMainExitCode(t *testing.T) {
	dir := repo(t, map[string]string{".github/workflows/ci.yml": "uses: a/b@v1\n"})
	old := os.Args
	defer func() { os.Args = old }()
	code := -1
	osExit = func(c int) { code = c }
	defer func() { osExit = os.Exit }()
	os.Args = []string{"actionpin", dir}
	main()
	if code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
}

func TestBadDirectoryPatternIsAnError(t *testing.T) {
	_, err := run(t, "[")
	if err == nil || strings.Contains(err.Error(), "read nothing") {
		t.Fatalf("the glob's own error must surface, not the no-workflow one: %v", err)
	}
}

func TestUnreadableWorkflowIsAnError(t *testing.T) {
	dir := repo(t, map[string]string{".github/workflows/ok.yml": "uses: ./x\n"})
	// A directory named like a workflow cannot be read as a file.
	if err := os.Mkdir(filepath.Join(dir, ".github", "workflows", "dir.yml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, dir); err == nil {
		t.Fatal("an unreadable workflow must be an error, not skipped")
	}
}
