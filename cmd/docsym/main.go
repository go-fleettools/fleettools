// docsym reports the Go identifiers a module's README names that its code
// does not have.
//
//	docsym DIR...                     # each DIR a module checkout
//	docsym -goos linux,darwin DIR...  # the platforms to type-check under
//	docsym -fail DIR...               # exit 1 when anything is missing
//
// A README rots in a few ways, and this catches the commonest one a program
// can: a NAME that was renamed or removed. It reads inline code spans
// (`toolkit.NewButton`, `LoadGTKTheme(css)`, `Entry.Text`) and ```go fences,
// and type-checks the module -- and the packages it requires from its own
// organisation -- under each platform asked for, so a name that exists only in
// window's linux build is found.
//
// What it does NOT catch, and was found by reading on 2026-10-10 in go-widgets:
// a wrong count ("a 5-primitive interface" over seven methods), a wrong
// signature (`wg.Draw(surface, width, theme)` after the toolkit moved to a
// Painter), a claim about the import graph ("toolkit never imports mvvm").
// Those need a person. This is the part that does not.
//
// Each reference ends as found, MISSING (no build of the package has it),
// not ours (the qualifier is a variable or another organisation's package --
// skipped, and counted), or unreadable (the package failed to type-check
// somewhere, so a miss there would be a guess -- counted, never reported as
// missing). A module that cannot be loaded at all fails the run: a scan that
// read nothing must not look like a clean one.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

func main() {
	opts, code, proceed := parseArgs(os.Args[1:], os.Stderr)
	if !proceed {
		os.Exit(code)
	}
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(opts, os.Stdout, os.Stderr, Load))
}

type options struct {
	goos []string
	fail bool
	bare bool
	dirs []string
}

func parseArgs(args []string, stderr io.Writer) (options, int, bool) {
	fs := flag.NewFlagSet("docsym", flag.ContinueOnError)
	fs.SetOutput(stderr)
	goos := fs.String("goos", "linux,darwin,windows,js", "comma-separated GOOS values to type-check under")
	fail := fs.Bool("fail", false, "exit 1 when a README names something the code does not have")
	bare := fs.Bool("bare", false, "also list bare names in code spans that no loaded package has (mostly platform and protocol names; never fails the run)")
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		return options{}, 0, false
	case err != nil:
		return options{}, 2, false
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: docsym [-goos list] [-fail] DIR...")
		return options{}, 2, false
	}
	var gs []string
	for _, g := range strings.Split(*goos, ",") {
		if g = strings.TrimSpace(g); g != "" {
			gs = append(gs, g)
		}
	}
	if len(gs) == 0 {
		fmt.Fprintln(stderr, "docsym: -goos names no platform")
		return options{}, 2, false
	}
	return options{goos: gs, fail: *fail, bare: *bare, dirs: fs.Args()}, 0, true
}

func run(o options, stdout, stderr io.Writer, load func(string, []string) (*Index, error)) int {
	var missing, found, notOurs, unreadable, unconfirmed, modules, failed int
	for _, dir := range o.dirs {
		readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			fmt.Fprintf(stderr, "docsym: %s: %v\n", dir, err)
			failed++
			continue
		}
		refs := Extract(string(readme))
		ix, err := load(dir, o.goos)
		if err != nil {
			fmt.Fprintf(stderr, "docsym: %s: %v\n", dir, err)
			failed++
			continue
		}
		modules++
		for _, r := range refs {
			v, where := ix.Check(r)
			switch v {
			case Found:
				found++
			case NotOurs:
				notOurs++
			case Unreadable:
				unreadable++
			case Unconfirmed:
				unconfirmed++
				if o.bare {
					fmt.Fprintf(stdout, "%s:%d  `%s`: unconfirmed (no loaded package has it)\n",
						filepath.Join(dir, "README.md"), r.Line, r.Text)
				}
			case Missing:
				missing++
				name := r.Name
				if r.Pkg != "" {
					name = r.Pkg + "." + name
				}
				if r.Member != "" {
					name += "." + r.Member
				}
				fmt.Fprintf(stdout, "%s:%d  `%s`: %s is not in %s (%s)\n",
					filepath.Join(dir, "README.md"), r.Line, r.Text, name, where, strings.Join(o.goos, ", "))
			}
		}
	}
	fmt.Fprintf(stdout, "\n%d module(s) read, %d unreadable: %d references found, %d MISSING, %d bare names unconfirmed (-bare lists them), %d not ours (skipped), %d in a package that did not type-check (not judged)\n",
		modules, failed, found, missing, unconfirmed, notOurs, unreadable)
	switch {
	case failed > 0 || modules == 0:
		return 2
	case o.fail && missing > 0:
		return 1
	}
	return 0
}
