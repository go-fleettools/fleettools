// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"runtime"
	"strings"
	"testing"
)

// ⛔ cloudbase/garm: --vet reported 7 configurations that do not compile, all
// false. A plain `go build` configuration was vetted WITH test files, which need a
// -tags testing package the CI never builds untagged; and a cgo dependency was
// vetted with cgo off, where the CI job runs natively with cgo on.

const ciBuildAndVet = "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n" +
	"      - run: go build ./...\n" +
	"      - run: go vet -tags testing ./...\n"

const (
	tpTagged = "//go:build testing\n\npackage tp\n"
	tpBroken = "//go:build testing\n\npackage tp\n\nvar V int = \"not an int\"\n"
	aTest    = "package a\n\nimport _ \"example.test/internal/tp\"\n"
)

func vetPasses(t *testing.T, dir string) bool {
	t.Helper()
	return strings.Contains(vetRun(t, dir), "recorded a vet pass")
}

func TestBuildConfigurationDoesNotVetTestFilesNeedingATag(t *testing.T) {
	dir := newClone(t, false)
	write(t, dir, ".github/workflows/ci.yml", ciBuildAndVet)
	write(t, dir, "internal/tp/tp.go", tpTagged)
	write(t, dir, "a_test.go", aTest)
	commit(t, dir, "x_windows.go", "package a\n")
	if !vetPasses(t, dir) {
		t.Fatal("the untagged build configuration was vetted with the tagged test imports")
	}
}

// The tagged configuration still compiles the tagged file: a real defect there
// is still caught.
func TestTaggedFileDefectIsStillCaught(t *testing.T) {
	dir := newClone(t, false)
	write(t, dir, ".github/workflows/ci.yml", ciBuildAndVet)
	write(t, dir, "internal/tp/tp.go", tpBroken)
	write(t, dir, "a_test.go", aTest)
	commit(t, dir, "x_windows.go", "package a\n")
	if vetPasses(t, dir) {
		t.Fatal("a type error in a //go:build testing file was not caught")
	}
}

// A test file with a defect is caught by a configuration that compiles tests.
func TestTestFileDefectIsCaughtWhereTheCITestsIt(t *testing.T) {
	dir := newClone(t, false)
	write(t, dir, ".github/workflows/ci.yml", ciBuildAndVet)
	write(t, dir, "internal/tp/tp.go", tpTagged)
	write(t, dir, "a_test.go", aTest+"\nvar V int = \"x\"\n")
	commit(t, dir, "x_windows.go", "package a\n")
	if vetPasses(t, dir) {
		t.Fatal("a type error in a test file was not caught by the vet configuration")
	}
}

const (
	cgoOnly = "//go:build cgo\n\npackage a\n\nfunc CG() int { return 1 }\n"
	cgoUse  = "package a\n\nvar _ = CG()\n"
)

const ciNativeBuild = "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go build ./...\n"

func TestNativeBuildCompilesWithCgo(t *testing.T) {
	dir := newClone(t, false)
	write(t, dir, ".github/workflows/ci.yml", ciNativeBuild)
	write(t, dir, "cg.go", cgoOnly)
	write(t, dir, "use.go", cgoUse)
	commit(t, dir, "x_windows.go", "package a\n")
	if !vetPasses(t, dir) {
		t.Fatal("a native linux build was compiled with cgo off")
	}
}

func TestCgoOnlyFileDefectIsStillCaught(t *testing.T) {
	dir := newClone(t, false)
	write(t, dir, ".github/workflows/ci.yml", ciNativeBuild)
	write(t, dir, "cg.go", "//go:build cgo\n\npackage a\n\nfunc CG() int { return \"no\" }\n")
	write(t, dir, "use.go", cgoUse)
	commit(t, dir, "x_windows.go", "package a\n")
	if vetPasses(t, dir) {
		t.Fatal("a type error in a cgo-only file was not caught")
	}
}

// The step's own CGO_ENABLED wins over the native default.
func TestPinnedCgoOffIsHonoured(t *testing.T) {
	dir := newClone(t, false)
	write(t, dir, ".github/workflows/ci.yml",
		"jobs:\n  test:\n    runs-on: ubuntu-latest\n    env:\n      CGO_ENABLED: 0\n    steps:\n      - run: go build ./...\n")
	write(t, dir, "cg.go", cgoOnly)
	write(t, dir, "use.go", cgoUse)
	commit(t, dir, "x_windows.go", "package a\n")
	if vetPasses(t, dir) {
		t.Fatal("CGO_ENABLED=0 in the job env was ignored")
	}
}

func TestGoCommandsOf(t *testing.T) {
	for _, c := range []struct {
		run   string
		tests bool
		pkgs  string
	}{
		{"go build ./...", false, "./..."},
		{"go build -mod=vendor -o /dev/null ./cmd/garm-cli", false, "./cmd/garm-cli"},
		{"go test -race -tags testing -timeout=60m -parallel=4 ./...", true, "./..."},
		{"GOOS=windows go build -o out ./cmd/a ./cmd/b", false, "./cmd/a ./cmd/b"},
		{`go build -ldflags "-s -w -X a.B=${V}" -o "dist/${bin}" "./cmd/${bin}"`, false, "./..."},
		{"go build ./cmd/x && go vet ./pkg/y", true, "./cmd/x ./pkg/y"},
		{"go build ./cmd/x && go vet ./...", true, "./..."},
		{"go vet -tags testing", true, "./..."},
	} {
		tests, pkgs := goCommandsOf(c.run)
		if tests != c.tests || strings.Join(pkgs, " ") != c.pkgs {
			t.Errorf("goCommandsOf(%q) = %v %q, want %v %q", c.run, tests, pkgs, c.tests, c.pkgs)
		}
	}
}

// A build of one command is vetted for that command's packages, not the module:
// a cgo package the lane never builds is not its business.
func TestBuildOfOneCommandDoesNotCompileTheRest(t *testing.T) {
	dir := newClone(t, false)
	write(t, dir, ".github/workflows/ci.yml",
		"jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go build -o /dev/null ./cmd/ok\n")
	write(t, dir, "cmd/ok/main.go", "package main\n\nfunc main() {}\n")
	write(t, dir, "other/broken.go", "package other\n\nvar V int = \"never built by the lane\"\n")
	commit(t, dir, "x_windows.go", "package a\n")
	if !vetPasses(t, dir) {
		t.Fatal("a package the command does not build was compiled")
	}
	// ... and what it does build is still checked.
	commit(t, dir, "cmd/ok/main.go", "package main\n\nvar V int = \"x\"\n\nfunc main() {}\n")
	if vetPasses(t, dir) {
		t.Fatal("a type error in the built command was not caught")
	}
}

// ⛔ A cgo build of another platform cannot be compiled where its C toolchain is
// missing; that is not a verdict on the tree. The host platform stands in, and a
// real Go error is still caught there.
func TestForeignCgoBuildFallsBackToTheHostPlatform(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux is the foreign target of this fixture")
	}
	dir := newClone(t, false)
	write(t, dir, ".github/workflows/ci.yml", ciNativeBuild)
	write(t, dir, "cg_linux.go", "package a\n\n/*\n#include <linux/limits.h>\n*/\nimport \"C\"\n\nvar _ = C.PATH_MAX\n")
	commit(t, dir, "x_windows.go", "package a\n")
	if !vetPasses(t, dir) {
		t.Fatal("the foreign C toolchain's failure was taken for a verdict on the tree")
	}
	commit(t, dir, "a.go", "package a\n\nvar V int = \"x\"\n")
	if vetPasses(t, dir) {
		t.Fatal("a Go error went unseen behind the C toolchain's failure")
	}
}

// ⛔ A `-race` lane of another platform needs that platform's C toolchain (the
// race runtime is C). Seen on go-widgets/tray from a Mac: "go: -race requires
// cgo" and "relocation target __tsan_free not defined" failed a gate that had
// nothing wrong to report.
func TestForeignRaceLaneIsTheCToolchainNotTheTree(t *testing.T) {
	foreign := "linux"
	if runtime.GOOS == "linux" {
		foreign = "darwin"
	}
	c := config{goos: foreign, goarch: runtime.GOARCH, native: true, cgo: cgoEither}
	for _, out := range []string{
		"go: -race requires cgo; enable cgo by setting CGO_ENABLED=1\n",
		"runtime.(*mspan).setUserArenaChunkToFault: relocation target __tsan_free not defined\n",
	} {
		if !cgoCrossBlocked(c, out) {
			t.Errorf("%q was taken for a verdict on the tree", out)
		}
	}
	if cgoCrossBlocked(c, "./a.go:3:5: undefined: x\n") {
		t.Error("a Go error was taken for the C toolchain")
	}
	c.goos, c.goarch = runtime.GOOS, runtime.GOARCH
	if cgoCrossBlocked(c, "go: -race requires cgo\n") {
		t.Error("a host-platform build was excused: its failure is real")
	}
}

// ⛔ A `go test -race` lane is compiled with -race, not with a bare `race` tag
// (which links the race runtime's references without the runtime and failed
// every tree on every host). A foreign-platform lane falls back to the host.
func TestRaceLaneIsVettedWithTheRaceFlag(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux is the foreign target of this fixture")
	}
	dir := newClone(t, false)
	write(t, dir, ".github/workflows/ci.yml",
		"jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -race ./...\n")
	commit(t, dir, "a.go", "package a\n")
	if !vetPasses(t, dir) {
		t.Fatal("a clean tree failed its -race lane: the bare race tag was compiled")
	}
	commit(t, dir, "r_race.go", "//go:build race\n\npackage a\n\nvar R int = \"x\"\n")
	if vetPasses(t, dir) {
		t.Fatal("a type error in a race-only file went unseen")
	}
}

func TestConfigurationsCarryTheirVerbAndNativeness(t *testing.T) {
	src := "jobs:\n  t:\n    runs-on: ubuntu-latest\n    steps:\n" +
		"      - run: go build ./...\n" +
		"      - run: go test ./...\n" +
		"      - run: GOOS=windows go build ./...\n" +
		"      - run: go build ./... && go vet ./...\n"
	cfgs, _ := configsOfWorkflow("ci.yml", []byte(src))
	var got []string
	for _, c := range cfgs {
		got = append(got, c.String()+" tests="+map[bool]string{true: "y", false: "n"}[c.tests]+" native="+map[bool]string{true: "y", false: "n"}[c.native])
	}
	want := "linux/amd64 tests=n native=y|linux/amd64 tests=y native=y|windows/amd64 tests=n native=n|linux/amd64 tests=y native=y"
	if strings.Join(got, "|") != want {
		t.Fatalf("got %q\nwant %q", strings.Join(got, "|"), want)
	}
}
