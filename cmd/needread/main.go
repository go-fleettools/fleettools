// Command needread prints the DT_NEEDED entries of every ELF under a tree,
// and refuses to report unless a symbol it was told to expect is actually
// there.
//
// It exists because "this package links no openssl" and "this program cannot
// read a DT_NEEDED" print identically, and the second gets read as the first.
//
// ⛔ THAT IS NOT HYPOTHETICAL. On 2026-10-28 a sweep of a 189-project pantry
// with go-pkgx/bk's `bk unlinked` produced 140 findings, of which ten were C++
// libraries reported as linking no C++ runtime. The finding was not surprising
// but IMPOSSIBLE, and that is the only reason anybody looked: the check had
// attributed `libstdc++.so.6` to whichever of two projects shipping a file of
// that name it walked last. A second reader, sharing no code with the first,
// is what settled it — and it was only worth trusting because its control
// passed.
//
//	needread -must libgcc_s.so.1 store/gnu.org/libiconv store/gnu.org/gcc
//
// prints each ELF with its NEEDED list and then, having found the control,
// says so. If nothing under the roots needs that soname it exits non-zero and
// says the measurement proved nothing, rather than printing an absence that
// looks like a finding.
//
// ⛔ CHOOSE THE CONTROL SO IT CAN FAIL FOR THE RIGHT REASON. The first run of
// this against libiconv used `-must libstdc++.so.6` and failed — correctly:
// nothing in a three-package closure consumes it, so the control said nothing
// about the reader. Re-run with `libgcc_s.so.1`, which libstdc++ itself needs,
// it passed and the absence below it became evidence. A control naming
// something the tree has no reason to contain only measures the fixture.
package main

import (
	"debug/elf"
	"fmt"
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

const longHelp = `Reads the DT_NEEDED entries of every ELF file under the roots given, so that
what a binary actually links against is read from the binary rather than from a
build file.

⛔ IT REFUSES AN ANSWER IT CANNOT STAND BEHIND, twice over. Reading no ELF file
at all is a broken measurement and not an empty answer. And --must names a
soname that something under the roots MUST need: without that positive control,
an absence above has not been shown to mean anything.

Exit status: 0 the files were read and the control held, 1 nothing was read or
the control failed, 2 a usage error.`

var osExit = os.Exit

func main() {
	fleet.WarnIfStale(os.Stderr)
	// This command's own osExit, kept: its tests replace it to observe the
	// status without ending the test binary, and cli.Drive returns the status
	// rather than exiting, so the two fit together.
	osExit(cli.Drive(RootCmd(), os.Args[1:], os.Stdout, os.Stderr))
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var (
		must  string
		quiet bool
	)
	c := cli.New("needread [flags] <dir|file>...",
		"What every ELF file under the roots actually NEEDs", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(read(cli.Out(cmd), cli.Err(cmd), must, quiet, args))
	}
	f := c.Flags()
	f.StringVar(&must, "must", "",
		"fail unless some file under the roots NEEDs this soname (the positive control)")
	f.BoolVar(&quiet, "quiet", false, "print only the totals and the control's verdict")
	return c
}

// run keeps the signature this command's tests were written against.
func run(args []string, stdout, stderr io.Writer) int {
	return cli.Drive(RootCmd(), args, stdout, stderr)
}

type found struct {
	path   string
	needed []string
}

func read(stdout, stderr io.Writer, must string, quiet bool, roots []string) int {
	if len(roots) == 0 {
		fmt.Fprintln(stderr, "usage: needread [--must soname] [--quiet] <dir|file>...")
		return 2
	}

	var all []found
	elves := 0
	controlSeen := false
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			needed, ok := neededOf(p)
			if !ok {
				return nil // not an ELF; silence is correct
			}
			elves++
			for _, n := range needed {
				if must != "" && n == must {
					controlSeen = true
				}
			}
			all = append(all, found{p, needed})
			return nil
		})
		if err != nil {
			fmt.Fprintln(stderr, "needread:", err)
			return 1
		}
	}

	sort.Slice(all, func(i, j int) bool { return all[i].path < all[j].path })
	if !quiet {
		for _, f := range all {
			fmt.Fprintf(stdout, "%s\n    NEEDED %s\n", f.path, strings.Join(f.needed, " "))
		}
		fmt.Fprintln(stdout)
	}
	fmt.Fprintf(stdout, "%d ELF file(s) read\n", elves)

	// ⛔ ZERO IS NOT AN ANSWER. A tree with no readable ELF reports the same
	// emptiness as a tree whose binaries link nothing, and the difference is
	// the whole value of the measurement.
	if elves == 0 {
		fmt.Fprintln(stderr, "needread: no ELF file was read at all — a broken measurement, not an empty answer")
		return 1
	}
	if must != "" && !controlSeen {
		fmt.Fprintf(stderr, "needread: positive control FAILED: nothing under the roots NEEDs %q, so this run has not shown it can see one\n", must)
		return 1
	}
	if must != "" {
		fmt.Fprintf(stdout, "positive control: some file here does NEED %s, so an absence above is an absence\n", must)
	}
	return 0
}

// neededOf reads one file's DT_NEEDED list, reporting whether it was an ELF at
// all. A file that is not an ELF and a file whose .dynamic cannot be read are
// both "nothing to say", never "needs nothing".
func neededOf(path string) ([]string, bool) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	needed, err := f.DynString(elf.DT_NEEDED)
	if err != nil {
		return nil, false
	}
	return needed, true
}
