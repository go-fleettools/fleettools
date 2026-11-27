// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Where a step compiles: working-directory at job level (defaults.run), at
// step level, a `cd`, and the go command's own -C flag (go-simd/highway's
// goat.yml runs `go test -C ./hwy/goat/tests`, which v0.181.1 read as a
// package argument and vetted in the root).
func TestWhereAStepCompiles(t *testing.T) {
	for _, tc := range []struct {
		name, wf, dir string
		pkgs          string
	}{
		{"step working-directory", `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - working-directory: sub/mod
        run: go test ./...
`, "sub/mod", "./..."},
		{"defaults.run working-directory", `
jobs:
  j:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: sub/mod
    steps:
      - run: go test ./...
`, "sub/mod", "./..."},
		{"step beats defaults", `
jobs:
  j:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: other
    steps:
      - working-directory: sub/mod
        run: go test ./...
`, "sub/mod", "./..."},
		{"nested module by cd", `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: cd hwy/goat/tests && GOARCH=arm64 GOOS=linux go build .
`, "hwy/goat/tests", "."},
		{"go test -C", `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: go test -C ./hwy/goat/tests -v
`, "hwy/goat/tests", "./..."},
		{"go build -C with -o", `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: go build -C hwy/goat -o ../../bin/goat .
`, "hwy/goat", "."},
		{"-C under a step working-directory", `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - working-directory: a
        run: go test -C b ./...
`, "a/b", "./..."},
		{"-C leaving the checkout is the root", `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: go test -C ../x ./...
`, ".", "./..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgs, _ := configsOfWorkflow("ci.yml", []byte(tc.wf))
			if len(cfgs) != 1 {
				t.Fatalf("want 1 configuration, got %d: %v", len(cfgs), cfgs)
			}
			if cfgs[0].dir != tc.dir {
				t.Errorf("dir = %q, want %q", cfgs[0].dir, tc.dir)
			}
			if got := strings.Join(cfgs[0].pkgs, ","); got != tc.pkgs {
				t.Errorf("pkgs = %q, want %q", got, tc.pkgs)
			}
		})
	}
}

// recordingGo puts a `go` on PATH that appends its GOEXPERIMENT and arguments
// to a file and succeeds.
func recordingGo(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell shim")
	}
	bin := t.TempDir()
	rec := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\necho \"GOEXPERIMENT=$GOEXPERIMENT ARGS=$*\" >> " + rec + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOEXPERIMENT", "")
	return rec
}

// ⛔ goexperiment.X is GOEXPERIMENT in the environment, never `-tags`: the
// latter compiled the runtime inconsistently ("relocation target
// simd/archsimd.Uint64x2.SetElem not defined") and refused every tree of
// go-simd/highway. GOEXPERIMENT in an env: block and a goexperiment tag on the
// command line must both reach the vet the same way.
func TestGoexperimentReachesTheVetAsEnvironmentNotTags(t *testing.T) {
	for _, tc := range []struct {
		name, wf, env, tags string
	}{
		{"env block", `
jobs:
  j:
    runs-on: ubuntu-latest
    env:
      GOEXPERIMENT: simd
    steps:
      - run: go test ./...
`, "simd", ""},
		{"inline assignment", `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: GOEXPERIMENT=simd go test ./...
`, "simd", ""},
		{"goexperiment tag", `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: go test -tags goexperiment.simd ./...
`, "simd", ""},
		{"tag beside a real tag", `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: go test -tags=evexguard,goexperiment.simd ./...
`, "simd", "evexguard"},
		{"no experiment", `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: go test -tags=evexguard ./...
`, "", "evexguard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := recordingGo(t)
			cfgs, _ := configsOfWorkflow("ci.yml", []byte(tc.wf))
			if len(cfgs) != 1 {
				t.Fatalf("want 1 configuration, got %d", len(cfgs))
			}
			if _, err := vetOnce(cfgs[0], t.TempDir(), nil, nil); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(rec)
			if err != nil {
				t.Fatal(err)
			}
			got := string(b)
			if !strings.Contains(got, "GOEXPERIMENT="+tc.env+" ") {
				t.Errorf("GOEXPERIMENT not %q: %s", tc.env, got)
			}
			if strings.Contains(got, "goexperiment.") {
				t.Errorf("a goexperiment tag was passed as -tags: %s", got)
			}
			if tc.tags != "" && !strings.Contains(got, "-tags="+tc.tags+" ") {
				t.Errorf("real tags %q lost: %s", tc.tags, got)
			}
			if tc.tags == "" && strings.Contains(got, "-tags") {
				t.Errorf("an empty -tags was passed: %s", got)
			}
		})
	}
}

// A step that compiles in a directory outside the checkout (`cd /tmp/x && go
// build .`) is a lane for coverage but has nothing of the tree to vet: it fell
// back to the root and `go build .` there said "no Go files".
func TestAnExternalDirectoryLaneIsNotVetted(t *testing.T) {
	const wf = `
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: |
          cd /tmp/crossout && GOARCH=arm64 GOOS=linux go build .
      - run: |
          cd /opt/goat
          go build -C hwy/goat -o ../../bin/goat .
`
	cfgs, _ := configsOfWorkflow("ci.yml", []byte(wf))
	if len(cfgs) != 2 {
		t.Fatalf("want 2 configurations, got %v", cfgs)
	}
	if !cfgs[0].external || cfgs[0].dir != "." {
		t.Errorf("the /tmp lane = %+v, want external at the root", cfgs[0])
	}
	plain, _ := configsOfWorkflow("ci.yml", []byte("jobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go build .\n"))
	if len(plain) != 1 || plain[0].external {
		t.Errorf("a step with no cd is external: %+v", plain)
	}
	if cfgs[1].external || cfgs[1].dir != "hwy/goat" {
		t.Errorf("the -C lane = %+v, want in hwy/goat and not external", cfgs[1])
	}
	f := finding{configs: cfgs[:1]}
	root := t.TempDir()
	write(t, root, "go.mod", "module example.test\n\ngo 1.21\n")
	if got := vetAll(f, root); got != nil {
		t.Errorf("an external lane was vetted: %v", got)
	}
}
