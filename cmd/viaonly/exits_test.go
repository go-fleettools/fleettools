// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
	"testing"
)

// The shape go-crdt/collab#274 had, reduced to what the pass has to see: a
// predicate, and a function whose exits are supposed to ask it.
const asksFixture = `package p

type Peer struct{ ready bool }

func gathered(p *Peer) bool { return p.ready }
`

// ⛔⛔ THE INCIDENT, AS SMALL AS IT GETS. Round 2 of collab#274: the question
// "is this offer usable?" was asked in ONE branch, and the exits around it
// returned flatly. Three of the five exits below never ask it — the fast path
// at the top, the sibling case, and THE FLAT RETURN AFTER THE SWITCH, which is
// the one a reader credits to the branch that did ask.
func TestTheQuestionAskedInOneBranchOnly(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": asksFixture + `
func (p *Peer) Offer(n int) (string, error) {
	if p.ready {
		return "fast", nil
	}
	switch n {
	case 1:
		return "one", nil
	case 2:
		if !gathered(p) {
			return "", nil
		}
		return "two", nil
	}
	return "", nil
}
`})
	code, out, errOut := drive(t, "--asks", "gathered", "--in", "(*Peer).Offer", dir)
	if code != 1 {
		t.Fatalf("code %d, want 1\nout: %s\nerr: %s", code, out, errOut)
	}
	for _, want := range []string{
		"(*Peer).Offer returns without asking gathered",
		"3 of 5 exit(s) of (*Peer).Offer do not ask gathered",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report should contain %q; it said %q", want, out)
		}
	}
	// ⛔ The two exits that DID ask must not be reported, or the count means
	// nothing — a report that names every exit is a report that found nothing.
	if n := strings.Count(out, "without asking gathered"); n != 3 {
		t.Errorf("3 exits should be named, %d were:\n%s", n, out)
	}
}

// Every exit asking is the clean case, and the count is printed so a caller can
// tell it from a rule that matched nothing.
func TestEveryExitAskingIsClean(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": asksFixture + `
func (p *Peer) Offer() (string, error) {
	if !gathered(p) {
		return "", nil
	}
	if p.ready {
		return "fast", nil
	}
	return "slow", nil
}
`})
	code, out, errOut := drive(t, "--asks", "gathered", "--in", "(*Peer).Offer", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0\nout: %s\nerr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "every one of the 3 exit(s) of (*Peer).Offer asks gathered") {
		t.Errorf("it should say how many it examined; it said %q", out)
	}
}

// ⛔ The table is the enumeration this lesson is about: a statement kind left
// out of the walk is an exit the pass cannot see, and an exit it cannot see is
// a rule it reports as held. Each row is ONE function, with how many of its
// exits ask the predicate.
func TestEveryStatementKindCarriesTheAnswer(t *testing.T) {
	const minRows = 16
	cases := []struct {
		name  string
		body  string
		exits int // exits found
		bad   int // of those, the ones that never asked
	}{
		{"a single bare return", `
	return 0, nil`, 1, 1},
		{"the predicate before the return", `
	_ = gathered(p)
	return 0, nil`, 1, 0},
		{"an if whose condition asks and whose body is empty", `
	if gathered(p) {
	}
	return 0, nil`, 1, 0},
		{"a fast path above the question", `
	if p.ready {
		return 1, nil
	}
	if !gathered(p) {
		return 0, nil
	}
	return 2, nil`, 3, 1},
		{"the question in an if condition reaches its else", `
	if gathered(p) {
		return 1, nil
	} else {
		return 2, nil
	}`, 2, 0},
		{"a nested block inherits the answer", `
	_ = gathered(p)
	{
		return 1, nil
	}`, 1, 0},
		{"a for body inherits it", `
	_ = gathered(p)
	for i := 0; i < 3; i++ {
		return i, nil
	}
	return 0, nil`, 2, 0},
		{"a for condition asks it, and it is evaluated before the first pass", `
	for gathered(p) {
		return 1, nil
	}
	return 0, nil`, 2, 0},
		{"a range body inherits it", `
	for _, x := range []int{1} {
		return x, nil
	}
	return 0, nil`, 2, 2},
		{"a range expression asks it, and it is evaluated once", `
	for _, x := range pick(gathered(p)) {
		return x, nil
	}
	return 0, nil`, 2, 0},
		{"a switch tag asks it", `
	switch gathered(p) {
	case true:
		return 1, nil
	}
	return 0, nil`, 2, 0},
		// ⛔⛔ THE ROW THE WHOLE MODE TURNS ON, and the one that was wrong first:
		// an ask inside ONE case is conditional, so the sibling case AND the exit
		// after the switch have not asked it. The first version of the walk
		// credited them both and reported this function clean.
		{"an ask in one switch case reaches neither its sibling nor past the switch", `
	switch p.ready {
	case gathered(p):
		return 1, nil
	case false:
		return 2, nil
	}
	return 0, nil`, 3, 2},
		{"a type switch init asks it", `
	switch v := any(gathered(p)).(type) {
	case bool:
		_ = v
		return 1, nil
	}
	return 0, nil`, 2, 0},
		{"a select case body inherits it", `
	_ = gathered(p)
	select {
	case <-make(chan int):
		return 1, nil
	}`, 1, 0},
		{"a select case body without it", `
	select {
	case <-make(chan int):
		return 1, nil
	}`, 1, 1},
		{"a labelled statement passes it down", `
	_ = gathered(p)
loop:
	for {
		break loop
	}
	return 0, nil`, 1, 0},
		{"a deferred ask does NOT count", `
	defer gathered(p)
	return 0, nil`, 1, 1},
		{"an ask inside a go statement does NOT count", `
	go func() { _ = gathered(p) }()
	return 0, nil`, 1, 1},
		{"an ask inside a function literal does NOT count", `
	f := func() bool { return gathered(p) }
	_ = f
	return 0, nil`, 1, 1},
		{"a return inside a function literal is NOT this function's exit", `
	_ = gathered(p)
	f := func() (int, error) { return 9, nil }
	_ = f
	return 0, nil`, 1, 0},
	}
	if len(cases) < minRows {
		t.Fatalf("this table is the enumeration: %d rows, at least %d", len(cases), minRows)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := pkg(t, map[string]string{"p.go": asksFixture + `
func pick(b bool) []int { return nil }

func (p *Peer) Offer() (int, error) {` + c.body + `
}
`})
			code, out, errOut := drive(t, "--asks", "gathered", "--in", "(*Peer).Offer", dir)
			wantCode := 0
			if c.bad > 0 {
				wantCode = 1
			}
			if code != wantCode {
				t.Fatalf("code %d, want %d\nout: %s\nerr: %s", code, wantCode, out, errOut)
			}
			if got := strings.Count(out, "without asking gathered"); got != c.bad {
				t.Errorf("%d exit(s) named, want %d\n%s", got, c.bad, out)
			}
			if c.bad > 0 {
				if !strings.Contains(out, exitTally(c.bad, c.exits)) {
					t.Errorf("the tail should read %q; it said %q", exitTally(c.bad, c.exits), out)
				}
			} else if !strings.Contains(out, cleanTally(c.exits)) {
				t.Errorf("the tail should read %q; it said %q", cleanTally(c.exits), out)
			}
		})
	}
}

func exitTally(bad, total int) string {
	return fmt.Sprintf("%d of %d exit(s) of (*Peer).Offer do not ask gathered", bad, total)
}

func cleanTally(total int) string {
	return fmt.Sprintf("every one of the %d exit(s) of (*Peer).Offer asks gathered", total)
}

// ⛔ THE CLOSING BRACE IS AN EXIT for a function with no results, and it is the
// one a reader never writes down. Without it the common void function had its
// final exit counted nowhere.
func TestTheEndOfAVoidFunctionIsAnExit(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": asksFixture + `
func (p *Peer) Close() {
	if p.ready {
		return
	}
}
`})
	code, out, _ := drive(t, "--asks", "gathered", "--in", "(*Peer).Close", dir)
	if code != 1 {
		t.Fatalf("code %d, want 1\nout: %s", code, out)
	}
	if !strings.Contains(out, "falls off the end") {
		t.Errorf("the implicit exit should be named as such; it said %q", out)
	}
	if !strings.Contains(out, "2 of 2 exit(s)") {
		t.Errorf("both exits should be counted; it said %q", out)
	}
}

// A function with results cannot fall off its end — the compiler refuses it —
// so nothing is added, and the count is the returns only.
func TestAFunctionWithResultsHasNoImplicitExit(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": asksFixture + `
func (p *Peer) Offer() int {
	_ = gathered(p)
	return 1
}
`})
	code, out, errOut := drive(t, "--asks", "gathered", "--in", "(*Peer).Offer", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0\nout: %s\nerr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "every one of the 1 exit(s)") {
		t.Errorf("one exit, not two; it said %q", out)
	}
}

// ⛔ A declared name that is not there is the refusal this command exists for:
// "every exit asked it" is true and worthless about a function the package does
// not have.
func TestAnAbsentNameIsRefused(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": asksFixture + `
func (p *Peer) Offer() int { return 1 }
`})
	for _, c := range []struct{ asks, in, want string }{
		{"gathered", "(*Peer).Missing", "--in"},
		{"missing", "(*Peer).Offer", "--asks"},
	} {
		code, out, errOut := drive(t, "--asks", c.asks, "--in", c.in, dir)
		if code != 2 {
			t.Errorf("%s/%s: code %d, want 2", c.asks, c.in, code)
		}
		if !strings.Contains(errOut, c.want) {
			t.Errorf("%s/%s: the refusal should name %s; it said %q", c.asks, c.in, c.want, errOut)
		}
		if strings.Contains(out, "every one of the") {
			t.Errorf("%s/%s: a refusal must not also report a clean function: %q", c.asks, c.in, out)
		}
	}
}

// ⛔ A method predicate is matched by the spelling of its selector, which can
// count a call on an unrelated receiver as asking — a FALSE PASS. It is said on
// every run rather than documented.
func TestAMethodPredicateSaysWhatItCannotTell(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": `package p

type Peer struct{ ready bool }

func (p *Peer) gathered() bool { return p.ready }

func (p *Peer) Offer() (int, error) {
	if !p.gathered() {
		return 0, nil
	}
	return 1, nil
}
`})
	code, out, errOut := drive(t, "--asks", "(*Peer).gathered", "--in", "(*Peer).Offer", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0\nout: %s\nerr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "ANY receiver counts as asking") {
		t.Errorf("the run must say what the spelling match cannot tell; it said %q", out)
	}
}

// ⛔⛔ NO EXIT IS NOT A CLEAN FUNCTION: --in is declared, so finding nothing
// means the pass looked at the wrong node.
func TestAFunctionWithNoExitIsRefused(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": asksFixture + `
func (p *Peer) Spin() {
	for {
		_ = gathered(p)
	}
}
`})
	code, out, errOut := drive(t, "--asks", "gathered", "--in", "(*Peer).Spin", dir)
	if code != 2 {
		t.Fatalf("code %d, want 2\nout: %s\nerr: %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "no exit point found") {
		t.Errorf("it should refuse rather than pass; it said %q", errOut)
	}
	// --expect 0 is how a caller says they meant it.
	code, _, _ = drive(t, "--asks", "gathered", "--in", "(*Peer).Spin", "--expect", "0", dir)
	if code != 0 {
		t.Errorf("--expect 0 should make it a verdict, got code %d", code)
	}
}

// ⛔ A waiver naming the FUNCTION would waive every exit at once, which is a
// blanket exemption wearing the clothes of a single one.
func TestAFunctionNamedWaiverIsRefusedHere(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": asksFixture + `
func (p *Peer) Offer() (int, error) {
	if p.ready {
		return 1, nil
	}
	if !gathered(p) {
		return 0, nil
	}
	return 2, nil
}
`})
	code, _, errOut := drive(t, "--asks", "gathered", "--in", "(*Peer).Offer",
		"--allow", "(*Peer).Offer=the whole function", dir)
	if code != 2 {
		t.Errorf("code %d, want 2", code)
	}
	if !strings.Contains(errOut, "would waive all of them at once") {
		t.Errorf("it should refuse a blanket waiver; it said %q", errOut)
	}
}

// A waiver that matches one exit is printed with its reason, and one that
// matches nothing is reported rather than silently carried.
func TestAWaiverIsPrintedAndAStaleOneIsReported(t *testing.T) {
	src := asksFixture + `
func (p *Peer) Offer() (int, error) {
	if p.ready {
		return 1, nil
	}
	if !gathered(p) {
		return 0, nil
	}
	return 2, nil
}
`
	dir := pkg(t, map[string]string{"p.go": src})
	// The fast path is p.go:9 — the fixture's five lines, then the signature.
	code, out, errOut := drive(t, "--asks", "gathered", "--in", "(*Peer).Offer",
		"--allow", "p.go:9=the fast path cannot have gathered anything yet", dir)
	if code != 0 {
		t.Fatalf("code %d, want 0\nout: %s\nerr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "the fast path cannot have gathered anything yet") {
		t.Errorf("the reason must be printed; it said %q", out)
	}
	if !strings.Contains(out, "except 1 waived above") {
		t.Errorf("the tail must not claim every exit asks; it said %q", out)
	}

	// ⛔ A FINDING IS NOT DOWNGRADED BY A STALE WAIVER. The run below still has a
	// fast path that never asks, so the verdict is 1: the command DID decide,
	// and 2 means "refused to decide". My first version of this test asserted 2
	// here and was simply wrong about its own fixture.
	code, _, errOut = drive(t, "--asks", "gathered", "--in", "(*Peer).Offer",
		"--allow", "p.go:999=moved long ago", dir)
	if code != 1 {
		t.Errorf("a finding outranks a stale waiver, got %d", code)
	}
	if !strings.Contains(errOut, "match no exit") {
		t.Errorf("it should name the stale waiver; it said %q", errOut)
	}

	// ⛔⛔ AND HERE IS THE SILENT ONE: everything asks, so without this check the
	// run exits 0 carrying a public exemption for an exit that is not there.
	clean := pkg(t, map[string]string{"p.go": asksFixture + `
func (p *Peer) Offer() (int, error) {
	if !gathered(p) {
		return 0, nil
	}
	return 1, nil
}
`})
	code, out, errOut = drive(t, "--asks", "gathered", "--in", "(*Peer).Offer",
		"--allow", "p.go:999=moved long ago", clean)
	if code != 2 {
		t.Errorf("a stale waiver on an otherwise clean run must refuse, got %d\nout: %s", code, out)
	}
	if !strings.Contains(errOut, "match no exit") {
		t.Errorf("it should name the stale waiver; it said %q", errOut)
	}
}

// ⛔ go/parser does not evaluate //go:build, so a function declared once per
// build tag has every variant read — and each variant's exits are its own.
func TestEveryBuildTagVariantIsExamined(t *testing.T) {
	dir := pkg(t, map[string]string{
		"p.go": asksFixture,
		"a_js.go": `package p

func (p *Peer) Offer() (int, error) { return 1, nil }
`,
		"a_other.go": `package p

func (p *Peer) Offer() (int, error) {
	if !gathered(p) {
		return 0, nil
	}
	return 1, nil
}
`,
	})
	code, out, _ := drive(t, "--asks", "gathered", "--in", "(*Peer).Offer", dir)
	if code != 1 {
		t.Fatalf("code %d, want 1\nout: %s", code, out)
	}
	if !strings.Contains(out, "is declared 2 times here") {
		t.Errorf("the split must be said on the run; it said %q", out)
	}
	if !strings.Contains(out, "1 of 3 exit(s)") {
		t.Errorf("both variants' exits should be counted; it said %q", out)
	}
}

// ⛔ The same bytes must give the same verdict: ParseDir returns a map, and the
// file sort is what keeps the report and the exit status from moving. This is
// the call-site rule's measured defect, asserted for the other rule too.
func TestTheExitVerdictIsTheSameOnTheSameBytes(t *testing.T) {
	dir := pkg(t, map[string]string{
		"p.go": asksFixture,
		"b_test.go": `package p

func (p *Peer) Offer() (int, error) { return 1, nil }
`,
		"a.go": `package p

func (p *Peer) Offer() (int, error) {
	if !gathered(p) {
		return 0, nil
	}
	return 1, nil
}
`,
	})
	first, firstOut, _ := drive(t, "--asks", "gathered", "--in", "(*Peer).Offer", "--tests", dir)
	for i := 0; i < 32; i++ {
		code, out, _ := drive(t, "--asks", "gathered", "--in", "(*Peer).Offer", "--tests", dir)
		if code != first {
			t.Fatalf("run %d: code %d, first run %d", i, code, first)
		}
		if out != firstOut {
			t.Fatalf("run %d: the report moved\nfirst: %s\nnow:   %s", i, firstOut, out)
		}
	}
}

// ⛔⛔ WHERE A VOID FUNCTION ENDS IS A JUDGEMENT, and these rows are the ones a
// mutation sweep found nothing holding. "Can this body fall off its end" decides
// whether the closing brace is an exit at all, so believing a body terminates
// when it does not LOSES an exit — and a lost exit is a rule reported as held,
// which is the failure this command exists to refuse. Each row is a void method
// Close(), with how many exits it has.
func TestWhetherAVoidBodyCanFallOffItsEnd(t *testing.T) {
	for _, c := range []struct {
		name  string
		body  string
		exits int // 0 means the run refuses: no exit found
	}{
		{"an empty body falls off it", ``, 1},
		{"a last statement that is an expression but NOT a call", `
	ch := make(chan int)
	<-ch`, 1},
		{"a switch whose default returns but whose other case does not", `
	switch {
	case p.ready:
		_ = 1
	default:
		return
	}`, 2},
		{"a select clause whose body does not terminate", `
	select {
	case <-make(chan int):
		_ = 1
	}`, 1},
		{"a switch that terminates but carries a break out of itself", `
	switch {
	default:
		if p.ready {
			break
		}
		return
	}`, 2},
		{"a switch with no default may match nothing", `
	switch {
	case p.ready:
		return
	}`, 2},
		{"for with no condition and no break cannot fall off it", `
	for {
		_ = p.ready
	}`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := pkg(t, map[string]string{"p.go": asksFixture + `
func (p *Peer) Close() {` + c.body + `
}
`})
			code, out, errOut := drive(t, "--asks", "gathered", "--in", "(*Peer).Close", dir)
			if c.exits == 0 {
				if code != 2 {
					t.Fatalf("code %d, want 2 (no exit to judge)\nout: %s", code, out)
				}
				if !strings.Contains(errOut, "no exit point found") {
					t.Errorf("it should refuse rather than pass; it said %q", errOut)
				}
				return
			}
			if code != 1 {
				t.Fatalf("code %d, want 1\nout: %s\nerr: %s", code, out, errOut)
			}
			want := fmt.Sprintf("%d of %d exit(s) of (*Peer).Close do not ask gathered", c.exits, c.exits)
			if !strings.Contains(out, want) {
				t.Errorf("the tail should read %q; it said %q", want, out)
			}
		})
	}
}

// ⛔ A malformed --asks gets its OWN refusal, not the absent-name one. Both exit
// 2, which is why a mutation that deleted this check survived until the message
// was asserted: an exit status alone cannot tell a caller which mistake they
// made.
func TestAMalformedPredicateSaysSo(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": asksFixture + `
func (p *Peer) Offer() int { return 1 }
`})
	code, _, errOut := drive(t, "--asks", "p.gathered", "--in", "(*Peer).Offer", dir)
	if code != 2 {
		t.Errorf("code %d, want 2", code)
	}
	if !strings.Contains(errOut, "neither a function name nor a method") {
		t.Errorf("it should name the shape it wanted; it said %q", errOut)
	}
}

// ⛔ A duplicate in the predicate set makes the PRINTED rule disagree with the
// rule enforced — "gathered or gathered" — so it is refused rather than quietly
// deduplicated.
func TestADuplicatePredicateIsRefused(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": asksFixture + `
func (p *Peer) Offer() int {
	if gathered(p) {
	}
	return 1
}
`})
	code, out, errOut := drive(t, "--asks", "gathered", "--asks", "gathered", "--in", "(*Peer).Offer", dir)
	if code != 2 {
		t.Errorf("code %d, want 2\nout: %s", code, out)
	}
	if !strings.Contains(errOut, "given twice") {
		t.Errorf("it should say which name repeated; it said %q", errOut)
	}
}

// ⛔⛔ THE THREE CONTRACTS A MUTATION CAN ONLY BREAK BY CRASHING, asserted as
// VALUES so the next change finds them held by an assertion rather than by
// luck. A sweep caught the first two "by PANIC": the guard exists to stop a
// crash, so deleting it crashes, and a panic red says the code broke without
// saying what the guard was for.
func TestTheEdgesOfTheTerminationJudgement(t *testing.T) {
	if endsTerminating(nil) {
		t.Error("an empty body cannot terminate: there is no statement to do it")
	}
	// An expression statement that is not a call — a receive — is not a panic
	// call, so it does not terminate.
	recv := &ast.ExprStmt{X: &ast.UnaryExpr{Op: token.ARROW, X: ast.NewIdent("ch")}}
	if terminating(recv) {
		t.Error("<-ch does not terminate a body")
	}
	if !terminating(&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic")}}) {
		t.Error("panic() does terminate a body")
	}
	// ⛔ asksIn is handed a nil node on nearly every statement it reads, and it
	// no longer guards that itself: the check inside the walk answers it. If
	// this ever panics, that check went away.
	if asksIn(map[string]bool{"gathered": true}, nil) {
		t.Error("a nil node asks nothing")
	}
}

// --expect is a guard against a rule that matched nothing, and it counts exits
// here rather than calls.
func TestExpectCountsExits(t *testing.T) {
	dir := pkg(t, map[string]string{"p.go": asksFixture + `
func (p *Peer) Offer() (int, error) {
	if !gathered(p) {
		return 0, nil
	}
	return 1, nil
}
`})
	code, _, errOut := drive(t, "--asks", "gathered", "--in", "(*Peer).Offer", "--expect", "5", dir)
	if code != 2 {
		t.Errorf("code %d, want 2", code)
	}
	if !strings.Contains(errOut, "examined 2 exit(s)") {
		t.Errorf("it should say what it examined; it said %q", errOut)
	}
}
