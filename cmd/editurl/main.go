// editurl asks whether a Hextra site's "Edit this page on GitHub" link
// reaches the page's own source.
//
//	editurl                          # every checkout under the clone root
//	editurl --repo go-desktop/docs   # one of them
//	editurl --fail-on-broken         # exit 1 if any site's links do not resolve
//
// ⛔ WHY IT READS THE REPOSITORY AND NOT THE URL -- which is the opposite of
// pagescheck, deliberately. GitHub's /edit/ route AUTHENTICATES BEFORE IT
// RESOLVES THE PATH, so an anonymous request to an existing repository answers
// 302 to the login page whatever the path is. Measured 2026-11-15:
//
//	github.com/go-desktop/docs/edit/main/content/en/finding.md     302  (correct)
//	github.com/go-desktop/docs/edit/main/content/en/en/finding.md  302  (no such file)
//	github.com/go-desktop/edit/main/content/en/finding.md          404  (no such repository)
//
// A fetcher can only see the third. It would have scored eleven of the
// seventy-seven broken sites as healthy, and it would need a token to see even
// that much. The repository knows: the href names a path, and either that path
// is in the tree or it is not.
//
// ⛔ WHAT THE DEFECT LOOKS LIKE, and why nothing saw it for months
// (go-fleettools/fleettools#287, 77 of 100 docs sites). Hextra appends the
// page's path relative to content/ -- which on a multilingual site already
// starts with en/ -- so a base ending at content/en renders
// …/content/en/en/page.md. On top of that, 66 of those bases drop the
// repository segment and name github.com/<org>/edit/main/…, a repository
// called "edit" that does not exist. Both live in a .yaml, and the checks that
// read published pages walked .md, .html and .toml and never opened one
// (go-desktop/catalog#28 added .yaml to that walk, and go-desktop's own broken
// link surfaced on the first run).
//
// ⛔ A SIGNED-IN READER DOES NOT GET A 404 FOR THE DOUBLED FORM, which is
// worse than one. GitHub offers them its "create a new file" editor at a path
// the site never serves, so a correction typed there lands in a file nothing
// builds. Measured through the contents API, which is the oracle this tool
// uses locally:
//
//	repos/go-fleettools/docs/contents/content/en/en/repos/fleettools.md  404
//	repos/go-fleettools/docs/contents/content/en/repos/fleettools.md     200
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/go-fleettools/fleettools/internal/checkout"
	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(rootCmd())
}

// defaultBranch is the branch a checkout's remote serves by default, short
// name. A variable so a test can answer for a fixture tree.
var defaultBranch = func(dir string) string {
	ref, ok := checkout.DefaultRef(dir)
	if !ok {
		return ""
	}
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

func rootCmd() *cobra.Command {
	c := cli.New("editurl [flags]",
		"whether a Hextra site's edit-this-page link reaches the page's source",
		`editurl reads the checkouts under the clone root, derives the href the
Hextra theme renders for every content page, and checks that the path it names
is in the repository.

It makes no HTTP request and needs no token: GitHub's /edit/ route redirects an
anonymous caller to a login page before it resolves the path, so fetching the
href cannot tell a correct one from a broken one.

Every finding is re-derived against the remote's default ref after a fetch, so
a base fixed upstream is dropped rather than reported. The direction that is
NOT covered is the other one: a base broken upstream since a checkout was taken
is invisible here, which makes this understate.`)

	var (
		root      string
		only      string
		failOn    bool
		noConfirm bool
		all       bool
	)
	c.Flags().StringVar(&root, "root", checkout.DefaultRoot(), "the clone root to read")
	c.Flags().StringVar(&only, "repo", "", "read only this org/repo")
	c.Flags().BoolVar(&failOn, "fail-on-broken", false, "exit 1 if any site's edit links do not resolve")
	c.Flags().BoolVar(&noConfirm, "no-confirm", false, "do not re-derive findings on checkouts that are behind their remote")
	c.Flags().BoolVar(&all, "all", false, "list every site that renders an edit link, with its content directory and verdict")

	c.RunE = func(cmd *cobra.Command, _ []string) error {
		return run(cmd, root, only, failOn, noConfirm, all)
	}
	return c
}

// site is one checkout that renders an edit link.
type site struct {
	repo, dir, owner, name, branch string
	cfg                            Config
}

// result is one site, the configuration that was JUDGED, and the verdict.
//
// ⛔ The base and the verdict are one record because they were one judgement.
// Reading them from two places is how a row came to say that a base resolved
// while naming the base the site no longer has.
type result struct {
	site
	base    string // the base judged
	content string // the content directory judged
	at      string // the ref that configuration was read at, "" for the checkout's own tree
	problem *Problem
}

func run(cmd *cobra.Command, root, only string, failOn, noConfirm, all bool) error {
	repos, err := checkout.Repos(root, only)
	if err != nil {
		return cli.Wrap(err)
	}
	// ⛔ AN EMPTY POPULATION IS NOT A CLEAN RESULT. Pointed at the flat
	// <org>-<repo> working farm rather than the <org>/<repo> canonical one,
	// Repos finds nothing, and an empty table with a status of 0 would report
	// "every edit link resolves" when the truth is that nothing was read.
	if len(repos) == 0 {
		return cli.Unanswerable("no checkout under %s: nothing was read, which is not the same as nothing found", root)
	}

	var (
		sites      []site
		unreadable []string
		disabled   int
	)
	for _, repo := range repos {
		dir := filepath.Join(root, filepath.FromSlash(repo))
		cfg, found, why := ReadConfig(dir)
		if !found {
			if why != "" {
				unreadable = append(unreadable, repo+": "+why)
			}
			continue
		}
		if !cfg.Enabled {
			// The theme renders no link at all, so the base cannot be wrong
			// for a reader. Counted, because a base being unused today is not
			// the same as its being right.
			disabled++
			continue
		}
		owner, name, ok := originRepo(dir)
		if !ok {
			unreadable = append(unreadable, repo+": no origin remote to compare the href against")
			continue
		}
		sites = append(sites, site{repo: repo, dir: dir, owner: owner, name: name, branch: defaultBranch(dir), cfg: cfg})
	}

	var (
		results   []result
		problems  []*Problem
		ages      []checkout.Age
		confirmed = true
	)
	for _, s := range sites {
		r := result{site: s, base: s.cfg.Base, content: s.cfg.Content}
		p, _, err := Inspect(s.dir, s.repo, s.owner, s.name, s.branch, s.cfg)
		if err != nil {
			unreadable = append(unreadable, s.repo+": "+err.Error())
			continue
		}
		if p == nil {
			results = append(results, r)
			continue
		}
		behind, age, known := checkout.Staleness(s.dir)
		// ⛔ THE DISTANCE MUST BE FETCHED BEFORE IT IS TRUSTED, and this tool
		// got that wrong on its very first run. Staleness compares HEAD with
		// the LOCAL copy of origin/main, so go-desktop/docs -- whose fix had
		// been merged and published hours earlier -- came back "1 behind", was
		// re-derived against that same stale ref, and the dead finding
		// survived under a sentence claiming it had been confirmed. A ref
		// nobody fetched is not the remote's answer.
		if !noConfirm {
			if _, ok := checkout.Refresh(s.dir); ok {
				behind, age, known = checkout.Staleness(s.dir)
			}
		}
		// ⛔ REFRESHING IS NOT RE-DERIVING EITHER. Knowing the distance does
		// not change the bytes being parsed. For the handful of sites that
		// produce a finding -- never for the fleet -- the scan runs again over
		// the tree at the remote's default ref, and a finding that does not
		// survive is dropped.
		if behind > 0 && !noConfirm {
			if ref, ok := checkout.DefaultRef(s.dir); ok {
				if at, cleanup, ok := checkout.MaterialiseAt(s.dir, ref); ok {
					cfg, found, _ := ReadConfig(at)
					var again *Problem
					if found && cfg.Enabled {
						again, _, _ = Inspect(at, s.repo, s.owner, s.name, s.branch, cfg)
					}
					cleanup()
					// ⛔ THE VERDICT AND THE BASE IT JUDGED TRAVEL TOGETHER.
					// The first version kept the verdict from the materialised
					// tree and printed the base from the stale working tree,
					// which put "go-desktop/docs … resolves" beside the broken
					// base it no longer has -- a row true in neither tree. It
					// also made the table disagree with its own breakdown by
					// one, because a site fixed upstream in one respect was
					// counted under the shape of its OLD base.
					r.base, r.content, r.at = cfg.Base, cfg.Content, ref
					if again == nil {
						results = append(results, r)
						continue
					}
					p = again
				} else {
					confirmed = false
				}
			} else {
				confirmed = false
			}
		} else if behind > 0 {
			confirmed = false
		}
		r.problem = p
		results = append(results, r)
		problems = append(problems, p)
		ages = append(ages, checkout.Age{Behind: behind, FetchAge: age, FetchKnown: known})
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Repo < problems[j].Repo })

	out := cli.Out(cmd)
	fmt.Fprintf(out, "%d checkouts under %s\n", len(repos), root)
	fmt.Fprintf(out, "%d render an edit link, %d have one configured but disabled\n", len(sites), disabled)
	fmt.Fprintf(out, "%d of the %d render an href that does not resolve\n", len(problems), len(sites))

	// ⛔ THE SHAPE OF THE DEFECT IS PART OF THE COUNT. "77 of 100 are broken"
	// invites one sweep; the two shapes need different edits and have
	// different consequences -- a href naming a repository that does not
	// exist is a 404 a reader sees, while a href naming a missing path inside
	// a real repository drops a signed-in reader into GitHub's "create a new
	// file" editor at a path the site never serves.
	byReason := map[Reason]int{}
	for _, p := range problems {
		byReason[p.Reason]++
	}
	for _, r := range []struct {
		Reason
		label string
	}{
		{ReasonOtherRepo, "name a repository that is not the site's own"},
		{ReasonOtherBranch, "name a branch the remote does not serve by default"},
		{ReasonNoSuchPath, "name a path that is not in the repository"},
		{ReasonNotAnEditURL, "are not a GitHub edit URL at all"},
	} {
		if n := byReason[r.Reason]; n > 0 {
			fmt.Fprintf(out, "  %d %s\n", n, r.label)
		}
	}
	fmt.Fprintln(out)

	for _, p := range problems {
		fmt.Fprintf(out, "%s (%s)\n", p.Repo, p.Config)
		fmt.Fprintf(out, "  %d of %d pages: %s\n", p.Broken, p.Pages, p.Why)
		fmt.Fprintf(out, "  href    %s\n", p.Href)
		fmt.Fprintf(out, "  base    %s\n", p.Base)
		fmt.Fprintf(out, "  correct %s\n", p.Correct)
	}

	// ⭐ THE COUNT IS NOT THE ANSWER, THE LIST IS -- and here the list is what
	// reconciled two measurements of the same fleet. A grep for bases ending
	// at content/en said 11 sites doubled the language where this tool finds
	// 4: the difference is sites whose contentDir IS that directory, for which
	// the same base is CORRECT. A tool that only printed the number would have
	// left the earlier figure standing as an overstatement.
	if all {
		fmt.Fprintln(out, "every site that renders an edit link:")
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  repo\tcontentDir\tverdict\tbase judged\tread at")
		for _, r := range results {
			verdict := "resolves"
			if r.problem != nil {
				verdict = fmt.Sprintf("broken %d/%d", r.problem.Broken, r.problem.Pages)
			}
			at := "checkout"
			if r.at != "" {
				at = r.at
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", r.repo, r.content, verdict, r.base, at)
		}
		tw.Flush()
		fmt.Fprintln(out)
	}

	if len(unreadable) > 0 {
		fmt.Fprintf(out, "\n⚠ %d configuration(s) this tool could not read, so these sites are\n"+
			"  counted NOWHERE above -- an absence here means nobody looked:\n", len(unreadable))
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, u := range unreadable {
			fmt.Fprintln(tw, "  "+u)
		}
		tw.Flush()
	}

	if w := checkout.StalenessWarning(ages, confirmed); w != "" {
		fmt.Fprint(out, w)
	}

	if failOn && len(problems) > 0 {
		return cli.Found("%d of %d sites render an edit link that does not resolve", len(problems), len(sites))
	}
	return nil
}
