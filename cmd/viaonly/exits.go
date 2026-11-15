// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"path/filepath"
	"strings"
)

// ⛔⛔ WHY THIS MODE EXISTS, and why --wraps could not answer it.
//
// The rule --wraps/--via enforces is about CALL SITES: every call to the raw
// function goes through the wrapper. go-crdt/collab#274 is the same family —
// "a rule honoured at some siblings and not at others" — arriving by a door a
// call-site pass cannot see. Four rounds of independent review, each finding
// the same shape in the previous round's FIX:
//
//	round 2  "is this offer usable?" asked at ONE of four exits; the other
//	         three returned nil flatly
//	round 3  the expiry branch carried its OWN copy of the contract
//	round 4  one more exit (awaitOpen) did not ask, and a comment of mine
//	         stated a false invariant that licensed breaking the code
//
// ⛔ THE MECHANISM, which is the thing to keep: an early `return` does not read
// as an exit. Every missed gate was a fast path — `if alreadyReady { return ok }`
// in the first two lines of a function. A reader re-reads the `select` and the
// error branches, never the top. So the question a program must ask is not
// "which calls bypass the wrapper" but "which EXITS of this function never
// asked the question at all", and the two are different nodes: a `return` is
// not a call, which is why viaonly saw the sixth site of that incident and not
// the seventh or eighth.
//
// ⛔ IT INFERS NOTHING, exactly as the call-site rule does not: --in names the
// function and --asks names the predicate. A pass that guessed which call was
// "the contract" would cry wolf on every `return nil, err`.

// exit is one point a function returns from, with whether the predicate was
// asked on the way there.
type exit struct {
	file     string
	line     int
	in       string // the function, for the report and for nothing else
	asked    bool
	implicit bool // the closing brace of a function with no results
}

func (e exit) key() string { return fmt.Sprintf("%s:%d", e.file, e.line) }

func (e exit) what() string {
	if e.implicit {
		return "falls off the end"
	}
	return "returns"
}

// checkExits enforces "every exit of --in asks --asks". It is given the files
// the shared reader already parsed and sorted, so the two rules cannot disagree
// about which files a directory holds.
func checkExits(out, errOut io.Writer, fset *token.FileSet, files []*ast.File, o opts, dir string, waived map[string]string, skipped int) int {
	// ⛔ A waiver may not name the FUNCTION here, which is the one place this
	// mode has to differ from the call-site rule. Every exit of --in is inside
	// the same function, so '(*Peer).Offer=why' would waive all of them at once
	// — a blanket exemption wearing the clothes of a single one. file.go:LINE is
	// brittle, as --allow's own help says; here it is the only handle that can
	// mean one exit.
	for site := range waived {
		if !strings.Contains(site, ":") {
			fmt.Fprintf(errOut, "viaonly: --allow %q names a function, and every exit of --in is in the same function: that would waive all of them at once. Name file.go:LINE for one exit\n", site)
			return 2
		}
	}

	decls := findDecl(files, o.in)
	if len(decls) == 0 {
		fmt.Fprintf(errOut, "viaonly: --in %q is not declared in %s; refusing to call a function clean against a rule it cannot have broken\n", o.in, dir)
		return 2
	}
	// ⛔ EVERY DECLARED PREDICATE HAS TO EXIST, for the reason the call-site rule
	// refuses an absent --wraps: "every exit asked it" is true and worthless
	// about a name this package does not have. One absent spelling in a set of
	// three is the dangerous case, because the other two make the run look like
	// it worked.
	names := map[string]bool{}
	// ⛔ The ORDER GIVEN, never sorted. "compose the primitives in a WRITTEN
	// order" is what this mode exists to hold a branch to, so the report prints
	// the order the caller wrote and a re-ordering is a visible change.
	var ordered, methodForms []string
	for _, a := range o.asks {
		recv, name := splitMethod(a)
		if !isIdent(name) {
			fmt.Fprintf(errOut, "viaonly: --asks %q is neither a function name nor a method written (*T).name\n", a)
			return 2
		}
		if !declaresAny(files, recv, name) {
			fmt.Fprintf(errOut, "viaonly: --asks %q is not declared in %s; the same refusal\n", a, dir)
			return 2
		}
		if names[name] {
			fmt.Fprintf(errOut, "viaonly: --asks %q is given twice (as %q); a set with a duplicate makes the printed rule disagree with the rule enforced\n", name, a)
			return 2
		}
		names[name] = true
		ordered = append(ordered, name)
		if recv != "" {
			methodForms = append(methodForms, a)
		}
	}
	asksName := strings.Join(ordered, " or ")
	if skipped > 0 {
		fmt.Fprintf(out, "%d test file(s) not scanned; --tests includes them\n", skipped)
	}
	// ⛔ ONE DIRECTORY, said on every run for the reason the call-site rule says
	// it: go/parser does not recurse, and a run at a repository root would read
	// the root package only and call everything below it clean.
	fmt.Fprintf(out, "read %d file(s) of %s itself; subdirectories are NOT scanned\n", len(files), dir)
	for _, m := range methodForms {
		// ⚠ Said on every run, because it is a FALSE PASS risk and that is the
		// dangerous direction: a method call is recognised by the spelling of its
		// selector, so `other.gathered()` on an unrelated type would count as
		// asking. Narrowing it needs go/types, which this command does not run.
		_, name := splitMethod(m)
		fmt.Fprintf(out, "--asks %s is matched by selector spelling: a call to .%s on ANY receiver counts as asking\n", m, name)
	}
	if len(decls) > 1 {
		fmt.Fprintf(out, "%s is declared %d times here; go/parser does not evaluate //go:build, so every build-tag variant was read and each one's exits are reported\n",
			o.in, len(decls))
	}

	var found []exit
	for _, fd := range decls {
		found = append(found, exitsOf(fset, fd, o.in, names)...)
	}

	// ⛔⛔ NO EXIT IS NOT A CLEAN FUNCTION. --in is declared — that was checked —
	// so a body this pass found nothing in means the pass is looking at the wrong
	// node, not that the function obeys. This is the refusal the call-site rule
	// carries for "declared and never called", written again because it is the
	// same defect: a gate that passes because it had nothing to look at.
	if len(found) == 0 && o.expect < 0 {
		fmt.Fprintf(errOut, "viaonly: no exit point found in %s; refusing to report it clean against a rule nothing exercised (--expect 0 if that is meant)\n", o.in)
		return 2
	}
	if o.expect >= 0 && len(found) != o.expect {
		fmt.Fprintf(errOut, "viaonly: examined %d exit(s) of %s, expected exactly %d\n", len(found), o.in, o.expect)
		return 2
	}

	bad, waivedCount := 0, 0
	for _, e := range found {
		if why, ok := waived[e.key()]; ok {
			waivedCount++
			fmt.Fprintf(out, "waived %s (%s %s) without asking %s — %s\n", e.key(), e.in, e.what(), asksName, why)
			continue
		}
		if e.asked {
			continue
		}
		fmt.Fprintf(out, "%s: %s %s without asking %s\n", e.key(), e.in, e.what(), asksName)
		bad++
	}
	stale := unmatchedExits(waived, found)
	if len(stale) > 0 {
		fmt.Fprintf(errOut, "viaonly: %d --allow waiver(s) match no exit; a waiver nobody can check is worse than none:\n", len(stale))
		for _, s := range stale {
			fmt.Fprintf(errOut, "  %s\n", s)
		}
	}
	if bad > 0 {
		fmt.Fprintf(out, "%d of %d exit(s) of %s do not ask %s\n", bad, len(found), o.in, asksName)
		return 1
	}
	if len(stale) > 0 {
		return 2
	}
	if waivedCount > 0 {
		fmt.Fprintf(out, "every one of the %d exit(s) of %s asks %s, except %d waived above\n", len(found), o.in, asksName, waivedCount)
		return 0
	}
	fmt.Fprintf(out, "every one of the %d exit(s) of %s asks %s\n", len(found), o.in, asksName)
	return 0
}

// unmatchedExits returns the waiver keys no exit answers to, sorted.
//
// ⛔ It is a second function rather than a reuse of unmatched(), because that
// one keys on site and a shared helper over an interface would hide which kind
// of thing a waiver failed to match. The sort is the same and the reason is the
// same: a stale exemption is reported, never silently carried.
func unmatchedExits(waived map[string]string, found []exit) []string {
	used := map[string]bool{}
	for _, e := range found {
		used[e.key()] = true
	}
	return keysNotIn(waived, used)
}

// findDecl returns EVERY declaration of the named function, for the reason
// findFunc returns every body: go/parser reads all build-tag variants.
func findDecl(files []*ast.File, name string) []*ast.FuncDecl {
	recv, fn := splitMethod(name)
	var out []*ast.FuncDecl
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != fn || fd.Body == nil {
				continue
			}
			switch {
			case recv == "" && fd.Recv == nil, recv != "" && receiverName(fd) == recv:
				out = append(out, fd)
			}
		}
	}
	return out
}

// declaresAny says whether the package declares that function or method.
func declaresAny(files []*ast.File, recv, fn string) bool {
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != fn {
				continue
			}
			if recv == "" || receiverName(fd) == recv {
				return true
			}
		}
	}
	return false
}

// exitsOf returns every exit point of one declaration, in source order, saying
// for each whether a call to asks dominates it syntactically.
//
// ⚠ DOMINANCE HERE IS SYNTACTIC, and that is the honest claim: a call counts
// for the statements that FOLLOW it in the same list, and for the bodies of a
// statement whose own header contains it. It is not a control-flow analysis, so
// a package that reaches the predicate through a variable it set earlier is not
// seen. The direction of the approximation is stated below for each exclusion,
// because a check that errs toward PASSING is the one that lies.
func exitsOf(fset *token.FileSet, fd *ast.FuncDecl, in string, asks map[string]bool) []exit {
	var out []exit
	add := func(pos token.Pos, asked, implicit bool) {
		p := fset.Position(pos)
		out = append(out, exit{file: filepath.Base(p.Filename), line: p.Line, in: in, asked: asked, implicit: implicit})
	}
	asked := walkStmts(fd.Body.List, false, asks, add)
	// ⛔ THE CLOSING BRACE IS AN EXIT TOO, for a function with no results. Without
	// this, a void function whose last statement is not a `return` had its final
	// exit — the common one — counted nowhere, and a rule can be broken at a hole
	// a pass does not look through. A function WITH results cannot fall off its
	// end; the compiler refuses it, so there is nothing to add.
	if !hasResults(fd) && !endsTerminating(fd.Body.List) {
		add(fd.Body.Rbrace, asked, true)
	}
	return out
}

func hasResults(fd *ast.FuncDecl) bool {
	return fd.Type.Results != nil && len(fd.Type.Results.List) > 0
}

// walkStmts walks a statement list and returns whether the predicate has been
// asked UNCONDITIONALLY by the end of it.
func walkStmts(list []ast.Stmt, asked bool, asks map[string]bool, add func(token.Pos, bool, bool)) bool {
	for _, s := range list {
		asked = walkStmt(s, asked, asks, add)
	}
	return asked
}

// walkStmt records the exits inside s and returns what the statements AFTER s
// may assume.
//
// ⛔⛔ IT USED TO RETURN "s CONTAINS AN ASK" and that made the check err toward
// PASSING, which is the one direction a gate must not take. A `switch` with the
// question inside ONE of its cases marked every later exit of the function as
// having asked it — so the exact shape of collab#274 round 2, the question
// asked in one branch and the flat `return` after the switch, came back clean.
// What follows a statement may assume only what that statement evaluates
// UNCONDITIONALLY: an if's or a switch's header, never a branch of its body.
func walkStmt(s ast.Stmt, asked bool, asks map[string]bool, add func(token.Pos, bool, bool)) bool {
	switch st := s.(type) {
	case *ast.ReturnStmt:
		// The return's own expressions count: `return gathered(p), nil` asks.
		add(st.Return, asked || asksIn(asks, st), false)
		return asked
	case *ast.BlockStmt:
		// A bare block always runs, so what it establishes carries out of it.
		return walkStmts(st.List, asked, asks, add)
	case *ast.IfStmt:
		head := asked || asksIn(asks, st.Init) || asksIn(asks, st.Cond)
		walkStmts(st.Body.List, head, asks, add)
		if st.Else != nil {
			walkStmt(st.Else, head, asks, add)
		}
		return head
	case *ast.ForStmt:
		// Init and Cond are evaluated before the first iteration; Post is NOT
		// evaluated at all when the body never runs, so it counts for nothing.
		head := asked || asksIn(asks, st.Init) || asksIn(asks, st.Cond)
		walkStmts(st.Body.List, head, asks, add)
		return head
	case *ast.RangeStmt:
		head := asked || asksIn(asks, st.X) // the range expression is evaluated once
		walkStmts(st.Body.List, head, asks, add)
		return head
	case *ast.LabeledStmt:
		return walkStmt(st.Stmt, asked, asks, add)
	case *ast.SwitchStmt:
		head := asked || asksIn(asks, st.Init) || asksIn(asks, st.Tag)
		walkCases(st.Body.List, head, asks, add)
		return head
	case *ast.TypeSwitchStmt:
		head := asked || asksIn(asks, st.Init) || asksIn(asks, st.Assign)
		walkCases(st.Body.List, head, asks, add)
		return head
	case *ast.SelectStmt:
		// ⛔⛔ A `select` IS WHERE THE INCIDENT LIVED. collab's waits are select
		// statements, not calls, and that is precisely why the call-site rule saw
		// the sixth site of #274 and not the seventh and eighth. A pass that
		// skipped select bodies would miss the defect it was written for.
		//
		// ⚠ Go evaluates every channel operand on entry, so an ask written there
		// IS unconditional — it is deliberately not credited, because the safe
		// direction for a gate is to report one exit too many.
		walkCases(st.Body.List, asked, asks, add)
		return asked
	case *ast.DeferStmt, *ast.GoStmt:
		// ⛔ Not walked, and not credited: a `return` inside belongs to another
		// function, and the call happens after this function's exits or on
		// another goroutine. Crediting a deferred ask would make the check err
		// toward PASSING.
		return asked
	}
	// Every simple statement — an expression, an assignment, a declaration — is
	// evaluated where it stands. asksIn stops at a function literal, so a
	// predicate mentioned inside one is not credited here either.
	return asked || asksIn(asks, s)
}

func walkCases(list []ast.Stmt, asked bool, asks map[string]bool, add func(token.Pos, bool, bool)) {
	for _, c := range list {
		switch cc := c.(type) {
		case *ast.CaseClause:
			inner := asked
			for _, e := range cc.List {
				if asksIn(asks, e) {
					inner = true
				}
			}
			walkStmts(cc.Body, inner, asks, add)
		case *ast.CommClause:
			walkStmts(cc.Body, asked || asksIn(asks, cc.Comm), asks, add)
		}
	}
}

// endsTerminating reports whether a body cannot fall off its end, following the
// specification's "terminating statements" closely enough to be honest about
// which way it is wrong.
//
// ⚠ THE DIRECTION OF THE ERROR IS THE WHOLE POINT. Believing a body terminates
// when it does not LOSES an exit, and a lost exit is a rule reported as held —
// the failure this command exists to refuse. Believing it does not terminate
// when it does adds a phantom exit at the closing brace, which is a visible
// false positive a reader can answer. So every case below has to be certain,
// and anything else is "does not terminate".
func endsTerminating(list []ast.Stmt) bool {
	if len(list) == 0 {
		return false
	}
	return terminating(list[len(list)-1])
}

func terminating(s ast.Stmt) bool {
	switch st := s.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return st.Tok == token.GOTO
	case *ast.ExprStmt:
		call, ok := st.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		id, ok := call.Fun.(*ast.Ident)
		return ok && id.Name == "panic"
	case *ast.BlockStmt:
		return endsTerminating(st.List)
	case *ast.IfStmt:
		// Without an else, the body may be skipped.
		return st.Else != nil && endsTerminating(st.Body.List) && terminating(st.Else)
	case *ast.ForStmt:
		// `for {}` with no break: the specification's own case.
		return st.Cond == nil && !hasBreak(st.Body)
	case *ast.LabeledStmt:
		return terminating(st.Stmt)
	case *ast.SwitchStmt:
		return casesTerminate(st.Body, true)
	case *ast.TypeSwitchStmt:
		return casesTerminate(st.Body, true)
	case *ast.SelectStmt:
		return casesTerminate(st.Body, false)
	}
	return false
}

// casesTerminate asks whether every clause of a switch or select ends in a
// terminating statement, with no break out of it. A switch also needs a
// default, or it may match nothing; a select always blocks until one clause
// runs, so it does not.
func casesTerminate(body *ast.BlockStmt, needDefault bool) bool {
	if hasBreak(body) {
		return false
	}
	seenDefault := false
	for _, c := range body.List {
		switch cc := c.(type) {
		case *ast.CaseClause:
			if cc.List == nil {
				seenDefault = true
			}
			if !endsTerminating(cc.Body) {
				return false
			}
		case *ast.CommClause:
			if cc.Comm == nil {
				seenDefault = true
			}
			if !endsTerminating(cc.Body) {
				return false
			}
		default:
			return false
		}
	}
	return seenDefault || !needDefault
}

// hasBreak reports whether a break appears anywhere inside, other than within a
// nested function literal — where it would belong to that literal's own loop.
//
// ⚠ It does not read labels, so a break that leaves an INNER loop is counted
// against the outer one. That is the safe direction: the statement is then
// called non-terminating and a phantom exit is reported rather than a real one
// lost.
func hasBreak(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if found || x == nil {
			return false
		}
		switch v := x.(type) {
		case *ast.FuncLit:
			return false
		case *ast.BranchStmt:
			if v.Tok == token.BREAK {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// asksIn reports whether n contains a call to ANY of the declared predicates
// that this function's own flow reaches: a plain call or a selector call of that
// spelling, and never one inside a function literal, a defer or a go statement.
//
// ⛔ ANY of them, because a branch that needs a different error sentence
// composes the primitives rather than recopying the contract — which is what
// collab's expiry branch does, and recopying it is how the third round's defect
// was born. The set is DECLARED, so "any" is still the caller's rule and not an
// inference.
// ⛔ It is called with a nil node constantly — st.Init of an `if` without one,
// st.Tag of a bare `switch` — and it used to carry its own `if n == nil` on top
// of the check inside the walk. A sweep measured that guard REDUNDANT: the
// inner one answers false for the nil node, ast.Inspect's Visit then returns nil
// and Walk returns before its type switch, so nothing panics and nothing
// changed when the outer guard was deleted. Two guards where neither is
// load-bearing is worse than one that is, so the outer one is gone.
func asksIn(asks map[string]bool, n ast.Node) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		// x == nil is the nil node itself AND the end of every node's visit;
		// `found` only short-circuits the rest of the walk, and deleting it
		// cannot change the answer.
		if found || x == nil {
			return false
		}
		switch v := x.(type) {
		case *ast.FuncLit, *ast.DeferStmt, *ast.GoStmt:
			return false // runs later, or never: see walkStmt
		case *ast.CallExpr:
			switch fun := v.Fun.(type) {
			case *ast.Ident:
				if asks[fun.Name] {
					found = true
					return false
				}
			case *ast.SelectorExpr:
				if asks[fun.Sel.Name] {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}
