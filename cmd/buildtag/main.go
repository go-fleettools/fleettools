// Command buildtag adds a build constraint to Go source files: it ANDs the
// given expression into each file's //go:build line, or writes that line when
// the file has none.
//
//	buildtag -and '!rbgo_core' a.go b.go …
//
// It exists because a build tag across a few hundred files is an edit nobody
// should make by hand or with a stream editor. go-embedded-ruby's rbgo_core
// build needed it on 288 gem-binding files: leaving a gem's register function
// uncalled does NOT drop the gem from the binary, because every imported
// package's init runs, and the linker keeps it and everything it reaches. Only a
// file that is not compiled stops importing.
//
// Each file is handled the way gofmt expects:
//
//   - an existing //go:build line X becomes `X && EXPR`, parenthesised by
//     go/build/constraint, so `!(js && wasm)` becomes `!(js && wasm) && !tag`;
//   - a file already carrying EXPR as one of its conjuncts is left alone, so a
//     second run changes nothing;
//   - a new line goes after the leading comment block when a blank line closes
//     it (a licence header), otherwise at the top, and is always followed by a
//     blank line, so it can never be read as part of a package doc comment.
//
// It refuses, and writes nothing at all, when any file cannot be handled
// safely: a legacy "// +build" line (gofmt keeps the two in step, a tool adding
// to one would split them), two //go:build lines, a /* */ comment before the
// package clause, or a result go/parser cannot read. Every file is checked
// before any is written. -n lists what would change and writes nothing.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"os"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("buildtag", flag.ContinueOnError)
	fs.SetOutput(stderr)
	and := fs.String("and", "", "build constraint expression to AND into each file, e.g. '!rbgo_core'; required")
	dry := fs.Bool("n", false, "list the files that would change, write nothing")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: buildtag -and EXPR [-n] file.go...")
		fs.PrintDefaults()
	}
	if code, ok := fleet.ParseFlags(fs, args); !ok {
		return code
	}
	if *and == "" || fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	add, err := constraint.Parse("//go:build " + *and)
	if err != nil {
		fmt.Fprintf(stderr, "buildtag: -and %q: %v\n", *and, err)
		return 2
	}

	type edit struct {
		path string
		out  []byte
		mode os.FileMode
	}
	var edits []edit
	unchanged := 0
	failed := false
	for _, path := range fs.Args() {
		fi, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(stderr, "buildtag: %v\n", err)
			failed = true
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "buildtag: %v\n", err)
			failed = true
			continue
		}
		out, changed, err := addConstraint(src, add)
		if err != nil {
			fmt.Fprintf(stderr, "buildtag: %s: %v\n", path, err)
			failed = true
			continue
		}
		if !changed {
			unchanged++
			continue
		}
		edits = append(edits, edit{path, out, fi.Mode().Perm()})
	}
	if failed {
		fmt.Fprintln(stderr, "buildtag: nothing written")
		return 2
	}
	for _, e := range edits {
		if *dry {
			fmt.Fprintln(stdout, e.path)
			continue
		}
		if err := os.WriteFile(e.path, e.out, e.mode); err != nil {
			fmt.Fprintf(stderr, "buildtag: %v\n", err)
			return 2
		}
	}
	verb := "changed"
	if *dry {
		verb = "would change"
	}
	fmt.Fprintf(stdout, "%d %s, %d already carried %s\n", len(edits), verb, unchanged, add)
	return 0
}

// addConstraint returns src with add ANDed into its //go:build line, and
// whether anything changed.
func addConstraint(src []byte, add constraint.Expr) ([]byte, bool, error) {
	text := string(src)
	lines := strings.SplitAfter(text, "\n")
	// The header is everything before the package clause: blank lines and line
	// comments only. That is where the go command looks for a constraint.
	header := 0
	buildAt := -1
	for header < len(lines) {
		l := strings.TrimSpace(lines[header])
		switch {
		case l == "":
		case strings.HasPrefix(l, "//"):
			if constraint.IsPlusBuild(l) {
				return nil, false, fmt.Errorf("line %d: legacy %q line; run gofmt first, which converts it", header+1, "// +build")
			}
			if constraint.IsGoBuild(l) {
				if buildAt >= 0 {
					return nil, false, fmt.Errorf("lines %d and %d: two //go:build lines", buildAt+1, header+1)
				}
				buildAt = header
			}
		case strings.HasPrefix(l, "/*"):
			return nil, false, fmt.Errorf("line %d: a /* */ comment before the package clause; not handled", header+1)
		default:
			goto done
		}
		header++
	}
done:
	if header == len(lines) {
		return nil, false, errors.New("no package clause")
	}

	var out []string
	if buildAt >= 0 {
		old, err := constraint.Parse(strings.TrimSpace(lines[buildAt]))
		if err != nil {
			return nil, false, fmt.Errorf("line %d: %v", buildAt+1, err)
		}
		if hasConjunct(old, add) {
			return src, false, nil
		}
		out = append(out, lines[:buildAt]...)
		out = append(out, "//go:build "+(&constraint.AndExpr{X: old, Y: add}).String()+"\n")
		out = append(out, lines[buildAt+1:]...)
	} else {
		// After a leading comment block a blank line closes (a licence header):
		// the line after that blank line. Otherwise at the very top.
		at := 0
		for i := 0; i < header; i++ {
			if strings.TrimSpace(lines[i]) == "" {
				if i > 0 {
					at = i + 1
				}
				break
			}
		}
		out = append(out, lines[:at]...)
		out = append(out, "//go:build "+add.String()+"\n", "\n")
		out = append(out, lines[at:]...)
	}
	res := []byte(strings.Join(out, ""))
	if _, err := parser.ParseFile(token.NewFileSet(), "", res, parser.PackageClauseOnly); err != nil {
		return nil, false, fmt.Errorf("the result does not parse: %v", err)
	}
	if !bytes.Contains(res, []byte("//go:build ")) {
		return nil, false, errors.New("the result carries no //go:build line")
	}
	return res, true, nil
}

// hasConjunct reports whether want is x itself or one of the operands of the
// && chain x is made of.
func hasConjunct(x, want constraint.Expr) bool {
	if x.String() == want.String() {
		return true
	}
	if a, ok := x.(*constraint.AndExpr); ok {
		return hasConjunct(a.X, want) || hasConjunct(a.Y, want)
	}
	return false
}
