// tagcells keeps the version a page states for each module equal to the
// module's latest release.
//
//	tagcells --org go-widgets content/*/modules.md hugo.toml           # report
//	tagcells --org go-widgets --write content/*/modules.md hugo.toml   # rewrite
//
// It reads two shapes: a Markdown table row linking the repository with the
// tag in the next cell (go-widgets/docs' modules page, in every language), and
// a landing page's [[params.modules]] block in hugo.toml (name = ..., tag = ...).
// A row linking another organisation's repository is left alone.
//
// The latest release is the highest vX.Y.Z tag with no prerelease -- what
// `go get module@latest` resolves -- read with `git ls-remote`, one call per
// module and none against the REST budget Renovate shares.
//
// Written 2026-10-11, after the same five tag cells had been edited by hand in
// four languages and a hugo.toml five times in two days -- the first four with
// perl one-liners, which is how a cell in one language gets missed and nothing
// says so. Exit status: 0 every cell current (or rewritten), 1 a cell is stale
// and -write was not given, 2 a tag could not be read or no cell was found at
// all -- a page with no cells is not an up-to-date page.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const longHelp = `Keeps the version a page states for each module equal to the module's latest
release.

It reads two shapes: a Markdown table row linking the repository with the tag in
the next cell, and a landing page's [[params.modules]] block in hugo.toml. A row
linking another organisation's repository is left alone.

The latest release is the highest vX.Y.Z tag with no prerelease — what
` + "`go get module@latest`" + ` resolves — read with ` + "`git ls-remote`" + `,
one call per module and none against the REST budget Renovate shares.

Exit status: 0 every cell current (or rewritten), 1 a cell is stale and --write
was not given, 2 a tag could not be read or no cell was found at all — a page
with no cells is not an up-to-date page.`

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

type options struct {
	org   string
	write bool
	files []string
	today string // YYYY-MM-DD written into a rewritten claim's date; empty = now

	// minCells is how many cells the caller knows these pages hold.
	//
	// ⛔ WITHOUT IT, A PAGE THAT LOSES ITS CLAIM GOES QUIET INSTEAD OF RED.
	// Exit 2 fires when a run reads NO cell at all, which is the right answer
	// for one file and almost no answer for thirteen: a sentence reworded until
	// the claim no longer matches leaves ten other pages reporting cheerfully,
	// and the page nobody checks any more looks exactly like a page with nothing
	// to check. That is how the eleven claims on the documentation site went
	// unread from 2026-10-11 to 2026-11-22 -- not because anything was red, but
	// because the count nobody stated could not fall.
	//
	// A caller that states the number turns a lost claim into a refusal, and a
	// deliberate removal into a one-line change in the same pull request that
	// removes the page. It defaults to 0, which is exactly today's rule.
	minCells int
}

// writeAtomic replaces path without ever leaving it truncated: the new content
// is written to a temporary file in the SAME directory (so the rename stays on
// one filesystem), given the original's mode, and renamed over it. A kill at any
// point leaves the old page or the new one, never an empty one.
// tempFile is the part of *os.File that writeAtomic uses.
//
// ⛔ IT EXISTS SO THAT EACH FAILURE BELOW CAN BE MADE TO HAPPEN. Every one of
// them was held by nothing until 2026-11-22 (mutation sweep: five survivors in
// this function), and they are the only paths that decide whether a page is
// left whole: a filesystem does not run out of space, refuse a chmod or fail a
// close on request, and the branch that is never executed is the one that
// deletes the half-written file. The seam is the smallest that lets a test
// choose the step that fails.
type tempFile interface {
	io.Writer
	Chmod(os.FileMode) error
	Close() error
	Name() string
}

// newTempFile is replaced in tests. It must create a REAL file: the point of
// the tests below is what is left on disk afterwards.
var newTempFile = func(dir, pattern string) (tempFile, error) {
	return os.CreateTemp(dir, pattern)
}

func writeAtomic(path string, data []byte) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := newTempFile(filepath.Dir(path), ".tagcells-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(st.Mode().Perm()); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// RootCmd builds the command. Tests drive it through parseArgs, below.
func RootCmd() *cobra.Command {
	var o options
	c := command(&o)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if err := validate(&o, args); err != nil {
			return err
		}
		return cli.FromStatus(run(o, cli.Out(cmd), cli.Err(cmd), latestTag))
	}
	return c
}

func command(o *options) *cobra.Command {
	c := cli.New("tagcells --org ORG [--write] FILE...",
		"Keep each page's stated version equal to the module's latest release", longHelp)
	f := c.Flags()
	f.StringVar(&o.org, "org", "", "the GitHub organisation whose modules the pages list (required)")
	f.BoolVar(&o.write, "write", false, "rewrite stale cells in place")
	f.IntVar(&o.minCells, "min-cells", 0,
		"refuse with exit 2 if fewer than N cells were read in all (0: only a run that read none at all)")
	return c
}

func validate(o *options, args []string) error {
	if o.org == "" || len(args) == 0 {
		return cli.Unanswerable("usage: tagcells --org ORG [--write] FILE...")
	}
	o.files = args
	return nil
}

// parseArgs keeps the signature this command's tests were written against.
func parseArgs(args []string, stderr io.Writer) (options, int, bool) {
	var o options
	c := command(&o)
	c.RunE = func(_ *cobra.Command, rest []string) error { return validate(&o, rest) }
	code := cli.Drive(c, args, stderr, stderr)
	return o, code, code == 0 && !cli.HelpRequested(c)
}

func run(o options, stdout, stderr io.Writer, latest func(org, repo string) (string, error)) int {
	known := map[string]string{}
	var unread []string
	lookup := func(repo string) string {
		if t, ok := known[repo]; ok {
			return t
		}
		t, err := latest(o.org, repo)
		if err != nil {
			unread = append(unread, repo+": "+err.Error())
		}
		known[repo] = t
		return t
	}

	today := o.today
	if today == "" {
		today = time.Now().Format("2006-01-02")
	}
	cellsSeen, stale := 0, 0
	for _, f := range o.files {
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(stderr, "tagcells:", err)
			return 2
		}
		src := string(b)
		cells := Find(src, o.org, strings.HasSuffix(f, ".toml"))
		cellsSeen += len(cells)
		if len(cells) == 0 {
			// Named, not counted away: this file is one this command cannot
			// speak for, and that is a different thing from a file it checked.
			fmt.Fprintf(stdout, "%s  no version claim of %s found on this page\n", f, o.org)
		}
		want := map[string]string{}
		for _, c := range cells {
			t := lookup(c.Module)
			want[c.Module] = t
			if t != "" && t != c.Tag {
				stale++
				verb := "stale"
				if o.write {
					verb = "rewritten"
				}
				fmt.Fprintf(stdout, "%s:%d  %s: page says %s, latest is %s (%s)\n", f, c.Line, c.Module, c.Tag, t, verb)
			}
		}
		if o.write {
			if out := Rewrite(src, cells, want, today); out != src {
				if err := writeAtomic(f, []byte(out)); err != nil {
					fmt.Fprintln(stderr, "tagcells:", err)
					return 2
				}
			}
		}
	}
	for _, u := range unread {
		fmt.Fprintln(stderr, "tagcells: could not read the tags of", u)
	}
	fmt.Fprintf(stdout, "\n%d file(s), %d cell(s), %d module(s), %d stale, %d unread\n",
		len(o.files), cellsSeen, len(known), stale, len(unread))
	switch {
	case cellsSeen == 0 || len(unread) > 0:
		return 2
	case cellsSeen < o.minCells:
		fmt.Fprintf(stderr, "tagcells: %d cell(s) read, --min-cells says %d: a claim these pages used to state is gone,"+
			" or was reworded past the shapes this command reads. The lines above name every page that holds none.\n",
			cellsSeen, o.minCells)
		return 2
	case stale > 0 && !o.write:
		return 1
	}
	return 0
}

// lsRemote is the one network call this command makes, replaced in tests. It is
// a variable because the guard below cannot otherwise be exercised without
// going out to GitHub, and a test whose only shield from the network is the
// guard it is testing is no test.
var lsRemote = func(url string) ([]byte, error) {
	return exec.Command("git", "ls-remote", "--tags", "--refs", url).Output()
}

// latestTag asks git, not the REST API, for the repository's tags.
func latestTag(org, repo string) (string, error) {
	out, err := lsRemote("https://github.com/" + org + "/" + repo)
	if err != nil {
		// ⛔ "I COULD NOT ASK" IS NOT "THERE IS NO RELEASE". Without this
		// branch a failed call returns no output, highest("") says "no release
		// tag", and every module reads as unreleased the moment the network is
		// down -- a page would then be reported current because nothing could
		// contradict it. The two answers travel apart: this one is counted as
		// unread, which is exit 2.
		return "", fmt.Errorf("git ls-remote: %w", err)
	}
	return highest(string(out))
}

// highest picks the highest release tag from ls-remote output: vX.Y.Z only,
// no prerelease, no build, compared as semver and never as text (v0.10.0 is
// above v0.9.0). A module with no release tag at all is an error, not "".
func highest(lsRemote string) (string, error) {
	best := ""
	for _, line := range strings.Split(lsRemote, "\n") {
		i := strings.Index(line, "refs/tags/")
		if i < 0 {
			continue
		}
		t := strings.TrimSpace(line[i+len("refs/tags/"):])
		if !semver.IsValid(t) || semver.Prerelease(t) != "" || semver.Build(t) != "" || semver.Canonical(t) != t {
			continue
		}
		if best == "" || semver.Compare(t, best) > 0 {
			best = t
		}
	}
	if best == "" {
		return "", errors.New("no vX.Y.Z release tag")
	}
	return best, nil
}
