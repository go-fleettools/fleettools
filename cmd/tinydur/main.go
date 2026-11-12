// tinydur flags a sub-resolution duration in a Go test: time.Nanosecond or
// time.Microsecond used as a value in a *_test.go file.
//
//	tinydur DIR...    # each DIR (or *_test.go file) is walked
//
// The smell: a test forces cache/TTL staleness with a duration so small that
// `time.Since(x) < d` is expected to read "stale" between two near-instant
// calls (`Refresh: caddy.Duration(time.Nanosecond)`). On Windows the clock is
// coarse, time.Since can read 0, the entry still counts as fresh, the refresh is
// skipped and the test fails on windows-latest only (mholt/caddy-l4 #429,
// TestSRVReleasesDroppedPeerReferences). Expire the state deterministically
// instead: an injected clock, an explicit invalidate/expire call, a fake entry
// with a past timestamp.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const waiver = "//fleet:allow-tiny-duration"

const longHelp = `Flags time.Nanosecond and time.Microsecond used as a value in *_test.go files.

Forcing cache/TTL staleness with a sub-resolution duration relies on
time.Since(x) < d reading "stale" between two near-instant calls. On Windows the
clock is coarse, time.Since can read 0, the entry stays fresh and the test fails
on windows-latest only (mholt/caddy-l4 #429,
TestSRVReleasesDroppedPeerReferences). Expire state deterministically: an
injected clock, an explicit expire call, or an entry stamped in the past.

Each argument is a directory (walked for *_test.go, skipping .git and vendor)
or a single file. Detection is AST-based: a selector time.Nanosecond or
time.Microsecond (the import is resolved, so an aliased "time" is followed and
another package named time is not). Only *_test.go files are read.

Waiver: a comment on the same line,

    //fleet:allow-tiny-duration <reason>

suppresses the finding. The reason is required; a bare marker is ignored.

Exit status: 0 nothing found, 1 at least one finding, 2 a path or file could not
be read or parsed (a scan that read nothing must not look like a clean one).`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	c := cli.New("tinydur DIR|FILE...",
		"Flag time.Nanosecond/time.Microsecond used as a value in Go tests", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cli.Unanswerable("usage: tinydur DIR|FILE...")
		}
		return cli.FromStatus(run(args, cli.Out(cmd), cli.Err(cmd)))
	}
	return c
}

// Finding is one flagged use.
type Finding struct {
	File string
	Line int
	Unit string
}

func run(paths []string, stdout, stderr io.Writer) int {
	var all []Finding
	files := 0
	for _, p := range paths {
		list, err := collect(p)
		if err != nil {
			fmt.Fprintf(stderr, "tinydur: %v\n", err)
			return 2
		}
		for _, f := range list {
			src, err := os.ReadFile(f)
			if err != nil {
				fmt.Fprintf(stderr, "tinydur: %v\n", err)
				return 2
			}
			found, err := Scan(f, src)
			if err != nil {
				fmt.Fprintf(stderr, "tinydur: %v\n", err)
				return 2
			}
			files++
			all = append(all, found...)
		}
	}
	if files == 0 {
		fmt.Fprintln(stderr, "tinydur: no *_test.go file under the given paths; nothing was read")
		return 2
	}
	for _, f := range all {
		fmt.Fprintf(stdout, "%s:%d: time.%s as a duration in a test: expire state deterministically "+
			"(injected clock, explicit expire, past timestamp), not via a tiny duration; "+
			"coarse clocks (Windows) read time.Since as 0 (waive: %s <reason>)\n",
			f.File, f.Line, f.Unit, waiver)
	}
	fmt.Fprintf(stdout, "%d file(s) read, %d finding(s)\n", files, len(all))
	if len(all) > 0 {
		return 1
	}
	return 0
}

// collect returns the *_test.go files under p (a file or a directory).
func collect(p string) ([]string, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		if strings.HasSuffix(p, "_test.go") {
			return []string{p}, nil
		}
		return nil, nil
	}
	var out []string
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "vendor" || n == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

// Scan reports the unwaived time.Nanosecond / time.Microsecond uses in src. A
// name that does not end in _test.go is never flagged.
func Scan(name string, src []byte) ([]Finding, error) {
	if !strings.HasSuffix(name, "_test.go") {
		return nil, nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	// The local name(s) the standard "time" package is imported under.
	local := map[string]bool{}
	for _, im := range f.Imports {
		if strings.Trim(im.Path.Value, "\"`") != "time" {
			continue
		}
		switch {
		case im.Name == nil:
			local["time"] = true
		case im.Name.Name != "_" && im.Name.Name != ".":
			local[im.Name.Name] = true
		}
	}
	if len(local) == 0 {
		return nil, nil
	}
	waived := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if i := strings.Index(c.Text, waiver); i >= 0 {
				if strings.TrimSpace(c.Text[i+len(waiver):]) != "" {
					waived[fset.Position(c.Slash).Line] = true
				}
			}
		}
	}
	var out []Finding
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || !local[id.Name] || id.Obj != nil {
			return true
		}
		if u := sel.Sel.Name; u == "Nanosecond" || u == "Microsecond" {
			line := fset.Position(sel.Pos()).Line
			if !waived[line] {
				out = append(out, Finding{File: name, Line: line, Unit: u})
			}
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out, nil
}
