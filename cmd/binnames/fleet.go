package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/go-fleettools/fleettools/internal/binnames"
	"github.com/go-fleettools/fleettools/internal/checkout"
	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

const fleetHelp = `binnames fleet sweeps every repository of the fleet.

Local clones come first: each directory under --clones that is a git clone is
read at origin's default branch, as last fetched, without checking anything
out. Their origins name the organisations; each organisation's repositories
(archived ones and forks left out, as every sweep here does) that have no clone
are read from GitHub, one recursive git-trees call each, until --max-calls REST
calls have been spent. The cap is deliberately small: sweeps starve Renovate,
which draws on the same hourly budget.

⛔ A repository that could not be read (no default ref, a truncated tree, the
cap reached, an organisation that could not be listed) is printed as UNREAD and
makes the status 2. It is never counted as clean.`

var githubOrigin = regexp.MustCompile(`github\.com(?::[0-9]+)?[:/]([A-Za-z0-9][A-Za-z0-9-]*)/([A-Za-z0-9._-]+?)(?:\.git)?/?$`)

func fleetCmd(o *opts) *cobra.Command {
	var (
		clones   string
		orgs     []string
		maxCalls int
		noRemote bool
		list     bool
	)
	c := cli.New("fleet [flags]", "Sweep the fleet's clones, then GitHub, for colliding binary names", fleetHelp)
	c.Args = cobra.NoArgs
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if maxCalls < 0 {
			return cli.Unanswerable("--max-calls must not be negative")
		}
		oo := *o
		ck, err := newChecker(cmd.ErrOrStderr(), oo)
		if err != nil {
			return cli.Wrap(err)
		}
		if clones == "" {
			home, _ := os.UserHomeDir()
			clones = filepath.Join(home, "Documents", "VCS", "GIT", "localhost")
		}
		b := &binnames.Budget{Cap: maxCalls}
		res, err := ck.sweep(clones, orgs, b, noRemote)
		if err != nil {
			return cli.Wrap(err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "REST calls spent on GitHub: %d of a cap of %d; file contents come from raw.githubusercontent.com, which is not the REST API\n", b.Used, b.Cap)
		return ck.finish(cmd.OutOrStdout(), res, list)
	}
	c.Flags().StringVar(&clones, "clones", "", "directory of local clones (default ~/Documents/VCS/GIT/localhost)")
	c.Flags().StringSliceVar(&orgs, "org", nil, "an organisation to read from GitHub besides the ones the clones name (repeatable)")
	c.Flags().IntVar(&maxCalls, "max-calls", 600, "REST calls this sweep may spend")
	c.Flags().BoolVar(&noRemote, "no-remote", false, "read local clones only; repositories without a clone are not looked for")
	c.Flags().BoolVar(&list, "list", false, "list every main package, not only collisions and unread")
	return c
}

// A repoClones is a repository and every local clone of it, the one named
// owner-repo first.
type repoClones struct {
	repo string
	dirs []string
}

// findClones lists the git clones directly under root, grouped by repository.
// A worktree (its .git is a file) is the same repository again and is skipped.
func findClones(root string) (clones []repoClones, unread []binnames.Unread, err error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, err
	}
	best := map[string]*repoClones{}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		fi, err := os.Stat(filepath.Join(dir, ".git"))
		if err != nil || !fi.IsDir() {
			continue
		}
		out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
		if err != nil {
			unread = append(unread, binnames.Unread{What: e.Name(), Why: "no origin remote"})
			continue
		}
		m := githubOrigin.FindStringSubmatch(strings.TrimSpace(string(out)))
		if m == nil {
			unread = append(unread, binnames.Unread{What: e.Name(), Why: "origin is not on github.com: " + redact(strings.TrimSpace(string(out)))})
			continue
		}
		repo := m[1] + "/" + m[2]
		key := strings.ToLower(repo)
		rc := best[key]
		if rc == nil {
			rc = &repoClones{repo: repo}
			best[key] = rc
		}
		if filepath.Base(dir) == m[1]+"-"+m[2] {
			rc.dirs = append([]string{dir}, rc.dirs...)
		} else {
			rc.dirs = append(rc.dirs, dir)
		}
	}
	for _, c := range best {
		clones = append(clones, *c)
	}
	sort.Slice(clones, func(i, j int) bool { return clones[i].repo < clones[j].repo })
	return clones, unread, nil
}

// redact drops credentials from an origin URL before it is printed.
func redact(s string) string {
	if u, err := url.Parse(s); err == nil && u.User != nil {
		u.User = nil
		return u.String()
	}
	return s
}

func (ck *checker) sweep(root string, extraOrgs []string, b *binnames.Budget, noRemote bool) ([]result, error) {
	cl, unread, err := findClones(root)
	if err != nil {
		return nil, err
	}
	if len(cl) == 0 {
		return nil, fmt.Errorf("no git clone with a github.com origin under %s: a sweep of nothing is not a clean one", root)
	}
	var res []result
	for _, u := range unread {
		res = append(res, result{repo: u.What, unread: []binnames.Unread{u}})
	}
	have := map[string]bool{}
	orgSet := map[string]bool{}
	for _, o := range extraOrgs {
		orgSet[o] = true
	}
	// Clones with no default ref (a topic clone that was never given
	// origin/HEAD) are not read here. GitHub is asked for them below, and the
	// ones it does not answer for are UNREAD at the end.
	noRef := map[string]result{}
	for _, c := range cl {
		orgSet[checkout.OwnerOf(c.repo)] = true
		var dir, ref string
		for _, d := range c.dirs {
			if r, ok := checkout.DefaultRef(d); ok {
				dir, ref = d, r
				break
			}
		}
		if dir == "" {
			noRef[strings.ToLower(c.repo)] = result{repo: c.repo, unread: []binnames.Unread{{What: c.repo,
				Why: "clone " + c.dirs[0] + " has no origin/HEAD, origin/main or origin/master"}}}
			continue
		}
		have[strings.ToLower(c.repo)] = true
		r := ck.scan(c.repo, binnames.GitTree{Dir: dir, Ref: ref}, binnames.Options{RepoName: filepath.Base(c.repo)})
		r.note = "clone " + dir + " at " + ref
		res = append(res, r)
	}
	flushNoRef := func() {
		var keys []string
		for k := range noRef {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			res = append(res, noRef[k])
		}
	}
	if noRemote {
		flushNoRef()
		return res, nil
	}

	orgs := make([]string, 0, len(orgSet))
	for o := range orgSet {
		orgs = append(orgs, o)
	}
	sort.Strings(orgs)
	for _, org := range orgs {
		if err := b.Spend(1); err != nil {
			res = append(res, result{repo: org, unread: []binnames.Unread{{What: org, Why: "its repositories were not listed: " + err.Error()}}})
			continue
		}
		repos, err := fleet.Repos(org)
		if err != nil {
			res = append(res, result{repo: org, unread: []binnames.Unread{{What: org, Why: "repository listing failed: " + err.Error()}}})
			continue
		}
		// --paginate made ceil(n/100) calls, the first of which is charged.
		_ = b.Spend(len(repos) / 100)
		for _, r := range repos {
			if have[strings.ToLower(r.FullName)] {
				continue
			}
			delete(noRef, strings.ToLower(r.FullName)) // GitHub answers for it
			res = append(res, ck.remote(r.FullName, r.DefaultBranch, b))
		}
	}
	flushNoRef()
	return res, nil
}

// remote reads owner/name from GitHub. An empty branch is looked up first.
func (ck *checker) remote(repo, branch string, b *binnames.Budget) result {
	if ck.raw == nil {
		tok := ""
		if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
			tok = strings.TrimSpace(string(out))
		}
		ck.raw = binnames.HTTPRaw(tok)
	}
	api := func(args ...string) ([]byte, error) { return fleet.GH(args...) }
	return ck.remoteWith(repo, branch, b, api, ck.raw)
}

func (ck *checker) remoteWith(repo, branch string, b *binnames.Budget, api func(...string) ([]byte, error), raw func(string) ([]byte, error)) result {
	if branch == "" {
		if err := b.Spend(1); err != nil {
			return result{repo: repo, unread: []binnames.Unread{{What: repo, Why: err.Error()}}}
		}
		out, err := api("api", "repos/"+repo, "--jq", ".default_branch")
		if err != nil || strings.TrimSpace(string(out)) == "" {
			return result{repo: repo, unread: []binnames.Unread{{What: repo, Why: fmt.Sprintf("default branch unknown: %v", err)}}}
		}
		branch = strings.TrimSpace(string(out))
	}
	t := &binnames.Remote{Repo: repo, Branch: branch, Budget: b, API: api, Raw: raw}
	r := ck.scan(repo, t, binnames.Options{RepoName: filepath.Base(repo), Candidate: binnames.RemoteCandidate})
	r.note = "GitHub " + branch
	return r
}
