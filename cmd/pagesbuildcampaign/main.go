// Command pagesbuildcampaign rolls the pagesbuild step (go-redfish/docs#11)
// out to docs repositories: one small pull request per repository, merged on
// green. It drives the tools this machine already trusts rather than
// reimplementing them: agentsync (claims), git, gitpush (the push, with no
// token on a command line), gh (pull request, checks, rate limit) and ghmerge.
//
// Per repository: claim it (a repository another session holds is SKIPPED,
// never waited on), clone it under --workdir/<org>-docs-pagesbuild, check the
// clone is the tip of main, branch, run pagesbuildadd's edit, commit, push,
// open the pull request. Waves of --wave repositories: the pull requests of a
// wave are opened first, then each is watched and merged only if green; the
// campaign pauses between waves and stops when the REST core budget is below
// --min-core. Every outcome is appended to --state, and a re-run skips what
// finished (merged, already, refused-shape).
//
// Outcomes: merged, already, skipped-claimed, refused-shape, failed-ci, failed.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/pagesroll"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// Outcomes.
const (
	Merged        = "merged"
	Already       = "already"
	SkippedClaim  = "skipped-claimed"
	RefusedShape  = "refused-shape"
	FailedCI      = "failed-ci"
	Failed        = "failed"
	branchName    = "pagesbuild-after-push"
	evidenceRun   = "https://github.com/go-redfish/docs/actions/runs/42268708918"
	referencePR   = "go-redfish/docs#11"
	commitSubject = "docs.yml: wait for the Pages build of the pushed gh-pages commit"
)

// Result is one repository's outcome, one line of the state file.
type Result struct {
	Repo   string `json:"repo"`
	Status string `json:"status"`
	PR     int    `json:"pr,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Runner runs a command (in dir when not empty) and returns its combined
// output and exit code; err is set only when it could not be started.
type Runner func(dir, name string, args ...string) (out string, code int, err error)

func execRunner(dir, name string, args ...string) (string, int, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Stdin = nil
	b, err := c.CombinedOutput()
	if err == nil {
		return string(b), 0, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return string(b), ee.ExitCode(), nil
	}
	return string(b), -1, err
}

// Config is what a campaign needs.
type Config struct {
	Workdir    string
	State      string
	Version    string
	Actionlint string
	Wave       int
	Pause      time.Duration
	MinCore    int
	CheckWait  time.Duration
	Run        Runner
	Sleep      func(time.Duration)
	Out        io.Writer
}

type pending struct {
	repo string
	pr   int
}

// Campaign processes repos and returns every result (including those already
// finished in an earlier run, which are not redone).
func Campaign(cfg Config, repos []string) ([]Result, error) {
	done, err := loadState(cfg.State)
	if err != nil {
		return nil, err
	}
	var todo []string
	for _, r := range repos {
		switch done[r].Status {
		case Merged, Already, RefusedShape:
			fmt.Fprintf(cfg.Out, "%s: %s earlier, not redone\n", r, done[r].Status)
		default:
			todo = append(todo, r)
		}
	}
	var results []Result
	record := func(r Result) {
		results = append(results, r)
		fmt.Fprintf(cfg.Out, "%s: %s %s\n", r.Repo, r.Status, r.Detail)
		if err := appendState(cfg.State, r); err != nil {
			fmt.Fprintf(cfg.Out, "WARNING: state file: %v\n", err)
		}
	}
	for i := 0; i < len(todo); i += cfg.Wave {
		if i > 0 {
			cfg.Sleep(cfg.Pause)
		}
		core, err := coreRemaining(cfg.Run)
		if err != nil {
			return results, fmt.Errorf("rate limit unreadable, stopping: %w", err)
		}
		if core < cfg.MinCore {
			return results, fmt.Errorf("REST core budget %d < %d, stopping before %s", core, cfg.MinCore, todo[i])
		}
		end := i + cfg.Wave
		if end > len(todo) {
			end = len(todo)
		}
		var open []pending
		for _, r := range todo[i:end] {
			res, p := prepare(cfg, r)
			if p == nil {
				release(cfg, r)
				record(res)
				continue
			}
			open = append(open, *p)
		}
		for _, p := range open {
			record(finish(cfg, p))
			release(cfg, p.repo)
		}
	}
	return results, nil
}

func release(cfg Config, repo string) { _, _, _ = cfg.Run("", "agentsync", "release", repo) }

func coreRemaining(run Runner) (int, error) {
	out, code, err := run("", "gh", "api", "rate_limit")
	if err != nil || code != 0 {
		return 0, fmt.Errorf("gh api rate_limit: code %d %v %s", code, err, strings.TrimSpace(out))
	}
	var v struct {
		Resources struct {
			Core struct {
				Remaining int `json:"remaining"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return 0, err
	}
	return v.Resources.Core.Remaining, nil
}

func fail(repo, format string, a ...any) Result {
	return Result{Repo: repo, Status: Failed, Detail: strings.TrimSpace(fmt.Sprintf(format, a...))}
}

func message(version string) string {
	return commitSubject + `

The deploy job of docs.yml now runs pagesbuild on the gh-pages commit it
pushed (only when something was pushed). GitHub's legacy "pages build and
deployment" sometimes stalls in updating_pages and times out, and a green
push is then not a published site; pagesbuild waits for a build of that
commit, requests a new one when needed, and fails the job if the site is
still not built.

Same change as ` + referencePR + ` (merge 1beaa7d), made by pagesbuildadd:
'pages: write' on the deploy job, 'id: push' and the pushed SHA as an output
of "Push gh-pages", and a "Wait for the Pages build" step running
pagesbuild@` + version + `. Proven in production by ` + evidenceRun + `:
pagesbuild requested a rebuild after a 5 minute stall and the commit was
built two minutes later.

Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>
`
}

func body(version string) string {
	return `Rolls out ` + referencePR + ` to this repository: .github/workflows/docs.yml only.

- the deploy job gets 'pages: write';
- "Push gh-pages" gets 'id: push' and writes the pushed SHA to its output;
- a new step runs ` + "`go run github.com/go-fleettools/fleettools/cmd/pagesbuild@" + version + "`" + ` on that SHA, only when something was pushed.

Why: GitHub's legacy Pages build of the gh-pages branch sometimes stalls and times out, and a green push is then not a published site. Evidence it works: ` + evidenceRun + ` (pagesbuild requested a rebuild after a 5 minute stall; the commit was built 2 minutes later).

The edit is made by ` + "`pagesbuildadd`" + ` (go-fleettools/fleettools), which refuses any deploy job that is not the shape #11 was proven on and runs actionlint on the result (clean). A merge does not exercise the new step: nothing is pushed to gh-pages when the site is unchanged, so no deploy is triggered to test it.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
`
}

// prepare claims, clones, edits, pushes and opens the pull request.
func prepare(cfg Config, repo string) (Result, *pending) {
	org, _, ok := strings.Cut(repo, "/")
	if !ok {
		return fail(repo, "not owner/repo"), nil
	}
	if out, code, err := cfg.Run("", "agentsync", "claim", "--note", "pagesbuild rollout (go-redfish/docs#11)", repo); err != nil || code != 0 {
		return Result{Repo: repo, Status: SkippedClaim, Detail: strings.TrimSpace(out)}, nil
	}
	sha, code, err := cfg.Run("", "gh", "api", "repos/"+repo+"/commits/main", "--jq", ".sha")
	sha = strings.TrimSpace(sha)
	if err != nil || code != 0 || len(sha) != 40 {
		return fail(repo, "cannot read main: %s", sha), nil
	}
	dir := filepath.Join(cfg.Workdir, org+"-docs-pagesbuild")
	if _, err := os.Stat(dir); err == nil {
		return fail(repo, "%s already exists; look at it and remove it", dir), nil
	}
	if out, code, err := cfg.Run("", "git", "clone", "-q", "https://github.com/"+repo, dir); err != nil || code != 0 {
		return fail(repo, "clone: %s", out), nil
	}
	head, code, err := cfg.Run(dir, "git", "rev-parse", "HEAD")
	if err != nil || code != 0 || strings.TrimSpace(head) != sha {
		return fail(repo, "clone is %s, main is %s", strings.TrimSpace(head), sha), nil
	}
	if out, code, err := cfg.Run(dir, "git", "switch", "-c", branchName); err != nil || code != 0 {
		return fail(repo, "branch: %s", out), nil
	}
	_, err = pagesroll.Edit(dir, cfg.Version, cfg.Actionlint)
	switch {
	case errors.Is(err, pagesroll.ErrAlready):
		return Result{Repo: repo, Status: Already, Detail: "docs.yml has pagesbuild"}, nil
	case errors.Is(err, pagesroll.ErrRefused):
		return Result{Repo: repo, Status: RefusedShape, Detail: strings.TrimSpace(err.Error())}, nil
	case err != nil:
		return fail(repo, "edit: %v", err), nil
	}
	commitFile := dir + ".commit.txt"
	bodyFile := dir + ".pr.md"
	if err := os.WriteFile(commitFile, []byte(message(cfg.Version)), 0o644); err != nil {
		return fail(repo, "%v", err), nil
	}
	if err := os.WriteFile(bodyFile, []byte(body(cfg.Version)), 0o644); err != nil {
		return fail(repo, "%v", err), nil
	}
	for _, c := range [][]string{
		{"git", "add", pagesroll.Workflow},
		{"git", "commit", "-q", "-F", commitFile},
		{"gitpush", "-q", "-u", "origin", branchName},
	} {
		if out, code, err := cfg.Run(dir, c[0], c[1:]...); err != nil || code != 0 {
			return fail(repo, "%s: %s", c[0]+" "+c[1], out), nil
		}
	}
	out, code, err := cfg.Run(dir, "gh", "pr", "create", "--repo", repo, "--base", "main", "--head", branchName,
		"--title", commitSubject, "--body-file", bodyFile)
	if err != nil || code != 0 {
		return fail(repo, "gh pr create: %s", out), nil
	}
	n, ok := prNumber(out)
	if !ok {
		return fail(repo, "no pull request URL in: %s", out), nil
	}
	return Result{}, &pending{repo: repo, pr: n}
}

// prNumber reads the number from the URL `gh pr create` prints.
func prNumber(out string) (int, bool) {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "https://github.com/") && strings.Contains(l, "/pull/") {
			n, err := strconv.Atoi(l[strings.LastIndex(l, "/")+1:])
			return n, err == nil && n > 0
		}
	}
	return 0, false
}

// finish waits for the checks of an opened pull request and merges it if,
// and only if, they all passed.
func finish(cfg Config, p pending) Result {
	n := strconv.Itoa(p.pr)
	// Let GitHub register the checks: `gh pr checks` reports "no checks"
	// (exit 1) in the first seconds, which is not a failure.
	var out string
	var code int
	for attempt := 0; attempt < 6; attempt++ {
		var err error
		out, code, err = cfg.Run("", "gh", "pr", "checks", n, "--repo", p.repo, "--watch", "--fail-fast")
		if err != nil {
			return fail(p.repo, "gh pr checks: %v", err)
		}
		if code != 1 || !strings.Contains(out, "no checks reported") {
			break
		}
		cfg.Sleep(20 * time.Second)
	}
	if code != 0 {
		return Result{Repo: p.repo, Status: FailedCI, PR: p.pr, Detail: "checks not green: " + lastLines(out, 4)}
	}
	if out, code, err := cfg.Run("", "ghmerge", p.repo, n); err != nil || code != 0 {
		return Result{Repo: p.repo, Status: Failed, PR: p.pr, Detail: "ghmerge: " + lastLines(out, 4)}
	}
	return Result{Repo: p.repo, Status: Merged, PR: p.pr}
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, " | ")
}

func loadState(path string) (map[string]Result, error) {
	m := map[string]Result{}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Result
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		m[r.Repo] = r
	}
	return m, sc.Err()
}

func appendState(path string, r Result) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(r)
	_, err = fmt.Fprintf(f, "%s\n", b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Summary counts the results by outcome and names every one that is not merged.
func Summary(rs []Result) string {
	count := map[string]int{}
	var b strings.Builder
	for _, r := range rs {
		count[r.Status]++
		if r.Status != Merged {
			fmt.Fprintf(&b, "  %s: %s (PR %d) %s\n", r.Repo, r.Status, r.PR, r.Detail)
		}
	}
	return fmt.Sprintf("merged %d, already %d, skipped-claimed %d, refused-shape %d, failed-ci %d, failed %d\n%s",
		count[Merged], count[Already], count[SkippedClaim], count[RefusedShape], count[FailedCI], count[Failed], b.String())
}

// RootCmd builds the command; tests drive it with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	cfg := Config{Run: execRunner, Sleep: time.Sleep}
	cmd := &cobra.Command{
		Use:   "pagesbuildcampaign [flags] <owner/repo>...",
		Short: "roll the pagesbuild step out to docs repositories, one pull request each, merged on green",
		Long: `pagesbuildcampaign claims each repository, clones it under --workdir, applies
pagesbuildadd's edit, pushes a branch with gitpush, opens a pull request, and merges it with
ghmerge once every check is green. Repositories another session holds are skipped, a workflow
that is not the known shape is refused, a red pull request is left open. See the package comment.`,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Out = cmd.OutOrStdout()
			if cfg.Wave < 1 {
				return fmt.Errorf("--wave must be at least 1")
			}
			rs, err := Campaign(cfg, args)
			fmt.Fprint(cmd.OutOrStdout(), Summary(rs))
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&cfg.Workdir, "workdir", filepath.Join(os.Getenv("HOME"), "Documents", "VCS", "GIT", "localhost"), "durable directory holding the clones")
	f.StringVar(&cfg.State, "state", "", "JSON-lines file recording each outcome (default <workdir>/pagesbuild-campaign.jsonl)")
	f.StringVar(&cfg.Version, "version", pagesroll.DefaultVersion, "the fleettools release pagesbuild is pinned to")
	f.StringVar(&cfg.Actionlint, "actionlint", "actionlint", "actionlint binary (empty: skip)")
	f.IntVar(&cfg.Wave, "wave", 10, "repositories per wave")
	f.DurationVar(&cfg.Pause, "pause", 2*time.Minute, "pause between waves")
	f.IntVar(&cfg.MinCore, "min-core", 1000, "stop when the REST core budget is below this")
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if cfg.State == "" {
			cfg.State = filepath.Join(cfg.Workdir, "pagesbuild-campaign.jsonl")
		}
		return nil
	}
	return cmd
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	if err := RootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		osExit(1)
	}
}
