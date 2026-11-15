// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

// viaonly says where a rule is honoured at some siblings and not at others.
//
//	viaonly --wraps await --via '(*Peer).awaited' .          # call sites
//	viaonly --asks '(*Peer).gathered' --in '(*Peer).Offer' .  # exit points
//
// The second rule, and why the first could not answer it, is in exits.go.
//
// ⛔ WHY IT EXISTS. go-crdt/collab shipped the same defect three times in four
// days: a rule honoured at some call sites and not at its siblings.
//
//	#247  RetryPolicy.Permanent asked about each attempt — one of two loops did
//	#268  the context's expiry returned through stalled() — two of three did
//	#271  the wait went through awaited() — ONE OF FIVE did
//
// The third was born inside the fix for the second, and a CONSUMER measured it,
// not the author. A lesson that cannot stop its own recurrence in the commit
// that cites it is not a lesson, so this is a command instead of a note.
//
// ⛔ IT INFERS NOTHING. The pairing is DECLARED: --wraps names the callee,
// --via names the only function allowed to call it, and neither has a default.
// A tool that guessed which idiom a package "meant" would cry wolf, and a guard
// that cries wolf is a guard somebody turns off — this fleet has that measured
// twice over.
//
// Exit status, following assetpath's and priorart's, and the same for both
// rules:
//
//	0  every call to --wraps is inside --via; or every exit of --in asked one
//	   of --asks
//	1  at least one call bypasses it, or one exit never asked; each is printed
//	   with its position
//	2  REFUSED TO DECIDE: the directory would not parse; a declared name is
//	   not in it; nothing was examined; every site is waived; or, under
//	   --asks/--in, the examined function contains a goto
//
// ⛔ The 2 is the whole point, as it is for assetpath. If --wraps resolves to no
// function, "0 direct calls" is true and worthless: the tool measured a name it
// could not find, not a package that obeys. A gate that passes because it had
// nothing to look at is worse than no gate, and this fleet has three memories of
// exactly that shape.
//
// # What it does not do yet
//
// --wraps takes an UNQUALIFIED function name, because that is the shape the
// three defects above had: a package-level helper called from its own package.
// A dotted name is refused (exit 2) rather than half-matched on the selector,
// which would report calls to any method of that name on any receiver.
//
// Test files are not scanned unless --tests is given, because a test calling the
// raw function directly is ordinary. The count of files skipped is printed, so
// the skipping is never silent.
//
// ⛔ --asks, the exit-point rule, DOES accept a method written (*T).name, and
// matches it by the spelling of the selector — the half-match --wraps refuses.
// The reason is the direction of the error: for "which call bypasses the
// wrapper" a half-match invents a finding, while for "did this exit ask the
// question" it can only invent a PASS, so the run says on every line that a
// call to that name on any receiver counts. Narrowing it needs go/types.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Says where a rule is honoured at some siblings and not at others.

There are two rules, one per run, and each pairing is DECLARED, never inferred:

    viaonly --wraps await --via '(*Peer).awaited' ./path
    viaonly --asks '(*Peer).gathered' --in '(*Peer).gatherICE' ./path

--wraps is the callee that must not be called directly. --via is the only
function allowed to call it. Neither has a default: without both, there is no
rule to enforce and the command refuses rather than passing.

--asks is a predicate every EXIT POINT of --in must have asked, and it is
repeatable: an exit satisfies the rule by asking any one of them, which is how
a branch that needs its own error sentence composes the primitives instead of
recopying the contract. An exit is a return, or the closing brace of a function
with no results whose body does not terminate.

⛔ The second rule exists because an early return does not read as an exit, and
a return is not a CALL: the first rule cannot see it.

Dominance is syntactic, and what counts is what is EVALUATED, never what is
merely written nearby. A call counts for the statements after it in the same
list, and for a body under a header that evaluates it unconditionally: an if's
or a switch's init and tag, a for's init and condition, a range expression, a
clause's FIRST case expression. It does NOT count on the right of && or ||, in
a case expression after the first, in a clause a fallthrough can enter, or
inside a defer, a go statement or a function literal — each of those is a way
to reach the exit without asking, and crediting it would make the check err
toward passing. A goto in the examined function is refused outright (2): source
order is all this pass has, and a jump makes it a lie.

⛔ --allow names file.go:LINE under this rule, NOT a function: every exit of
--in is in the same function, so a function-named waiver would waive them all
at once. Waiving every exit is refused as well, because then nothing was
judged.

--via accepts a plain function name (helper) or a method, written as Go writes
it in a doc comment: (*Peer).awaited or (Peer).awaited.

A waiver names a site and says why, and the reason is PRINTED with the result.
Under --wraps/--via, name the FUNCTION rather than the line: a line number is
the most brittle handle there is, and a CI file that carries one stops naming
the site the day somebody adds an import. Under --asks/--in it is the other way
round, and the function form is REFUSED — see that rule above.

    --allow '(*Peer).AcceptAnswer=no call after it can be blocked'
    --allow 'peer_js.go:180=…'                 # also accepted, and worse

Exit status: 0 clean, 1 at least one bypass or one exit that never asked, 2
REFUSED TO DECIDE. A name that resolves to nothing is never reported as clean,
and 2 covers every such case: the directory would not parse; more than one
directory was named; a declared name is not in it; nothing was examined; a
waiver matches no site; every site is waived (so nothing was judged); or, under
--asks/--in, the examined function contains a goto, which makes source order
unusable.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var (
		wraps  string
		via    string
		asks   []string
		in     string
		allow  []string
		tests  bool
		expect int
	)
	c := cli.New("viaonly --wraps <func> --via <func> | --asks <pred> --in <func> [flags] [directory]",
		"Which calls bypass the wrapper, or which exits never asked the question", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(check(cli.Out(cmd), cli.Err(cmd), opts{
			wraps: wraps, via: via, asks: asks, in: in, allow: allow, tests: tests, expect: expect,
		}, args))
	}
	c.Flags().StringVar(&wraps, "wraps", "",
		"the callee that must not be called directly (required, no default)")
	c.Flags().StringVar(&via, "via", "",
		"the only function allowed to call it: name, (*T).name or (T).name (required, no default)")
	c.Flags().StringArrayVar(&asks, "asks", nil,
		"a predicate every exit of --in must have asked: name or (*T).name. Repeatable, and an exit satisfies the rule by asking ANY of them — that is how a branch composes the primitives instead of recopying the contract (the other rule; no default)")
	c.Flags().StringVar(&in, "in", "",
		"the function whose exit points are examined, with --asks: name, (*T).name or (T).name")
	c.Flags().StringArrayVar(&allow, "allow", nil,
		"waive one site, with a reason that is PRINTED (repeatable). Under --wraps/--via: '(*T).Func=why' or 'file.go:LINE=why', and prefer the function — a line moves. Under --asks/--in: 'file.go:LINE=why' ONLY, because every exit is in the same function and a function-named waiver would waive them all")
	c.Flags().BoolVar(&tests, "tests", false,
		"scan _test.go files too; by default they are skipped and the count is printed")
	c.Flags().IntVar(&expect, "expect", -1,
		"fail unless EXACTLY this many sites were examined — calls under --wraps/--via, exits under --asks/--in: a guard against a rule that matched nothing")
	return c
}

// run keeps the shape this package's tests drive, through the real command.
func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

type opts struct {
	wraps  string
	via    string
	asks   []string
	in     string
	allow  []string
	tests  bool
	expect int
}

// site is one call to --wraps, with where it was written.
type site struct {
	file string // as given to the reader, relative to the directory
	line int
	in   string // the function that contains it, for the report
}

// where is the enclosing name as the report PRINTS it.
//
// ⛔ It exists because the printed name and the waiver key had drifted apart: a
// package-level bypass printed a blank, and once it printed "package level"
// that string still did not waive it — the lookup compared the empty `in`. A
// name a report prints and a waiver cannot match is decoration, so both now go
// through here.
func (s site) where() string {
	if s.in == "" {
		return "package level"
	}
	return s.in
}

// key is the file:line handle a waiver may name instead of the function.
func (s site) key() string { return fmt.Sprintf("%s:%d", s.file, s.line) }

func check(out, errOut io.Writer, o opts, args []string) int {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	// ⛔⛔ EXIT 0 ON A DIRECTORY IT NEVER READ. The extra arguments were silently
	// discarded, so `viaonly --wraps await --via '(*Peer).awaited' a b` printed
	// "every one of the 1 call(s) … goes through" and exited 0 while b was never
	// parsed. That is the same family as reading ONE directory and calling
	// everything below it clean — a clean verdict about something unexamined,
	// which is the defect this command exists to catch.
	if len(args) > 1 {
		fmt.Fprintf(errOut, "viaonly: one directory at a time, got %d (%s); go/parser reads a single directory, and the others would not have been read while the run still exited 0\n",
			len(args), strings.Join(args, " "))
		return 2
	}
	// ⛔ TWO RULES, ONE AT A TIME, and the refusal names both rather than the one
	// the caller happened to half-spell. A run that silently picked a rule
	// because the other's flags were empty would be deciding what the caller
	// meant, which is the inference this command refuses everywhere else.
	exitsRule := len(o.asks) > 0 || o.in != ""
	callsRule := o.wraps != "" || o.via != ""
	switch {
	case exitsRule && callsRule:
		fmt.Fprintln(errOut, "viaonly: one rule at a time: --wraps/--via asks which CALLS bypass a wrapper, --asks/--in asks which EXITS never asked a question. They report different sites and a single exit status cannot carry both")
		return 2
	case !exitsRule && !callsRule:
		fmt.Fprintln(errOut, "viaonly: give a rule: --wraps <func> --via <func> for call sites, or --asks <pred> --in <func> for exit points. Neither has a default, and without one there is nothing to enforce")
		return 2
	case exitsRule && (len(o.asks) == 0 || o.in == ""):
		fmt.Fprintln(errOut, "viaonly: --asks and --in are both required: without both there is no rule to enforce")
		return 2
	case callsRule && (o.wraps == "" || o.via == ""):
		fmt.Fprintln(errOut, "viaonly: --wraps and --via are both required: without both there is no rule to enforce")
		return 2
	}
	// ⛔ The contract is "a Go identifier", not "contains no dot or paren". The
	// charset form was written first and a mutation walked through it: dropping
	// "(" changed nothing, because every method-shaped example also carries a
	// dot. A charset says what is forbidden and keeps missing a case; this says
	// what is required.
	if callsRule && !isIdent(o.wraps) {
		fmt.Fprintf(errOut, "viaonly: --wraps %q is not an unqualified function name; a dotted or method-shaped name would half-match on the selector and report calls to any receiver\n", o.wraps)
		return 2
	}
	waived, err := waivers(o.allow)
	if err != nil {
		fmt.Fprintln(errOut, "viaonly:", err)
		return 2
	}

	fset := token.NewFileSet()
	skipped := 0
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		if !o.tests && strings.HasSuffix(fi.Name(), "_test.go") {
			skipped++
			return false
		}
		return true
	}, 0)
	if err != nil {
		fmt.Fprintln(errOut, "viaonly: reading the directory:", err)
		return 2
	}
	// ⛔⛔ THE SORT WAS INSIDE THE PACKAGE LOOP, so each package's files were
	// ordered and the PACKAGES were not: ParseDir returns a map, and Go
	// randomises map iteration. A directory holding `package p` and
	// `package p_test` therefore produced a different file order between runs on
	// the SAME BYTES — and with it a different report order and, measured by
	// review, a different EXIT STATUS in 1 run of 16. A verdict that flips
	// without an input changing is worse than a wrong one: it cannot be
	// reproduced, so it cannot be fixed, and a green run proves nothing. One
	// sort over every file name, across every package.
	names := make([]string, 0, len(pkgs))
	byName := map[string]*ast.File{}
	for _, p := range pkgs {
		for name, f := range p.Files {
			names = append(names, name)
			byName[name] = f
		}
	}
	sort.Strings(names)
	files := make([]*ast.File, 0, len(names))
	for _, name := range names {
		files = append(files, byName[name])
	}
	if len(files) == 0 {
		fmt.Fprintf(errOut, "viaonly: no Go files in %s that this run would read\n", dir)
		return 2
	}
	// The two rules share the reader, so they can never disagree about which
	// files a directory holds — and the file order they both report is the one
	// sort above.
	if exitsRule {
		return checkExits(out, errOut, fset, files, o, dir, waived, skipped)
	}

	// ⛔ Both declared names have to EXIST. A rule naming something absent is a
	// rule that passes for the wrong reason, which is the defect this command
	// was written against rather than a corner of it.
	viaBodies, ok := findFunc(files, o.via)
	if !ok {
		fmt.Fprintf(errOut, "viaonly: --via %q is not declared in %s; refusing to call a package clean against a rule it cannot have broken\n", o.via, dir)
		return 2
	}
	if !declaresFunc(files, o.wraps) {
		fmt.Fprintf(errOut, "viaonly: --wraps %q is not declared in %s; the same refusal\n", o.wraps, dir)
		return 2
	}

	found, values, examined := bypasses(fset, files, o.wraps, viaBodies, dir)

	if skipped > 0 {
		fmt.Fprintf(out, "%d test file(s) not scanned; --tests includes them\n", skipped)
	}
	// ⛔ Said on every run where it happens, because it is the signal that build
	// tags split this package and that go/parser read every variant: the count
	// is how a reader knows why two files declare the same wrapper.
	if len(viaBodies) > 1 {
		fmt.Fprintf(out, "%s is declared %d times here; go/parser does not evaluate //go:build, so every build-tag variant was read and a call inside ANY of them goes through\n",
			o.via, len(viaBodies))
	}
	// ⛔ ONE DIRECTORY. go/parser does not recurse, and a run at a repository
	// root — the natural spelling in CI — would read the root package only and
	// call everything below it clean. Said on every run rather than documented.
	fmt.Fprintf(out, "read %d file(s) of %s itself; subdirectories are NOT scanned\n", len(files), dir)

	if o.expect >= 0 && examined != o.expect {
		fmt.Fprintf(errOut, "viaonly: examined %d call(s) to %s, expected exactly %d\n", examined, o.wraps, o.expect)
		return 2
	}
	// ⛔⛔ NOTHING EXAMINED IS NOT A CLEAN PACKAGE. `--wraps` is declared here —
	// that was checked above — and no call to it was found, so either the rule
	// is aimed at the wrong place or every use is a value. Printing "every one
	// of the 0 call(s) goes through" was the exact defect this command exists
	// to catch, written into the command itself. Exit 2 is "refused to decide",
	// and --expect 0 is how a caller says they meant it.
	if examined == 0 && len(values) == 0 && o.expect < 0 {
		fmt.Fprintf(errOut, "viaonly: %s is declared in %s and never called there; refusing to report a package clean against a rule nothing exercised (--expect 0 if that is meant)\n", o.wraps, dir)
		return 2
	}

	bad, waivedCount := 0, 0
	for _, s := range found {
		key := s.key()
		// ⛔ A waiver may name the SITE or the FUNCTION that contains it, and a
		// CI file should carry the function. A line number is the most brittle
		// handle there is: one inserted line above it and the waiver names
		// something else, or nothing — which is how a gate ends up carrying a
		// waiver nobody can check. The function survives every edit that does
		// not move the call out of it, and it is what a reader recognises.
		why, isWaived := waived[key]
		if !isWaived {
			why, isWaived = waived[s.where()]
		}
		if isWaived {
			waivedCount++
			fmt.Fprintf(out, "waived %s (%s) calls %s directly — %s\n", key, s.where(), o.wraps, why)
			continue
		}
		fmt.Fprintf(out, "%s: %s calls %s directly; it must go through %s\n", key, s.where(), o.wraps, o.via)
		bad++
	}
	// ⛔ A WAIVER THAT MATCHED NOTHING USED TO PASS IN SILENCE, exit 0 and
	// unmentioned. That is the stale-waiver failure the function-versus-line
	// comment above warns about, arriving by the other door: the line moved, or
	// the function was renamed, so the gate carries a public exemption for
	// something that is no longer there — and the next reader believes a call is
	// being excused that nothing excuses. A name with no match is reported,
	// which is the same rule a coverage ratchet needs.
	stale := unmatched(waived, found)
	if len(stale) > 0 {
		fmt.Fprintf(errOut, "viaonly: %d --allow waiver(s) match no reported site; a waiver nobody can check is worse than none:\n", len(stale))
		for _, s := range stale {
			fmt.Fprintf(errOut, "  %s\n", s)
		}
	}
	// ⛔ A value cannot go through the wrapper, and no waiver hides one: it is
	// reported as what it is, a use the rule cannot cover.
	for _, s := range values {
		fmt.Fprintf(out, "%s: %s takes %s as a VALUE; the call that follows it cannot be seen here\n", s.key(), s.where(), o.wraps)
	}
	if bad > 0 || len(values) > 0 {
		fmt.Fprintf(out, "%d of %d call(s) to %s bypass %s, and %d use(s) take it as a value\n",
			bad, examined, o.wraps, o.via, len(values))
		// ⛔ 1, not 2, even with a stale waiver printed above: the command DID
		// decide, and a finding must not be downgraded to "refused to decide"
		// by a second, lesser problem. 2 is for the case where nothing else
		// went wrong, below — which is the silent one.
		return 1
	}
	// ⛔⛔ THIS is the dangerous case the check exists for: everything else is
	// clean, so without it the run exits 0 carrying a public exemption for
	// something that is not there.
	if len(stale) > 0 {
		return 2
	}
	// ⛔ The count says what was WAIVED too. It used to say "every one of the N
	// goes through" while N included waived sites that plainly did not — a tail
	// line contradicting the lines above it.
	if waivedCount > 0 {
		fmt.Fprintf(out, "every one of the %d call(s) to %s goes through %s, except %d waived above\n",
			examined, o.wraps, o.via, waivedCount)
		return 0
	}
	fmt.Fprintf(out, "every one of the %d call(s) to %s goes through %s\n", examined, o.wraps, o.via)
	return 0
}

// isIdent reports whether s is a single Go identifier: what --wraps has to be
// for an unqualified call to be recognised by spelling.
func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || unicode.IsLetter(r):
		case unicode.IsDigit(r) && i > 0:
		default:
			return false
		}
	}
	return true
}

// waivers reads --allow into a map, refusing one without a reason: a waiver is
// made in public or not at all.
func waivers(allow []string) (map[string]string, error) {
	out := map[string]string{}
	for _, a := range allow {
		site, why, ok := strings.Cut(a, "=")
		if !ok || strings.TrimSpace(why) == "" {
			return nil, fmt.Errorf("--allow %q needs a reason: 'file.go:LINE=why', and the reason is printed", a)
		}
		// ⛔ An empty SITE matched every call whose enclosing function was "" —
		// package level — and printed `waived p.go:5 ()`. A waiver that names
		// nothing waives by accident.
		if strings.TrimSpace(site) == "" {
			return nil, fmt.Errorf("--allow %q names no site: it must be 'file.go:LINE=why' or 'Func=why'", a)
		}
		out[strings.TrimSpace(site)] = strings.TrimSpace(why)
	}
	return out, nil
}

// unmatched returns the waiver keys that no reported site answers to, sorted so
// the refusal reads the same twice.
func unmatched(waived map[string]string, found []site) []string {
	used := map[string]bool{}
	for _, s := range found {
		used[s.key()] = true
		used[s.where()] = true
	}
	return keysNotIn(waived, used)
}

// keysNotIn returns the waiver keys nothing answered to, sorted so a refusal
// reads the same twice. Both rules report stale waivers, and one sort means
// they cannot differ about the order — the lesson of the file sort above.
func keysNotIn(waived map[string]string, used map[string]bool) []string {
	var stale []string
	for k := range waived {
		if !used[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	return stale
}

// findFunc returns EVERY body declared under the named function, which may be a
// method written as Go writes it in a doc comment: (*T).name or (T).name.
//
// ⛔⛔ IT USED TO RETURN THE FIRST ONE, AND THAT MADE THE WRAPPER ITS OWN
// BYPASS. go/parser does not evaluate `//go:build`, so a package split by build
// tags has every variant parsed at once: go-crdt/collab declares
// `(*Peer).awaited` in both peer_js.go and peer_other.go. The first body found
// became "the wrapper", and the OTHER variant's identical wrapper — whose whole
// job is to call the wrapped function — was then reported as a bypass:
//
//	peer_other.go:11: (*Peer).awaited calls await directly; it must go through (*Peer).awaited
//
// A rule that accuses its own subject is not a near miss, it is a false
// positive on the only repository this command was written for. Every variant
// IS the wrapper, so a call inside any of them goes through.
func findFunc(files []*ast.File, name string) ([]*ast.BlockStmt, bool) {
	recv, fn := splitMethod(name)
	var bodies []*ast.BlockStmt
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != fn || fd.Body == nil {
				continue
			}
			switch {
			case recv == "" && fd.Recv == nil, recv != "" && receiverName(fd) == recv:
				bodies = append(bodies, fd.Body)
			}
		}
	}
	return bodies, len(bodies) > 0
}

// within reports whether pos falls inside any of the wrapper's bodies.
func within(bodies []*ast.BlockStmt, pos token.Pos) bool {
	for _, b := range bodies {
		if b.Pos() <= pos && pos <= b.End() {
			return true
		}
	}
	return false
}

// declaresFunc says whether the package declares a plain function of that name.
func declaresFunc(files []*ast.File, name string) bool {
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name {
				return true
			}
		}
	}
	return false
}

// splitMethod reads "(*T).name" or "(T).name" into its receiver and its name.
func splitMethod(s string) (recv, fn string) {
	if !strings.HasPrefix(s, "(") {
		return "", s
	}
	end := strings.Index(s, ")")
	if end < 0 || !strings.HasPrefix(s[end:], ").") {
		return "", s
	}
	return s[:end+1], s[end+2:]
}

// enclosing is the stack of function names the walk is currently inside.
type enclosing []string

// name is the innermost function, or "" at package level.
//
// ⛔ IT IS A METHOD SO THAT A TEST CAN CALL IT. As a closure over a local
// slice, its empty check could only be exercised through the whole walk, and a
// mutsweep said exactly what that was worth: deleting the check made the suite
// go red by PANIC — an index of -1 — and never by an assertion. A panic red is
// luck. It says the code crashed, not what the guard is for, and the next
// change that adds a check ahead of it turns that red into a silent survivor.
func (e enclosing) name() string {
	if len(e) == 0 {
		return "" // package level: no enclosing function
	}
	return e[len(e)-1]
}

// receiverName renders a declaration's receiver the way --via spells it.
func receiverName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	switch t := fd.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return "(*" + id.Name + ")"
		}
	case *ast.Ident:
		return "(" + t.Name + ")"
	}
	return ""
}

// bypasses walks every file for unqualified calls to wraps, and reports the ones
// outside the body of via. examined counts ALL of them, so a caller can tell a
// clean package from a rule that matched nothing.
func bypasses(fset *token.FileSet, files []*ast.File, wraps string, via []*ast.BlockStmt, dir string) (found, values []site, examined int) {
	// The identifiers that ARE the declaration of wraps: `func await(...)` names
	// it without using it.
	declNames := map[*ast.Ident]bool{}
	// The identifiers that are the Fun of a call to wraps: counted as calls, not
	// as values.
	called := map[*ast.Ident]bool{}
	// ⛔⛔ THE IDENTIFIERS THAT ARE THE Sel OF A SELECTOR, which is the
	// half-match the package comment says is refused — and which this pass was
	// making anyway. `o.await(3)` on some OTHER type, and a struct field used as
	// `h.await(x)`, both reach an *ast.Ident spelled `await` that this pass
	// reported as "takes await as a VALUE": exit 1 on a non-finding. Worse, a
	// value is unwaivable by design, so there was no way to say it was wrong.
	// --wraps is an unqualified name (isIdent enforces it), so a selected name
	// is simply not the subject.
	selected := map[*ast.Ident]bool{}
	// ⛔⛔ THE IDENTIFIERS THAT DEFINE A NAME RATHER THAN USE ONE, which is the
	// same lesson as the charset that became isIdent, one level up: a pass that
	// matches a SPELLING keeps finding one more position where that spelling is
	// not the subject. Found by this change's own test — `type Holder struct{
	// await func(int) int }` was reported as taking await as a VALUE at package
	// level, and a value use cannot be waived.
	//
	// ⚠ The positions are ENUMERATED, not inferred, and that is the limit of a
	// spelling-based pass: a struct field or interface method name, a parameter
	// or result name, a var or const name, a type name, the left-hand side of a
	// `:=` — BOTH an assignment and a range clause, which are different nodes —
	// the key of a keyed composite literal, and a label on either side of its
	// branch.
	//
	// ⛔⛔ THIS LIST HAS BEEN ONE POSITION SHORT THREE TIMES. Each time the
	// missing one produced an exit 1 on something that is not the subject, and
	// a value use cannot be waived, so there was no way for a caller to say the
	// tool was wrong. If you are reading this because you found a fourth,
	// add it here and raise the count in this sentence.
	//
	// ⚠⚠ AND HERE IS WHAT IT STILL CANNOT DO, said plainly rather than left for
	// the next reader: a local name that SHADOWS the function — `await := 3`
	// followed by `return await`, or a parameter named `await` that is then
	// used — is a USE of something else spelled the same, and this pass reports
	// it. Only scope resolution answers that, which means go/types, which this
	// command deliberately does not run. The honest claim is therefore narrow:
	// it reports an unqualified identifier spelled like --wraps that is not a
	// call, not the declaration, not selected from a value, and not one of the
	// defining positions above. A package that shadows the very function it
	// wraps will get a false positive, and `--allow` cannot hide a value use,
	// so that case is a defect to file rather than a waiver to write.
	defined := map[*ast.Ident]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.Field: // struct fields, interface methods, parameters, results
				for _, id := range d.Names {
					defined[id] = true
				}
			case *ast.ValueSpec:
				for _, id := range d.Names {
					defined[id] = true
				}
			case *ast.TypeSpec:
				defined[d.Name] = true
			case *ast.AssignStmt:
				if d.Tok == token.DEFINE {
					for _, lhs := range d.Lhs {
						if id, ok := lhs.(*ast.Ident); ok {
							defined[id] = true
						}
					}
				}
			case *ast.LabeledStmt:
				defined[d.Label] = true
			case *ast.BranchStmt: // break/continue/goto NAME: a label, never a function
				if d.Label != nil {
					defined[d.Label] = true
				}
			case *ast.KeyValueExpr:
				// ⛔⛔ THE KEY OF A KEYED STRUCT LITERAL. `Holder{await: nil}`
				// was reported as taking await as a VALUE — and keyed literals
				// are this fleet's house style, so the false positive is the
				// common case rather than a corner. The field DECLARATION was
				// already excluded and the field KEY was not, which is the
				// third time this enumeration has been one position short.
				//
				// Sound because declaresFunc requires --wraps to name a plain
				// function: a func value can be neither a map key (func is not
				// comparable) nor an array index (not a constant), so a key
				// spelled like --wraps can only be a struct field.
				if id, ok := d.Key.(*ast.Ident); ok {
					defined[id] = true
				}
			case *ast.RangeStmt:
				// ⛔ `for await := range xs` defines it, exactly as `await := 3`
				// does — and the comment below used to say "a := left-hand
				// side", which covered only *ast.AssignStmt. A range clause is
				// the other one.
				if d.Tok == token.DEFINE {
					for _, e := range []ast.Expr{d.Key, d.Value} {
						if id, ok := e.(*ast.Ident); ok {
							defined[id] = true
						}
					}
				}
			}
			return true
		})
	}
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == wraps {
				declNames[fd.Name] = true
			}
		}
	}
	for _, f := range files {
		// ⛔ A STACK, not a variable. The enclosing name used to be assigned on
		// the way in and never cleared, so everything after the last function in
		// a file — a package-level `var seeded = await(7)` — was reported as
		// being inside that function, and a waiver written for the function
		// waived a call the function does not contain.
		var stack enclosing
		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil { // leaving a node
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				return true
			}
			if fd, ok := n.(*ast.FuncDecl); ok {
				name := fd.Name.Name
				if r := receiverName(fd); r != "" {
					name = r + "." + fd.Name.Name
				}
				stack = append(stack, name)
				return true
			}
			// Push a placeholder for every other node, so the pop on the way out
			// stays paired with the push on the way in.
			stack = append(stack, stack.name())

			// A selector's Sel is reached before the walk descends to it, so
			// marking it here is enough for the value pass below.
			if se, ok := n.(*ast.SelectorExpr); ok {
				selected[se.Sel] = true
				return true
			}
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == wraps {
					// ast.Inspect walks into the call's own Fun, so without this
					// every call site was ALSO reported as taking the name as a
					// value — the real collab case printed both lines for one
					// call.
					called[id] = true
					examined++
					if within(via, call.Pos()) {
						return true
					}
					pos := fset.Position(call.Pos())
					found = append(found, site{file: filepath.Base(pos.Filename), line: pos.Line, in: stack.name()})
				}
				return true
			}
			// ⛔⛔ THE USE THIS PASS WAS BLIND TO. `var h = await`, `register(await)`,
			// `go await(ctx)`'s function value — the name taken as a VALUE is
			// exactly how a caller reaches the wrapped function without a call
			// site to find. It was not reported and not even counted, so three
			// such uses read as "every one of the 0 calls goes through". A value
			// escapes the rule by construction, so it is reported, never waived
			// silently by being invisible.
			id, ok := n.(*ast.Ident)
			if !ok || id.Name != wraps || declNames[id] || called[id] || selected[id] || defined[id] {
				return true
			}
			if within(via, id.Pos()) {
				return true // inside the wrapper, where naming it is the point
			}
			pos := fset.Position(id.Pos())
			values = append(values, site{file: filepath.Base(pos.Filename), line: pos.Line, in: stack.name()})
			return true
		})
	}
	_ = dir
	return found, values, examined
}
