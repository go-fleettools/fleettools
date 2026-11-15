// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"strings"
	"testing"
)

// ⛔⛔ EXIT 0 ON A DIRECTORY IT NEVER READ. The extra arguments were discarded,
// so a run naming two directories reported the first one clean and said nothing
// about the second — a clean verdict about something unexamined, which is the
// defect this command exists to catch, written into the command itself.
func TestASecondDirectoryIsRefusedRatherThanIgnored(t *testing.T) {
	a := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) ok(x int) int { return p.awaited(x) }
`})
	b := pkg(t, map[string]string{"q.go": wrapped + `
func (p *Peer) bad(x int) int { return await(x) }
`})
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", a, b)
	if code != 2 {
		t.Fatalf("code %d, want 2: refused to decide\nout: %s\nerr: %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "one directory at a time") {
		t.Errorf("the refusal must say what it cannot do; it said %q", errOut)
	}
	// ⛔ And it must not have reported on the first one either: a report plus a
	// refusal reads as "here is the answer, with a caveat".
	if strings.Contains(out, "goes through") {
		t.Errorf("nothing may be reported when the request was refused; it said %q", out)
	}
}

// ⛔⛔ A SELECTOR HALF-MATCH WAS REPORTED AS A VALUE USE: exit 1 on a
// non-finding. --wraps is an unqualified name, so a method of ANOTHER type
// spelled the same, and a struct field of that name, are simply not the
// subject — and a value use is unwaivable by design, so there was no way to
// say the tool was wrong.
func TestANameSelectedFromSomethingElseIsNotTheWrappedFunction(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
type Other struct{}

func (o *Other) await(x int) int { return x }

type Holder struct{ await func(int) int }

func (p *Peer) ok(x int) int { return p.awaited(x) }

func useMethod(o *Other) int { return o.await(3) }

func useField(h Holder) int { return h.await(4) }

func takeField(h Holder) func(int) int { return h.await }
`})
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0: nothing here reaches the wrapped function\nout: %s\nerr: %s", code, out, errOut)
	}
	if strings.Contains(out, "as a VALUE") {
		t.Errorf("a selected name is not the unqualified function; it said %q", out)
	}
}

// ⛔ A NAME THAT IS BEING DEFINED IS NOT A USE, and a spelling-based pass keeps
// finding one more position where that is true — the same lesson as the charset
// that became isIdent, one level up. Each defining position the pass excludes
// has a row here; found by this change's own test, which tripped on a struct
// field named `await`.
func TestANameBeingDefinedIsNotAValueUse(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
type Holder struct{ await func(int) int }

type Awaiter interface{ await(int) int }

type await2 struct{}

func param(await int) int { return 0 }

func named() (await int) { return 0 }

func (p *Peer) ok(x int) int { return p.awaited(x) }

func labelled() {
await:
	for i := 0; i < 1; i++ {
		break await
	}
}

func keyed() Holder { return Holder{await: nil} }

// ⚠ The range variable is DEFINED and never read, on purpose: reading it would
// be a shadowing use, which this pass reports by design, and the row below
// would then be testing the documented limit instead of the exclusion. Go
// allows an unused range variable, so the definition can stand alone.
func ranged(xs []int) {
	for await := range xs {
		_ = xs
	}
}

func shadowed() {
	await := 3
	_ = func() {}
	_ = await2{}
	_ = Holder{}
	_ = param
	_ = named
	_ = labelled
	_ = shadowed
	if await == 0 {
		return
	}
}
`})
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	// ⚠ shadowed() READS its own local `await`, and this pass CANNOT see that
	// the name refers to a local: resolving that needs go/types. So the
	// expected answer is the documented one, not the ideal one — this row
	// exists to pin what the tool actually claims, so a future reader does not
	// take the limit for a bug nobody noticed.
	if code != 1 {
		t.Fatalf("code %d, want 1: the shadowing READ is reported, as documented\nout: %s\nerr: %s", code, out, errOut)
	}
	// ⛔ Asserted by NAME, not by line number. A line number I worked out by
	// counting is a measure aligned to an index rather than to the thing, and
	// it passes just as happily when it points at nothing.
	if !strings.Contains(out, "shadowed takes await as a VALUE") {
		t.Errorf("the shadowing read inside shadowed() is the one documented report; it said %q", out)
	}
	// Exactly one value use, and it is that one: a count that drifts upward is
	// how a defining position creeps back in unnoticed, and the enclosing names
	// below are every place a definition was written.
	if !strings.Contains(out, "1 use(s) take it as a value") {
		t.Errorf("only the shadowing read may be reported; it said %q", out)
	}
	// ⛔⛔ `keyed` and `ranged` are here because a review reproduced BOTH, and
	// because the fixture above is why nothing saw them: it had `_ = Holder{}`,
	// the UNKEYED literal, so the field KEY was never in the population while
	// the field DECLARATION was. And `for await := range xs` is a different AST
	// node from `await := 3`, so excluding the second excluded nothing of the
	// first — the comment claiming "a := left-hand side" covered one of two.
	// A missing defining position is an exit 1 on something that is not the
	// subject, and a value use cannot be waived, so the caller has no way to
	// say the tool is wrong.
	for _, name := range []string{"param", "named", "labelled", "keyed", "ranged", "Holder", "Awaiter", "await2", "package level"} {
		if strings.Contains(out, name+" takes await as a VALUE") {
			t.Errorf("a defining position was reported as a value use (%s); it said %q", name, out)
		}
	}
}

// ⛔⛔ THE WRAPPER WAS ITS OWN BYPASS. go/parser does not evaluate //go:build,
// so a package split by build tags has every variant parsed at once — which is
// exactly go-crdt/collab's shape, with (*Peer).awaited in peer_js.go and
// peer_other.go. The first body found became "the wrapper" and the other
// variant's identical wrapper was reported as a bypass of itself. A rule that
// accuses its own subject is a false positive on the only repository this
// command was written for.
func TestABuildTagVariantOfTheWrapperIsNotItsOwnBypass(t *testing.T) {
	dir := pkg(t, map[string]string{
		"peer_js.go": `//go:build js

package p

type Peer struct{}

func await(x int) int { return x }

func (p *Peer) awaited(x int) int { return await(x) }

func (p *Peer) one(x int) int { return p.awaited(x) }
`,
		"peer_other.go": `//go:build !js

package p

func (p *Peer) awaited(x int) int { return await(x) }
`,
	})
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0: both variants ARE the wrapper\nout: %s\nerr: %s", code, out, errOut)
	}
	if strings.Contains(out, "calls await directly") {
		t.Errorf("a build-tag variant of the wrapper is not a bypass; it said %q", out)
	}
	// ⛔ And the run SAYS that two variants were read, because that is the only
	// signal a reader gets that //go:build was ignored.
	if !strings.Contains(out, "declared 2 times") {
		t.Errorf("it must say how many declarations it found; it said %q", out)
	}
}

// ⛔⛔ A VERDICT THAT FLIPPED ON THE SAME BYTES. The file-name sort was INSIDE
// the loop over packages, and ParseDir returns a map, so each package's files
// were ordered and the packages were not. A review measured the report order
// flipping in 1 run of 4 and the EXIT STATUS in 1 of 16, on one directory. A
// verdict that changes without an input changing cannot be reproduced, so it
// cannot be fixed, and a green run proves nothing about it.
//
// The fixture is the shape that exposes it: two packages in one directory, each
// declaring its own wrapper and its own bypass, so which package is read first
// decides both the report order and which wrapper is found.
func TestTheVerdictIsTheSameOnTheSameBytes(t *testing.T) {
	dir := pkg(t, map[string]string{
		"a.go": wrapped + `
func (p *Peer) bad(x int) int { return await(x) }
`,
		"z_test.go": `package p_test

type Peer struct{}

func await(x int) int { return x }

func (p *Peer) awaited(x int) int { return await(x) }

func (p *Peer) alsoBad(x int) int { return await(x) }
`,
	})
	wantCode, wantOut, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--tests", dir)
	for i := 0; i < 64; i++ {
		code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--tests", dir)
		if code != wantCode {
			t.Fatalf("run %d exited %d, run 0 exited %d, on identical bytes", i+1, code, wantCode)
		}
		if out != wantOut {
			t.Fatalf("run %d differs from run 0 on identical bytes:\nfirst: %q\nthis:  %q", i+1, wantOut, out)
		}
	}
}

// ⛔ A WAIVER THAT MATCHED NOTHING PASSED IN SILENCE: exit 0 and unmentioned.
// That is the stale-waiver failure arriving by the other door — the line moved
// or the function was renamed, so the gate carries a public exemption for
// something that is no longer there, and the next reader believes a call is
// being excused that nothing excuses.
func TestAWaiverThatMatchesNothingIsRefusedOnAnOtherwiseCleanRun(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) ok(x int) int { return p.awaited(x) }
`})
	// The control first: with no waiver this package is clean and exits 0, so
	// the status below cannot come from anything else.
	if code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir); code != 0 {
		t.Fatalf("the package must be clean without the waiver; code %d\n%s", code, out)
	}
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited",
		"--allow", "(*Peer).gone=a function that is not here any more", dir)
	if code != 2 {
		t.Fatalf("code %d, want 2: a waiver nobody can check\nout: %s\nerr: %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "(*Peer).gone") {
		t.Errorf("the refusal must name the stale waiver; it said %q", errOut)
	}
}

// ⛔ And a finding is NOT downgraded to "refused to decide" by a stale waiver
// alongside it: the command did decide. The stale waiver is still printed.
func TestAStaleWaiverDoesNotDowngradeARealFinding(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) bad(x int) int { return await(x) }
`})
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited",
		"--allow", "(*Peer).gone=a function that is not here any more", dir)
	if code != 1 {
		t.Fatalf("code %d, want 1: there is a real bypass\nout: %s\nerr: %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "(*Peer).gone") {
		t.Errorf("the stale waiver must still be reported; stderr = %q", errOut)
	}
	if !strings.Contains(out, "calls await directly") {
		t.Errorf("the bypass must still be named; it said %q", out)
	}
}

// A bypass written outside any function printed a BLANK where the reader looks
// for a name — `p.go:11:  calls await…` — and could not be waived by one
// either. The value report already said "package level"; the bypass report
// says it too.
func TestAPackageLevelBypassIsNamedRatherThanBlank(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) ok(x int) int { return p.awaited(x) }

var seeded = await(7)
`})
	code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 1 {
		t.Fatalf("code %d, want 1\nout: %s", code, out)
	}
	if !strings.Contains(out, "package level calls await directly") {
		t.Errorf("it must name the package level; it said %q", out)
	}
	// And the waiver spelling the report prints must actually work, or the
	// name is decoration.
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited",
		"--allow", "package level=the seed runs before any peer exists", dir)
	if code != 0 {
		t.Fatalf("the printed name must be waivable; code %d\nout: %s\nerr: %s", code, out, errOut)
	}
}
