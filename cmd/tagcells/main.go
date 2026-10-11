// tagcells keeps the version a page states for each module equal to the
// module's latest release.
//
//	tagcells -org go-widgets content/*/modules.md hugo.toml          # report
//	tagcells -org go-widgets -write content/*/modules.md hugo.toml   # rewrite
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
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

func main() {
	o, code, proceed := parseArgs(os.Args[1:], os.Stderr)
	if !proceed {
		os.Exit(code)
	}
	fleet.WarnIfStale(os.Stderr)
	os.Exit(run(o, os.Stdout, os.Stderr, latestTag))
}

type options struct {
	org   string
	write bool
	files []string
}

func parseArgs(args []string, stderr io.Writer) (options, int, bool) {
	fs := flag.NewFlagSet("tagcells", flag.ContinueOnError)
	fs.SetOutput(stderr)
	org := fs.String("org", "", "the GitHub organisation whose modules the pages list (required)")
	write := fs.Bool("write", false, "rewrite stale cells in place")
	switch err := fs.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		return options{}, 0, false
	case err != nil:
		return options{}, 2, false
	}
	if *org == "" || fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: tagcells -org ORG [-write] FILE...")
		return options{}, 2, false
	}
	return options{org: *org, write: *write, files: fs.Args()}, 0, true
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
			if out := Rewrite(src, cells, want); out != src {
				if err := os.WriteFile(f, []byte(out), 0o644); err != nil {
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
	case stale > 0 && !o.write:
		return 1
	}
	return 0
}

// latestTag asks git, not the REST API, for the repository's tags.
func latestTag(org, repo string) (string, error) {
	out, err := exec.Command("git", "ls-remote", "--tags", "--refs", "https://github.com/"+org+"/"+repo).Output()
	if err != nil {
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
