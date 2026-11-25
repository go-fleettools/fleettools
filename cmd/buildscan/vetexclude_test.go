// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func writeFixture(t *testing.T, ci string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":                   "module example.com/fx\n\ngo 1.21\n",
		"good/good.go":             "package good\n\nfunc G() int { return 1 }\n",
		"quake-tamago/t.go":        "package tamago\n\nimport _ \"example.invalid/needs-the-toolchain\"\n",
		"quake-tamago/sub/sub.go":  "package sub\n\nfunc S() int { return 2 }\n",
		".github/workflows/ci.yml": ci,
		"reference/ref.go":         "package ref\n\nimport _ \"example.invalid/vendored-c\"\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func ciWith(run string) string {
	return "name: ci\non: [push]\njobs:\n  vet:\n    runs-on: ubuntu-latest\n    steps:\n      - name: go vet\n        run: " + run + "\n"
}

const excludingRun = `go vet $(go list -e ./... | grep -vE '/(reference)($|/)|/quake-tamago$')`

// The CI drops two packages that cannot build here; buildscan must judge the
// set the CI judges, not ./... (go-quake1/engine's quake-tamago).
func TestVetModuleJudgesThePackagesTheCIJudges(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	dir := writeFixture(t, ciWith(excludingRun))
	c := config{goos: runtime.GOOS, goarch: runtime.GOARCH}

	if _, err := vetModule(c, dir, nil, nil); err == nil {
		t.Fatal("control: ./... must fail on the fixture's unbuildable packages")
	}
	out, err := vetModule(c, dir, nil, vetExclusions(dir))
	if err != nil {
		t.Fatalf("vet of the CI's package set failed: %v\n%s", err, out)
	}
}

// The exclusion is EXACT: quake-tamago/sub is not excluded by `/quake-tamago$`,
// so a broken sub package is still a failure.
func TestVetModuleStillVetsWhatTheCIDoesNotExclude(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	dir := writeFixture(t, ciWith(excludingRun))
	if err := os.WriteFile(filepath.Join(dir, "quake-tamago", "sub", "sub.go"),
		[]byte("package sub\n\nimport _ \"example.invalid/broken\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := config{goos: runtime.GOOS, goarch: runtime.GOARCH}
	if _, err := vetModule(c, dir, nil, vetExclusions(dir)); err == nil {
		t.Fatal("a package the CI does not exclude was skipped")
	}
}

func TestExclusionsOf(t *testing.T) {
	cases := []struct {
		name  string
		run   string
		pats  []string
		plain bool
	}{
		{"list form", excludingRun, []string{`/(reference)($|/)|/quake-tamago$`}, false},
		{"double quotes", `go vet $(go list ./... | grep -Ev "/x$")`, []string{`/x$`}, false},
		{"split flags", `go vet $(go list -e ./... | grep -v -E '/x$')`, []string{`/x$`}, false},
		{"whole tree", `go vet ./...`, nil, true},
		{"whole tree with flag", `go vet -tags=a ./...`, nil, true},
		{"continued line", "go vet \\\n          $(go list -e ./... | grep -vE '/x$')", []string{`/x$`}, false},
		{"unparsable pattern vets everything", `go vet $(go list -e ./... | grep -vE '(')`, nil, true},
		{"not a vet", `go test ./...`, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, plain := exclusionsOf([]byte(ciWith("|\n          " + tc.run)))
			if plain != tc.plain {
				t.Fatalf("plain = %v, want %v", plain, tc.plain)
			}
			var got []string
			for _, r := range res {
				got = append(got, r.String())
			}
			if len(got) != len(tc.pats) {
				t.Fatalf("patterns %q, want %q", got, tc.pats)
			}
			for i := range got {
				if got[i] != tc.pats[i] {
					t.Fatalf("patterns %q, want %q", got, tc.pats)
				}
			}
		})
	}
}

// A step that changes directory is not the root's package set.
func TestExclusionsOfSkipsWorkingDirectorySteps(t *testing.T) {
	src := "jobs:\n  v:\n    steps:\n      - working-directory: sub\n        run: " + excludingRun + "\n"
	if res, plain := exclusionsOf([]byte(src)); len(res) != 0 || plain {
		t.Fatalf("read a step that runs elsewhere: %v %v", res, plain)
	}
}

// A whole-tree vet anywhere means the CI vets everything: nothing is excluded.
func TestVetExclusionsWholeTreeLineWins(t *testing.T) {
	dir := writeFixture(t, ciWith(excludingRun))
	extra := ciWith("go vet ./...")
	if err := os.WriteFile(filepath.Join(dir, ".github", "workflows", "b.yml"), []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := vetExclusions(dir); got != nil {
		t.Fatalf("excluded %v although a line vets the whole tree", got)
	}
}

func TestExcluded(t *testing.T) {
	a, b := regexp.MustCompile(`/x$`), regexp.MustCompile(`/y$`)
	if excluded(nil, "m/x") {
		t.Fatal("no vet line excludes nothing")
	}
	if !excluded([]*regexp.Regexp{a}, "m/x") || excluded([]*regexp.Regexp{a}, "m/z") {
		t.Fatal("single line")
	}
	if excluded([]*regexp.Regexp{a, b}, "m/x") {
		t.Fatal("a second line that vets it must win")
	}
	if !excluded([]*regexp.Regexp{a, a}, "m/x") {
		t.Fatal("every line drops it")
	}
}
