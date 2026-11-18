// Package binnames answers one question about a Go repository: what will
// `go install` call each of its commands, and does a well-known command already
// own that name?
//
// ⛔ WHY IT EXISTS. go-authn/kinit v0.1.0 had its main.go at the MODULE ROOT, so
// `go install github.com/go-authn/kinit@latest` built `kinit` (`kinit.exe` on
// Windows) and put it on the PATH of anyone who ran it, in front of or behind
// MIT krb5's, Heimdal's and MIT Kerberos for Windows' own `kinit`. The README
// said `authn-kinit`; the README was not what `go install` reads. The name of an
// installed binary is decided by the import path, so it is a property of the
// layout and nothing in a test or a review looks at it.
package binnames

import (
	"fmt"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// A Main is one `package main` and the name `go install` gives it.
type Main struct {
	// Dir is the slash-separated directory inside the repository, "" for the root.
	Dir string
	// Import is the import path `go install` is given.
	Import string
	// Name is what the build writes, without the `.exe` Windows adds.
	Name string
	// Note says when Name is a guess, for example a repository with no go.mod.
	Note string
}

// A Tree is the files of one repository at one instant, wherever they live: a
// working tree, a ref read through git, or a GitHub tree read over the API.
type Tree interface {
	// Files lists every file, slash-separated, relative to the root.
	Files() ([]string, error)
	// Read returns one file's bytes.
	Read(path string) ([]byte, error)
}

// Unread is something the scan could not read. It is never a clean answer:
// a repository with an Unread entry has NOT been judged.
type Unread struct {
	What string
	Why  string
}

func (u Unread) String() string { return u.What + ": " + u.Why }

// Options narrow a scan.
type Options struct {
	// RepoName is the last element of the repository, used to name a main at
	// the root of a tree that has no go.mod (the GOPATH-era layout).
	RepoName string
	// Candidate, when set, limits which directories are opened. A remote scan
	// has to pay a request per directory and sets it; a local one leaves it nil.
	Candidate func(dir string, files []string) bool
}

var versionElem = regexp.MustCompile(`^v([2-9][0-9]*|[1-9][0-9]+)$`)

// InstallName is the name `go install` gives the package at importPath.
//
// It is the last element, except that a major-version element is skipped:
// example.com/mycmd/v2 installs as mycmd, not as v2 (cmd/go DefaultExecName,
// golang.org/issue/24667). A path that is nothing but a version element keeps
// it.
func InstallName(importPath string) string {
	elems := strings.Split(strings.Trim(importPath, "/"), "/")
	last := elems[len(elems)-1]
	if versionElem.MatchString(last) && len(elems) > 1 {
		last = elems[len(elems)-2]
	}
	return last
}

// Scan lists the `package main` directories of a tree.
func Scan(t Tree, opt Options) ([]Main, []Unread, error) {
	files, err := t.Files()
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)

	var unread []Unread
	mods := map[string]string{} // module root dir -> module path
	byDir := map[string][]string{}
	for _, f := range files {
		dir, base := path.Split(f)
		dir = strings.TrimSuffix(dir, "/")
		if skippedDir(dir) {
			continue
		}
		switch {
		case base == "go.mod":
			b, err := t.Read(f)
			if err != nil {
				unread = append(unread, Unread{f, err.Error()})
				continue
			}
			if mp := modfile.ModulePath(b); mp != "" {
				mods[dir] = mp
			} else {
				unread = append(unread, Unread{f, "no module directive"})
			}
		case strings.HasSuffix(base, ".go") && !strings.HasSuffix(base, "_test.go"):
			byDir[dir] = append(byDir[dir], f)
		}
	}

	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var out []Main
	for _, dir := range dirs {
		if opt.Candidate != nil && !opt.Candidate(dir, byDir[dir]) {
			continue
		}
		isMain, err := packageMain(t, byDir[dir])
		if err != nil {
			unread = append(unread, Unread{dirLabel(dir), err.Error()})
			continue
		}
		if !isMain {
			continue
		}
		m := Main{Dir: dir}
		root, mp, ok := nearestModule(mods, dir)
		switch {
		case ok:
			m.Import = mp
			if rel := strings.TrimPrefix(strings.TrimPrefix(dir, root), "/"); rel != "" {
				m.Import = mp + "/" + rel
			}
			m.Name = InstallName(m.Import)
		case dir == "":
			m.Name = opt.RepoName
			m.Import = opt.RepoName
			m.Note = "no go.mod: the name is the repository's"
		default:
			m.Name = path.Base(dir)
			m.Import = dir
			m.Note = "no go.mod: the name is the directory's"
		}
		if m.Name == "" {
			unread = append(unread, Unread{dirLabel(dir), "a main package whose install name cannot be derived"})
			continue
		}
		out = append(out, m)
	}
	return out, unread, nil
}

func dirLabel(d string) string {
	if d == "" {
		return "."
	}
	return d
}

// skippedDir is a directory `go install` never builds from: testdata and
// vendor by the go command's own rule, and the dot and underscore directories
// it ignores.
func skippedDir(dir string) bool {
	for _, e := range strings.Split(dir, "/") {
		if e == "testdata" || e == "vendor" || e == "node_modules" ||
			strings.HasPrefix(e, ".") || strings.HasPrefix(e, "_") {
			return true
		}
	}
	return false
}

// nearestModule finds the go.mod that owns dir: the longest module root that
// is dir or one of its ancestors.
func nearestModule(mods map[string]string, dir string) (root, mp string, ok bool) {
	for d := dir; ; {
		if p, has := mods[d]; has {
			return d, p, true
		}
		if d == "" {
			return "", "", false
		}
		d = strings.TrimSuffix(path.Dir(d), ".")
		if d == "/" {
			d = ""
		}
	}
}

// packageMain reads the first file of a directory that the build would
// include and says whether it declares package main. A file constrained to
// `ignore` is a `go run` script and is not part of the package.
func packageMain(t Tree, files []string) (bool, error) {
	var firstErr error
	for _, f := range files {
		b, err := t.Read(f)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if ignored(b) {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, b, parser.PackageClauseOnly)
		if err != nil || af.Name == nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: no package clause", f)
			}
			continue
		}
		return af.Name.Name == "main", nil
	}
	// Every file was unreadable: that is not "not a main".
	return false, firstErr
}

// ignored reports a `//go:build ignore` line in the header of a Go file.
func ignored(src []byte) bool {
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if constraint.IsGoBuild(line) {
			x, err := constraint.Parse(line)
			if err != nil {
				return false
			}
			return !x.Eval(func(tag string) bool { return tag != "ignore" })
		}
	}
	return false
}

// DirTree reads a working tree as it is on disk.
type DirTree struct{ Root string }

func (d DirTree) Files() ([]string, error) {
	var out []string
	err := filepath.WalkDir(d.Root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(d.Root, p)
		if e.IsDir() {
			if rel != "." && (e.Name() == ".git" || skippedDir(filepath.ToSlash(rel))) {
				return filepath.SkipDir
			}
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

func (d DirTree) Read(p string) ([]byte, error) {
	return os.ReadFile(filepath.Join(d.Root, filepath.FromSlash(p)))
}
