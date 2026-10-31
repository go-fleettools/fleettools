// Command wasmsize says where the bytes of a Go WebAssembly binary go: the
// code of each package (or module), measured from the module's own code
// section, plus the data and the other sections.
//
// It exists because `go tool nm` and `go version -m` both answer
// "unrecognized file format" for a .wasm, and the question came up for real:
// go-embedded-ruby's browser build was 102 MB (21.8 MB gzipped), six times the
// go-ndarray playground that wanted to embed it, and cutting it down needs to
// know which of its ~700 packages weigh what.
//
// Function names come from the "name" custom section, which cmd/link writes
// unless -s is given. It writes them sanitized (every byte outside
// [0-9A-Za-z_.] becomes "_"), so a name cannot be split back into a package
// path; wasmsize matches it against the sanitized form of the build's package
// list instead, given with -deps (the output of `go list -deps` for the same
// GOOS/GOARCH and tags). Two paths that sanitize alike are reported, never
// guessed between.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Prints the size of each section of a WebAssembly module, then the code of each
package (or module) of a Go build, largest first, from the function bodies of
the code section and the function names of the "name" section.

Build the module WITHOUT -ldflags=-s, which drops the names, and list the same
build's packages:

    GOOS=js GOARCH=wasm go build -o app.wasm ./cmd/app
    GOOS=js GOARCH=wasm go list -deps \
        -f '{{.ImportPath}} {{with .Module}}{{.Path}}{{end}}' ./cmd/app > deps.txt
    wasmsize --deps deps.txt --by module app.wasm

The module column is optional; without it --by module guesses modules from the
first three path elements (wrong for google.golang.org/protobuf). Use the same
GOOS, GOARCH and -tags for go list as for the build.

Exit status: 0 the module was measured, 2 it could not be — a module built with
-ldflags=-s carries no names, and attributing its code would be a guess.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var (
		deps  string
		by    string
		top   int
		match string
	)
	c := cli.New("wasmsize --deps FILE [flags] file.wasm",
		"Where a WebAssembly module's bytes went, by package or module", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(measure(cli.Out(cmd), cli.Err(cmd), deps, by, top, match, args))
	}
	f := c.Flags()
	f.StringVar(&deps, "deps", "", "file with the build's package paths, one per line (`go list -deps`); required")
	f.StringVar(&by, "by", "package", "group by package or module")
	f.IntVar(&top, "top", 30, "print the N largest groups (0: all)")
	f.StringVar(&match, "match", "", "print only groups whose name contains this")
	return c
}

// run keeps the signature this command's tests were written against.
func run(args []string, stdout, stderr io.Writer) int {
	return cli.Drive(RootCmd(), args, stdout, stderr)
}

func measure(stdout, stderr io.Writer, deps, by string, top int, match string, args []string) int {
	if len(args) != 1 || deps == "" || (by != "package" && by != "module") {
		fmt.Fprintln(stderr, "usage: wasmsize --deps FILE [--by package|module] "+
			"[--top N] [--match S] file.wasm")
		return 2
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "wasmsize:", err)
		return 2
	}
	m, err := parse(b)
	if err != nil {
		fmt.Fprintf(stderr, "wasmsize: %s: %v\n", args[0], err)
		return 2
	}
	// ⛔ No names means no attribution. Printing the section sizes with a
	// silent zero per package would read as "every package is tiny".
	if len(m.names) == 0 {
		fmt.Fprintf(stderr, "wasmsize: %s has no function names: it was built with -ldflags=-s; build it without -s to measure it\n", args[0])
		return 2
	}
	paths, err := readLines(deps)
	if err != nil {
		fmt.Fprintln(stderr, "wasmsize:", err)
		return 2
	}
	a := newAttributor(paths)
	report(stdout, len(b), m, a, by == "module", top, match)
	return 0
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		out = append(out, s.Text())
	}
	return out, s.Err()
}

func report(w io.Writer, total int, m *module, a *attributor, byModule bool, top int, match string) {
	pct := func(n int) float64 { return 100 * float64(n) / float64(total) }
	fmt.Fprintf(w, "%d bytes in all\n\nsections\n", total)
	names := make([]string, 0, len(m.sections))
	for k := range m.sections {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool { return m.sections[names[i]] > m.sections[names[j]] })
	for _, k := range names {
		fmt.Fprintf(w, "  %12d  %5.1f%%  %s\n", m.sections[k], pct(m.sections[k]), k)
	}
	if len(a.collisions) > 0 {
		keys := make([]string, 0, len(a.collisions))
		for k := range a.collisions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintln(w, "\npaths that sanitize alike (their functions are reported as <ambiguous ...>)")
		for _, k := range keys {
			fmt.Fprintf(w, "  %s: %s\n", k, strings.Join(a.collisions[k], ", "))
		}
	}
	unit := "package"
	if byModule {
		unit = "module"
	}
	code := m.sections["code"]
	fmt.Fprintf(w, "\ncode by %s (%% of all bytes, %% of code)\n", unit)
	shown := 0
	for _, r := range tally(m, a, byModule) {
		if match != "" && !strings.Contains(r.key, match) {
			continue
		}
		if top > 0 && shown == top {
			break
		}
		fmt.Fprintf(w, "  %12d  %5.1f%%  %5.1f%%  %6d funcs  %s\n", r.bytes, pct(r.bytes), 100*float64(r.bytes)/float64(max(code, 1)), r.funcs, r.key)
		shown++
	}
}
