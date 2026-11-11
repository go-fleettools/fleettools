// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pkg writes a one-file package and returns its directory.
func pkg(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The shape the three go-crdt defects had: a package-level helper, a wrapper
// around it, and call sites that are supposed to use the wrapper.
const wrapped = `package p

type Peer struct{}

func await(x int) int { return x }

func (p *Peer) awaited(x int) int { return await(x) }
`

func drive(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestEveryCallThroughTheWrapperIsClean(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) one(x int) int { return p.awaited(x) }
func (p *Peer) two(x int) int { return p.awaited(x) }
`})
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0\nout: %s\nerr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "every one of the 1 call(s) to await") {
		t.Errorf("it should say how many it examined; it said %q", out)
	}
}

// The finding, with the position and the function that wrote it.
func TestACallThatBypassesTheWrapperIsNamed(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) good(x int) int { return p.awaited(x) }
func (p *Peer) bad(x int) int  { return await(x) }
`})
	code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 1 {
		t.Fatalf("code %d, want 1\nout: %s", code, out)
	}
	for _, want := range []string{"p.go:", "(*Peer).bad", "must go through (*Peer).awaited", "1 of 2 call(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report should contain %q; it said %q", want, out)
		}
	}
}

// ⛔ THE TEST THIS COMMAND EXISTS FOR. A rule naming something the package does
// not declare must REFUSE, not pass: "0 direct calls" against a name that is not
// there is true and worthless, and is the exact shape of gate this fleet has
// three memories of being fooled by.
func TestANameThatResolvesToNothingIsRefusedNotPassed(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped})
	for _, tc := range []struct{ name, wraps, via, says string }{
		{"the callee is not declared", "awaitt", "(*Peer).awaited", `--wraps "awaitt" is not declared`},
		{"the wrapper is not declared", "await", "(*Peer).awaitedd", `--via "(*Peer).awaitedd" is not declared`},
		{"the wrapper's receiver is wrong", "await", "(*Other).awaited", `--via "(*Other).awaited" is not declared`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := drive(t, "--wraps", tc.wraps, "--via", tc.via, dir)
			if code != 2 {
				t.Fatalf("code %d, want 2 (refused)\nout: %s\nerr: %s", code, out, errOut)
			}
			if !strings.Contains(errOut, tc.says) {
				t.Errorf("it should say %q; it said %q", tc.says, errOut)
			}
			if strings.Contains(out, "every one of") {
				t.Error("a refusal must not also report a clean package")
			}
		})
	}
}

// Neither flag has a default, so a run without them has no rule to enforce.
func TestWithoutBothDeclarationsItRefuses(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped})
	for _, args := range [][]string{
		{dir},
		{"--wraps", "await", dir},
		{"--via", "(*Peer).awaited", dir},
	} {
		code, _, errOut := drive(t, args...)
		if code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
		if !strings.Contains(errOut, "both required") {
			t.Errorf("%v: it should say both are required; it said %q", args, errOut)
		}
	}
}

// A dotted name would match a selector on any receiver, which is a different
// question from the one asked.
func TestADottedCalleeIsRefused(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped})
	code, _, errOut := drive(t, "--wraps", "p.await", "--via", "(*Peer).awaited", dir)
	if code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
	if !strings.Contains(errOut, "unqualified") {
		t.Errorf("it should say why; it said %q", errOut)
	}
}

// A waiver is made in public: it names the site, carries a reason, and the
// reason is printed beside the result.
func TestAWaiverIsPrintedAndNeedsAReason(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) bad(x int) int { return await(x) }
`})
	code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 1 {
		t.Fatalf("without a waiver it should fail, got %d", code)
	}
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "(*Peer).bad") {
			line = strings.SplitN(l, ":", 3)[1]
		}
	}
	if line == "" {
		t.Fatalf("no site in %q", out)
	}

	code, out, _ = drive(t, "--wraps", "await", "--via", "(*Peer).awaited",
		"--allow", "p.go:"+line+"=measured: this one runs before there is a context",
		dir)
	if code != 0 {
		t.Fatalf("a waived site should pass, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "measured: this one runs before there is a context") {
		t.Errorf("the reason must be printed; it said %q", out)
	}

	if code, _, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited",
		"--allow", "p.go:9", dir); code != 2 || !strings.Contains(errOut, "needs a reason") {
		t.Errorf("a waiver without a reason must be refused, got %d %q", code, errOut)
	}
}

// ⛔ A waiver should name the FUNCTION, and it must keep naming the same site
// when lines move. A CI file carrying 'peer_js.go:180' stops naming anything the
// day somebody adds an import, and a gate with a waiver nobody can check is
// worse than a gate with none.
func TestAWaiverByFunctionSurvivesLinesMovingAbove(t *testing.T) {
	body := wrapped + `
func (p *Peer) bad(x int) int { return await(x) }
`
	waiver := "(*Peer).bad=measured: this one runs before there is a context"

	dir := pkg(t, map[string]string{"p.go": body})
	code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--allow", waiver, dir)
	if code != 0 {
		t.Fatalf("a function waiver should pass, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "(*Peer).bad") || !strings.Contains(out, "runs before there is a context") {
		t.Errorf("the site and its reason must both be printed; it said %q", out)
	}

	// The same package with four lines inserted above everything. A line-keyed
	// waiver would now name the wrong place; this one still names the function.
	moved := pkg(t, map[string]string{"p.go": "package p\n\n// one\n// two\n// three\n// four\n" +
		strings.TrimPrefix(body, "package p\n")})
	code, out, _ = drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--allow", waiver, moved)
	if code != 0 {
		t.Fatalf("the waiver must survive lines moving above it, got %d\n%s", code, out)
	}
}

// --expect is the second half of the refusal: a rule can resolve and still match
// nothing, and a count the caller states out loud catches that.
func TestExpectCatchesARuleThatMatchedTooFew(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped})
	if code, _, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--expect", "1", dir); code != 0 {
		t.Errorf("one call examined, --expect 1: code %d, want 0", code)
	}
	code, _, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--expect", "5", dir)
	if code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
	if !strings.Contains(errOut, "examined 1 call(s)") {
		t.Errorf("it should say what it examined; it said %q", errOut)
	}
}

// Test files are skipped by default, and the skipping is never silent.
func TestSkippedTestFilesAreCounted(t *testing.T) {
	dir := pkg(t, map[string]string{
		"p.go": wrapped,
		"p_test.go": `package p

func exercise() int { return await(3) }
`})
	code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0: a test calling it directly is ordinary\n%s", code, out)
	}
	if !strings.Contains(out, "1 test file(s) not scanned") {
		t.Errorf("the skip must be printed; it said %q", out)
	}

	code, out, _ = drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--tests", dir)
	if code != 1 {
		t.Fatalf("--tests should see it, got %d\n%s", code, out)
	}
}

// A directory with nothing to read is refused, not called clean.
func TestAnUnreadableDirectoryIsRefused(t *testing.T) {
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", t.TempDir())
	if code != 2 {
		t.Fatalf("code %d, want 2\nout: %s", code, out)
	}
	if !strings.Contains(errOut, "no Go files") {
		t.Errorf("it should say why; it said %q", errOut)
	}
}
