// pullresidue finds the large files GitHub still serves only through pull
// request refs, writes the Support request that can remove them, and tells
// when they are gone.
//
//	pullresidue scan [--min 1MiB] <git-dir>
//	pullresidue letter --repo owner/name [--min 1MiB] [--open] [--token-file F] <git-dir>
//	pullresidue check --repo owner/name [--token-file F] <blob-id>...
//
// A build output committed by mistake and removed by the next commit leaves a
// main that a squash merge kept clean, and a blob that refs/pull/N/head still
// reaches. The owner cannot delete refs/pull/*; only GitHub Support can collect
// the blob, and Support has no API: its form is the only way in. go-quake1/
// engine#79 left a 14 MB binary that way on 2026-11-08.
//
// scan reads a --mirror clone (which fetches refs/pull/*) and lists every blob
// of at least --min that some pull ref reaches and no branch or tag does, with
// its path, the pull requests, and the commit that added it. A clone without
// pull refs, or one where nothing was read, is a question that could not be
// put (exit 2), never "nothing found".
//
// letter adds what GitHub says about each blob, with a HEAD request on the
// blobs API, so a 14 MB blob is not downloaded to learn it exists, and prints
// the request for the blobs still served. --open opens the form; sending it is
// the account owner's act.
//
// check answers, per blob, served or gone: run it after Support replies.
//
// The token is read from --token-file (default ~/.github-token), which must be
// readable by its owner alone, and goes in the Authorization header only.
//
// Exit status: 0 nothing to do (no residue, or every blob gone), 1 residue
// found or still served, 2 the question could not be put.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

const usage = `usage:
  pullresidue scan [--min 1MiB] <git-dir>
  pullresidue letter --repo owner/name [--min 1MiB] [--open] [--token-file F] <git-dir>
  pullresidue check --repo owner/name [--token-file F] <blob-id>...`

// newProberFn and openURL are replaced by tests.
var (
	newProberFn = newProber
	openURL     = func(url string) error {
		name := "xdg-open"
		if runtime.GOOS == "darwin" {
			name = "open"
		}
		return exec.Command(name, url).Run()
	}
)

// RootCmd is the command and its subcommands. Naming none is a usage error.
func RootCmd() *cobra.Command {
	c := cli.New("pullresidue", "Find blobs only pull-request refs reach, and ask GitHub Support to collect them", usage)
	c.AddCommand(scanCmd(), letterCmd(), checkCmd())
	c.RunE = func(*cobra.Command, []string) error { return cli.Unanswerable("%s", usage) }
	return c
}

func args1(_ *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%s", usage)
	}
	return nil
}

// scanned runs scan and turns an empty reading into an unanswerable question.
func scanned(dir, minFlag string) ([]residue, error) {
	min, err := parseSize(minFlag)
	if err != nil {
		return nil, cli.Unanswerable("%v", err)
	}
	rs, pulls, objects, err := scan(dir, min)
	if err != nil {
		return nil, cli.Wrap(err)
	}
	if pulls == 0 {
		return nil, cli.Unanswerable("%s holds no refs/pull/*: clone it with `git clone --mirror` to read them", dir)
	}
	_ = objects // zero objects is an answer: every pull ref is also on a branch or tag
	return rs, nil
}

func printResidues(w io.Writer, rs []residue) {
	for _, r := range rs {
		prs := make([]string, len(r.PRs))
		for i, n := range r.PRs {
			prs[i] = fmt.Sprintf("#%d", n)
		}
		path := "(no path)"
		if len(r.Paths) > 0 {
			path = strings.Join(r.Paths, ", ")
		}
		fmt.Fprintf(w, "%s  %10s  %s  PR %s  added %s\n", r.ID, human(r.Size), path, strings.Join(prs, ","), short(r.Added))
	}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "?"
	}
	return sha
}

func scanCmd() *cobra.Command {
	var minFlag string
	c := cli.New("scan <git-dir>", "List the blobs only pull-request refs reach", usage)
	c.Args = args1
	c.RunE = func(cmd *cobra.Command, args []string) error {
		rs, err := scanned(args[0], minFlag)
		if err != nil {
			return err
		}
		if len(rs) == 0 {
			fmt.Fprintf(cli.Out(cmd), "no blob of %s or more is reachable only through refs/pull/*\n", minFlag)
			return nil
		}
		printResidues(cli.Out(cmd), rs)
		return cli.Found("%d blob(s) reachable only through refs/pull/*", len(rs))
	}
	c.Flags().StringVar(&minFlag, "min", "1MiB", "report blobs of at least this size")
	return c
}

func letterCmd() *cobra.Command {
	var minFlag, repo, tokenFile string
	var open bool
	c := cli.New("letter --repo owner/name <git-dir>", "Write the Support request for the residue GitHub still serves", usage)
	c.Args = args1
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if !validRepo(repo) {
			return cli.Unanswerable("--repo %q: want owner/name", repo)
		}
		rs, err := scanned(args[0], minFlag)
		if err != nil {
			return err
		}
		p, err := newProberFn(tokenFile)
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		var live []residue
		for _, r := range rs {
			s, err := p.blob(context.Background(), repo, r.ID)
			if err != nil {
				return cli.Wrap(err)
			}
			fmt.Fprintf(cli.Err(cmd), "%s %s: %s\n", repo, short(r.ID), s)
			if s == servedYes {
				live = append(live, r)
			}
		}
		if len(live) == 0 {
			fmt.Fprintf(cli.Out(cmd), "%s: GitHub serves no blob that only refs/pull/* reaches; nothing to ask\n", repo)
			return nil
		}
		fmt.Fprint(cli.Out(cmd), letter(repo, live))
		if open {
			if err := openURL(supportURL); err != nil {
				fmt.Fprintf(cli.Err(cmd), "could not open %s: %v\n", supportURL, err)
			}
		} else {
			fmt.Fprintf(cli.Err(cmd), "file it at %s (no API exists; --open opens it)\n", supportURL)
		}
		return cli.Found("%d blob(s) of %s still served", len(live), repo)
	}
	f := c.Flags()
	f.StringVar(&minFlag, "min", "1MiB", "consider blobs of at least this size")
	f.StringVar(&repo, "repo", "", "the GitHub repository, owner/name")
	f.StringVar(&tokenFile, "token-file", "", "file holding the token (default ~/.github-token)")
	f.BoolVar(&open, "open", false, "open the Support form in the browser")
	return c
}

func checkCmd() *cobra.Command {
	var repo, tokenFile string
	c := cli.New("check --repo owner/name <blob-id>...", "Say whether GitHub still serves each blob", usage)
	c.Args = func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("%s", usage)
		}
		return nil
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if !validRepo(repo) {
			return cli.Unanswerable("--repo %q: want owner/name", repo)
		}
		p, err := newProberFn(tokenFile)
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		still := 0
		for _, id := range args {
			s, err := p.blob(context.Background(), repo, id)
			if err != nil {
				return cli.Wrap(err)
			}
			fmt.Fprintf(cli.Out(cmd), "%s  %s\n", id, s)
			if s == servedYes {
				still++
			}
		}
		if still > 0 {
			return cli.Found("%d of %d blob(s) still served", still, len(args))
		}
		return nil
	}
	f := c.Flags()
	f.StringVar(&repo, "repo", "", "the GitHub repository, owner/name")
	f.StringVar(&tokenFile, "token-file", "", "file holding the token (default ~/.github-token)")
	return c
}
