// docsym reports the Go identifiers a module's README names that its code
// does not have.
//
//	docsym DIR...                      # each DIR a module checkout
//	docsym --goos linux,darwin DIR...  # the platforms to type-check under
//	docsym --fail DIR...               # exit 1 when anything is missing
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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Reports the Go identifiers a module's README names that its code does not have.

It reads inline code spans (` + "`toolkit.NewButton`, `Entry.Text`" + `) and go
fences, and type-checks the module — and the packages it requires from its own
organisation — under each platform asked for, so a name that exists only in one
platform's build is found.

Each reference ends as found, MISSING, not ours (the qualifier is a variable or
another organisation's package: skipped and counted), or unreadable (the package
failed to type-check, so a miss there would be a guess: counted, never reported
as missing).

Exit status: 0 nothing missing, 1 something is missing and --fail was given,
2 a module could not be loaded at all — a scan that read nothing must not look
like a clean one.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

type options struct {
	goos []string
	fail bool
	bare bool
	dirs []string
}

// RootCmd builds the command. Tests drive it through parseArgs, below.
func RootCmd() *cobra.Command {
	var (
		o    options
		goos string
	)
	c := command(&o, &goos)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if err := settle(&o, goos, args); err != nil {
			return err
		}
		// run keeps its body and its three statuses, each documented against
		// the wrong answer it came from.
		return cli.FromStatus(run(o, cli.Out(cmd), cli.Err(cmd), Load))
	}
	return c
}

func command(o *options, goos *string) *cobra.Command {
	c := cli.New("docsym [flags] DIR...",
		"Identifiers a README names that the code does not have", longHelp)
	f := c.Flags()
	f.StringVar(goos, "goos", "linux,darwin,windows,js", "comma-separated GOOS values to type-check under")
	f.BoolVar(&o.fail, "fail", false, "exit 1 when a README names something the code does not have")
	f.BoolVar(&o.bare, "bare", false,
		"also list bare names in code spans that no loaded package has "+
			"(mostly platform and protocol names; never fails the run)")
	return c
}

// settle turns the flags into the options run takes, and refuses the two inputs
// that would make the measurement meaningless: no directory to read, and no
// platform to read it under.
func settle(o *options, goos string, args []string) error {
	if len(args) == 0 {
		return cli.Unanswerable("usage: docsym [--goos list] [--fail] DIR...")
	}
	o.goos = nil
	for _, g := range strings.Split(goos, ",") {
		if g = strings.TrimSpace(g); g != "" {
			o.goos = append(o.goos, g)
		}
	}
	if len(o.goos) == 0 {
		return cli.Unanswerable("--goos names no platform")
	}
	o.dirs = args
	return nil
}

// parseArgs keeps the signature this command's tests were written against.
func parseArgs(args []string, stderr io.Writer) (options, int, bool) {
	var (
		o    options
		goos string
	)
	c := command(&o, &goos)
	c.RunE = func(_ *cobra.Command, rest []string) error { return settle(&o, goos, rest) }
	code := cli.Drive(c, args, stderr, stderr)
	// --help exits 0 and is not a reason to proceed; cobra returns nil for it.
	return o, code, code == 0 && !cli.HelpRequested(c)
}

func run(o options, stdout, stderr io.Writer, load func(string, []string) (*Index, error)) int {
	var missing, found, notOurs, unreadable, unconfirmed, exempt, modules, failed int
	for _, dir := range o.dirs {
		readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
		if err != nil {
			fmt.Fprintf(stderr, "docsym: %s: %v\n", dir, err)
			failed++
			continue
		}
		refs := Extract(string(readme))
		allowed := Allowed(string(readme))
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
				name := r.Name
				if r.Pkg != "" {
					name = r.Pkg + "." + name
				}
				if r.Member != "" {
					name += "." + r.Member
				}
				if _, ok := allowed[name]; ok {
					exempt++
					continue
				}
				missing++
				fmt.Fprintf(stdout, "%s:%d  `%s`: %s is not in %s (%s)\n",
					filepath.Join(dir, "README.md"), r.Line, r.Text, name, where, strings.Join(o.goos, ", "))
			}
		}
	}
	fmt.Fprintf(stdout, "\n%d module(s) read, %d unreadable: %d references found, %d MISSING, %d allowed by a docsym:allow with its reason, %d bare names unconfirmed (-bare lists them), %d not ours (skipped), %d in a package that did not type-check (not judged)\n",
		modules, failed, found, missing, exempt, unconfirmed, notOurs, unreadable)
	switch {
	case failed > 0 || modules == 0:
		return 2
	case o.fail && missing > 0:
		return 1
	}
	return 0
}
