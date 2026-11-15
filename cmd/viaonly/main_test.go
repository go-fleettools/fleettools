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
//
// ⛔ The bare run's refusal CHANGED when the exit-point rule was added, and the
// want is written per case rather than as one shared substring: with no flag at
// all there are now two rules to choose between, and a message naming only
// --wraps/--via would tell a caller the other one does not exist. Half-spelling
// one rule still says "both required" about THAT rule.
func TestWithoutBothDeclarationsItRefuses(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped})
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{dir}, "give a rule"},
		{[]string{"--wraps", "await", dir}, "--wraps and --via are both required"},
		{[]string{"--via", "(*Peer).awaited", dir}, "--wraps and --via are both required"},
		{[]string{"--asks", "gathered", dir}, "--asks and --in are both required"},
		{[]string{"--in", "(*Peer).Offer", dir}, "--asks and --in are both required"},
	} {
		code, _, errOut := drive(t, c.args...)
		if code != 2 {
			t.Errorf("%v: code %d, want 2", c.args, code)
		}
		if !strings.Contains(errOut, c.want) {
			t.Errorf("%v: want %q in the refusal; it said %q", c.args, c.want, errOut)
		}
	}
}

// ⛔ Two rules cannot share one exit status. A run given both used to be
// decided by whichever block came first in the source, which is the tool
// guessing what the caller meant.
func TestTheTwoRulesCannotBeGivenTogether(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped})
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited",
		"--asks", "gathered", "--in", "(*Peer).Offer", dir)
	if code != 2 {
		t.Errorf("code %d, want 2", code)
	}
	if !strings.Contains(errOut, "one rule at a time") {
		t.Errorf("it should say one rule at a time; it said %q", errOut)
	}
	if strings.Contains(out, "goes through") || strings.Contains(out, "asks") {
		t.Errorf("a refusal must not also report on either rule: %q", out)
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

// ⛔⛔ THE DEFECT THIS COMMAND EXISTS TO CATCH, WRITTEN INTO THE COMMAND. A
// package where the wrapped function is declared and never called printed
// "every one of the 0 call(s) goes through" and exited 0 — a gate reporting a
// clean package it had not been able to judge. Found by an independent review.
func TestAPackageThatExercisesNothingIsRefused(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": `package p

type Peer struct{}

func await(x int) int { return x }

func (p *Peer) awaited(x int) int { return x }
`})
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 2 {
		t.Fatalf("code %d, want 2 (refused to decide)\nout: %s", code, out)
	}
	if !strings.Contains(errOut, "never called there") {
		t.Errorf("it should say what it could not judge; it said %q", errOut)
	}
	if strings.Contains(out, "every one of the") {
		t.Error("a refusal must not also report a clean package")
	}
	// --expect 0 is how a caller says they meant it.
	if code, _, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--expect", "0", dir); code != 0 {
		t.Errorf("--expect 0 should be accepted, got %d", code)
	}
}

// ⛔⛔ A FUNCTION TAKEN AS A VALUE REACHES IT WITHOUT A CALL SITE TO FIND, so a
// pass that only looks at CallExpr is blind to the plainest bypass there is.
func TestTakingTheNameAsAValueIsReported(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": `package p

type Peer struct{}

func await(x int) int { return x }

func (p *Peer) awaited(x int) int { return await(x) }

var handler = await

func register(f func(int) int) {}

func init() { register(await) }
`})
	code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 1 {
		t.Fatalf("code %d, want 1\nout: %s", code, out)
	}
	for _, want := range []string{"package level takes await as a VALUE", "init takes await as a VALUE", "2 use(s) take it as a value"} {
		if !strings.Contains(out, want) {
			t.Errorf("it should say %q; it said %q", want, out)
		}
	}
}

// ⛔ The control for the test above: the identifier that IS the call, and the
// one that IS the declaration, must not be counted as values. Both are visited
// by the same walk.
//
// ⛔⛔ THIS CONTROL WAS VACUOUS AS FIRST WRITTEN, which a review measured.
// Its fixture's only call to await sat INSIDE the wrapper, so what kept that
// identifier out of the value list was the "inside via" check — the
// `called[id]` exclusion was never reached at all, and deleting it left this
// test green. The call has to be OUTSIDE the wrapper for the exclusion to be
// the only thing standing between one call and two reports; the real collab
// case printed both lines for one call, and that is the case being pinned.
func TestACallAndADeclarationAreNotValueUses(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) direct(x int) int { return await(x) }
`})
	code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 1 {
		t.Fatalf("code %d, want 1: the direct call is a bypass\nout: %s", code, out)
	}
	if strings.Contains(out, "as a VALUE") {
		t.Errorf("neither the call nor the declaration is a value use; it said %q", out)
	}
	// One call, ONE report. The count is asserted so that the value list
	// growing silently cannot be mistaken for the bypass list.
	if !strings.Contains(out, "0 use(s) take it as a value") {
		t.Errorf("the value count must be zero; it said %q", out)
	}
}

// ⛔ The enclosing function was assigned on the way in and never cleared, so a
// package-level call after the last function was reported as inside it — and a
// waiver written for that function waived a call it does not contain.
func TestAPackageLevelCallIsNotAttributedToTheLastFunction(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) ok(x int) int { return p.awaited(x) }

var seeded = await(7)
`})
	code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 1 {
		t.Fatalf("code %d, want 1\nout: %s", code, out)
	}
	if strings.Contains(out, "(*Peer).ok calls await") {
		t.Errorf("the package-level call belongs to no function; it said %q", out)
	}
	// And a waiver for that function must not reach it.
	code, out, _ = drive(t, "--wraps", "await", "--via", "(*Peer).awaited",
		"--allow", "(*Peer).ok=not this one", dir)
	if code != 1 {
		t.Fatalf("the waiver must not cover a call outside the function; code %d\n%s", code, out)
	}
}

// ⛔ A waiver that names nothing waived every call with no enclosing function,
// printing `waived p.go:5 ()`.
func TestAWaiverThatNamesNothingIsRefused(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped})
	code, _, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--allow", "=why", dir)
	if code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
	if !strings.Contains(errOut, "names no site") {
		t.Errorf("it should say so; it said %q", errOut)
	}
}

// ⛔ The tail line used to say "every one of the N goes through" while N counted
// sites waived just above it, so the summary contradicted its own listing.
func TestTheSummaryCountsWhatWasWaived(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) direct(x int) int { return await(x) }
`})
	code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited",
		"--allow", "(*Peer).direct=measured, and deliberate", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0\nout: %s", code, out)
	}
	if !strings.Contains(out, "except 1 waived above") {
		t.Errorf("the summary must account for the waiver; it said %q", out)
	}
}

// ⛔ go/parser reads ONE directory. A run at a repository root would read the
// root package and call everything below it clean, so each run says so.
func TestItSaysThatSubdirectoriesAreNotScanned(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped})
	if _, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir); !strings.Contains(out, "subdirectories are NOT scanned") {
		t.Errorf("it must say what it did not read; it said %q", out)
	}
}

// ⛔ A file Go cannot parse is not a file with no calls in it. Mutation found
// this branch untested: the existing refusal test used an EMPTY directory,
// which exits at a different check.
func TestAFileThatWillNotParseIsRefused(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped, "broken.go": "package p\nfunc ("})
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 2 {
		t.Fatalf("code %d, want 2\nout: %s", code, out)
	}
	if !strings.Contains(errOut, "reading the directory") {
		t.Errorf("it should say what failed; it said %q", errOut)
	}
	if strings.Contains(out, "every one of the") {
		t.Error("a package it could not finish reading is not a clean one")
	}
}

// ⛔ --expect is the guard against a rule that matched nothing, so it must be
// EXACT. Mutation found that `!=` could become `<` and no test noticed: a run
// that examined MORE than expected would then pass silently.
func TestExpectIsExactInBothDirections(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) direct(x int) int { return await(x) }
`})
	for _, n := range []string{"1", "3"} {
		code, _, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--expect", n, dir)
		if code != 2 {
			t.Errorf("--expect %s must be refused against 2 calls, got %d", n, code)
		}
		if !strings.Contains(errOut, "expected exactly") {
			t.Errorf("it should say so; it said %q", errOut)
		}
	}
	if code, _, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--expect", "2", dir); code != 1 {
		t.Errorf("the right count passes the guard and reports the bypass, got %d", code)
	}
}

// ⛔ A dotted --wraps would half-match on the selector and report calls to any
// receiver, so it is refused. Mutation found the check could drop "(" unnoticed.
func TestADottedOrMethodShapedWrapsIsRefused(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped})
	for _, name := range []string{"pkg.await", "(*Peer).await"} {
		code, _, errOut := drive(t, "--wraps", name, "--via", "(*Peer).awaited", dir)
		if code != 2 {
			t.Errorf("--wraps %q must be refused, got %d", name, code)
		}
		if !strings.Contains(errOut, "unqualified") {
			t.Errorf("it should say why; it said %q", errOut)
		}
	}
}

// ⛔ The file order decides which declaration findFunc returns when a build-tag
// split declares the wrapper twice, and it decides the order of the report. It
// is sorted so a report reads the same twice; mutation found nothing asserted
// it, because every other fixture is one file.
func TestTheReportIsOrderedByFileName(t *testing.T) {
	dir := pkg(t, map[string]string{
		"a_first.go": wrapped + `
func (p *Peer) one(x int) int { return await(x) }
`,
		"b_second.go": `package p

func two(x int) int { return await(x) }
`,
	})
	code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 1 {
		t.Fatalf("code %d, want 1\nout: %s", code, out)
	}
	first := strings.Index(out, "a_first.go")
	second := strings.Index(out, "b_second.go")
	if first < 0 || second < 0 || first > second {
		t.Errorf("the report must be ordered by file name; it said %q", out)
	}
}
