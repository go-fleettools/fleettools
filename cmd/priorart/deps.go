package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// The third surface: the modules this repository ALREADY REQUIRES.
//
// ⛔⛔ WHY THE FIRST TWO WERE NOT ENOUGH, measured on 2026-11-08. Working in
// go-tex/go-tex.github.io, the gate was run exactly as written —
//
//	priorart --repo go-tex/go-tex.github.io "file manager"
//	priorart --repo go-tex/go-tex.github.io "file tree manager"
//
// — and reported clean, correctly. go-widgets/toolkit, a module that repository
// already imports, had carried `FileTree` since toolkit#498: a complete
// Explorer with BeginCreate, BeginRename, AskDelete, drag-to-move, import of
// files dropped from outside, inline name validation, a context menu,
// type-ahead and the WAI-ARIA treeview keys. A "New file" button was written
// over three planned steps anyway, and closed unmerged once a human said *there
// must already be one*.
//
// Nothing about a consumer's own tracker or branches can contain a widget that
// lives upstream, so this is not the failure #193 was built to stop — somebody
// forgetting to check. It is the check not covering the question. "Has this
// been done?" has two halves, and only one of them is about THIS repository.
//
// It reads the EXPORTED SURFACE rather than prose: a declared type, function or
// method whose NAME carries a term. `go doc` output or a grep over comments
// would answer "file" from any sentence containing the word; an identifier
// named FileTree is the thing itself.

// depResult is what the dependency surface answered: the hits, how many modules
// were read, and which ones could not be.
//
// ⛔ THE THREE TRAVEL TOGETHER ON PURPOSE. "No declaration carries this subject"
// over zero modules read is not the same answer as over eleven, and a module
// that could not be read is neither a hit nor a clean result. Collapsing any of
// them into the others is how a scan that could not read reports zero.
type depResult struct {
	Hits   []Decl
	Read   int
	Unread []string
}

// depSurface reads the build list of the module in dir and the exported surface
// of each module it selects.
//
// ⛔ A module it cannot read does NOT abandon the others. The first draft
// returned on the first failure, and on a real repository that meant one
// uncached module (go-iconoir/iconoir, not downloaded) hid every hit from the
// ten that were readable — a gate that refuses while showing nothing is one a
// reader cannot act on. Each failure is named, the readable modules still
// answer, and the caller still counts this surface as unread.
func depSurface(dir string, terms []string, all bool) (depResult, error) {
	ms, err := modules(dir)
	if err != nil {
		// The build list itself is the one failure with nothing behind it.
		return depResult{}, err
	}
	// ⛔ Without the org list we cannot tell our modules from a stranger's, so
	// the surface is unread rather than read with a guess. Failing open here
	// would restore the 322-hit noise that made the first draft useless.
	orgs := map[string]bool{}
	if !all {
		names, err := ourOrgs()
		if err != nil {
			return depResult{}, fmt.Errorf("listing the organisations this account belongs to: %w", err)
		}
		for _, o := range names {
			orgs[o] = true
		}
	}
	var r depResult
	selected := 0
	for _, m := range ms {
		if m.Main || (!all && !Owned(m.Path, orgs)) {
			continue
		}
		selected++
		ds, err := Surface(m, terms)
		if err != nil {
			r.Unread = append(r.Unread, err.Error())
			continue
		}
		r.Read++
		r.Hits = append(r.Hits, ds...)
	}
	if selected == 0 && len(ms) > 0 && !all {
		// ⛔ Not an error, and not silence either: a build list with no
		// first-party module means this surface answered nothing at all, and a
		// reader who took a clean run for coverage would be wrong.
		return r, fmt.Errorf("none of the %d modules in the build list is a fleet module; pass -deps-all to read them anyway", len(ms))
	}
	sort.Slice(r.Hits, func(i, j int) bool {
		if r.Hits[i].Module != r.Hits[j].Module {
			return r.Hits[i].Module < r.Hits[j].Module
		}
		return r.Hits[i].Name < r.Hits[j].Name
	})
	sort.Strings(r.Unread)
	return r, nil
}

// Module is one entry of the build list, as `go list -m -json all` prints it.
type Module struct {
	Path    string
	Version string
	Dir     string
	Main    bool
}

// modules is the build-list reader, replaced in tests.
var modules = goListModules

// goListModules asks the go command for the build list of the module in dir.
//
// ⛔ `go list -m all` is the honest source and ALSO a thing that fails: it
// accounts for `replace` directives and for the versions MVS actually selected,
// where parsing go.mod by hand reports neither — but a dependency that declares
// a tool requirement at a version resolved by a local replace makes it refuse
// outright for every consumer (see cmd/vscan/apidiff.go, which stands in for
// gorelease for exactly that reason). A refusal here is therefore reported as
// UNREAD, never as an empty build list: an answer not obtained is not a
// negative answer, which is this command's whole posture.
func goListModules(dir string) ([]Module, error) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// Stderr carries the go command's own sentence, which names the module
		// that could not be resolved. Without it the reader gets "exit status 1".
		var detail string
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			detail = ": " + strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("go list -m all in %s%s", dir, detail)
	}
	return decodeModules(out)
}

// decodeModules reads the concatenated JSON objects `go list -json` emits.
func decodeModules(out []byte) ([]Module, error) {
	dec := json.NewDecoder(strings.NewReader(string(out)))
	var ms []Module
	for dec.More() {
		var m Module
		if err := dec.Decode(&m); err != nil {
			return nil, fmt.Errorf("reading the build list: %w", err)
		}
		ms = append(ms, m)
	}
	return ms, nil
}

// ourOrgs lists the organisations the authenticated account belongs to,
// replaced in tests.
var ourOrgs = fleet.Orgs

// Owned reports whether a module path belongs to one of orgs.
//
// ⛔⛔ A PREFIX HEURISTIC WAS WRONG IN BOTH DIRECTIONS, and it was measured
// rather than reasoned about. The first draft matched any `github.com/go-*`
// owner, with a comment asserting that every organisation in this fleet is
// named that way — true, and useless, because so are a great many others. Run
// against go-tex's playground it answered **322 declarations in 38 modules**,
// among them `go-git/gcfg`'s `token.File`, `go-logr`, `go-jose` and
// `go-openapi`. A gate that fires on every run teaches the reader to skim it,
// and then it is not a control.
//
// The authoritative list is the one the fleet's own sweeps use: the
// organisations this account belongs to (fleet.Orgs). A third party cannot be
// in it, and a new organisation of ours appears the day it is created — which a
// list compiled into the binary could not do, and would fail in the direction
// that reports clean.
func Owned(path string, orgs map[string]bool) bool {
	rest, ok := strings.CutPrefix(path, "github.com/")
	if !ok {
		return false
	}
	owner, _, ok := strings.Cut(rest, "/")
	return ok && orgs[owner]
}

// Decl is one exported declaration whose name carries a term.
type Decl struct {
	Module  string
	Version string
	Pkg     string
	Kind    string // "type", "func", "method", "const", "var"
	Name    string // with the receiver for a method: "FileTree.BeginRename"
	Term    string
}

// Surface returns the exported declarations of m whose names carry a term,
// sorted, with at most one entry per name.
//
// ⛔ METHODS COUNT, and leaving them out would have missed most of the case
// that motivated this. The miss was findable by the type name `FileTree`, but
// the thing a reader needs to see is that `BeginRename`, `AskDelete` and
// `BeginCreate` already exist — the verbs, not the noun. A surface that listed
// only types would have answered "there is a FileTree" and not "it already does
// what you are about to write".
func Surface(m Module, terms []string) ([]Decl, error) {
	if m.Dir == "" {
		// A module in the build list with no local directory has not been
		// downloaded. Saying so beats reporting it clean.
		return nil, fmt.Errorf("module %s@%s is not in the module cache", m.Path, m.Version)
	}
	seen := map[string]bool{}
	var out []Decl
	fset := token.NewFileSet()

	err := filepath.WalkDir(m.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) && path != m.Dir {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			// One unparseable file is not a reason to abandon a module: it may
			// be a fixture or written for another toolchain. The rest still
			// answers the question.
			return nil
		}
		for _, decl := range f.Decls {
			for _, d := range declNames(decl) {
				if !ast.IsExported(lastComponent(d.Name)) {
					continue
				}
				term, ok := carriesTerm(d.Name, terms)
				if !ok || seen[d.Name] {
					continue
				}
				seen[d.Name] = true
				out = append(out, Decl{
					Module: m.Path, Version: m.Version,
					Pkg: f.Name.Name, Kind: d.Kind, Name: d.Name, Term: term,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading %s@%s: %w", m.Path, m.Version, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// skipDir names the directories whose contents answer a different question:
// vendored copies, test fixtures, and anything Go itself ignores.
func skipDir(name string) bool {
	return name == "vendor" || name == "testdata" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// declNames flattens one declaration into the names it introduces.
func declNames(decl ast.Decl) []Decl {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv != nil && len(d.Recv.List) > 0 {
			return []Decl{{Kind: "method", Name: receiverName(d.Recv.List[0].Type) + "." + d.Name.Name}}
		}
		return []Decl{{Kind: "func", Name: d.Name.Name}}
	case *ast.GenDecl:
		var out []Decl
		kind := map[token.Token]string{token.TYPE: "type", token.CONST: "const", token.VAR: "var"}[d.Tok]
		if kind == "" {
			return nil // an import group
		}
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				out = append(out, Decl{Kind: kind, Name: s.Name.Name})
			case *ast.ValueSpec:
				for _, n := range s.Names {
					out = append(out, Decl{Kind: kind, Name: n.Name})
				}
			}
		}
		return out
	}
	return nil
}

// receiverName is the bare type name of a method receiver, pointer or not.
func receiverName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr: // a generic receiver, Thing[T]
		return receiverName(t.X)
	case *ast.IndexListExpr:
		return receiverName(t.X)
	}
	return ""
}

// lastComponent is the part of a name whose case decides exportedness: for a
// method it is the method's own name, not the receiver's.
func lastComponent(name string) string {
	if _, after, ok := strings.Cut(name, "."); ok {
		return after
	}
	return name
}

// carriesTerm reports the first term the identifier carries as one of its
// CAMEL-CASE COMPONENTS, case-insensitively.
//
// ⛔⛔ A SUBSTRING MATCH WAS NOT GOOD ENOUGH, and the measurement said so: on
// go-tex's playground, "file" answered from `ICCLutProfile` — "Profile" ends in
// those four letters — and from `File_collab_proto`. Those are not prior art
// for a file manager; they are noise that drowns `FileTree`.
//
// Splitting the identifier on case changes and underscores and comparing whole
// components keeps `FileTree`, `FileChooser` and `BeginRename` while dropping
// `Profile`. ⛔ It also means a term must be a whole word of the name, so
// "rename" answers from `BeginRename` and not from `Renamed`-style prefixes of
// a longer word — which is the right trade for a gate: a missed hit is a gate
// that fails open, so the components are matched by EQUALITY and the subject is
// expected to name the thing (which this command's help already insists on).
func carriesTerm(name string, terms []string) (string, bool) {
	parts := camelComponents(lastComponent(name))
	// A method's receiver is part of the answer too: FileTree.BeginRename
	// should answer "file" as well as "rename".
	if recv, _, ok := strings.Cut(name, "."); ok {
		parts = append(parts, camelComponents(recv)...)
	}
	for _, t := range terms {
		// A multi-word phrase cannot be one component; its own words are in the
		// term list separately.
		if strings.ContainsAny(t, " \t") {
			continue
		}
		lower := strings.ToLower(t)
		for _, p := range parts {
			if p == lower {
				return t, true
			}
		}
	}
	return "", false
}

// camelComponents splits an identifier into its lower-cased words, on case
// changes and underscores: "FileTree" → file, tree; "ICCLutProfile" → icc, lut,
// profile; "File_collab_proto" → file, collab, proto.
func camelComponents(name string) []string {
	var out []string
	runes := []rune(name)
	start := 0
	flush := func(end int) {
		if end > start {
			out = append(out, strings.ToLower(string(runes[start:end])))
		}
		start = end
	}
	for i := 1; i < len(runes); i++ {
		switch {
		case runes[i] == '_':
			flush(i)
			start = i + 1
		case isUpper(runes[i]) && !isUpper(runes[i-1]):
			// aB → a | B
			flush(i)
		case isUpper(runes[i-1]) && isUpper(runes[i]) && i+1 < len(runes) && !isUpper(runes[i+1]):
			// ABc → A | Bc, which is what turns ICCLutProfile into icc|lut|profile
			flush(i)
		}
	}
	flush(len(runes))
	return out
}

func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }
