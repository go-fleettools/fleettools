package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/spf13/cobra"
)

// runGit is a seam; a test may replace it, main never does. A command that
// does not answer within gitTimeout is an unanswered question, not a zero.
var runGit = func(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	// GIT_OPTIONAL_LOCKS=0: an inventory must not refresh a repository's index
	// behind a session that is working in it.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(se.String()))
	}
	return so.String(), nil
}

const (
	gitTimeout = 2 * time.Minute
	workers    = 4
)

// clone is one entry directly under the inventoried directory.
type clone struct {
	name   string
	kind   string // repo, worktree, dir, file
	bytes  int64
	branch string
	dirty  string
	unpush string
	stash  string
	wt     string
	last   string
	errs   []string // questions that could not be answered
	scanN  int64    // unreadable paths during the size scan
}

func clonesCmd() *cobra.Command {
	var n int
	c := cli.New("clones <dir> [-n 50]", "Size and unsaved work of each clone under a directory", longHelp)
	c.Args = cobra.ExactArgs(1)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if n < 1 {
			return cli.Unanswerable("diskinv clones: -n must be at least 1")
		}
		return runClones(cli.Out(cmd), args[0], n)
	}
	c.Flags().SetInterspersed(true) // as in top: the usage line has the flags after <dir>
	c.Flags().IntVarP(&n, "top", "n", 50, "how many entries to print")
	return c
}

// resolveRoot follows a symlink named by the USER (/var -> /private/var) and
// refuses a path that is not a directory.
func resolveRoot(dir string) (string, error) {
	p, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if fi, _ := os.Stat(p); fi == nil || !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	return p, nil
}

func runClones(out io.Writer, dir string, n int) error {
	dir, err := resolveRoot(dir)
	if err != nil {
		return cli.Unanswerable("diskinv clones: %v", err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return cli.Unanswerable("diskinv clones: %v", err)
	}
	cs := make([]*clone, len(ents))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	for i, e := range ents {
		cs[i] = &clone{name: e.Name()}
		wg.Add(1)
		sem <- struct{}{}
		go func(c *clone, typ fs.FileMode) {
			defer wg.Done()
			defer func() { <-sem }()
			inspect(filepath.Join(dir, c.name), typ, c)
		}(cs[i], e.Type())
	}
	wg.Wait()

	sort.SliceStable(cs, func(i, j int) bool { return cs[i].bytes > cs[j].bytes })
	var total, shownBytes int64
	var repos, dirty, unpushedRepos, unknown, unreadable int
	for _, c := range cs {
		total += c.bytes
		unreadable += int(c.scanN)
		if c.kind == "repo" || c.kind == "worktree" {
			repos++
			if strings.HasPrefix(c.dirty, "yes") {
				dirty++
			}
			if c.unpush != "0" && !strings.HasPrefix(c.unpush, "?") {
				unpushedRepos++
			}
		}
		if len(c.errs) > 0 {
			unknown++
		}
	}
	fmt.Fprintf(out, "diskinv clones %s  (allocated bytes; deletes nothing)\n", dir)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SIZE\tKIND\tBRANCH\tDIRTY\tUNPUSHED\tSTASH\tWORKTREES\tLAST COMMIT\tNAME")
	shown := cs
	if len(shown) > n {
		shown = shown[:n]
	}
	for _, c := range shown {
		shownBytes += c.bytes
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			human(c.bytes), c.kind, c.branch, c.dirty, c.unpush, c.stash, c.wt, c.last, c.name)
	}
	_ = tw.Flush() // into the command's own writer, whose failure is the terminal's to report
	fmt.Fprintf(out, "\nshown %d of %d entries = %s of %s total\n", len(shown), len(cs), human(shownBytes), human(total))
	fmt.Fprintf(out, "git checkouts: %d, with uncommitted changes: %d, with commits on no remote: %d\n", repos, dirty, unpushedRepos)
	fmt.Fprintln(out, "UNPUSHED = git rev-list --branches --not --remotes --count; \"(no remote)\" means every commit counts")
	if unknown > 0 || unreadable > 0 {
		fmt.Fprintf(out, "INCOMPLETE: %d entries with an unanswered question, %d unreadable paths in size scans (not counted as zero)\n", unknown, unreadable)
		for _, c := range cs {
			for _, e := range c.errs {
				fmt.Fprintf(out, "  %s: %s\n", c.name, e)
			}
			if c.scanN > 0 {
				fmt.Fprintf(out, "  %s: %d unreadable paths in the size scan\n", c.name, c.scanN)
			}
		}
		return cli.FromStatus(1)
	}
	return nil
}

// inspect fills c for the entry at path, whose directory-entry type is typ.
// The size comes from the same scan as `top`: a symlink is counted as the link
// itself (its target is somebody else's bytes) and a path that cannot be
// examined is an unreadable path in the report, never a zero.
func inspect(path string, typ fs.FileMode, c *clone) {
	s := newScan()
	c.bytes, _ = s.run(path, 0, nil)
	c.scanN = s.unreadable
	c.errs = append(c.errs, s.errs...)
	switch {
	case typ&fs.ModeSymlink != 0:
		c.kind = "symlink"
	case typ.IsDir():
		c.kind = "dir"
	default:
		c.kind = "file"
	}
	c.branch, c.dirty, c.unpush, c.stash, c.wt, c.last = "-", "-", "-", "-", "-", "-"
	if c.kind == "dir" {
		inspectGit(path, c)
	}
}

// inspectGit asks git about a directory that may be a checkout.
func inspectGit(path string, c *clone) {
	gfi, err := os.Lstat(filepath.Join(path, ".git"))
	isDir := err == nil && gfi.IsDir()
	if err != nil {
		return // not a git checkout: a plain directory
	}
	c.kind = "repo"
	if !isDir {
		c.kind = "worktree"
		c.wt = linkedOf(filepath.Join(path, ".git"))
	}
	ask := func(what string, args ...string) string {
		o, err := runGit(path, args...)
		if err != nil {
			c.errs = append(c.errs, what+": "+err.Error())
			return "?"
		}
		return strings.TrimSpace(o)
	}
	if b := ask("branch", "branch", "--show-current"); b == "" {
		c.branch = "(detached)"
	} else {
		c.branch = b
	}
	if st := ask("status", "status", "--porcelain", "--untracked-files=normal"); st == "?" {
		c.dirty = "?"
	} else if st == "" {
		c.dirty = "no"
	} else {
		c.dirty = "yes(" + strconv.Itoa(strings.Count(st, "\n")+1) + ")"
	}
	c.unpush = ask("unpushed", "rev-list", "--branches", "--not", "--remotes", "--count")
	if c.unpush != "?" {
		if r, err := runGit(path, "remote"); err == nil && strings.TrimSpace(r) == "" {
			c.unpush += " (no remote)"
		}
	}
	if sl := ask("stash", "stash", "list"); sl == "?" {
		c.stash = "?"
	} else if sl == "" {
		c.stash = "0"
	} else {
		c.stash = strconv.Itoa(strings.Count(sl, "\n") + 1)
	}
	if l, err := runGit(path, "log", "-1", "--format=%cs"); err != nil {
		// An empty repository has no commit: that is an answer, not a failure.
		if _, herr := runGit(path, "rev-parse", "--verify", "-q", "HEAD"); herr != nil {
			c.last = "none"
		} else {
			c.errs = append(c.errs, "last commit: "+err.Error())
			c.last = "?"
		}
	} else {
		c.last = strings.TrimSpace(l)
	}
	if c.kind == "repo" {
		if w, err := runGit(path, "worktree", "list", "--porcelain"); err != nil {
			c.errs = append(c.errs, "worktrees: "+err.Error())
			c.wt = "?"
		} else if k := strings.Count(w, "worktree ") - 1; k > 0 {
			c.wt = fmt.Sprintf("+%d linked", k)
		}
	}
}

// linkedOf reads a worktree's .git file ("gitdir: <main>/.git/worktrees/<n>")
// and names the checkout it belongs to.
func linkedOf(gitfile string) string {
	b, err := os.ReadFile(gitfile)
	if err != nil {
		return "linked of ?"
	}
	g := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	// <main>/.git/worktrees/<name> -> <main>
	mainDir := filepath.Dir(filepath.Dir(filepath.Dir(g)))
	return "linked of " + filepath.Base(mainDir)
}
