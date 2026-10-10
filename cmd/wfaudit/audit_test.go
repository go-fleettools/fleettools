package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// rules returns "SEVERITY rule" for each finding, in order.
func rules(t *testing.T, src string) []string {
	t.Helper()
	fs, err := Audit("wf.yml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range fs {
		out = append(out, f.Severity.String()+" "+f.Rule)
	}
	return out
}

func eq(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// Every rule has a positive case and a near miss that must NOT match: a rule
// tested only on what it should find also passes when it finds everything.
func TestRules(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"a plain pull_request workflow with permissions is clean", `
on: pull_request
permissions:
  contents: read
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - run: go test ./...
`, nil},
		{"the pwn request: pull_request_target checking out the head", `
on:
  pull_request_target:
permissions:
  contents: read
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          ref: ${{ github.event.pull_request.head.sha }}
      - run: make test
`, []string{"HIGH dangerous-trigger"}},
		{"the same, by gh pr checkout in a shell step", `
on: [pull_request_target]
permissions: {}
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - run: gh pr checkout ${{ github.event.number }}
`, []string{"HIGH dangerous-trigger"}},
		{"pull_request_target that never touches the head is MEDIUM", `
on:
  pull_request_target:
    types: [labeled]
permissions:
  contents: read
jobs:
  label:
    runs-on: ubuntu-latest
    permissions:
      pull-requests: write
    steps:
      - uses: actions/labeler@v5
`, []string{"MEDIUM dangerous-trigger"}},
		{"a checkout ref naming a branch is not the pull request's code", `
on: workflow_run
permissions:
  contents: read
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          ref: gh-pages
`, []string{"MEDIUM dangerous-trigger"}},
		{"write-all at the job level is HIGH", `
on: push
permissions:
  contents: read
jobs:
  t:
    runs-on: ubuntu-latest
    permissions: write-all
    steps:
      - run: true
`, []string{"HIGH excessive-permissions"}},
		{"write at the workflow level is MEDIUM", `
on: push
permissions:
  contents: write
  pages: write
  id-token: read
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - run: true
`, []string{"MEDIUM excessive-permissions"}},
		{"write at the job level is the narrow form, and clean", `
on: push
permissions:
  contents: read
jobs:
  deploy:
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - run: true
`, nil},
		{"artipacked: the whole checkout uploaded with its token", `
on: push
permissions:
  contents: read
jobs:
  b:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/upload-artifact@v7
        with:
          name: tree
          path: .
`, []string{"HIGH artipacked"}},
		{"artipacked does not fire when the checkout drops its token", `
on: push
permissions:
  contents: read
jobs:
  b:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          persist-credentials: false
      - uses: actions/upload-artifact@v7
        with:
          path: .
`, nil},
		{"artipacked does not fire on a subdirectory upload", `
on: push
permissions:
  contents: read
jobs:
  b:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/upload-pages-artifact@v5
        with:
          path: public/
`, nil},
		{"a secret under a privileged trigger", `
on: workflow_run
permissions:
  contents: read
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - run: deploy --token "${{ secrets.DEPLOY_TOKEN }}"
`, []string{"MEDIUM dangerous-trigger", "MEDIUM secrets-under-privileged-trigger"}},
		{"a secret under push is not this rule's business", `
on: push
permissions:
  contents: read
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - run: deploy --token "${{ secrets.DEPLOY_TOKEN }}"
`, nil},
		{"no permissions anywhere is an INFO", `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    steps: [{run: "true"}]
  b:
    runs-on: ubuntu-latest
    steps: [{run: "true"}]
`, []string{"INFO no-permissions"}},
		{"no workflow permissions but every job has its own is clean", `
on: push
jobs:
  a:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps: [{run: "true"}]
`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { eq(t, rules(t, c.src), c.want...) })
	}
}

func TestTheFindingPointsAtTheLine(t *testing.T) {
	fs, err := Audit("wf.yml", []byte("on: push\npermissions:\n  contents: read\njobs:\n  t:\n    runs-on: x\n    permissions: write-all\n    steps: [{run: 'true'}]\n"))
	if err != nil || len(fs) != 1 {
		t.Fatalf("%v %v", fs, err)
	}
	if fs[0].Line != 7 {
		t.Fatalf("line %d, want 7", fs[0].Line)
	}
	if !strings.HasPrefix(fs[0].String(), "HIGH   wf.yml:7 excessive-permissions:") {
		t.Fatalf("%q", fs[0].String())
	}
}

func TestMalformedYAMLIsAnErrorNotAPass(t *testing.T) {
	if _, err := Audit("wf.yml", []byte("on: [push\n")); err == nil {
		t.Fatal("a file that does not parse must not read as clean")
	}
	if _, err := Audit("wf.yml", []byte("- a list\n")); err == nil {
		t.Fatal("a top-level list is not a workflow")
	}
	if fs, err := Audit("wf.yml", nil); err != nil || fs != nil {
		t.Fatalf("an empty file: %v %v", fs, err)
	}
}

func TestSlugNeverEchoesTheURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/go-widgets/toolkit.git":    "go-widgets/toolkit",
		"git@github.com:go-widgets/toolkit.git":        "go-widgets/toolkit",
		"ssh://git@github.com/go-widgets/toolkit":      "go-widgets/toolkit",
		"https://gitlab.com/x/y":                       "",
		"https://github.com/only-owner":                "",
		"https://x-access-token:SEKRIT@github.com/o/r": "",
	} {
		got, err := slugFromURL(in)
		if got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
		if err != nil && strings.Contains(err.Error(), "SEKRIT") {
			t.Fatalf("the error carries the credential: %v", err)
		}
	}
}

func writeRepo(t *testing.T, dir, name, wf string) string {
	t.Helper()
	d := filepath.Join(dir, name, ".github", "workflows")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "ci.yml"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name)
}

const noPerms = "on: push\njobs:\n  t:\n    runs-on: x\n    steps: [{run: 'true'}]\n"

func TestRunOverADirectoryOfCheckouts(t *testing.T) {
	dir := t.TempDir()
	writeRepo(t, dir, "a", noPerms)
	writeRepo(t, dir, "b", "on: push\npermissions: write-all\njobs: {}\n")
	var out, errb bytes.Buffer
	never := func(string) (string, error) { t.Fatal("no API call without -api"); return "", nil }
	if rc := run([]string{dir}, &out, &errb, never); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "2 repositories, 2 workflows read, 0 unreadable: 1 HIGH, 0 MEDIUM, 1 INFO") {
		t.Fatal(out.String())
	}
	out.Reset()
	if rc := run([]string{"-fail", dir}, &out, &errb, never); rc != 1 {
		t.Fatalf("-fail with a HIGH: rc %d", rc)
	}
}

func TestAnEmptyScanIsNotACleanBill(t *testing.T) {
	var out, errb bytes.Buffer
	if rc := run([]string{t.TempDir()}, &out, &errb, nil); rc != 2 {
		t.Fatalf("rc %d", rc)
	}
	if !strings.Contains(errb.String(), "no .github/workflows found") {
		t.Fatal(errb.String())
	}
}

func TestAnUnparsableWorkflowFailsTheRun(t *testing.T) {
	dir := t.TempDir()
	writeRepo(t, dir, "a", "on: [push\n")
	var out, errb bytes.Buffer
	if rc := run([]string{dir}, &out, &errb, nil); rc != 2 {
		t.Fatalf("rc %d", rc)
	}
}

func TestAPIResolvesTheDefault(t *testing.T) {
	dir := t.TempDir()
	repo := writeRepo(t, dir, "a", noPerms)
	for perm, want := range map[string]string{
		"read":  "0 HIGH, 0 MEDIUM, 0 INFO",
		"write": "0 HIGH, 1 MEDIUM, 0 INFO",
	} {
		var out, errb bytes.Buffer
		p := perm
		rc := run([]string{"-api", repo}, &out, &errb, func(string) (string, error) { return p, nil })
		if rc != 0 || !strings.Contains(out.String(), want) {
			t.Fatalf("%s: rc %d\n%s", perm, rc, out.String())
		}
	}
	// An unanswered question keeps the finding as it was.
	var out, errb bytes.Buffer
	run([]string{"-api", repo}, &out, &errb, func(string) (string, error) { return "", errors.New("403") })
	if !strings.Contains(out.String(), "1 INFO") || !strings.Contains(errb.String(), "unknown") {
		t.Fatal(out.String(), errb.String())
	}
}

func TestUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if rc := run(nil, &out, &errb, nil); rc != 2 {
		t.Fatalf("rc %d", rc)
	}
	if rc := run([]string{"-nope"}, &out, &errb, nil); rc != 2 {
		t.Fatalf("rc %d", rc)
	}
	if rc := run([]string{"/does/not/exist"}, &out, &errb, nil); rc != 2 {
		t.Fatalf("rc %d", rc)
	}
}

func TestOriginSlugReadsTheRemote(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", dir},
		{"-C", dir, "remote", "add", "origin", "https://github.com/go-widgets/toolkit.git"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if got, err := originSlug(dir); err != nil || got != "go-widgets/toolkit" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := originSlug(t.TempDir()); err == nil {
		t.Fatal("a directory with no remote has no slug")
	}
}
