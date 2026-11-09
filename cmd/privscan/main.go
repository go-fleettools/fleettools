// Command privscan finds, and fixes, a person's own paths, account name and
// document names in text that is about to be published — or has been.
//
//	privscan [flags] FILE...                 before sending
//	privscan --fix FILE...                   rewrite them
//	privscan --repo owner/name               after sending
//	privscan --org go-tex                    the whole fleet
//
// ⛔⛔ WHY IT EXISTS. On 2026-11-09 three issues were filed whose bodies each
// carried `/Users/<account>/Documents/<project>/<file>.tex` — a home directory,
// an account name, a directory layout and the name of an unpublished document.
// Nobody noticed until the user asked "did you leak my name", and the rule they
// then set is the one this enforces: no leak of a name, nor of a path or file
// name, on a user's file system.
//
// ⛔ Editing a body afterwards does not undo it. GitHub keeps the original under
// "edited" for anyone who opens the history, which is why this command is meant
// to run BEFORE the send — as a gate inside the program that sends — and why
// --fix refuses to touch a forge body: the remedy there is to delete and
// re-create, not to rewrite.
//
// ⛔ It reads issue and pull-request COMMENTS, not only bodies. A code search
// does not index comments reliably, so the first sweep done by searching found
// one leak and could not have found a second in a comment. The repository-wide
// comment endpoints cost one paginated call each, which is cheaper than one call
// per issue and reads everything.
//
// The findings do not reprint what they found unless --show asks: a refusal is
// printed where the body was going, and that is sometimes a public CI log.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/privpath"
	"github.com/spf13/cobra"
)

const longHelp = `Reports every place a text names a person's own file system: a home directory,
the account name inside it, and the project and document names along the path.
Each of the three is also hunted ON ITS OWN, away from the path that revealed
it, because a body can carry the path in one section and the bare name in a
session label further down.

    privscan body.md                       before you send it
    privscan --fix body.md                 rewrite it, in place
    privscan --repo go-tex/engine          what is already published
    privscan --org go-tex --org go-widgets a fleet sweep, comments included
    privscan --name bruno --repo o/r       hunt a name no path in the text shows

By default a finding prints its line, column and kind but NOT the text it
matched, because a refusal is printed where the body was going and that is
sometimes a public log. --show prints the match, for a local run.

--fix rewrites FILES only. A forge body cannot be fixed by rewriting: the edit
history keeps the original, so the remedy there is to delete and re-create.

The account of the user running the command is hunted by default, so that a body
naming it with no path in it is still caught; --no-self turns that off.

Exit status: 0 nothing found, 1 something found (or, with --fix, fixed), 2 the
question could not be put — an unreadable file, a failed API call.`

// ghAPI is the seam a test replaces.
var ghAPI = fleet.GH

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	var (
		o      options
		noSelf bool
	)
	c := cli.New("privscan [flags] FILE...",
		"Whether a text names a person's own paths, account or documents", longHelp)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		o.self = !noSelf
		return cli.FromStatus(scan(cli.Out(cmd), cli.Err(cmd), o, args))
	}
	f := c.Flags()
	f.BoolVar(&o.fix, "fix", false, "rewrite the files with every finding replaced")
	f.BoolVar(&o.show, "show", false, "print the matched text (a local run, never a log)")
	f.BoolVar(&noSelf, "no-self", false, "do not hunt the running account's own name")
	f.StringArrayVar(&o.names, "name", nil, "also hunt this name, with no path needed to learn it")
	f.StringArrayVar(&o.repos, "repo", nil, "scan this repository's issues, pulls and comments")
	f.StringArrayVar(&o.orgs, "org", nil, "scan every repository of this organisation")
	return c
}

func run(args []string, out, errOut io.Writer) int {
	return cli.Drive(RootCmd(), args, out, errOut)
}

type options struct {
	fix   bool
	show  bool
	self  bool
	names []string
	repos []string
	orgs  []string
}

// scan is the command's work.
func scan(out, errOut io.Writer, o options, files []string) int {
	if len(files) == 0 && len(o.repos) == 0 && len(o.orgs) == 0 {
		fmt.Fprintln(errOut, longHelp)
		return 2
	}
	// ⛔ --fix on a forge body is refused rather than attempted. Rewriting one
	// does not remove what it published: the original stays under "edited". A
	// tool that offered the fix would be telling the author the leak is undone.
	if o.fix && (len(o.repos) > 0 || len(o.orgs) > 0) {
		fmt.Fprintln(errOut, "privscan: --fix rewrites files only. A published body keeps its original under \"edited\", so rewriting it does not undo the disclosure — delete and re-create it instead (ghissue --delete).")
		return 2
	}

	known := o.names
	if o.self {
		if n := selfAccount(); n != "" {
			known = append(known, n)
		}
	}

	status := 0
	found, inputs := 0, 0

	for _, p := range files {
		inputs++
		n, code := scanFile(out, errOut, o, known, p)
		found += n
		status = worse(status, code)
	}

	repos := o.repos
	for _, org := range o.orgs {
		got, err := fleet.Repos(org)
		if err != nil {
			fmt.Fprintf(errOut, "privscan: listing %s: %v\n", org, err)
			status = worse(status, 2)
			continue
		}
		for _, r := range got {
			repos = append(repos, r.FullName)
		}
	}
	sort.Strings(repos)
	for _, r := range repos {
		inputs++
		n, code := scanRepo(out, errOut, o, known, r)
		found += n
		status = worse(status, code)
	}

	// ⛔ The summary says how many inputs were READ, not only what was found. A
	// sweep that could not read its population and reported zero findings reads
	// exactly like a clean fleet, and this repository has that defect on record.
	fmt.Fprintf(out, "\nprivscan: %d finding(s) across %d input(s)\n", found, inputs)
	if found > 0 {
		status = worse(status, 1)
	}
	return status
}

// worse keeps the most serious status seen: 2 (could not answer) outranks 1
// (found), which outranks 0. A sweep that failed halfway must not report the
// status of its clean half.
func worse(a, b int) int {
	if a == 2 || b == 2 {
		return 2
	}
	if a == 1 || b == 1 {
		return 1
	}
	return 0
}

// selfAccount is the account name of whoever is running the command, taken from
// their home directory.
//
// ⛔ It is hunted by default because the sweep's hardest case is a body that
// names the account with no path anywhere in it — a session label, a signature,
// a sentence. A scanner that can only learn names from paths calls such a body
// clean, which is most of what a sweep reads.
func selfAccount() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Base(h)
}

func scanFile(out, errOut io.Writer, o options, known []string, path string) (int, int) {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(errOut, "privscan: %v\n", err)
		return 0, 2
	}
	text := string(b)
	found := privpath.ScanKnown(text, known)
	if o.fix {
		fixed, n := privpath.RedactKnown(text, known)
		if n == 0 {
			fmt.Fprintf(out, "%s: clean\n", path)
			return 0, 0
		}
		if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
			fmt.Fprintf(errOut, "privscan: %v\n", err)
			return 0, 2
		}
		// ⛔ Checked after the write, against the names from BEFORE it: Scan
		// learns a name by reading it in a path, so re-scanning redacted text
		// cannot see a name whose path the fix just removed.
		if rest := privpath.ScanKnown(fixed, append(known, privpath.Names(text)...)); len(rest) > 0 {
			fmt.Fprintf(errOut, "privscan: %s still holds %d finding(s) after the fix\n", path, len(rest))
			return len(rest), 2
		}
		fmt.Fprintf(out, "%s: %d replacement(s)\n", path, n)
		return n, 0
	}
	report(out, o, path, found)
	return len(found), 0
}

// item is one piece of forge text.
type item struct {
	Ref  string
	Kind string
	Body string
}

func scanRepo(out, errOut io.Writer, o options, known []string, repo string) (int, int) {
	items, err := forgeText(repo)
	if err != nil {
		fmt.Fprintf(errOut, "privscan: %s: %v\n", repo, err)
		return 0, 2
	}
	found := 0
	for _, it := range items {
		f := privpath.ScanKnown(it.Body, known)
		if len(f) == 0 {
			continue
		}
		found += len(f)
		report(out, o, it.Ref+" ("+it.Kind+")", f)
	}
	if found == 0 {
		fmt.Fprintf(out, "%s: clean (%d texts read)\n", repo, len(items))
	}
	return found, 0
}

// forgeText reads every body and comment of a repository.
//
// ⛔ Three paginated calls, not one per issue: the repository-wide comment
// endpoints return every comment at once. A sweep that reads only issue bodies
// is blind to exactly the place a long thread puts a path, and a code search
// does not index comments reliably — which is why the first sweep of this leak
// could not have found a second occurrence in a comment.
func forgeText(repo string) ([]item, error) {
	var out []item
	for _, src := range []struct{ path, kind string }{
		{"repos/" + repo + "/issues?state=all&per_page=100", "issue or pull body"},
		{"repos/" + repo + "/issues/comments?per_page=100", "comment"},
		{"repos/" + repo + "/pulls/comments?per_page=100", "review comment"},
	} {
		raw, err := ghAPI("api", "-X", "GET", src.path, "--paginate")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", src.path, err)
		}
		got, err := decodeItems(raw, repo, src.kind)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// decodeItems reads gh's --paginate output, which concatenates one JSON array
// per page rather than merging them.
func decodeItems(raw []byte, repo, kind string) ([]item, error) {
	var out []item
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	for {
		var page []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			Body   string `json:"body"`
			URL    string `json:"html_url"`
			Issue  string `json:"issue_url"`
			PR     string `json:"pull_request_url"`
		}
		if err := dec.Decode(&page); err == io.EOF {
			return out, nil
		} else if err != nil {
			return out, fmt.Errorf("%s %s: %w", repo, kind, err)
		}
		for _, p := range page {
			ref := p.URL
			if p.Number != 0 {
				ref = fmt.Sprintf("%s#%d", repo, p.Number)
			}
			// A title is published text too, and the leak's own three issues
			// were caught in bodies only because nobody looked at the titles.
			if p.Title != "" {
				out = append(out, item{ref, kind + " title", p.Title})
			}
			out = append(out, item{ref, kind, p.Body})
		}
	}
}

func report(out io.Writer, o options, name string, found []privpath.Finding) {
	if len(found) == 0 {
		fmt.Fprintf(out, "%s: clean\n", name)
		return
	}
	fmt.Fprintf(out, "%s: %d finding(s)\n", name, len(found))
	for _, f := range found {
		if o.show {
			fmt.Fprintf(out, "  %s\n", f.Reveal())
		} else {
			fmt.Fprintf(out, "  %s\n", f.String())
		}
	}
}
