package main

// `gofloor fix ORG` and `gofloor merge ORG`: raise the go directive of every
// module below the floor, one organisation per run, without starving the
// budget ~25 sessions share.
//
// ⛔ WHY SUBCOMMANDS OF gofloor AND NOT A SIBLING COMMAND. The audit and the fix
// must read the SAME floor with the SAME parser: a sibling would copy
// parseVersion and the directive regexp and the two could disagree about which
// module is below the floor, which is the one question both answer. One binary
// is also one install, and the only cost is that an organisation literally
// named "fix" or "merge" can no longer be audited by name (none exists).
//
// ⛔ THE FIX READS THE CLONE, NOT THE REST API. The audit spent 181 REST calls
// for 24 repositories because every go.mod and workflow is a contents call.
// A clone goes through git, which the REST budget does not count, so deciding
// "is anything below the floor" costs nothing; REST is spent only where it
// cannot be avoided (the open-pull-request check, opening the PR, ratewait).
//
// ⛔ A MODE THAT WRITES ANSWERS EVERY REPOSITORY WITH A STATUS. Held by another
// session, conflicting pull request, build failure, refused go.mod, not visited
// because of --max: none of these is "done", and none is ever printed as clean.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

// defaultFloor is the lowest go directive that is not a finding. 1.27.3 carries
// GO-2026-6859 in net/http, fixed in 1.27.4.
const defaultFloor = "1.27.4"

const defaultAdvisory = "GO-2026-6859"

const (
	defaultAttribution = "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
	defaultPRFooter    = "🤖 Generated with [Claude Code](https://claude.com/claude-code)"
)

// Statuses of one repository. Only OPENED, CLEAN, ALREADY and MERGED are
// finished; everything else is listed with its reason.
const (
	stOpened     = "OPENED"
	stClean      = "CLEAN"
	stAlready    = "ALREADY"
	stHeld       = "HELD"
	stConflict   = "CONFLICT"
	stBuild      = "BUILD-FAILED"
	stRefused    = "REFUSED"
	stCIPin      = "CI-PIN"
	stUnreadable = "UNREADABLE"
	stNotVisited = "NOT-VISITED"
	stMerged     = "MERGED"
	stNotGreen   = "NOT-GREEN"
	stMergeNo    = "MERGE-REFUSED"
)

// runFunc runs a program in dir (empty: here). out is stdout and stderr
// together; status is the exit status; err is set only when the program could
// not be started, which is not the same as it failing.
type runFunc func(dir, name string, args ...string) (out string, status int, err error)

func realRun(dir, name string, args ...string) (string, int, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return b.String(), ee.ExitCode(), nil
	}
	if err != nil {
		return b.String(), -1, err
	}
	return b.String(), 0, nil
}

// realRate reads rate_limit, which costs nothing. It returns an error rather
// than a zero when the answer is not the number: unreadable is not "spent".
func realRate() (int, error) {
	out, err := exec.Command("gh", "api", "rate_limit").Output()
	if err != nil {
		return 0, fmt.Errorf("rate_limit: %w", err)
	}
	var v struct {
		Resources struct {
			Core *struct {
				Remaining *int `json:"remaining"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return 0, fmt.Errorf("rate_limit is not JSON: %w", err)
	}
	if v.Resources.Core == nil || v.Resources.Core.Remaining == nil {
		return 0, errors.New("rate_limit carries no core remaining")
	}
	return *v.Resources.Core.Remaining, nil
}

type fixOpts struct {
	floor, advisory, attribution, footer string
	max, reserve                         int
}

func (o fixOpts) branch() string { return "gofloor-" + o.floor }

// fixEnv is every seam the fix mode touches, so a test replaces all of them.
type fixEnv struct {
	gh    ghFunc
	repos reposFunc
	run   runFunc
	rate  func() (int, error)
	base  string // durable directory holding the clones
	reads int    // REST reads this process made through gh
}

func (e *fixEnv) ghc(args ...string) ([]byte, error) {
	e.reads++
	return e.gh(args...)
}

func defaultBase() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, "Documents", "VCS", "GIT", "localhost")
}

func realEnv(gh ghFunc, repos reposFunc) *fixEnv {
	return &fixEnv{gh: gh, repos: repos, run: realRun, rate: realRate, base: defaultBase()}
}

// addFixCommands hangs `fix` and `merge` under the audit command.
func addFixCommands(root *cobra.Command, e *fixEnv) {
	root.AddCommand(fixCommand(e), mergeCommand(e))
}

func addOptFlags(c *cobra.Command, o *fixOpts, withMax bool) {
	c.Flags().StringVar(&o.floor, "floor", defaultFloor, "the go directive to raise every lower module to")
	c.Flags().StringVar(&o.advisory, "advisory", defaultAdvisory, "the advisory the pull request names")
	c.Flags().StringVar(&o.attribution, "attribution", defaultAttribution, "the trailer ending every commit message")
	c.Flags().StringVar(&o.footer, "footer", defaultPRFooter, "the line ending every pull request body")
	c.Flags().IntVar(&o.reserve, "reserve", 2500, "REST calls that must remain before each repository (ratewait --rest)")
	if withMax {
		c.Flags().IntVar(&o.max, "max", 0, "visit at most this many repositories (0: all); the rest are listed NOT-VISITED")
	}
}

func fixCommand(e *fixEnv) *cobra.Command {
	var o fixOpts
	c := cli.New("fix [flags] ORG", "Open one pull request per repository raising every go directive to the floor",
		`For ONE organisation, per repository and in this order: ratewait --rest N, an
agentsync claim (a repository held by another session is skipped and listed as
held, never as done), a clone or update under <localhost>/<org>-<repo>-gofloor,
a branch from the fresh origin default branch, go mod edit -go=FLOOR in EVERY
go.mod below the floor (root and sub-modules), go build ./... in each, a commit,
gitpush, then prbody and paritycheck over the body, then gh pr create.

It does NOT wait for CI and does NOT merge: gofloor merge does that later.

A repository is skipped, and listed with the reason, when: another session holds
it; an open pull request from another branch changes a go directive (or
edits a go.mod whose patch GitHub withheld), or git merge-tree reports one
that touches a go.mod as conflicting with the floor commit (a Renovate PR
that only edits require lines does not stop it); a go.mod is
malformed; a build fails; a workflow pins setup-go to a version under the floor
that this cannot rewrite (a hard-coded pin under the directive REFUSES in CI,
because setup-go sets GOTOOLCHAIN=local).

Simple go-version pins of three components under the floor are rewritten in the
same commit: the PR would otherwise be red.

Every repository gets a cost line: its own REST reads, and the change of the
rate_limit core counter around it (which includes whatever the other sessions
spent meanwhile). A cost that could not be read says so.

Exit 0 every repository finished, 1 something was skipped or not visited,
2 something could not be read.`)
	addOptFlags(c, &o, true)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if cli.HelpRequested(cmd) {
			return nil
		}
		if len(args) != 1 {
			fmt.Fprintln(cli.Err(cmd), "usage: gofloor fix [--max N] ORG")
			return cli.FromStatus(2)
		}
		if _, ok := parseVersion(o.floor); !ok {
			fmt.Fprintf(cli.Err(cmd), "gofloor: --floor %q is not a Go version\n", o.floor)
			return cli.FromStatus(2)
		}
		return cli.FromStatus(e.fixOrg(cli.Out(cmd), o, args[0]))
	}
	return c
}

func mergeCommand(e *fixEnv) *cobra.Command {
	var o fixOpts
	c := cli.New("merge [flags] ORG", "Merge the pull requests fix opened, those whose checks are green",
		`Finds the open pull requests from the branch gofloor-FLOOR in ONE organisation
(one search call), judges each with prwait (a pull request that is not finished
is never green), merges the green ones with ghmerge and lists the others with
their state. ratewait --rest N runs before each.

Exit 0 every pull request merged (or none open), 1 some were not merged,
2 something could not be read.`)
	addOptFlags(c, &o, false)
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if cli.HelpRequested(cmd) {
			return nil
		}
		if len(args) != 1 {
			fmt.Fprintln(cli.Err(cmd), "usage: gofloor merge ORG")
			return cli.FromStatus(2)
		}
		if _, ok := parseVersion(o.floor); !ok {
			fmt.Fprintf(cli.Err(cmd), "gofloor: --floor %q is not a Go version\n", o.floor)
			return cli.FromStatus(2)
		}
		return cli.FromStatus(e.mergeOrg(cli.Out(cmd), o, args[0]))
	}
	return c
}

type outcome struct {
	Repo, Status, Detail string
	Cost                 string
}

func (r outcome) finished() bool {
	switch r.Status {
	case stOpened, stClean, stAlready, stMerged:
		return true
	}
	return false
}

type tally struct {
	out                io.Writer
	results            []outcome
	delta, readsTotal  int
	costKnown, costUnk int
}

func (t *tally) add(r outcome) {
	t.results = append(t.results, r)
	fmt.Fprintf(t.out, "%-13s %-36s %s\n              cost: %s\n", r.Status, r.Repo, r.Detail, r.Cost)
}

// exit maps the results to a status: 2 outranks 1 outranks 0.
func (t *tally) exit() int {
	code := 0
	for _, r := range t.results {
		if r.finished() {
			continue
		}
		if r.Status == stUnreadable {
			return 2
		}
		code = 1
	}
	return code
}

func (t *tally) summary(e *fixEnv) {
	counts := map[string]int{}
	for _, r := range t.results {
		counts[r.Status]++
	}
	var keys []string
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	fmt.Fprintf(t.out, "\n%d repositories: %s\n", len(t.results), strings.Join(parts, ", "))
	fmt.Fprintf(t.out, "TOTAL cost: %d REST reads by this process (%d in all, tool processes excluded); rate_limit core changed by %d over %d readable repositories",
		t.readsTotal, e.reads, t.delta, t.costKnown)
	if t.costUnk > 0 {
		fmt.Fprintf(t.out, ", %d with an UNREADABLE cost", t.costUnk)
	}
	fmt.Fprintln(t.out)
}

// measure runs f between two rate_limit readings.
func (t *tally) measure(e *fixEnv, f func() outcome) outcome {
	before, errB := e.rate()
	reads0 := e.reads
	r := f()
	after, errA := e.rate()
	own := e.reads - reads0
	t.readsTotal += own
	if errB != nil || errA != nil {
		t.costUnk++
		r.Cost = fmt.Sprintf("%d own REST reads; rate_limit UNREADABLE", own)
		return r
	}
	d := before - after
	t.costKnown++
	t.delta += d
	r.Cost = fmt.Sprintf("%d own REST reads; core %d -> %d (%+d, other sessions included)", own, before, after, -d)
	return r
}

// reserve runs ratewait; a failure ends the run, because the next repository
// would spend the half of the budget that belongs to others.
func (e *fixEnv) reserve(o fixOpts) error {
	out, st, err := e.run("", "ratewait", "--rest", strconv.Itoa(o.reserve))
	if err != nil {
		return fmt.Errorf("ratewait could not run: %w", err)
	}
	if st != 0 {
		return fmt.Errorf("ratewait exit %d: %s", st, lastLine(out))
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return clipRunes(strings.TrimSpace(lines[len(lines)-1]), 200)
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (e *fixEnv) fixOrg(out io.Writer, o fixOpts, org string) int {
	t := &tally{out: out}
	rs, err := e.repos(org)
	if err != nil {
		fmt.Fprintf(out, "%s listing %s: %v\n", stUnreadable, org, err)
		return 2
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].FullName < rs[j].FullName })
	visited := 0
	for i, r := range rs {
		if o.max > 0 && visited >= o.max {
			for _, left := range rs[i:] {
				t.add(outcome{left.FullName, stNotVisited, "stopped by --max " + strconv.Itoa(o.max), "none"})
			}
			break
		}
		if err := e.reserve(o); err != nil {
			for _, left := range rs[i:] {
				t.add(outcome{left.FullName, stNotVisited, "the run stopped: " + err.Error(), "none"})
			}
			break
		}
		visited++
		t.add(t.measure(e, func() outcome { return e.fixOne(o, r) }))
	}
	t.summary(e)
	return t.exit()
}

var heldRe = regexp.MustCompile(`held by (\S+)(?: until (\S+))?`)

func (e *fixEnv) dirOf(r fleet.Repo) string {
	return filepath.Join(e.base, strings.ReplaceAll(r.FullName, "/", "-")+"-gofloor")
}

func (e *fixEnv) fixOne(o fixOpts, r fleet.Repo) outcome {
	res := outcome{Repo: r.FullName}
	bad := func(st, f string, a ...any) outcome {
		res.Status, res.Detail = st, fmt.Sprintf(f, a...)
		return res
	}
	if r.DefaultBranch == "" {
		return bad(stUnreadable, "the listing carries no default branch")
	}
	if e.base == "" {
		return bad(stUnreadable, "no durable directory to clone into")
	}

	// A claim is the check for "held by another session": the refusal names the
	// holder, and a lease this session already holds is not another's.
	cout, st, err := e.run("", "agentsync", "claim", "--note", "gofloor fix "+o.floor, r.FullName)
	switch {
	case err != nil:
		return bad(stUnreadable, "agentsync could not run: %v", err)
	case st == 1 && strings.Contains(cout, "REFUSED"):
		if m := heldRe.FindStringSubmatch(cout); m != nil {
			return bad(stHeld, "held by %s until %s", m[1], m[2])
		}
		return bad(stHeld, "held by another session (%s)", lastLine(cout))
	case st != 0:
		return bad(stUnreadable, "agentsync claim exit %d: %s", st, lastLine(cout))
	}
	defer e.run("", "agentsync", "release", r.FullName)

	dir := e.dirOf(r)
	remote := "https://github.com/" + r.FullName + ".git"
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if out, st, err := e.run(e.base, "git", "clone", remote, dir); err != nil || st != 0 {
			return bad(stUnreadable, "git clone: %s", gitWhy(out, err))
		}
	} else if err != nil {
		return bad(stUnreadable, "stat %s: %v", dir, err)
	} else {
		out, st, err := e.run(dir, "git", "status", "--porcelain")
		if err != nil || st != 0 {
			return bad(stUnreadable, "git status: %s", gitWhy(out, err))
		}
		if strings.TrimSpace(out) != "" {
			return bad(stUnreadable, "%s has uncommitted changes, which are not mine to discard", dir)
		}
	}
	for _, g := range [][]string{
		{"fetch", "origin", "--prune"},
		{"checkout", "-B", o.branch(), "origin/" + r.DefaultBranch},
	} {
		if out, st, err := e.run(dir, "git", g...); err != nil || st != 0 {
			return bad(stUnreadable, "git %s: %s", g[0], gitWhy(out, err))
		}
	}

	edits, refused, err := planFloor(dir, o.floor)
	if err != nil {
		return bad(stUnreadable, "%v", err)
	}
	if len(refused) > 0 {
		return bad(stRefused, "%s", strings.Join(refused, "; "))
	}
	if len(edits) == 0 {
		return bad(stClean, "no go.mod below %s", o.floor)
	}

	// Open pull requests: REST, so only for a repository that needs a change.
	conflict, already, check, err := e.openPRs(r, o)
	if err != nil {
		return bad(stUnreadable, "open pull requests: %v", err)
	}
	if already != "" {
		return bad(stAlready, "%s", already)
	}
	if conflict != "" {
		return bad(stConflict, "%s", conflict)
	}

	// Everything from here changes files in OUR clone; any failure puts them
	// back (staged or not), so the next run finds a clean directory.
	pins, blocked, perr := fixPins(dir, o.floor)
	touched := []string{}
	for _, ed := range edits {
		touched = append(touched, ed.Path)
	}
	touched = append(touched, pins...)
	failed := func(st, f string, a ...any) outcome {
		e.run(dir, "git", append([]string{"restore", "--staged", "--worktree", "--source=HEAD", "--"}, touched...)...)
		return bad(st, f, a...)
	}
	if perr != nil {
		return failed(stUnreadable, "%v", perr)
	}
	if len(blocked) > 0 {
		return failed(stCIPin, "setup-go pinned under %s and not rewritable here: %s", o.floor, strings.Join(blocked, "; "))
	}
	if err := applyFloor(e.run, dir, o.floor, edits); err != nil {
		return failed(stUnreadable, "%v", err)
	}
	for _, ed := range edits {
		mdir := filepath.Join(dir, filepath.FromSlash(path.Dir(ed.Path)))
		if out, st, err := e.run(mdir, "go", "build", "./..."); err != nil || st != 0 {
			return failed(stBuild, "%s: go build ./... %s", ed.Path, gitWhy(out, err))
		}
	}

	msgDir := filepath.Join(e.base, "gofloor-msgs")
	if err := os.MkdirAll(msgDir, 0o755); err != nil {
		return failed(stUnreadable, "%v", err)
	}
	stem := filepath.Join(msgDir, strings.ReplaceAll(r.FullName, "/", "-")+"-"+o.floor)
	title := fmt.Sprintf("go.mod: raise the go directive to %s (%s)", o.floor, o.advisory)
	if err := os.WriteFile(stem+".commit.txt", []byte(commitMessage(o, title, edits, pins)), 0o644); err != nil {
		return failed(stUnreadable, "%v", err)
	}
	if err := os.WriteFile(stem+".body.md", []byte(prBody(o, edits, pins)), 0o644); err != nil {
		return failed(stUnreadable, "%v", err)
	}
	// The body gates run BEFORE anything is pushed: a refusal then costs nothing.
	if out, st, err := e.run(dir, "paritycheck", "text", stem+".body.md"); err != nil || st != 0 {
		return failed(stUnreadable, "paritycheck refused the body: %s", gitWhy(out, err))
	}
	if out, st, err := e.run(dir, "prbody", "--repo", r.FullName, stem+".body.md"); err != nil || st != 0 {
		return failed(stUnreadable, "prbody refused the body: %s", gitWhy(out, err))
	}
	for _, g := range [][]string{
		append([]string{"add", "--"}, touched...),
		{"commit", "-F", stem + ".commit.txt"},
	} {
		if out, st, err := e.run(dir, "git", g...); err != nil || st != 0 {
			return failed(stUnreadable, "git %s: %s", g[0], gitWhy(out, err))
		}
	}
	// The pull requests that edit a go.mod without touching the directive are
	// judged by git, on the commit just made, before anything is pushed.
	clash, merr := e.mergeConflicts(dir, check)
	if merr != nil || len(clash) > 0 {
		e.run(dir, "git", "reset", "--hard", "origin/"+r.DefaultBranch) // our own throwaway commit
		if merr != nil {
			return bad(stUnreadable, "merge check: %v", merr)
		}
		return bad(stConflict, "%s", strings.Join(clash, "; "))
	}
	if out, st, err := e.run(dir, "gitpush", "-u", "origin", o.branch()); err != nil || st != 0 {
		return failed(stUnreadable, "gitpush: %s", gitWhy(out, err))
	}
	out, st, err := e.run(dir, "gh", "pr", "create", "--repo", r.FullName, "--base", r.DefaultBranch,
		"--head", o.branch(), "--title", title, "--body-file", stem+".body.md")
	if err != nil || st != 0 {
		return failed(stUnreadable, "gh pr create: %s (the branch %s is pushed)", gitWhy(out, err), o.branch())
	}
	return bad(stOpened, "%s (%d module(s), %d workflow pin(s))", prURL(out), len(edits), len(pins))
}

func gitWhy(out string, err error) string {
	if err != nil {
		return err.Error()
	}
	return lastLine(out)
}

var prURLRe = regexp.MustCompile(`https://github\.com/\S+/pull/\d+`)

func prURL(out string) string {
	if m := prURLRe.FindString(out); m != "" {
		return m
	}
	return "(no URL in gh's answer: " + lastLine(out) + ")"
}

// directiveEditRe finds a changed `go` directive line in a unified diff.
var directiveEditRe = regexp.MustCompile(`(?m)^[+-]go\s+\d`)

// openPRs says whether another branch's open pull request collides with the
// floor change.
//
// A pull request that only edits `require` lines (Renovate's) rarely collides
// textually, so it is NOT a conflict by itself: conflict is set when a go.mod
// diff changes the `go` directive line, or when its patch is missing (GitHub
// omits big ones) so that cannot be judged. Every other pull request touching a
// go.mod is returned in check, to be judged by `git merge-tree` against the
// real commit. already is the line to print when this tool's own is open.
func (e *fixEnv) openPRs(r fleet.Repo, o fixOpts) (conflict, already string, check []int, err error) {
	b, err := e.ghc("api", "repos/"+r.FullName+"/pulls?state=open&per_page=100")
	if err != nil {
		return "", "", nil, err
	}
	var prs []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Head    struct {
			Ref string `json:"ref"`
		} `json:"head"`
	}
	if err := json.Unmarshal(b, &prs); err != nil {
		return "", "", nil, fmt.Errorf("the pull request list is not JSON: %w", err)
	}
	if len(prs) >= 100 {
		return "", "", nil, errors.New("100 open pull requests or more: the list may be cut")
	}
	var found []string
	for _, p := range prs {
		if p.Head.Ref == o.branch() {
			return "", "pull request already open: " + p.HTMLURL, nil, nil
		}
	}
	for _, p := range prs {
		fb, err := e.ghc("api", fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", r.FullName, p.Number))
		if err != nil {
			return "", "", nil, fmt.Errorf("files of #%d: %w", p.Number, err)
		}
		var files []struct {
			Filename string `json:"filename"`
			Patch    string `json:"patch"`
		}
		if err := json.Unmarshal(fb, &files); err != nil {
			return "", "", nil, fmt.Errorf("files of #%d are not JSON: %w", p.Number, err)
		}
		if len(files) >= 100 {
			return "", "", nil, fmt.Errorf("#%d has 100 files or more: the list may be cut", p.Number)
		}
		touches := false
		for _, f := range files {
			if path.Base(f.Filename) != "go.mod" {
				continue
			}
			touches = true
			switch {
			case f.Patch == "":
				found = append(found, fmt.Sprintf("#%d (%s) edits %s, patch not available", p.Number, p.Head.Ref, f.Filename))
			case directiveEditRe.MatchString(f.Patch):
				found = append(found, fmt.Sprintf("#%d (%s) changes the go directive of %s", p.Number, p.Head.Ref, f.Filename))
			}
		}
		if touches {
			check = append(check, p.Number)
		}
	}
	if len(found) > 0 {
		return "open pull request(s) editing the go directive: " + strings.Join(found, "; "), "", nil, nil
	}
	return "", "", check, nil
}

// mergeConflicts asks git, against the real commit, whether each pull request
// that touches a go.mod would merge with it. `git merge-tree --write-tree`
// exits 1 on a conflict; anything else non-zero is unreadable, never "clean".
func (e *fixEnv) mergeConflicts(dir string, nums []int) (conflicts []string, err error) {
	for _, n := range nums {
		ref := fmt.Sprintf("pull/%d/head", n)
		if out, st, err := e.run(dir, "git", "fetch", "origin", ref); err != nil || st != 0 {
			return nil, fmt.Errorf("fetch %s: %s", ref, gitWhy(out, err))
		}
		out, st, err := e.run(dir, "git", "merge-tree", "--write-tree", "HEAD", "FETCH_HEAD")
		switch {
		case err != nil:
			return nil, err
		case st == 1:
			conflicts = append(conflicts, fmt.Sprintf("#%d conflicts with the floor commit", n))
		case st != 0:
			return nil, fmt.Errorf("git merge-tree exit %d: %s", st, lastLine(out))
		}
	}
	return conflicts, nil
}

// An Edit is one go.mod below the floor.
type Edit struct {
	Path string // slash path from the repository root
	Old  string // the directive it had
}

// planFloor lists every go.mod below the floor and every go.mod it refuses.
//
// ⛔ A go.mod it cannot judge is REFUSED, never skipped: "no go directive" and
// "not a version" are the cases the audit already calls findings, and editing a
// file whose directive was not read would be a guess.
func planFloor(root, floor string) (edits []Edit, refused []string, err error) {
	f, ok := parseVersion(floor)
	if !ok {
		return nil, nil, fmt.Errorf("floor %q is not a version", floor)
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "testdata" || rel == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		dir := goDirective(string(src))
		v, vok := parseVersion(dir)
		switch {
		case dir == "":
			refused = append(refused, rel+" has no go directive")
		case !vok:
			refused = append(refused, rel+": go "+dir+" is not a version")
		case less(v, f):
			edits = append(edits, Edit{rel, dir})
		}
		return nil
	})
	sort.Slice(edits, func(i, j int) bool { return edits[i].Path < edits[j].Path })
	return edits, refused, err
}

// applyFloor runs `go mod edit -go=FLOOR` on every edit.
func applyFloor(run runFunc, root, floor string, edits []Edit) error {
	for _, ed := range edits {
		out, st, err := run(root, "go", "mod", "edit", "-go="+floor, filepath.Join(root, filepath.FromSlash(ed.Path)))
		if err != nil || st != 0 {
			return fmt.Errorf("go mod edit %s: %s", ed.Path, gitWhy(out, err))
		}
	}
	return nil
}

var pinLineRe = regexp.MustCompile(`(?m)^(\s*(?:-\s*)?go-version:\s*)(['"]?)(\d+\.\d+\.\d+)(['"]?)(\s*(?:#.*)?)$`)

// fixPins rewrites the simple hard-coded setup-go pins under the floor, and
// names the pins it cannot rewrite.
//
// ⛔ A PIN UNDER THE DIRECTIVE IS A RED PULL REQUEST. setup-go sets
// GOTOOLCHAIN=local, so go.mod raised to 1.27.4 with the job pinned to 1.27.3
// REFUSES to build (measured 2026-11-15: five of six repositories red). Only a
// three-component pin is judged: "1.27" resolves to the latest patch.
func fixPins(root, floor string) (changed, blocked []string, err error) {
	f, _ := parseVersion(floor)
	dir := filepath.Join(root, ".github", "workflows")
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, ent := range ents {
		n := ent.Name()
		if ent.IsDir() || !(strings.HasSuffix(n, ".yml") || strings.HasSuffix(n, ".yaml")) {
			continue
		}
		p := filepath.Join(dir, n)
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		rel := ".github/workflows/" + n
		setups, _, _, aerr := Audit(rel, src)
		if aerr != nil {
			return nil, nil, fmt.Errorf("%s: %w", rel, aerr)
		}
		pinned := 0
		for _, s := range setups {
			if v, ok := strings.CutPrefix(s.Source, "hard-coded "); ok && lowThreePart(v, f) {
				pinned++
			}
		}
		if pinned == 0 {
			continue
		}
		replaced := 0
		out := pinLineRe.ReplaceAllStringFunc(string(src), func(line string) string {
			m := pinLineRe.FindStringSubmatch(line)
			if !lowThreePart(m[3], f) {
				return line
			}
			replaced++
			return m[1] + m[2] + floor + m[4] + m[5]
		})
		if replaced > 0 {
			if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
				return nil, nil, err
			}
			changed = append(changed, rel)
		}
		if replaced < pinned {
			blocked = append(blocked, fmt.Sprintf("%s (%d of %d pin(s) rewritten)", rel, replaced, pinned))
		}
	}
	return changed, blocked, nil
}

func lowThreePart(v string, floor [3]int) bool {
	if strings.Count(v, ".") != 2 {
		return false
	}
	p, ok := parseVersion(v)
	return ok && less(p, floor)
}

func commitMessage(o fixOpts, title string, edits []Edit, pins []string) string {
	var b strings.Builder
	b.WriteString(title + "\n\n")
	fmt.Fprintf(&b, "%s is a vulnerability in net/http that is fixed in Go %s. A module whose go\n"+
		"directive is lower can be built with the vulnerable standard library, and\n"+
		"govulncheck fails the next pull request of every repository still there.\n\n", o.advisory, o.floor)
	for _, e := range edits {
		fmt.Fprintf(&b, "- %s: go %s -> %s\n", e.Path, e.Old, o.floor)
	}
	for _, p := range pins {
		fmt.Fprintf(&b, "- %s: setup-go pin raised to %s (a pin under the directive refuses to build)\n", p, o.floor)
	}
	b.WriteString("\n" + o.attribution + "\n")
	return b.String()
}

func prBody(o fixOpts, edits []Edit, pins []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Raise the go directive to %s (%s)\n\n", o.floor, o.advisory)
	fmt.Fprintf(&b, "%s is a vulnerability in net/http, fixed in Go %s. govulncheck fails the "+
		"next pull request of any repository whose go directive is lower.\n\n", o.advisory, o.floor)
	b.WriteString("Changed by `go mod edit -go=" + o.floor + "`, then `go build ./...` in each module:\n\n")
	for _, e := range edits {
		fmt.Fprintf(&b, "- `%s`: go %s -> %s\n", e.Path, e.Old, o.floor)
	}
	for _, p := range pins {
		fmt.Fprintf(&b, "- `%s`: hard-coded setup-go pin raised to %s\n", p, o.floor)
	}
	b.WriteString("\nOnly the go directive (and a hard-coded setup-go pin that would otherwise refuse the " +
		"new directive) changes. Opened by `gofloor fix`; CI decides.\n\n" + o.footer + "\n")
	return b.String()
}

// ---- merge ----

func (e *fixEnv) mergeOrg(out io.Writer, o fixOpts, org string) int {
	t := &tally{out: out}
	q := fmt.Sprintf("is:pr is:open org:%s head:%s", org, o.branch())
	b, err := e.ghc("api", "search/issues?per_page=100&q="+url.QueryEscape(q))
	if err != nil {
		fmt.Fprintf(out, "%s searching %s: %v\n", stUnreadable, org, err)
		return 2
	}
	var res struct {
		Total      int  `json:"total_count"`
		Incomplete bool `json:"incomplete_results"`
		Items      []struct {
			Number  int    `json:"number"`
			Repo    string `json:"repository_url"`
			HTMLURL string `json:"html_url"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &res); err != nil {
		fmt.Fprintf(out, "%s the search answer is not JSON: %v\n", stUnreadable, err)
		return 2
	}
	if res.Incomplete || res.Total > len(res.Items) {
		fmt.Fprintf(out, "%s the search is incomplete (%d of %d): nothing was merged\n", stUnreadable, len(res.Items), res.Total)
		return 2
	}
	for i, it := range res.Items {
		repo := strings.TrimPrefix(it.Repo, "https://api.github.com/repos/")
		if repo == it.Repo || repo == "" {
			t.add(outcome{it.HTMLURL, stUnreadable, "repository_url " + it.Repo + " is not a repository", "none"})
			continue
		}
		if err := e.reserve(o); err != nil {
			for _, left := range res.Items[i:] {
				t.add(outcome{left.HTMLURL, stNotVisited, "the run stopped: " + err.Error(), "none"})
			}
			break
		}
		t.add(t.measure(e, func() outcome {
			r := outcome{Repo: repo + "#" + strconv.Itoa(it.Number)}
			num := strconv.Itoa(it.Number)
			wout, st, err := e.run("", "prwait", "-repo", repo, "-timeout", "1s", "-interval", "1s", num)
			switch {
			case err != nil || st == 2:
				r.Status, r.Detail = stUnreadable, "prwait: "+gitWhy(wout, err)
			case st != 0:
				r.Status, r.Detail = stNotGreen, lastLine(wout)
			default:
				mout, mst, merr := e.run("", "ghmerge", repo, num)
				if merr != nil || mst != 0 {
					r.Status, r.Detail = stMergeNo, "ghmerge: "+gitWhy(mout, merr)
				} else {
					r.Status, r.Detail = stMerged, lastLine(mout)
				}
			}
			return r
		}))
	}
	if len(res.Items) == 0 {
		fmt.Fprintf(out, "no open pull request from %s in %s\n", o.branch(), org)
	}
	t.summary(e)
	return t.exit()
}
