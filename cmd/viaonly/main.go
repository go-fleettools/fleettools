// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

// viaonly says which calls bypass the wrapper they were supposed to go through.
//
//	viaonly --wraps await --via '(*Peer).awaited' .
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
// Exit status, following assetpath's and priorart's:
//
//	0  every call to --wraps is inside --via
//	1  at least one call bypasses it; each is printed with its position
//	2  REFUSED TO DECIDE: the directory would not parse, or --wraps or --via
//	   names nothing in it
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

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Says which calls bypass the wrapper they were supposed to go through.

The pairing is DECLARED, never inferred:

    viaonly --wraps await --via '(*Peer).awaited' ./path

--wraps is the callee that must not be called directly. --via is the only
function allowed to call it. Neither has a default: without both, there is no
rule to enforce and the command refuses rather than passing.

--via accepts a plain function name (helper) or a method, written as Go writes
it in a doc comment: (*Peer).awaited or (Peer).awaited.

A waiver names a site and says why, and the reason is PRINTED with the result.
Name the FUNCTION, not the line: a line number is the most brittle handle there
is, and a CI file that carries one stops naming the site the day somebody adds
an import.

    --allow '(*Peer).AcceptAnswer=no call after it can be blocked'
    --allow 'peer_js.go:180=…'                 # also accepted, and worse

Exit status: 0 clean, 1 at least one bypass, 2 refused to decide — the
directory would not parse, or a declared name is not in it. A name that
resolves to nothing is never reported as a clean package.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var (
		wraps  string
		via    string
		allow  []string
		tests  bool
		expect int
	)
	c := cli.New("viaonly --wraps <func> --via <func> [flags] [directory]",
		"Which calls bypass the wrapper they were supposed to go through", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(check(cli.Out(cmd), cli.Err(cmd), opts{
			wraps: wraps, via: via, allow: allow, tests: tests, expect: expect,
		}, args))
	}
	c.Flags().StringVar(&wraps, "wraps", "",
		"the callee that must not be called directly (required, no default)")
	c.Flags().StringVar(&via, "via", "",
		"the only function allowed to call it: name, (*T).name or (T).name (required, no default)")
	c.Flags().StringArrayVar(&allow, "allow", nil,
		"waive one site: '(*T).Func=why' or 'file.go:LINE=why'. Name the function — a line moves. The reason is printed (repeatable)")
	c.Flags().BoolVar(&tests, "tests", false,
		"scan _test.go files too; by default they are skipped and the count is printed")
	c.Flags().IntVar(&expect, "expect", -1,
		"fail unless EXACTLY this many calls were examined: a guard against a rule that matched nothing")
	return c
}

// run keeps the shape this package's tests drive, through the real command.
func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

type opts struct {
	wraps  string
	via    string
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

func check(out, errOut io.Writer, o opts, args []string) int {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	if o.wraps == "" || o.via == "" {
		fmt.Fprintln(errOut, "viaonly: --wraps and --via are both required: without both there is no rule to enforce")
		return 2
	}
	if strings.ContainsAny(o.wraps, ".(") {
		fmt.Fprintf(errOut, "viaonly: --wraps %q is not an unqualified function name; a dotted name would half-match on the selector and report calls to any receiver\n", o.wraps)
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
	files := []*ast.File{}
	for _, p := range pkgs {
		names := make([]string, 0, len(p.Files))
		for name := range p.Files {
			names = append(names, name)
		}
		// Sorted, so a report reads the same twice.
		sort.Strings(names)
		for _, name := range names {
			files = append(files, p.Files[name])
		}
	}
	if len(files) == 0 {
		fmt.Fprintf(errOut, "viaonly: no Go files in %s that this run would read\n", dir)
		return 2
	}

	// ⛔ Both declared names have to EXIST. A rule naming something absent is a
	// rule that passes for the wrong reason, which is the defect this command
	// was written against rather than a corner of it.
	viaBody, ok := findFunc(files, o.via)
	if !ok {
		fmt.Fprintf(errOut, "viaonly: --via %q is not declared in %s; refusing to call a package clean against a rule it cannot have broken\n", o.via, dir)
		return 2
	}
	if !declaresFunc(files, o.wraps) {
		fmt.Fprintf(errOut, "viaonly: --wraps %q is not declared in %s; the same refusal\n", o.wraps, dir)
		return 2
	}

	found, examined := bypasses(fset, files, o.wraps, viaBody, dir)

	if skipped > 0 {
		fmt.Fprintf(out, "%d test file(s) not scanned; --tests includes them\n", skipped)
	}
	if o.expect >= 0 && examined != o.expect {
		fmt.Fprintf(errOut, "viaonly: examined %d call(s) to %s, expected exactly %d\n", examined, o.wraps, o.expect)
		return 2
	}

	bad := 0
	for _, s := range found {
		key := fmt.Sprintf("%s:%d", s.file, s.line)
		// ⛔ A waiver may name the SITE or the FUNCTION that contains it, and a
		// CI file should carry the function. A line number is the most brittle
		// handle there is: one inserted line above it and the waiver names
		// something else, or nothing — which is how a gate ends up carrying a
		// waiver nobody can check. The function survives every edit that does
		// not move the call out of it, and it is what a reader recognises.
		why, isWaived := waived[key]
		if !isWaived {
			why, isWaived = waived[s.in]
		}
		if isWaived {
			fmt.Fprintf(out, "waived %s (%s) calls %s directly — %s\n", key, s.in, o.wraps, why)
			continue
		}
		fmt.Fprintf(out, "%s: %s calls %s directly; it must go through %s\n", key, s.in, o.wraps, o.via)
		bad++
	}
	if bad > 0 {
		fmt.Fprintf(out, "%d of %d call(s) to %s bypass %s\n", bad, examined, o.wraps, o.via)
		return 1
	}
	fmt.Fprintf(out, "every one of the %d call(s) to %s goes through %s\n", examined, o.wraps, o.via)
	return 0
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
		out[strings.TrimSpace(site)] = strings.TrimSpace(why)
	}
	return out, nil
}

// findFunc returns the body of the named function, which may be a method
// written as Go writes it in a doc comment: (*T).name or (T).name.
func findFunc(files []*ast.File, name string) (*ast.BlockStmt, bool) {
	recv, fn := splitMethod(name)
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != fn || fd.Body == nil {
				continue
			}
			if recv == "" {
				if fd.Recv == nil {
					return fd.Body, true
				}
				continue
			}
			if receiverName(fd) == recv {
				return fd.Body, true
			}
		}
	}
	return nil, false
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
func bypasses(fset *token.FileSet, files []*ast.File, wraps string, via *ast.BlockStmt, dir string) (found []site, examined int) {
	for _, f := range files {
		var enclosing string
		ast.Inspect(f, func(n ast.Node) bool {
			if fd, ok := n.(*ast.FuncDecl); ok {
				enclosing = fd.Name.Name
				if r := receiverName(fd); r != "" {
					enclosing = r + "." + fd.Name.Name
				}
				return true
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != wraps {
				return true
			}
			examined++
			if via.Pos() <= call.Pos() && call.Pos() <= via.End() {
				return true
			}
			pos := fset.Position(call.Pos())
			found = append(found, site{file: filepath.Base(pos.Filename), line: pos.Line, in: enclosing})
			return true
		})
	}
	_ = dir
	return found, examined
}
