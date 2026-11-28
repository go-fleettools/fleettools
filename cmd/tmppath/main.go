// Command tmppath refuses a fixed path in the shared temporary directory in
// the non-test Go source of a tree.
//
// ⛔ WHY IT EXISTS. go-filesystems/ext4 v0.7.1-v0.7.3 wrote logs and raw block
// dumps to fixed `/tmp/ext4_*` paths, and go-filesystems/kerneljudge <= v0.4.0
// wrote a Python script to `os.TempDir()/kerneljudge-ole.py` and EXECUTED it:
// a fixed name in a directory every user can write is a swap or a symlink away
// from overwriting a file or running somebody else's code. Both were fixed with
// a copied source-test guard; this is the one shared gate.
//
// It reads the SYNTAX TREE, not the text: a comment that mentions os.TempDir()
// does not match (unarchive dmg.go:48, oci limits.go:147 do exactly that). In
// every non-_test.go file, skipping testdata/ and dot-directories, it flags
//
//   - a string literal that is /tmp or /var/tmp, or begins with either followed
//     by a slash (so "/tmpl" is not one);
//   - os.TempDir, ioutil.TempFile and ioutil.TempDir, called or taken as a
//     value (the import is resolved, so an alias is seen and a local variable
//     named os is not).
//
// os.CreateTemp and os.MkdirTemp are NOT flagged: their names are random and
// created O_EXCL.
//
// An allow comment `//tmppath:ok <reason>` on the same line accepts one finding.
// A comment with an EMPTY reason is itself a finding and accepts nothing.
//
// ⛔ A FILE IT COULD NOT READ IS UNREAD, NEVER CLEAN: a parse error or an
// unreadable directory is reported UNREAD and makes the status 2.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// allow is the marker of an accepted finding.
const allow = "//tmppath:ok"

// finding is one line to report.
type finding struct {
	line int
	what string
}

// isTmp reports whether s names the shared temporary directory or something in it.
func isTmp(s string) bool {
	for _, p := range []string{"/var/tmp", "/tmp"} {
		if s == p || strings.HasPrefix(s, p+"/") {
			return true
		}
	}
	return false
}

// banned maps an import path to the functions of it that are refused.
var banned = map[string][]string{
	"os":        {"TempDir"},
	"io/ioutil": {"TempFile", "TempDir"},
}

// scan returns the findings of one parsed file, in source order.
func scan(fset *token.FileSet, f *ast.File) []finding {
	var out []finding
	// names maps a local package name to the import path it stands for. A blank
	// import is named "_", which no selector can use; a dot import is itself a
	// finding, because it hides the call from the selector.
	names := map[string]string{}
	for _, im := range f.Imports {
		path, _ := strconv.Unquote(im.Path.Value)
		name := filepath.Base(path)
		if im.Name != nil {
			name = im.Name.Name
		}
		if name == "." && banned[path] != nil {
			out = append(out, finding{fset.Position(im.Pos()).Line, "dot import of " + path + " hides its temporary-directory functions"})
		}
		names[name] = path
	}

	// allowed lines, and the refused markers.
	ok := map[int]bool{}
	for _, g := range f.Comments {
		for _, c := range g.List {
			rest, found := strings.CutPrefix(c.Text, allow)
			if !found || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
				continue
			}
			line := fset.Position(c.Pos()).Line
			if strings.TrimSpace(rest) == "" {
				out = append(out, finding{line, allow + " has no reason, so it accepts nothing"})
				continue
			}
			ok[line] = true
		}
	}

	report := func(n ast.Node, what string) {
		if line := fset.Position(n.Pos()).Line; !ok[line] {
			out = append(out, finding{line, what})
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			if s, err := strconv.Unquote(n.Value); err == nil && isTmp(s) {
				report(n, "string literal "+n.Value+" names the shared temporary directory")
			}
		case *ast.SelectorExpr:
			pkg := packageName(n.X)
			for _, fn := range banned[names[pkg]] {
				if n.Sel.Name == fn {
					report(n, pkg+"."+fn+" names the shared temporary directory")
				}
			}
		}
		return true
	})
	sortFindings(out)
	return out
}

// packageName is the name x spells when it is an identifier no declaration of
// the file binds (a local variable or parameter named os is not the package),
// and "" otherwise.
func packageName(x ast.Expr) string {
	if id, isIdent := x.(*ast.Ident); isIdent && id.Obj == nil {
		return id.Name
	}
	return ""
}

// sortFindings orders by line, keeping the order of equal lines (insertion sort:
// the lists are short).
func sortFindings(a []finding) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j].line < a[j-1].line; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// skipDir reports whether a directory is not walked.
func skipDir(name string) bool {
	return name == "testdata" || (strings.HasPrefix(name, ".") && name != "." && name != "..")
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	c := cli.New("tmppath [DIR...]",
		"Refuse a fixed path in the shared temporary directory in non-test Go source",
		`Parses every non-_test.go file under each DIR (default .), skipping testdata/
and dot-directories, and prints file:line: what for each string literal that is
/tmp or /var/tmp (or begins with either and a slash) and each os.TempDir,
ioutil.TempFile and ioutil.TempDir. Comments never match. os.CreateTemp and
os.MkdirTemp are safe and are not flagged.

//tmppath:ok <reason> on the same line accepts a finding; an empty reason is
refused and is a finding itself.

A file that does not parse, or a directory that cannot be read, is UNREAD, never clean.

exit 0 every file read, none holds a finding
exit 1 at least one finding (and every file was read)
exit 2 something could not be read`)

	c.RunE = func(c *cobra.Command, dirs []string) error {
		if cli.HelpRequested(c) {
			return nil
		}
		if len(dirs) == 0 {
			dirs = []string{"."}
		}
		out := cli.Out(c)
		files, hits, unread := 0, 0, 0
		for _, root := range dirs {
			if _, err := os.Lstat(root); err != nil {
				// said here, not left to WalkDir's callback, whose directory entry is nil
				fmt.Fprintf(out, "UNREAD %s: named directory: %v\n", root, err)
				unread++
				continue
			}
			// the callback never returns an error, so WalkDir has none of its own.
			_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					fmt.Fprintf(out, "UNREAD %s: %v\n", path, err)
					unread++
					return nil
				}
				if d.IsDir() {
					if path != root && skipDir(d.Name()) {
						return fs.SkipDir
					}
					return nil
				}
				if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				fset := token.NewFileSet()
				f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
				if err != nil {
					fmt.Fprintf(out, "UNREAD %s: %v\n", path, err)
					unread++
					return nil
				}
				files++
				for _, fd := range scan(fset, f) {
					fmt.Fprintf(out, "%s:%d: %s\n", path, fd.line, fd.what)
					hits++
				}
				return nil
			})
		}
		fmt.Fprintf(out, "SUMMARY files=%d findings=%d unread=%d\n", files, hits, unread)
		if unread > 0 {
			return cli.Unanswerable("%d paths could not be read", unread)
		}
		if hits > 0 {
			return cli.Found("%d findings", hits)
		}
		return nil
	}
	return c
}
