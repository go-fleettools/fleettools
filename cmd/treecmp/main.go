// Command treecmp says whether two directory trees hold the same files, byte
// for byte:
//
//	treecmp GENERATED REFERENCE
//
// Every file under GENERATED must exist under REFERENCE with the same bytes;
// with --both, every file under REFERENCE must also exist under GENERATED. It
// answers "does this generator, run again, give back what is committed?" --
// the question a moved or regenerated brand kit raises.
//
// ⛔ A byte difference is not yet a defect. A PNG, a PDF or an ICO written by a
// newer library version can differ in every byte and hold the same picture;
// treecmp says WHICH files differ, and pngeq says whether their pixels do.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// failure is a comparison that found a difference (exit 1). Anything else
// out of the command is a usage or reading error (exit 2).
type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }

const longHelp = `treecmp compares two directory trees file by file, byte for byte.

Every file under GENERATED must exist under REFERENCE with the same bytes.
With --both, files present only under REFERENCE are reported too.

It prints one line per differing or missing file (up to --show), then the
counts. A tree with no file at all is exit 2: a comparison of nothing is not
a match.

Exits 0 when the trees agree, 1 when a file differs or is missing, 2 on a
usage or reading error.`

// Result is what one comparison found.
type Result struct {
	Same, Differ, Missing, Extra int
	Lines                        []string // one per finding, sorted
}

func RootCmd() *cobra.Command {
	var both bool
	var show int
	c := &cobra.Command{
		Use:           "treecmp GENERATED REFERENCE [flags]",
		Short:         "Do two directory trees hold the same files, byte for byte?",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(2),
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if show < 0 {
			return errors.New("--show must not be negative")
		}
		r, err := Compare(args[0], args[1], both)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		for i, l := range r.Lines {
			if i == show {
				fmt.Fprintf(out, "… and %d more\n", len(r.Lines)-show)
				break
			}
			fmt.Fprintln(out, l)
		}
		fmt.Fprintf(out, "identical %d, differing %d, missing %d", r.Same, r.Differ, r.Missing)
		if both {
			fmt.Fprintf(out, ", only in reference %d", r.Extra)
		}
		fmt.Fprintln(out)
		if r.Differ+r.Missing+r.Extra > 0 {
			return &failure{fmt.Errorf("the trees differ")}
		}
		return nil
	}
	c.Flags().BoolVarP(&both, "both", "b", false, "also report files present only under REFERENCE")
	c.Flags().IntVarP(&show, "show", "n", 20, "how many findings to print")
	return c
}

func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.ErrOrStderr(), "treecmp: %v\n", err)
	var f *failure
	if errors.As(err, &f) {
		return 1
	}
	return 2
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	osExit(execute(RootCmd()))
}

// files lists the regular files under root, as slash paths relative to it.
func files(root string) (map[string]bool, error) {
	out := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	return out, err
}

// Compare compares the two trees.
func Compare(gen, ref string, both bool) (Result, error) {
	var r Result
	g, err := files(gen)
	if err != nil {
		return r, err
	}
	if len(g) == 0 {
		return r, fmt.Errorf("%s holds no file: a comparison of nothing is not a match", gen)
	}
	f, err := files(ref)
	if err != nil {
		return r, err
	}
	for rel := range g {
		if !f[rel] {
			r.Missing++
			r.Lines = append(r.Lines, "MISSING "+rel)
			continue
		}
		same, err := sameBytes(filepath.Join(gen, rel), filepath.Join(ref, rel))
		if err != nil {
			return r, err
		}
		if same {
			r.Same++
		} else {
			r.Differ++
			r.Lines = append(r.Lines, "DIFFERS "+rel)
		}
	}
	if both {
		for rel := range f {
			if !g[rel] {
				r.Extra++
				r.Lines = append(r.Lines, "ONLY IN REFERENCE "+rel)
			}
		}
	}
	sort.Strings(r.Lines)
	return r, nil
}

// sameBytes compares two files in chunks, so a pair of large renders is not
// read whole into memory.
func sameBytes(a, b string) (bool, error) {
	fa, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer fb.Close()
	sa, err := fa.Stat()
	if err != nil {
		return false, err
	}
	sb, err := fb.Stat()
	if err != nil {
		return false, err
	}
	if sa.Size() != sb.Size() {
		return false, nil
	}
	ba, bb := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		na, ea := io.ReadFull(fa, ba)
		nb, eb := io.ReadFull(fb, bb)
		if !bytes.Equal(ba[:na], bb[:nb]) {
			return false, nil
		}
		aDone := ea == io.EOF || ea == io.ErrUnexpectedEOF
		bDone := eb == io.EOF || eb == io.ErrUnexpectedEOF
		if ea != nil && !aDone {
			return false, ea
		}
		if eb != nil && !bDone {
			return false, eb
		}
		if aDone || bDone {
			return aDone && bDone, nil
		}
	}
}
