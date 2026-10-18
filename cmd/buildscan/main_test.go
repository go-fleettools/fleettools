package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// write puts a file in a fixture tree, making its directories.
func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A matrix written as `include:` alone must expand, and leave NOTHING
// unsubstituted.
//
// This is the first defect this tool had on a real repository: with no
// list-valued key, the cartesian product was still the single empty
// combination, so one pass read `runs-on: ${{ matrix.os }}` literally and
// reported "cannot tell what a ${{ matrix.os }} runner compiles" beside the two
// configurations it had correctly resolved. The control is both halves: the
// configurations appear AND nothing is left unresolved, because a version that
// merely added the configurations would have kept the false complaint.
func TestAMatrixOfIncludesAloneLeavesNothingUnsubstituted(t *testing.T) {
	const wf = `
jobs:
  arch-native:
    name: arch ${{ matrix.label }}
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        include:
          - { os: ubuntu-latest,    label: amd64 }
          - { os: ubuntu-24.04-arm, label: arm64 }
    steps:
      - run: go test ./...
`
	cfgs, open := configsOfWorkflow("ci.yml", []byte(wf))
	got := map[string]bool{}
	for _, c := range cfgs {
		got[c.String()] = true
	}
	for _, want := range []string{"linux/amd64", "linux/arm64"} {
		if !got[want] {
			t.Errorf("%s is not among the configurations %v", want, keysOf(got))
		}
	}
	for _, u := range open {
		if strings.Contains(u.what, "${{") {
			t.Errorf("a matrix value was read literally: %s", u.what)
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The command's own assignment beats the env it inherits.
func TestAStepsOwnGOOSBeatsTheJobsEnv(t *testing.T) {
	const wf = `
env:
  GOOS: linux
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: GOOS=js GOARCH=wasm go build ./...
      - run: go test ./...
`
	cfgs, _ := configsOfWorkflow("ci.yml", []byte(wf))
	var sawJS, sawLinux bool
	for _, c := range cfgs {
		switch c.String() {
		case "js/wasm":
			sawJS = true
		case "linux/amd64":
			sawLinux = true
		}
	}
	if !sawJS {
		t.Error("the step that writes GOOS=js was read as the job's linux")
	}
	// The control: the OTHER step, which does inherit, is still linux.
	if !sawLinux {
		t.Error("the step that inherits GOOS=linux was not read at all, so the test above proves nothing about precedence")
	}
}

// A value from outside the file is reported, never guessed.
func TestAGOOSFromSomewhereElseIsReportedUnresolved(t *testing.T) {
	const wf = `
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: GOOS=$TARGET go build ./...
`
	cfgs, open := configsOfWorkflow("ci.yml", []byte(wf))
	for _, c := range cfgs {
		if strings.Contains(c.goos, "$") {
			t.Errorf("a shell variable became a configuration: %s", c)
		}
	}
	if len(open) == 0 {
		t.Error("nothing was reported as unresolved, so the configuration vanished in silence")
	}
}

// A self-hosted label says nothing about its platform, and is reported.
func TestASelfHostedRunnerIsNotGuessed(t *testing.T) {
	const wf = `
jobs:
  build:
    runs-on: [self-hosted, big]
    steps:
      - run: go test ./...
`
	cfgs, open := configsOfWorkflow("ci.yml", []byte(wf))
	if len(cfgs) != 0 {
		t.Errorf("a self-hosted runner was given a platform: %v", cfgs)
	}
	if len(open) == 0 {
		t.Error("and nothing said so")
	}
}

// The runner's own architecture is read from its label, because the default is
// not simply amd64 any more.
func TestTheRunnerLabelDecidesTheArchitecture(t *testing.T) {
	for label, want := range map[string]string{
		"ubuntu-latest":    "linux/amd64",
		"ubuntu-24.04-arm": "linux/arm64",
		"macos-13":         "darwin/amd64",
		"macos-latest":     "darwin/arm64",
		"windows-latest":   "windows/amd64",
	} {
		goos, goarch, ok := hostPair(label)
		if !ok {
			t.Errorf("%s: not resolved at all", label)
			continue
		}
		if got := goos + "/" + goarch; got != want {
			t.Errorf("%s compiles %s, want %s", label, got, want)
		}
	}
}

// ⛔ A configuration belongs to the module its step runs in.
//
// This is the defect that made two of this tool's first three findings
// invented: a lane cross-compiling the ROOT module for js/wasm was applied to
// every module of the tree, and two submodules nobody had ever asked to build
// there were reported as broken. The test asserts both directions, because a
// tool that simply dropped submodules would pass half of it.
func TestAConfigurationAppliesToItsOwnModuleOnly(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")
	// Only the root is built for js/wasm; the submodule is built for linux.
	write(t, dir, "sub/go.mod", "module example.test/sub\n\ngo 1.27\n")
	write(t, dir, "sub/only_js.go", "//go:build js\n\npackage sub\n")
	write(t, dir, ".github/workflows/ci.yml", `
jobs:
  root:
    runs-on: ubuntu-latest
    steps:
      - run: GOOS=js GOARCH=wasm go build ./...
  sub:
    runs-on: ubuntu-latest
    steps:
      - working-directory: sub
        run: go test ./...
`)

	f, err := scan(dir, "example/test")
	if err != nil {
		t.Fatal(err)
	}
	// The control: the two configurations were found at all, each in its place.
	var places []string
	for _, c := range f.configs {
		places = append(places, c.String())
	}
	if len(f.configs) != 2 {
		t.Fatalf("configurations = %v, want two: one at the root, one in sub", places)
	}

	// sub/only_js.go is js-only and sub is built for linux ONLY, so the root's
	// js/wasm lane must not cover it.
	orphaned := false
	for _, files := range f.orphans {
		for _, p := range files {
			if filepath.ToSlash(p) == "sub/only_js.go" {
				orphaned = true
			}
		}
	}
	if !orphaned {
		t.Errorf("sub/only_js.go was taken as compiled, which only the ROOT's js lane could do: orphans=%v", f.orphans)
	}
	// And the other direction: the root's own file is not reported, since the
	// root IS built.
	for _, files := range f.orphans {
		for _, p := range files {
			if p == "a.go" {
				t.Errorf("a.go was reported although the root is built: %v", f.orphans)
			}
		}
	}
}

// A file behind a constraint no lane selects is named, with its constraint.
func TestAConstraintNoLaneSelectsIsNamed(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "plain.go", "package a\n")
	write(t, dir, "behind.go", "//go:build integration\n\npackage a\n")
	write(t, dir, "wasm_only.go", "//go:build js && wasm\n\npackage a\n")
	write(t, dir, ".github/workflows/ci.yml", `
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
`)
	f, err := scan(dir, "example/test")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"integration": true, "js && wasm": true}
	for c, files := range f.orphans {
		if !want[c] {
			t.Errorf("reported %q (%v), which linux/amd64 does compile", c, files)
		}
		delete(want, c)
	}
	if len(want) > 0 {
		t.Errorf("not reported: %v (orphans were %v)", want, f.orphans)
	}
}

// A file whose constraint is in its NAME counts as much as a //go:build line.
func TestAConstraintInTheFileNameCountsToo(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")
	write(t, dir, "a_plan9.go", "package a\n")
	write(t, dir, ".github/workflows/ci.yml", `
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
`)
	f, err := scan(dir, "example/test")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, files := range f.orphans {
		for _, p := range files {
			if p == "a_plan9.go" {
				found = true
			}
			if p == "a.go" {
				t.Error("a.go, which linux compiles, was reported")
			}
		}
	}
	if !found {
		t.Errorf("a_plan9.go was taken as compiled: %v", f.orphans)
	}
}

// A module no lane compiles is one finding, not one per file.
func TestAModuleNobodyBuildsIsNamedOnce(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")
	write(t, dir, "tools/go.mod", "module example.test/tools\n\ngo 1.27\n")
	for _, n := range []string{"one.go", "two.go", "three.go"} {
		write(t, dir, "tools/"+n, "package tools\n")
	}
	write(t, dir, ".github/workflows/ci.yml", `
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
`)
	f, err := scan(dir, "example/test")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.unbuilt["tools"]; got != 3 {
		t.Errorf("tools counted %d files, want 3 (unbuilt=%v)", got, f.unbuilt)
	}
	if len(f.unbuilt) != 1 {
		t.Errorf("unbuilt = %v, want tools alone", f.unbuilt)
	}
	if n := countFiles(f.orphans); n != 0 {
		t.Errorf("the same files were ALSO reported one by one: %v", f.orphans)
	}
}

// ⛔ `go run` compiles. Leaving it out of the commands that count reported
// go-crdt/collab's tools/ module as built by nobody, and the lane that builds
// it says `cd tools && go run ./covergate`.
func TestGoRunCountsAsCompilingAndCdSaysWhere(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")
	write(t, dir, "tools/go.mod", "module example.test/tools\n\ngo 1.27\n")
	write(t, dir, "tools/gate.go", "package main\n\nfunc main() {}\n")
	write(t, dir, ".github/workflows/ci.yml", `
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
      - run: cd tools && go run ./gate
`)
	f, err := scan(dir, "example/test")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.unbuilt) != 0 {
		t.Errorf("tools was reported as built by nobody although a lane runs it: %v", f.unbuilt)
	}
	var where string
	for _, c := range f.configs {
		if c.dir == "tools" {
			where = c.where
		}
	}
	if where == "" {
		t.Errorf("no configuration was attributed to tools: %v", f.configs)
	}
}

// A directory reached through a symlink is walked as a tree, not as one file.
//
// WalkDir lstats what it is given, so a symlinked root read as "no Go files
// here" -- which is a silence, not an answer. It happened the first time this
// tool was pointed at a worktree through one.
func TestARootReachedThroughASymlinkIsStillWalked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege here")
	}
	real := t.TempDir()
	write(t, real, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, real, "a_plan9.go", "package a\n")
	write(t, real, ".github/workflows/ci.yml", `
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
`)
	link := filepath.Join(t.TempDir(), "through")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}

	var out, errOut bytes.Buffer
	if code := run([]string{"-dir", link}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "a_plan9.go") {
		t.Errorf("the tree behind the symlink was not read:\n%s", out.String())
	}
}

// -vet compiles, and finds what only compiling can find.
//
// The positive control is the failure this tool was written for: a test file
// in the root package with no build constraint, calling something that exists
// only in a !js file. `go test ./...` passes here and the js/wasm lane does
// not compile at all.
func TestVetFindsAFileThatOnlyCompilesSomewhereElse(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiles for js/wasm")
	}
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")
	write(t, dir, "helper_notjs.go", "//go:build !js\n\npackage a\n\nfunc helper() int { return 1 }\n")
	// No constraint, so js/wasm compiles it -- and helper is not there.
	write(t, dir, "use_test.go", "package a\n\nimport \"testing\"\n\nfunc TestUse(t *testing.T) {\n\tif helper() != 1 {\n\t\tt.Fatal(\"no\")\n\t}\n}\n")
	write(t, dir, ".github/workflows/ci.yml", `
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
      - run: GOOS=js GOARCH=wasm go vet ./...
`)
	f, err := scan(dir, "example/test")
	if err != nil {
		t.Fatal(err)
	}
	f.vetFail = vetAll(f, dir)

	// The control FIRST: linux/amd64 must pass, or a failure under js/wasm
	// would say nothing about the configuration and everything about the
	// fixture.
	for _, v := range f.vetFail {
		if v.cfg.goos != "js" {
			t.Fatalf("the fixture does not compile under %s either: %s", v.cfg, v.err)
		}
	}
	if len(f.vetFail) == 0 {
		t.Fatalf("js/wasm was not reported although `helper` is !js; configurations were %v", f.configs)
	}
	if !strings.Contains(f.vetFail[0].err, "helper") {
		t.Errorf("the reason given does not name what is missing: %q", f.vetFail[0].err)
	}
}

// It reads its arguments: the lesson redscan#77 learned, where -h was a full
// fleet sweep.
func TestItReadsItsArguments(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")

	for _, c := range []struct {
		name string
		argv []string
		want int
	}{
		{"help", []string{"-h"}, 0},
		{"an unknown flag", []string{"-bogus"}, 2},
		{"a stray argument", []string{dir}, 2},
		{"-dir together with -root", []string{"-dir", dir, "-root", dir}, 2},
		{"a root with no checkouts", []string{"-root", dir}, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if got := run(c.argv, &out, &errOut); got != c.want {
				t.Errorf("exit %d, want %d (stderr: %q)", got, c.want, errOut.String())
			}
		})
	}
	// The control: given properly, it does scan and says so.
	var out, errOut bytes.Buffer
	if got := run([]string{"-dir", dir}, &out, &errOut); got != 0 {
		t.Fatalf("the control exited %d: %s", got, errOut.String())
	}
	if !strings.Contains(out.String(), "1 repositories") {
		t.Errorf("the control scanned nothing: %q", out.String())
	}
}

// -fail is what a CI step would use, and it must answer about findings rather
// than about the scan having run.
func TestFailExitsNonZeroOnlyWhenSomethingIsFound(t *testing.T) {
	clean := t.TempDir()
	write(t, clean, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, clean, "a.go", "package a\n")
	write(t, clean, ".github/workflows/ci.yml", `
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
`)
	var out, errOut bytes.Buffer
	if got := run([]string{"-dir", clean, "-fail"}, &out, &errOut); got != 0 {
		t.Errorf("a clean tree with -fail exited %d:\n%s", got, out.String())
	}

	dirty := t.TempDir()
	write(t, dirty, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dirty, "a.go", "package a\n")
	write(t, dirty, "b.go", "//go:build nobodyeverasksforthis\n\npackage a\n")
	write(t, dirty, ".github/workflows/ci.yml", `
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
`)
	out.Reset()
	if got := run([]string{"-dir", dirty, "-fail"}, &out, &errOut); got != 1 {
		t.Errorf("a tree with an uncompiled file and -fail exited %d:\n%s", got, out.String())
	}
}

// ⛔ `//go:build ignore` MEANS never compiled, and reporting it is reporting
// the author's decision back to them. Nineteen of 76 file findings on the
// first fleet scan were this.
//
// The control is a file behind another tag in the same tree: a version that
// reported nothing at all would pass the first half.
func TestAFileMarkedIgnoreIsNotAFinding(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")
	write(t, dir, "gen.go", "//go:build ignore\n\npackage main\n")
	write(t, dir, "other.go", "//go:build sometag\n\npackage a\n")
	write(t, dir, ".github/workflows/ci.yml", `
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
`)
	f, err := scan(dir, "example/test")
	if err != nil {
		t.Fatal(err)
	}
	if files, ok := f.orphans["ignore"]; ok {
		t.Errorf("a file the author marked `ignore` was reported: %v", files)
	}
	if _, ok := f.orphans["sometag"]; !ok {
		t.Errorf("the control was not reported either, so nothing is being filtered: %v", f.orphans)
	}
}

// ⛔ `go test -race` sets the `race` tag. Missing it reported two
// repositories' `//go:build race` files as compiled by nobody when the lane
// that compiles them is exactly that command.
func TestRaceIsATagTheFlagSets(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")
	write(t, dir, "race.go", "//go:build race\n\npackage a\n")
	write(t, dir, "norace.go", "//go:build !race\n\npackage a\n")
	write(t, dir, ".github/workflows/ci.yml", `
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: go test -race ./...
`)
	f, err := scan(dir, "example/test")
	if err != nil {
		t.Fatal(err)
	}
	if files, ok := f.orphans["race"]; ok {
		t.Errorf("a race file was reported although the lane passes -race: %v", files)
	}
	// The control and the cost: with -race ON, the !race half is now the one
	// nothing compiles, and that is true -- this lane does not run without it.
	if _, ok := f.orphans["!race"]; !ok {
		t.Errorf("nothing was reported at all, so the tag may simply be unread: %v", f.orphans)
	}
}

// ⛔ The last line counts REPOSITORIES, not sections. Three `hits++` in three
// branches made this tool's own first fleet reading say "91 with something to
// decide about" for a number of repositories that was smaller than that, and a
// total that cannot be compared with the next run is not a measurement.
func TestTheTotalCountsRepositoriesAndNotSections(t *testing.T) {
	// One repository with BOTH kinds of finding: a file nothing selects and a
	// module nothing builds. It must count once.
	both := finding{
		repo:    "o/both",
		configs: []config{{goos: "linux", goarch: "amd64", dir: "."}},
		orphans: map[string][]string{"sometag": {"a.go", "b.go"}},
		unbuilt: map[string]int{"tools": 3},
	}
	clean := finding{
		repo:    "o/clean",
		configs: []config{{goos: "linux", goarch: "amd64", dir: "."}},
		orphans: map[string][]string{},
		unbuilt: map[string]int{},
	}
	var out bytes.Buffer
	if code := report([]finding{both, clean}, false, false, true, false, &out); code != 1 {
		t.Errorf("-fail over a finding exited %d, want 1", code)
	}
	got := out.String()
	if !strings.Contains(got, "2 repositories, 1 with something to decide about") {
		t.Errorf("the count is not one repository:\n%s", got)
	}
	// And the detail is still there, so the fix did not simply stop counting.
	if !strings.Contains(got, "2 file(s) and 1 module(s)") {
		t.Errorf("the totals lost the detail:\n%s", got)
	}
}

// ⛔ "Could not resolve" is PRINTED but not COUNTED. Treating it as a finding
// moved the fleet total from 91 to 147 in one edit, and a number that mixes
// what the tool cannot read with what nobody compiles means neither.
func TestWhatTheToolCannotReadIsShownButNotCounted(t *testing.T) {
	only := finding{
		repo:    "o/opaque",
		configs: []config{{goos: "linux", goarch: "amd64", dir: "."}},
		orphans: map[string][]string{},
		unbuilt: map[string]int{},
		open:    []unresolved{{where: "ci.yml: build", what: "GOOS comes from a value not in the file: x"}},
	}
	var out bytes.Buffer
	if code := report([]finding{only}, false, false, true, false, &out); code != 0 {
		t.Errorf("-fail exited %d over nothing but an unresolved note, want 0", code)
	}
	got := out.String()
	if !strings.Contains(got, "1 repositories, 0 with something to decide about") {
		t.Errorf("an unresolved note was counted as a finding:\n%s", got)
	}
	// The control: it is still SHOWN, since it bounds what the rest means.
	if !strings.Contains(got, "could not resolve") {
		t.Errorf("it was not printed at all, so the reader cannot see the limit:\n%s", got)
	}
}

// ⛔ cgo has THREE answers, and a workflow that does not say is one of them.
//
// Fixing it on makes `//go:build cgo` read as compiled and `!cgo` read as
// compiled by nobody; fixing it off does the reverse. Forty-two files on
// tannevaled/purego were reported on that mistake. Both polarities are
// asserted here, because a tool that simply flipped the assumption would pass
// half of this test.
func TestCgoIsNotAssumedWhenTheWorkflowDoesNotSay(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")
	write(t, dir, "with_cgo.go", "//go:build cgo\n\npackage a\n")
	write(t, dir, "no_cgo.go", "//go:build !cgo\n\npackage a\n")
	write(t, dir, "never.go", "//go:build sometag\n\npackage a\n")
	write(t, dir, ".github/workflows/ci.yml", `
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
`)
	f, err := scan(dir, "example/test")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"cgo", "!cgo"} {
		if files, ok := f.orphans[c]; ok {
			t.Errorf("%q was reported although the lane says nothing about cgo: %v", c, files)
		}
	}
	// The control: a tag nothing passes IS still reported, so the pair of
	// contexts has not simply made everything match.
	if _, ok := f.orphans["sometag"]; !ok {
		t.Errorf("nothing was reported at all: %v", f.orphans)
	}

	// And when the workflow DOES say, it is believed: with cgo off, the `cgo`
	// half is compiled by nobody.
	write(t, dir, ".github/workflows/ci.yml", `
jobs:
  test:
    runs-on: ubuntu-latest
    env:
      CGO_ENABLED: "0"
    steps:
      - run: go test ./...
`)
	f, err = scan(dir, "example/test")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.orphans["cgo"]; !ok {
		t.Errorf("CGO_ENABLED=0 was not believed: %v", f.orphans)
	}
	if files, ok := f.orphans["!cgo"]; ok {
		t.Errorf("!cgo reported under CGO_ENABLED=0: %v", files)
	}
}

// ⛔ GOEXPERIMENT sets tags, and without reading it the tool cannot see the
// case it was written for: go-compressions/blake3's `goexperiment.simd &&
// arm64` file, each half covered by a lane and the conjunction by none.
//
// The fixture is that shape exactly: the experiment on amd64, arm64 without
// it. Both assertions are needed -- the file behind the conjunction is
// reported, and the file behind the experiment ALONE is not, which is what
// says the tag was read rather than ignored.
func TestGoexperimentIsATagAndAConjunctionNeedsBothHalves(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")
	write(t, dir, "simd_amd64.go", "//go:build goexperiment.simd && amd64\n\npackage a\n")
	write(t, dir, "simd_arm64.go", "//go:build goexperiment.simd && arm64\n\npackage a\n")
	write(t, dir, ".github/workflows/ci.yml", `
jobs:
  experiment:
    runs-on: ubuntu-latest
    env:
      GOEXPERIMENT: simd
    steps:
      - run: go test ./...
  arm:
    runs-on: ubuntu-24.04-arm
    steps:
      - run: go test ./...
`)
	f, err := scan(dir, "example/test")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.orphans["goexperiment.simd && arm64"]; !ok {
		t.Errorf("the conjunction no lane satisfies was not reported: %v", f.orphans)
	}
	if files, ok := f.orphans["goexperiment.simd && amd64"]; ok {
		t.Errorf("the half that IS covered was reported, so GOEXPERIMENT was not read: %v", files)
	}
}

// ⛔ A working-directory out of the checkout is REFUSED, not followed.
//
// `-vet` runs `go vet` with cmd.Dir set from a workflow's `working-directory`
// and from a `cd` inside a step. A workflow is a file in a repository, and a
// repository can be anybody's: `working-directory: ../../../etc` would point
// the compiler outside the tree, and attributing that configuration to this
// repository's code would be wrong even where it is harmless.
func TestAWorkingDirectoryOutsideTheCheckoutIsRefused(t *testing.T) {
	for _, bad := range []string{
		"../../../etc",
		"..",
		"/etc",
		"~/elsewhere",
		"sub/../..",
		"${{ github.workspace }}/x",
	} {
		if got := clean(bad); got != "" {
			t.Errorf("clean(%q) = %q, want it refused", bad, got)
		}
	}
	// The control: an ordinary subdirectory is kept, so the test above is
	// about leaving the tree rather than about rejecting everything.
	for in, want := range map[string]string{
		"gitstore":     "gitstore",
		"./pgstore":    "pgstore",
		"a/b":          "a/b",
		"sub/../other": "other",
	} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}

// And the whole way round: a step whose `cd` leaves the tree contributes its
// configuration to the ROOT rather than to somewhere outside it.
func TestACdOutOfTheTreeFallsBackToTheRoot(t *testing.T) {
	const wf = `
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: cd ../../../tmp && go build ./...
`
	cfgs, _ := configsOfWorkflow("ci.yml", []byte(wf))
	if len(cfgs) != 1 {
		t.Fatalf("configurations = %v, want one", cfgs)
	}
	if cfgs[0].dir != "." {
		t.Errorf("the configuration landed in %q, want the root", cfgs[0].dir)
	}
}

// A workflow the parser refuses is reported as unreadable. Reading it as empty
// would make every configuration in it invisible, and then "compiled by no
// lane" would be a statement about the parser.
func TestAnUnreadableWorkflowIsSaidSoRatherThanSkipped(t *testing.T) {
	cfgs, open := configsOfWorkflow("ci.yml", []byte("jobs:\n  - this: [is not\n   valid yaml\n"))
	if len(cfgs) != 0 {
		t.Errorf("configurations came out of a file that does not parse: %v", cfgs)
	}
	if len(open) != 1 || !strings.Contains(open[0].what, "could not be parsed") {
		t.Errorf("unresolved = %v, want one note saying it could not be parsed", open)
	}
}

// ⛔ A step with TWO invocations is two configurations.
//
// Using this tool is what found it. go-agentsync/agentsync's lane was first
// written as one block with `GOOS=darwin GOARCH=arm64 go vet ./...` and
// `GOOS=darwin GOARCH=amd64 go vet ./...`, and buildscan listed ONE: the
// regexps read the whole block and take their first match. The workflow was
// rewritten as two steps to work around it, with a comment explaining why --
// a tool making its reader carry its limitation.
//
// The three shapes asserted together, because a naive split fixes the first
// and breaks the other two.
func TestAStepWithTwoInvocationsIsTwoConfigurations(t *testing.T) {
	const wf = `
jobs:
  cross:
    runs-on: ubuntu-latest
    steps:
      - name: two of them in one block
        run: |
          set -eu
          GOOS=darwin GOARCH=arm64 go vet ./...
          GOOS=darwin GOARCH=amd64 go vet ./...
  continued:
    runs-on: ubuntu-latest
    steps:
      - name: a command continued onto the next line
        run: |
          GOOS=js GOARCH=wasm go test -count 1 \
            -timeout 5m ./...
  exported:
    runs-on: ubuntu-latest
    steps:
      - name: an assignment on its own line
        run: |
          export GOOS=windows
          go vet ./...
  chained:
    runs-on: ubuntu-latest
    steps:
      - name: a cd and a go run in one command
        run: cd tools && go run ./covergate
`
	cfgs, open := configsOfWorkflow("ci.yml", []byte(wf))
	got := map[string]bool{}
	for _, c := range cfgs {
		got[c.String()] = true
	}
	for _, want := range []string{
		"darwin/arm64",         // both halves of the pair, which is the point
		"darwin/amd64",         //
		"js/wasm",              // the continuation did not lose its packages
		"windows/amd64",        // the exported assignment reached the command after it
		"linux/amd64 in tools", // `&&` stayed inside one command
	} {
		if !got[want] {
			t.Errorf("%s is missing from %v", want, keysOf(got))
		}
	}
	// And nothing was invented: `set -eu` is not a configuration, and the
	// exported GOOS must not leak into another job.
	if got["linux/amd64"] && !got["linux/amd64 in tools"] {
		t.Errorf("a bare linux/amd64 appeared from a block that names its platform: %v", keysOf(got))
	}
	for _, u := range open {
		t.Errorf("unresolved: %s: %s", u.where, u.what)
	}
}

// ⛔ A `cd` ON ITS OWN LINE applies to the commands under it.
//
// Reading the whole block, `cd tools` anywhere in it reached workingDir.
// Reading line by line, it stopped doing so -- a regression the per-command
// split introduced, and one an A/B caught rather than a test: over the same
// 647 checkouts the fleet-wide count of modules nobody compiles went from 38
// to 40, which is what two mis-attributed modules look like from outside.
//
// Both spellings are asserted here, because the `&&` form kept working the
// whole time and would have hidden the broken one.
func TestACdOnItsOwnLineReachesTheCommandsUnderIt(t *testing.T) {
	const wf = `
jobs:
  own-line:
    runs-on: ubuntu-latest
    steps:
      - run: |
          cd tools
          go run ./covergate -floor 75 ../js.cover
  chained:
    runs-on: ubuntu-latest
    steps:
      - run: cd other && go run ./gate
  both:
    runs-on: ubuntu-latest
    steps:
      - run: |
          go test ./...
          cd third
          go vet ./...
`
	cfgs, open := configsOfWorkflow("ci.yml", []byte(wf))
	got := map[string]bool{}
	for _, c := range cfgs {
		got[c.String()] = true
	}
	for _, want := range []string{
		"linux/amd64 in tools",
		"linux/amd64 in other",
		"linux/amd64 in third",
		"linux/amd64", // the command BEFORE the cd stays at the root
	} {
		if !got[want] {
			t.Errorf("%s is missing from %v", want, keysOf(got))
		}
	}
	for _, u := range open {
		t.Errorf("unresolved: %s: %s", u.where, u.what)
	}
}

// And a `cd` that leaves the checkout is refused rather than followed, which
// is the path guard a step can reach through this carrying.
func TestACarriedCdOutOfTheTreeFallsBackToTheRoot(t *testing.T) {
	const wf = `
jobs:
  escape:
    runs-on: ubuntu-latest
    steps:
      - run: |
          cd ../../../tmp
          go build ./...
`
	cfgs, _ := configsOfWorkflow("ci.yml", []byte(wf))
	if len(cfgs) != 1 {
		t.Fatalf("configurations = %v, want one", cfgs)
	}
	if cfgs[0].dir != "." {
		t.Errorf("the configuration landed in %q, want the root", cfgs[0].dir)
	}
}

// ⛔ A SUBSHELL is how a loop body changes directory without changing it for
// the next turn, and `(cd x && go run .)` must be read as x.
//
// go-asmgen/asmgen does exactly that inside a `for`, and an anchor of
// `^\s*cd` does not match `(cd`. Without the bracket its verify module read
// as compiled by nobody -- found by diffing a fleet run against the previous
// implementation's, which is the only reason it was noticed at all.
//
// The surrounding block is the real one, because what makes it interesting is
// that the EARLIER standalone `cd` belongs to a `go mod download`: a command
// that downloads and compiles nothing. The old whole-block reading credited
// that one and got the right answer for the wrong reason.
func TestASubshellCdIsWhereItCompiles(t *testing.T) {
	const wf = `
jobs:
  wasm:
    runs-on: ubuntu-latest
    steps:
      - name: Regenerate + compile + verify each kernel
        run: |
          set -eo pipefail
          cd examples/wasm/verify
          go mod download
          cd ../../..
          for k in matchlen hex; do
            go run ./examples/wasm/"$k" > "/tmp/$k.wat"
            (cd examples/wasm/verify && go run . "$k" "/tmp/$k.wasm")
          done
      - name: go test the wasm example packages
        run: go test ./examples/wasm/...
`
	cfgs, open := configsOfWorkflow("ci.yml", []byte(wf))
	got := map[string]bool{}
	for _, c := range cfgs {
		got[c.String()] = true
	}
	if !got["linux/amd64 in examples/wasm/verify"] {
		t.Errorf("the subshell's directory was not read: %v", keysOf(got))
	}
	// And the two commands that run from the root are at the root: the
	// `cd ../../..` above them leaves the checkout, so it is refused rather
	// than followed.
	if !got["linux/amd64"] {
		t.Errorf("the root is missing from %v", keysOf(got))
	}
	for _, u := range open {
		// `./examples/wasm/"$k"` carries a shell variable, but it is a PACKAGE
		// path and not a GOOS, so nothing here should be unresolved.
		t.Errorf("unresolved: %s: %s", u.where, u.what)
	}
}
