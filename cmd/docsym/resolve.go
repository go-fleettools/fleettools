package main

import (
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"
)

// Index is every package a README may name, type-checked under each platform
// asked for. A name is found if ANY platform's build has it: window's Wayland
// back-end exists only under linux, and a README naming it is right.
type Index struct {
	byName  map[string][]*types.Package // package name -> its builds, every path and platform
	own     []*types.Package            // the module's own packages, every platform
	all     []*types.Package            // own and same-organisation dependencies
	broken  map[string]string           // package path -> the first load error seen
	version map[string]string           // package path -> the module version it was loaded at
	members map[string]bool             // every exported field and method name of any loaded type
}

// Load type-checks the module in dir and the packages it requires from its
// own organisation (github.com/go-widgets/* for a go-widgets module), once per
// GOOS. Other organisations' packages are not loaded: a README naming
// `opentype.Face` is outside what this module can be held to.
func Load(dir string, goos []string) (*Index, error) {
	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	mf, err := modfile.ParseLax("go.mod", gomod, nil)
	if err != nil || mf.Module == nil {
		return nil, fmt.Errorf("go.mod: cannot read the module path")
	}
	self := mf.Module.Mod.Path
	org := orgPrefix(self)
	patterns := []string{"./..."}
	for _, r := range mf.Require {
		if org != "" && strings.HasPrefix(r.Mod.Path, org) {
			patterns = append(patterns, r.Mod.Path+"/...")
		}
	}

	// ⛔ AN AUDIT MUST NOT WRITE INTO WHAT IT AUDITS. Loading under four
	// platforms needs go.sum lines the module has never needed on one, and
	// with -mod=mod the go command adds them to the module's own go.sum. The
	// first README fixes this tool found went out as "README only" pull
	// requests carrying six go.sum lines nobody had reviewed. -modfile points
	// the go command at a COPY of go.mod -- and the go.sum beside it -- so the
	// checkout is read and never written.
	modfileDir, err := os.MkdirTemp("", "docsym-modfile-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(modfileDir)
	if err := os.WriteFile(filepath.Join(modfileDir, "go.mod"), gomod, 0o644); err != nil {
		return nil, err
	}
	if sum, err := os.ReadFile(filepath.Join(dir, "go.sum")); err == nil {
		if err := os.WriteFile(filepath.Join(modfileDir, "go.sum"), sum, 0o644); err != nil {
			return nil, err
		}
	}

	ix := &Index{byName: map[string][]*types.Package{}, broken: map[string]string{}, version: map[string]string{}, members: map[string]bool{}}
	for _, g := range goos {
		cfg := &packages.Config{
			Mode: packages.NeedName | packages.NeedTypes | packages.NeedModule,
			Dir:  dir,
			Env:  loadEnv(g, filepath.Join(modfileDir, "go.mod")),
		}
		pkgs, err := packages.Load(cfg, patterns...)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", g, err)
		}
		for _, p := range pkgs {
			if len(p.Errors) > 0 {
				if _, seen := ix.broken[p.PkgPath]; !seen {
					ix.broken[p.PkgPath] = g + ": " + p.Errors[0].Msg
				}
			}
			if p.Types == nil {
				continue
			}
			if p.Module != nil && p.Module.Version != "" {
				ix.version[p.PkgPath] = p.Module.Version
			}
			ix.byName[p.Name] = append(ix.byName[p.Name], p.Types)
			ix.all = append(ix.all, p.Types)
			addMembers(ix.members, p.Types)
			if p.PkgPath == self || strings.HasPrefix(p.PkgPath, self+"/") {
				ix.own = append(ix.own, p.Types)
			}
		}
	}
	return ix, nil
}

// loadEnv is the environment one platform's load runs under. The module file
// is the scratch copy, never the checkout's own.
func loadEnv(goos, modfile string) []string {
	arch := "amd64"
	if goos == "js" {
		arch = "wasm"
	}
	return append(os.Environ(), "GOOS="+goos, "GOARCH="+arch, "CGO_ENABLED=0", "GOWORK=off",
		"GOFLAGS=-mod=mod -modfile="+modfile)
}

func orgPrefix(module string) string {
	parts := strings.Split(module, "/")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[:2], "/") + "/"
}

func addMembers(set map[string]bool, p *types.Package) {
	scope := p.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		ms := types.NewMethodSet(types.NewPointer(tn.Type()))
		for i := 0; i < ms.Len(); i++ {
			if f := ms.At(i).Obj(); f.Exported() {
				set[f.Name()] = true
			}
		}
		// ⛔ A pointer to an interface has an EMPTY method set, so the loop
		// above never sees an interface's methods: webcanvas's App.Draw and
		// App.KeyDown were reported missing on the first run. Ask the
		// interface itself.
		if it, ok := tn.Type().Underlying().(*types.Interface); ok {
			for i := 0; i < it.NumMethods(); i++ {
				if f := it.Method(i); f.Exported() {
					set[f.Name()] = true
				}
			}
		}
		if st, ok := tn.Type().Underlying().(*types.Struct); ok {
			addFields(set, st, 0)
		}
	}
}

func addFields(set map[string]bool, st *types.Struct, depth int) {
	if depth > 4 {
		return
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if f.Exported() {
			set[f.Name()] = true
		}
		if f.Embedded() {
			if inner, ok := f.Type().Underlying().(*types.Struct); ok {
				addFields(set, inner, depth+1)
			}
			if p, ok := f.Type().(*types.Pointer); ok {
				if inner, ok := p.Elem().Underlying().(*types.Struct); ok {
					addFields(set, inner, depth+1)
				}
			}
		}
	}
}

// Verdict is what became of one reference.
type Verdict int

const (
	Found      Verdict = iota
	Missing            // the README names something no build of the package has
	NotOurs            // the qualifier is not a package loaded here (a variable, another org)
	Unreadable         // the package it would be in failed to type-check somewhere
	// Unconfirmed is a BARE name in a code span that no loaded package has.
	// Measured on go-widgets' 19 READMEs, nearly all of these are not Go of
	// this organisation at all -- NSEvent, StretchDIBits, MessagePort, X11
	// event names, test names -- so they are counted and listed only on
	// request: a report that cries wolf 249 times is not read.
	Unconfirmed
)

// Check judges one reference, and for a Missing one says where it looked.
func (ix *Index) Check(r Ref) (Verdict, string) {
	if r.Pkg != "" {
		pkgs := ix.byName[r.Pkg]
		if len(pkgs) == 0 {
			return NotOurs, ""
		}
		var paths []string
		for _, p := range pkgs {
			if resolves(p, r.Name, r.Member) {
				return Found, ""
			}
			path := p.Path()
			if v := ix.version[path]; v != "" {
				// ⛔ The version is the point. window's README names
				// mvvm.Queue, which mvvm has had since v0.14.0 -- but window
				// builds against v0.9.0. "not in mvvm" alone read as a typo.
				path += "@" + v
			}
			paths = append(paths, path)
		}
		paths = uniq(paths)
		for _, p := range pkgs {
			if _, bad := ix.broken[p.Path()]; bad {
				return Unreadable, p.Path()
			}
		}
		where := strings.Join(paths, ", ")
		if r.Member == "" {
			if owner := methodOwner(pkgs, r.Name); owner != "" {
				where += "; " + r.Name + " is a method of " + owner + ", so `" + owner + "." + r.Name + "`"
			}
		}
		return Missing, where
	}
	// A bare name: the module's own packages first, then what it requires
	// from its organisation (window's README names toolkit's VBox).
	for _, p := range ix.all {
		if resolves(p, r.Name, r.Member) {
			return Found, ""
		}
	}
	// A bare `Draw` or `SetBounds` names a method, not a top-level name.
	if r.Member == "" && ix.members[r.Name] {
		return Found, ""
	}
	for path := range ix.broken {
		return Unreadable, path
	}
	return Unconfirmed, "any loaded package"
}

// methodOwner names the exported type in pkgs that has a method called name,
// for a README that writes `tray.Attach` where it means (*tray.Tray).Attach.
func methodOwner(pkgs []*types.Package, name string) string {
	for _, p := range pkgs {
		scope := p.Scope()
		for _, n := range scope.Names() {
			tn, ok := scope.Lookup(n).(*types.TypeName)
			if !ok || !tn.Exported() {
				continue
			}
			if m, _, _ := types.LookupFieldOrMethod(tn.Type(), true, p, name); m != nil {
				if _, isFunc := m.(*types.Func); isFunc {
					return tn.Name()
				}
			}
		}
	}
	return ""
}

// resolves reports whether p exports name, and when a member is named, whether
// name's type (or a variable's type) has that field or method.
func resolves(p *types.Package, name, member string) bool {
	obj := p.Scope().Lookup(name)
	if obj == nil || !obj.Exported() {
		return false
	}
	if member == "" {
		return true
	}
	switch o := obj.(type) {
	case *types.TypeName:
		m, _, _ := types.LookupFieldOrMethod(o.Type(), true, p, member)
		return m != nil
	case *types.Var:
		m, _, _ := types.LookupFieldOrMethod(o.Type(), true, p, member)
		return m != nil
	}
	return true // a function or constant: what follows the dot is not a member of it
}

func uniq(s []string) []string {
	sort.Strings(s)
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
