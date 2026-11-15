// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// ⛔⛔ THIS FILE EXISTS BECAUSE A FULL mutsweep SAID THE HELPERS WERE UNTESTED,
// and it said it about four of them at once. Each is a pure function, each was
// reachable only through the whole command, and each had a guard that either
// SURVIVED its own deletion or went red by PANIC:
//
//	isIdent        s == ""                                       SURVIVED
//	splitMethod    !strings.HasPrefix(s, "(")                    SURVIVED
//	splitMethod    end < 0 || !strings.HasPrefix(s[end:], ").")  SURVIVED
//	receiverName   fd.Recv == nil || len(fd.Recv.List) == 0      caught by PANIC
//	enclosing.name len(e) == 0                                   caught by PANIC
//
// ⛔ A survivor and a panic are different failures with the same remedy. A
// survivor means nothing looked. A PANIC red means the suite went red because
// the mutated code CRASHED, not because an assertion said what the guard is
// for — so the next change that adds a check ahead of it turns that red into a
// silent survivor. Both want the value asserted on each side of the guard, and
// that needs the function to be callable, which is why enclosing.name is a
// method now instead of a closure over a local slice.
//
// ⚠ AND HERE IS WHAT THAT DID NOT ACHIEVE, measured rather than assumed: the
// two PANIC reds are STILL panic reds after these tests were written, and they
// cannot be anything else. Deleting a bounds guard makes the very call the
// test performs index out of range, so the test does fail — by crashing inside
// itself. There is no input for which `e[len(e)-1]` on an empty slice returns
// a wrong value instead of panicking.
//
// What the direct tests DO buy is the thing the tool's warning is actually
// about: if a later change stopped the walk from ever reaching these functions
// with an empty stack or a nil receiver, the panic would quietly stop
// happening — and these tests would still call them that way and still assert
// the value. The guard is asserted; only its deletion is observed as a crash.
// Saying so is better than reporting it as caught by an assertion it is not.
//
// ⚠ The survivors above were NOT defects: each was held one layer up, by
// `--wraps` being refused empty or by `--via` failing to resolve. A survivor is
// not a bug; it is the statement that nothing here would notice if it became
// one.

func TestIsIdent(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"await", true},
		{"a", true},
		{"_", true},
		{"_x1", true},
		{"await2", true},
		{"Await", true},
		{"attendre", true},
		// ⛔ Empty. Held one layer up by `--wraps ""` being refused, so this
		// survived deletion; it is asserted here because isIdent is the thing
		// that claims to answer the question.
		{"", false},
		// A digit may not lead: the loop's `i > 0` is what says so.
		{"1await", false},
		{"2", false},
		// The shapes --wraps must refuse, and the reason the charset check was
		// replaced: a method-shaped name would half-match on the selector.
		{"(*Peer).awaited", false},
		{"Peer.awaited", false},
		{"await(", false},
		{"a b", false},
		{"a-b", false},
	} {
		t.Run(tc.in, func(t *testing.T) {
			if got := isIdent(tc.in); got != tc.want {
				t.Errorf("isIdent(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSplitMethod(t *testing.T) {
	for _, tc := range []struct{ in, recv, fn string }{
		{"(*Peer).awaited", "(*Peer)", "awaited"},
		{"(Peer).awaited", "(Peer)", "awaited"},
		// A plain function is itself, with no receiver.
		{"awaited", "", "awaited"},
		{"", "", ""},
		// ⛔ Both of these SURVIVED deletion of their guard, and they are the
		// cases the guards are for: a name that does not open with "(" is not
		// a method, and one that opens with "(" but is not followed by ")."
		// is not either. Without them `s[:end+1]` and `s[end+2:]` slice a
		// string that has no such shape.
		{"(Peer", "", "(Peer"},
		{"(Peer)awaited", "", "(Peer)awaited"},
		// ⛔ THE ROW THAT DISCRIMINATES THE FIRST GUARD. The two above do not:
		// they hold a ")" nowhere or in the wrong place, so the SECOND guard
		// returns the same answer and deleting the first changes nothing — a
		// mutsweep showed it surviving. This one has a ")" followed by "." and
		// does not open with "(", so without the first guard it would be read
		// as the method "b" on the receiver "a)".
		{"a).b", "", "a).b"},
		{"(Peer).", "(Peer)", ""},
		{"()", "", "()"},
		// ⚠ I wrote this row expecting ("(a)", "(b).c") and the code answered
		// ("", "(a)(b).c"). The code is right and the expectation was mine: the
		// first ")" is at index 2 and is followed by "(", not ".", so this is
		// not a method spelling and is handed back whole. Kept, because a row
		// that records what the function actually does is worth more than the
		// one I guessed.
		{"(a)(b).c", "", "(a)(b).c"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			recv, fn := splitMethod(tc.in)
			if recv != tc.recv || fn != tc.fn {
				t.Errorf("splitMethod(%q) = (%q, %q), want (%q, %q)", tc.in, recv, fn, tc.recv, tc.fn)
			}
		})
	}
}

// ⛔ receiverName's guard went red by PANIC when deleted — `fd.Recv.List[0]` on
// a nil receiver — so nothing asserted that a PLAIN FUNCTION has no receiver
// name. That is the case the guard is for, and it is the common one: most
// declarations in a package are plain functions.
func TestReceiverName(t *testing.T) {
	for _, tc := range []struct{ name, decl, want string }{
		{"a plain function", "func await(x int) int { return x }", ""},
		{"a pointer receiver", "func (p *Peer) awaited() {}", "(*Peer)"},
		{"a value receiver", "func (p Peer) awaited() {}", "(Peer)"},
		{"an anonymous pointer receiver", "func (*Peer) awaited() {}", "(*Peer)"},
		// A generic receiver renders as neither shape, and is reported as
		// nothing rather than as a wrong name.
		{"a generic receiver", "func (p *Box[T]) awaited() {}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parser.ParseFile(token.NewFileSet(), "p.go", "package p\n"+tc.decl+"\n", 0)
			if err != nil {
				t.Fatal(err)
			}
			fd, ok := f.Decls[0].(*ast.FuncDecl)
			if !ok {
				t.Fatalf("the fixture is not a function declaration")
			}
			if got := receiverName(fd); got != tc.want {
				t.Errorf("receiverName(%q) = %q, want %q", tc.decl, got, tc.want)
			}
		})
	}
}

// ⛔ enclosing.name's empty check is a BOUNDS guard: deleted, it indexes -1 and
// the suite goes red by panic. A panic red is luck — it says the code crashed,
// not what the guard is for. Asserted as a value now, which is why it is a
// method rather than a closure.
func TestEnclosingName(t *testing.T) {
	if got := enclosing(nil).name(); got != "" {
		t.Errorf("an empty stack is package level, got %q", got)
	}
	if got := (enclosing{}).name(); got != "" {
		t.Errorf("an empty non-nil stack is package level too, got %q", got)
	}
	if got := (enclosing{"(*Peer).one"}).name(); got != "(*Peer).one" {
		t.Errorf("got %q", got)
	}
	// The INNERMOST, which is what a report must name.
	if got := (enclosing{"outer", "inner"}).name(); got != "inner" {
		t.Errorf("got %q, want the innermost", got)
	}
}

// ⛔ AND THE ONE SURVIVOR THAT WAS NOT A HELPER: `within(via, id.Pos())` in the
// value pass. Naming the wrapped function INSIDE the wrapper is the point of
// the wrapper, so it must not be reported — and no fixture had a value use
// there, because the wrapper only ever CALLED it. Deleting the check left the
// suite green while the tool accused the wrapper of the one thing it exists to
// do.
func TestTheWrapperMayTakeTheNameAsAValue(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": `package p

type Peer struct{}

func await(x int) int { return x }

func (p *Peer) awaited(x int) int {
	h := await
	return h(x)
}

func (p *Peer) one(x int) int { return p.awaited(x) }
`})
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--expect", "0", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0: the wrapper may name what it wraps\nout: %s\nerr: %s", code, out, errOut)
	}
	if strings.Contains(out, "as a VALUE") {
		t.Errorf("the value use inside the wrapper is the wrapper working; it said %q", out)
	}
}

// ⛔⛔ `--expect 0` IS THE FLAG'S WHOLE DOCUMENTED PURPOSE — "this is how a
// caller says they meant zero calls" — and a BOUNDARY sweep found
// `o.expect >= 0` surviving its move to `> 0`: with that, `--expect 0` means
// OFF and the one value the help names stops working. The existing test that
// passes `--expect 0` could not see it, because the run exits 0 either way.
// This one requires it to REFUSE.
func TestExpectZeroIsAnExpectationAndNotOff(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) one(x int) int { return p.awaited(x) }
`})
	// The wrapper's own call makes this one examined call, so `--expect 0` is
	// a statement the package contradicts.
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--expect", "0", dir)
	if code != 2 {
		t.Fatalf("code %d, want 2: one call was examined and 0 were expected\nout: %s\nerr: %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "expected exactly 0") {
		t.Errorf("the refusal must say what was expected; it said %q", errOut)
	}
	// And the control: with the right number it passes, so the status above
	// cannot come from anything else.
	if code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", "--expect", "1", dir); code != 0 {
		t.Fatalf("--expect 1 is correct here; code %d\nout: %s\nerr: %s", code, out, errOut)
	}
}

// ⛔ "except N waived above" IS PRINTED ONLY WHEN SOMETHING WAS WAIVED. A
// boundary sweep found `waivedCount > 0` surviving its move to `>=`, which
// makes a clean run announce "except 0 waived above" — a tail line
// contradicting the absence of lines above it, which is the defect that
// sentence was added to fix in the first place.
func TestACleanRunDoesNotMentionWaivers(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) one(x int) int { return p.awaited(x) }
`})
	code, out, _ := drive(t, "--wraps", "await", "--via", "(*Peer).awaited", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0\n%s", code, out)
	}
	if strings.Contains(out, "waived") {
		t.Errorf("nothing was waived, so no line may mention one; it said %q", out)
	}
	// And the control: with a waiver, the count IS mentioned.
	dir = pkg(t, map[string]string{"p.go": wrapped + `
func (p *Peer) bad(x int) int { return await(x) }
`})
	code, out, errOut := drive(t, "--wraps", "await", "--via", "(*Peer).awaited",
		"--allow", "(*Peer).bad=measured, it cannot go through", dir)
	if code != 0 {
		t.Fatalf("the waiver should clear it; code %d\nout: %s\nerr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "except 1 waived above") {
		t.Errorf("the tail must say what was waived; it said %q", out)
	}
}

// ⚠ THREE BOUNDARY SURVIVORS THAT ARE NOT DEFECTS, named here so the next
// sweep's reader does not spend the morning on them again:
//
//	main.go  b.Pos() <= pos   [<= to <]   within()
//	main.go  pos <= b.End()   [<= to <]   within()
//	main.go  end < 0          [< to <=]   splitMethod
//
// The first two compare a call's position against a function BODY's braces: a
// call cannot begin exactly at `{` or exactly at `}`, so the equal case has no
// input. The third is reachable only for a name that does not open with "(",
// and the guard above it has already returned for those — the row "a).b" in
// TestSplitMethod is what pins that one. A survivor is not a defect; it is a
// statement that nothing would notice if it became one, and for these three
// nothing can.
