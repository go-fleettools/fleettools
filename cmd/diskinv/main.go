// Command diskinv inventories where a full disk went, and deletes nothing.
//
//	diskinv top <dir> [--depth N] [-n 30]   largest entries by ALLOCATED bytes
//	diskinv clones <dir> [-n 50]            size and unsaved work of each clone
//
// It exists because the questions "what is eating /private/var/folders" and
// "which of these clones can go" had no tool, and answering them with du, awk
// and a loop in a shell is the thing this fleet does not do: a pipeline cannot
// tell "no answer" from "wrong query". A directory it cannot read is LISTED,
// never counted as empty, and the status says the scan was incomplete.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Inventories disk use; it deletes nothing.

  diskinv top <dir> [--depth N] [-n 30]
      the largest entries by ALLOCATED bytes (st_blocks*512, which is what the
      volume gives back when they go, not the apparent size), N levels below
      <dir>, with the age of the newest mtime inside each. Symlinks are never
      followed, other filesystems are not entered (and are listed), a file with
      several hard links counts once. Whether a running process holds a
      directory open is NOT checked: an old age is not proof that nothing uses
      it, and a young one is proof that something wrote to it.

  diskinv clones <dir> [-n 50]
      for each entry directly under <dir>: size, branch, uncommitted changes,
      commits on no remote (git rev-list --branches --not --remotes), stashes,
      linked-worktree status and the last commit date. Entries that are not
      git repositories are listed too, so the total is the whole directory.

Exit status: 0 complete, 1 INCOMPLETE (a path or a git question could not be
answered; it is printed, never counted as zero), 2 refused to run.`

// now is a seam for the ages.
var now = time.Now

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	root := cli.New("diskinv <top|clones> <dir> [flags]",
		"Inventory where the disk went (deletes nothing)", longHelp)
	root.Args = cobra.ArbitraryArgs
	root.RunE = func(*cobra.Command, []string) error {
		return cli.Unanswerable("diskinv: give a subcommand, top or clones")
	}
	root.AddCommand(topCmd(), clonesCmd())
	return root
}

func topCmd() *cobra.Command {
	var depth, n int
	c := cli.New("top <dir> [--depth N] [-n 30]", "Largest entries by allocated bytes", longHelp)
	c.Args = cobra.ArbitraryArgs
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return cli.Unanswerable("diskinv top: exactly one directory is required")
		}
		if depth < 1 || n < 1 {
			return cli.Unanswerable("diskinv top: --depth and -n must be at least 1")
		}
		return runTop(cli.Out(cmd), args[0], depth, n)
	}
	c.Flags().IntVar(&depth, "depth", 1, "levels below <dir> at which entries are reported")
	c.Flags().IntVarP(&n, "top", "n", 30, "how many entries to print")
	return c
}

func runTop(out io.Writer, dir string, depth, n int) error {
	dir, err := resolveRoot(dir)
	if err != nil {
		return cli.Unanswerable("diskinv top: %v", err)
	}
	s := newScan()
	var rows []row
	total, newest, err := s.run(dir, depth, &rows)
	if err != nil {
		return cli.Unanswerable("diskinv top: %v", err)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].bytes > rows[j].bytes })
	t := now()
	fmt.Fprintf(out, "diskinv top %s  depth %d  (allocated bytes; symlinks not followed; one filesystem)\n", dir, depth)
	fmt.Fprintf(out, "%12s  %8s  %s\n", "SIZE", "AGE", "PATH")
	shown := rows
	if len(shown) > n {
		shown = shown[:n]
	}
	var shownBytes int64
	for _, r := range shown {
		shownBytes += r.bytes
		fmt.Fprintf(out, "%12s  %8s  %s\n", human(r.bytes), age(t, r.newest), r.path)
	}
	fmt.Fprintf(out, "\nshown %d of %d entries = %s of %s allocated under %s (newest mtime anywhere: %s ago)\n",
		len(shown), len(rows), human(shownBytes), human(total), dir, age(t, newest))
	fmt.Fprintf(out, "scanned %d files, %d directories\n", s.files, s.dirs)
	if len(s.otherFS) > 0 {
		fmt.Fprintf(out, "NOT entered (another filesystem): %d\n", len(s.otherFS))
		for _, p := range s.otherFS {
			fmt.Fprintf(out, "  %s\n", p)
		}
	}
	fmt.Fprintln(out, "note: whether a running process holds any of these open was NOT checked")
	if s.unreadable > 0 {
		fmt.Fprintf(out, "UNREADABLE: %d paths were not read and are NOT in the totals above\n", s.unreadable)
		for _, e := range s.errs {
			fmt.Fprintf(out, "  %s\n", e)
		}
		if int64(len(s.errs)) < s.unreadable {
			fmt.Fprintf(out, "  ... %d more not listed\n", s.unreadable-int64(len(s.errs)))
		}
		return cli.FromStatus(1)
	}
	fmt.Fprintln(out, "unreadable: 0")
	return nil
}
