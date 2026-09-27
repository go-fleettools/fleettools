package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"sync/atomic"
)

// ⛔ A JUDGE ONLY A BENCHMARK REACHES IS NOT ONE CI WAS GOING TO RUN.
// `go test` does not run benchmarks without -bench, so "the workflow never
// installs it" is true and costs nothing.
//
// Measured 2026-09-27 on two repositories this tool reported alike:
//
//	go-tpm2/tpm2    startSWTPM is called from six Benchmark* functions and
//	                nothing else — installing swtpm would change nothing
//	openweft/weft   startSwtpm is called from TestRunAttestationHandshake_
//	                RealSwtpm, a real test of a real TPM handshake that has
//	                never run
//
// Same tool, same shape of helper, opposite verdicts. Reporting them alike is
// how a list stops being read.

// funcInfo is one function in a package's test files.
type funcInfo struct {
	tools map[string]bool // looked up directly here
	calls map[string]bool // other functions in these files
}

// reachability answers, for a whole directory of test files, which tools a
// Test can reach and which only a Benchmark can.
//
// It is a call graph one package wide, which is where test helpers live: the
// LookPath is almost never in the Test itself. A single-file view called
// startSWTPM's tool "wanted" without being able to say by whom.
// ⛔ IT ONLY EXCLUDES; IT NEVER SELECTS. The answer is every tool the package
// names MINUS the ones a Benchmark reaches and no Test does. Reporting only
// what a Test provably reaches would be the other, tempting shape — and it
// would drop a judge whose call edge this cannot resolve: a helper invoked
// through a function value, an interface, a table of funcs. The call graph is
// name-based and therefore incomplete by construction, so it is allowed to
// take things off the list and never to be the reason something is on it.
//
// The test that caught this had a helper nothing calls at all.
func reachability(files map[string][]byte) (report, benchOnlyTools map[string]bool, dynamic bool, ok bool) {
	fns := map[string]*funcInfo{}
	var roots []string

	for _, src := range files {
		f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, false, false
		}
		for _, decl := range f.Decls {
			fd, is := decl.(*ast.FuncDecl)
			if !is || fd.Name == nil || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			if fd.Recv != nil {
				// A method: name it distinctly so two types' methods do not
				// merge into one node.
				name = "(method)" + name
			}
			info := fns[name]
			if info == nil {
				info = &funcInfo{tools: map[string]bool{}, calls: map[string]bool{}}
				fns[name] = info
			}
			gated, _, dyn, _ := lookPaths(nodeSource(src, fd))
			for t := range gated {
				info.tools[t] = true
			}
			if dyn {
				dynamic = true
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, is := n.(*ast.CallExpr)
				if !is {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					info.calls[fun.Name] = true
				case *ast.SelectorExpr:
					info.calls["(method)"+fun.Sel.Name] = true
				}
				return true
			})
			if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Benchmark") ||
				strings.HasPrefix(name, "Fuzz") || strings.HasPrefix(name, "Example") {
				roots = append(roots, name)
			}
		}
	}

	byTest, byBench := map[string]bool{}, map[string]bool{}
	for _, root := range roots {
		dest := byTest
		if strings.HasPrefix(root, "Benchmark") {
			dest = byBench
		}
		for t := range toolsFrom(root, fns, map[string]bool{}) {
			dest[t] = true
		}
	}

	report, benchOnlyTools = map[string]bool{}, map[string]bool{}
	for _, info := range fns {
		for t := range info.tools {
			// Excluded only when a Benchmark reaches it and no Test does.
			if byBench[t] && !byTest[t] {
				benchOnlyTools[t] = true
				continue
			}
			report[t] = true
		}
	}
	return report, benchOnlyTools, dynamic, true
}

// toolsFrom walks the call graph from one root, without looping.
func toolsFrom(name string, fns map[string]*funcInfo, seen map[string]bool) map[string]bool {
	out := map[string]bool{}
	if seen[name] {
		return out
	}
	seen[name] = true
	info := fns[name]
	if info == nil {
		return out
	}
	for t := range info.tools {
		out[t] = true
	}
	for callee := range info.calls {
		for t := range toolsFrom(callee, fns, seen) {
			out[t] = true
		}
	}
	return out
}

// nodeSource is one declaration wrapped so it parses on its own, which is what
// lookPaths takes. Cheaper and less fragile than threading a FileSet through.
func nodeSource(src []byte, fd *ast.FuncDecl) []byte {
	start, end := int(fd.Pos())-1, int(fd.End())-1
	if start < 0 || end > len(src) || start >= end {
		return []byte("package p\n")
	}
	return append([]byte("package p\n"), src[start:end]...)
}

// benchOnly counts the tools left out because only a benchmark reaches them.
// Printed rather than silent: the reader is entitled to know the list was
// narrowed, and by how much.
var benchOnly atomic.Int64
