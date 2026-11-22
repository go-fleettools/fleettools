// Command secretscan finds the daemons that take a secret as a plaintext
// value, in a config field, a flag, an environment variable or a unit file,
// instead of by reference.
//
//	secretscan sweep --json out.json --md out.md      # every clone under the root
//	secretscan repo --waive "<reason>" ./some-clone   # the gate for one repo
//
// # WHY IT EXISTS
//
// The fleet's rule is that a daemon receives a secret by REFERENCE
// (a go-authn/secretsource ref such as `systemd-creds:name`, `keychain:`,
// `keepassxc:`, or a `file:` path whose permissions are checked), never as the
// value. On 2026-11-22 the first inventory of how far the fleet is from that
// failed because no tool did the sweep: credscan reads git remote URLs and
// nothing else. This is that tool.
//
// # WHAT AN ANSWER IS WORTH
//
// A sweep that could not read reports zero. So the report opens with its
// DENOMINATOR (clones seen, read, skipped, files read, files unread), every
// clone carries how stale it is (FETCH_HEAD age, ahead/behind from the LOCAL
// refs, never a fetch), and the exit status is 2 when anything was skipped or
// unreadable: an unread clone is NOT READ, never clean.
//
// # PRECISION OVER RECALL
//
// Names are judged by words, not substrings, and `token_file`, `password_from`,
// `max_tokens`, `key_file` are not plaintext secrets; see internal/secretscan.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/secretscan"
	"github.com/spf13/cobra"
)

var (
	now = time.Now
	git = secretscan.ExecGit
)

const minReason = 12

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

const longHelp = `secretscan reports where a daemon takes a secret (password, token, API key,
client secret, passphrase, private key) as a plaintext value instead of by
reference, and gates one repository on it.

  sweep   read every clone under --root; JSON and a Markdown table; exit 2 when
          anything was skipped or unreadable (NOT READ is never clean)
  repo    the gate for one clone; exit 1 on a plaintext secret input of a
          daemon, 2 when a file could not be read

A plaintext input is classified as: plaintext (the value itself), path (a
*_file path; whether permissions are checked is a heuristic), ref (a *_from
secretsource reference), env (the name of a variable that holds it).`

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	root := cli.New("secretscan {sweep|repo} ...", "Find daemons taking a plaintext secret", longHelp)
	root.RunE = func(*cobra.Command, []string) error {
		return cli.Unanswerable("a subcommand is required: sweep or repo")
	}
	root.AddCommand(sweepCmd(), repoCmd())
	return root
}

func defaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Documents", "VCS", "GIT", "localhost")
}

func sweepCmd() *cobra.Command {
	var root, jsonOut, mdOut string
	var staleAfter time.Duration
	c := cli.New("sweep [flags]", "Read every clone under --root", "")
	c.Args = cobra.NoArgs
	c.Flags().SetInterspersed(true)
	c.Flags().StringVar(&root, "root", defaultRoot(), "directory holding the clones (<org>-<repo>[-topic])")
	c.Flags().StringVar(&jsonOut, "json", "", "write the JSON report to this file")
	c.Flags().StringVar(&mdOut, "md", "", "write the Markdown report to this file (default: standard output when --json is not given)")
	c.Flags().DurationVar(&staleAfter, "stale-after", 7*24*time.Hour, "a clone fetched longer ago than this is stale")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		rep, err := secretscan.Sweep(root, git, now(), staleAfter)
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		if jsonOut != "" {
			if err := writeFile(jsonOut, rep.WriteJSON); err != nil {
				return cli.Unanswerable("%v", err)
			}
		}
		switch {
		case mdOut != "":
			if err := writeFile(mdOut, rep.WriteMarkdown); err != nil {
				return cli.Unanswerable("%v", err)
			}
		case jsonOut == "":
			if err := rep.WriteMarkdown(cli.Out(cmd)); err != nil {
				return cli.Unanswerable("%v", err)
			}
		}
		d := rep.Denominator
		fmt.Fprintf(cli.Err(cmd), "secretscan: %d clones seen, %d read, %d skipped, %d files read, %d unread, %d stale (%d never fetched); %d daemon clones, %d with a plaintext secret\n",
			d.Seen, d.Read, d.Skipped, d.FilesRead, d.FilesUnread, d.Stale, d.NeverFetched, d.DaemonRepos, d.DaemonsPlaintext)
		if !rep.Complete() {
			return cli.Unanswerable("NOT READ: %d clone(s) skipped, %d file(s) unread, %d git answer(s) missing", d.Skipped, d.FilesUnread, d.GitUnreadable)
		}
		return nil
	}
	return c
}

func writeFile(path string, write func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func repoCmd() *cobra.Command {
	var waive string
	var all bool
	c := cli.New("repo [flags] CLONE", "Gate one clone on plaintext secret inputs", "")
	c.Args = cobra.ExactArgs(1)
	c.Flags().SetInterspersed(true)
	c.Flags().StringVar(&waive, "waive", "", "let the gate pass, stamping this reason (at least 12 characters) in its output")
	c.Flags().BoolVar(&all, "all", false, "gate a clone that is not a daemon too (default: only daemons)")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if c.Flags().Changed("waive") && len(strings.TrimSpace(waive)) < minReason {
			return cli.Unanswerable("--waive needs a reason of at least %d characters: a waiver that says nothing is a silent default", minReason)
		}
		repo, err := secretscan.ScanRepo(args[0], all)
		if err != nil {
			return cli.Unanswerable("%v", err)
		}
		out := cli.Out(cmd)
		if len(repo.Unread) > 0 {
			for _, u := range repo.Unread {
				fmt.Fprintf(out, "NOT READ %s: %s\n", u.File, u.Reason)
			}
			return cli.Unanswerable("%d file(s) could not be read; the gate has no verdict", len(repo.Unread))
		}
		if !repo.Daemon && !all {
			fmt.Fprintf(out, "ok: %s is not a daemon (%d Go files read); --all gates it anyway\n", args[0], repo.GoFiles)
			return nil
		}
		hits := repo.Plaintext()
		for _, f := range hits {
			fmt.Fprintf(out, "%s:%d: plaintext secret input `%s` (%s)\n", f.File, f.Line, f.Name, f.Source)
		}
		switch {
		case len(hits) == 0:
			fmt.Fprintf(out, "ok: no plaintext secret input in %s (%d files read)\n", args[0], repo.FilesRead)
			if waive != "" {
				fmt.Fprintln(out, "note: the waiver was not needed")
			}
			return nil
		case waive != "":
			fmt.Fprintf(out, "WAIVED %s %s: %d plaintext secret input(s): %s\n", now().Format(time.RFC3339), args[0], len(hits), strings.TrimSpace(waive))
			return nil
		}
		return cli.Found("%d plaintext secret input(s)", len(hits))
	}
	return c
}
