// Command clonescan says, for each local git repository under the directories
// it is given, whether deleting it would lose anything:
//
//	clonescan ~/src
//	clonescan --depth 3 --bundle ~/Backups/clones ~/src ~/work
//
// It is read-only. Each repository is reported SAFE or KEEP, with the reasons:
// uncommitted paths, stashes, branch commits that are on no remote and are not
// the head of a merged pull request, detached commits, the worktrees that hang
// off it, and how long ago its HEAD last moved.
//
// It replaces a throwaway program that swept stale clones on 2026-10-18 and
// 2026-10-20, and keeps what that program learned:
//
//   - ⛔ A SQUASH MERGE MAKES "UNPUSHED" MEANINGLESS. The commits of a branch
//     merged by squash are on no remote once GitHub deletes the branch, and
//     the work is safe. So a branch is safe when its tip is reachable from a
//     remote-tracking ref, OR when it is the head of a MERGED pull request
//     whose headRefOid equals the local tip. A tip that moved on after the
//     merge has commits nobody merged.
//   - ⛔ THREE "CLONES" WERE WORKTREES. Their `.git` is a FILE holding
//     `gitdir: …/.git/worktrees/<name>` (gitrepository-layout(5)), and their
//     metadata lives inside the main repository: deleting the main first
//     destroyed it. A worktree is reported under its main repository, never as
//     a clone of its own, and a main repository with a worktree hanging off it
//     is never SAFE.
//   - The age of the last HEAD movement shows whether a peer session is
//     working in a clone: a HEAD that moved within --recent is KEEP.
//
// There is no flag that deletes. Deleting stays a human act: this reads a
// repository at one instant, and a peer session can commit into it the next.
//
// Exit status: 0 every repository is SAFE; 1 at least one is KEEP (or a bundle
// failed its check); 2 a usage error or a repository that could not be read.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// errKeep is a scan that found something to keep: exit 1.
var errKeep = errors.New("at least one repository is KEEP")

const longHelp = `clonescan reports, for each git repository found under DIR (down to
--depth levels), whether deleting it would lose work: SAFE or KEEP, with the
reasons.

  - uncommitted paths (untracked files included), in every worktree;
  - stashes;
  - per local branch: commits on no remote-tracking ref, unless the tip is the
    head of a MERGED GitHub pull request (asked through gh), and commits made
    after such a merge;
  - detached HEAD commits on no branch and no remote;
  - linked worktrees, listed and judged under their main repository: a main
    repository with a worktree hanging off it is never SAFE, because its
    .git/worktrees holds the worktree's metadata;
  - how long ago HEAD last moved; within --recent it is KEEP, since something
    may be working in it.

It never writes into a repository (git runs with GIT_OPTIONAL_LOCKS=0).
--bundle DIR writes one verified git bundle of all local branches per
repository into DIR, which must be outside every git work tree. A bundle
holds commits only: not stashes, not uncommitted files.

There is no option to delete. Deleting stays a human act.

Exit status: 0 all SAFE; 1 at least one KEEP or a bundle that failed its
check; 2 a usage error or a repository that could not be read.`

// RootCmd is clonescan's command line.
func RootCmd() *cobra.Command {
	var (
		depth  int
		bundle string
		recent time.Duration
	)
	c := &cobra.Command{
		Use:           "clonescan [flags] DIR...",
		Short:         "Which local clones can be deleted without losing work?",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MinimumNArgs(1),
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if depth < 0 {
			return errors.New("--depth must not be negative")
		}
		if bundle != "" {
			abs, err := filepath.Abs(bundle)
			if err != nil {
				return err
			}
			if tree := insideWorkTree(abs); tree != "" {
				return fmt.Errorf("--bundle %s is inside the git work tree %s: a bundle there is one `git add -A` from being committed", abs, tree)
			}
			bundle = abs
		}
		s := scanner{recent: recent, now: time.Now()}
		repos, err := s.scan(args, depth)
		if err != nil {
			return err
		}
		if len(repos) == 0 {
			return fmt.Errorf("no git repository under %v (depth %d)", args, depth)
		}
		keep := false
		out := cmd.OutOrStdout()
		for _, r := range repos {
			if bundle != "" {
				r.bundle, r.bundleErr = writeBundle(r, bundle)
			}
			print(out, r)
			if !r.safe() || r.bundleErr != nil {
				keep = true
			}
		}
		if keep {
			return errKeep
		}
		return nil
	}
	c.Flags().IntVarP(&depth, "depth", "d", 2, "how many directory levels below each DIR to look for repositories")
	c.Flags().StringVarP(&bundle, "bundle", "b", "", "write a verified bundle of all local branches of each repository into this directory")
	c.Flags().DurationVar(&recent, "recent", time.Hour, "a HEAD that moved more recently than this is KEEP (0 to disable)")
	return c
}

func execute(c *cobra.Command, args []string) int {
	c.SetArgs(args)
	err := c.Execute()
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errKeep):
		return 1
	}
	fmt.Fprintf(c.ErrOrStderr(), "clonescan: %v\n", err)
	return 2
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	osExit(execute(RootCmd(), os.Args[1:]))
}

// print writes one repository's verdict and reasons.
func print(w io.Writer, r *repo) {
	verdict := "SAFE"
	if !r.safe() {
		verdict = "KEEP"
	}
	fmt.Fprintf(w, "%s %s\n", verdict, r.path)
	for _, n := range r.notes {
		fmt.Fprintf(w, "  %s\n", n)
	}
	for _, why := range r.reasons {
		fmt.Fprintf(w, "  KEEP: %s\n", why)
	}
	for _, wt := range r.worktrees {
		v := "SAFE"
		if len(wt.reasons) > 0 {
			v = "KEEP"
		}
		fmt.Fprintf(w, "  worktree %s %s (%s)\n", v, wt.path, wt.what)
		for _, n := range wt.notes {
			fmt.Fprintf(w, "    %s\n", n)
		}
		for _, why := range wt.reasons {
			fmt.Fprintf(w, "    KEEP: %s\n", why)
		}
	}
	switch {
	case r.bundleErr != nil:
		fmt.Fprintf(w, "  bundle FAILED: %v\n", r.bundleErr)
	case r.bundle != "":
		fmt.Fprintf(w, "  bundle %s (verified, heads match)\n", r.bundle)
	}
}

// insideWorkTree returns the work tree dir is in, or "": a .git, directory or
// file, in dir or any parent.
func insideWorkTree(dir string) string {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}
