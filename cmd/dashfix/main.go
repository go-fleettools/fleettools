// Migrates `dependencyDashboard: true` to `dependencyDashboard: { enabled: true }`
// on the branch of a RED Renovate pull request in an organisation's .github
// repository, and lands it (go-fleettools/fleettools#305).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/rollout"
	"github.com/spf13/cobra"
)

const longHelp = `Fixes one line of an organisation's Renovate runner config on the branch of a
red Renovate pull request, then waits for CI and merges it.

  dependencyDashboard: true   ->   dependencyDashboard: { enabled: true }

Upstream Renovate migrated that key to an object; the validator these
repositories run (renovate@latest, --strict) now exits 1 on the boolean, so every
Renovate pull request in them is red for a reason its own diff does not show.

Targets are spelled owner/repo#number. For each one, in order:

  1. the REST budget must hold --reserve requests and the trailing hour fewer
     than --max pull requests, or the run STOPS;
  2. the pull request must be open and have a FAILED check; a green or
     pending one is skipped, never touched;
  3. agentsync claim; a repository somebody else holds is skipped and listed;
  4. config.js is edited on the pull request's own branch. A repository whose
     workflows carry RENOVATE_VERSION (the two-halves pin of #305's second
     comment) is skipped and listed: aligning it is a different change;
  5. commit, gitpush, prwait, ghmerge — merge only when prwait says green.

Pull requests are handled one at a time, so at most one is in flight.

⛔ Any command that fails or refuses STOPS the whole run with status 1 and names
it. It is never retried another way. A skip is not a failure.

Exit status: 0 every target merged or skipped, 1 something was refused, 2 the
run could not start or stopped at a limit.`

type options struct {
	root    string
	ledger  string
	max     int
	reserve int
	dryRun  bool
	now     func() time.Time
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command. Tests drive it through run, below.
func RootCmd() *cobra.Command {
	home, _ := os.UserHomeDir()
	cfg, _ := os.UserConfigDir()
	o := options{now: time.Now}
	c := cli.New("dashfix [flags] <owner/repo#number>...",
		"Migrate dependencyDashboard on red Renovate pull requests and merge them", longHelp)
	c.Flags().StringVar(&o.root, "root", filepath.Join(home, "Documents", "VCS", "GIT", "localhost"),
		"directory holding the clones, named <owner>-<repo>")
	c.Flags().StringVar(&o.ledger, "ledger", filepath.Join(cfg, "dashfix", "ledger.txt"),
		"file recording one merged pull request per line, for the hourly cap")
	c.Flags().IntVar(&o.max, "max", 20, "pull requests allowed per trailing hour")
	c.Flags().IntVar(&o.reserve, "reserve", 1000, "REST requests that must remain")
	c.Flags().BoolVar(&o.dryRun, "dry-run", false, "judge and edit nothing, say what would happen")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		return cli.FromStatus(sweep(cli.Out(cmd), cli.Err(cmd), args, o))
	}
	return c
}

func run(stdout, stderr io.Writer, args []string) int {
	return cli.Drive(RootCmd(), args, stdout, stderr)
}

var targetRe = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#([0-9]+)$`)

func sweep(stdout, stderr io.Writer, args []string, o options) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "dashfix: name targets as owner/repo#number")
		return 2
	}
	for _, a := range args {
		if !targetRe.MatchString(a) {
			fmt.Fprintf(stderr, "dashfix: %q is not owner/repo#number\n", a)
			return 2
		}
	}
	merged, skipped := 0, 0
	for _, a := range args {
		m := targetRe.FindStringSubmatch(a)
		repo, number := m[1]+"/"+m[2], m[3]

		if st := budget(stderr, o); st != 0 {
			return st
		}
		res, err := fix(stdout, repo, number, o)
		switch {
		case err != nil:
			fmt.Fprintf(stderr, "dashfix: STOPPED at %s#%s: %v\n", repo, number, err)
			fmt.Fprintf(stdout, "totals: merged %d, skipped %d, refused 1\n", merged, skipped)
			return 1
		case res == "":
			merged++
			fmt.Fprintf(stdout, "merged   %s#%s\n", repo, number)
		default:
			skipped++
			fmt.Fprintf(stdout, "skipped  %s#%s: %s\n", repo, number, res)
		}
	}
	fmt.Fprintf(stdout, "totals: merged %d, skipped %d, refused 0\n", merged, skipped)
	return 0
}

func (o options) ledgerFile() rollout.Ledger { return rollout.Ledger{Path: o.ledger, Now: o.now} }

func budget(stderr io.Writer, o options) int {
	return rollout.Budget(stderr, "dashfix", o.ledgerFile(), o.reserve, o.max)
}

func call(dir, name string, args ...string) (string, error) { return rollout.Call(dir, name, args...) }

var (
	dashBool   = regexp.MustCompile(`(?m)^(\s*dependencyDashboard:\s*)true(\s*,)`)
	dashObject = regexp.MustCompile(`(?m)^\s*dependencyDashboard:\s*\{`)
)

// migrate rewrites the boolean to the object. changed is false when the file
// already carries the object; a file with neither shape is an error, because
// "nothing to do" and "could not find the key" must not read alike.
func migrate(src string) (out string, changed bool, err error) {
	if dashObject.MatchString(src) {
		return src, false, nil
	}
	if !dashBool.MatchString(src) {
		return "", false, fmt.Errorf("no `dependencyDashboard: true,` line found")
	}
	return dashBool.ReplaceAllString(src, "${1}{ enabled: true }${2}"), true, nil
}

type check struct {
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

type prView struct {
	State       string  `json:"state"`
	HeadRefName string  `json:"headRefName"`
	Checks      []check `json:"statusCheckRollup"`
}

func (p prView) red() bool {
	for _, c := range p.Checks {
		if c.Conclusion == "FAILURE" || c.State == "FAILURE" || c.State == "ERROR" {
			return true
		}
	}
	return false
}

// fix returns "" when the pull request was merged, a reason when it was
// skipped, and an error when a command failed or refused.
func fix(stdout io.Writer, repo, number string, o options) (string, error) {
	out, err := call("", "gh", "pr", "view", number, "-R", repo, "--json", "state,headRefName,statusCheckRollup")
	if err != nil {
		return "", err
	}
	var pv prView
	if err := json.Unmarshal([]byte(out), &pv); err != nil || pv.HeadRefName == "" {
		return "", fmt.Errorf("gh pr view answered something unreadable")
	}
	if pv.State != "OPEN" {
		return "not open", nil
	}
	if !pv.red() {
		return "not red", nil
	}
	if o.dryRun {
		return "dry run: red, would claim and edit", nil
	}
	if out, err := call("", "agentsync", "claim", "--note",
		"dashfix: dependencyDashboard object (fleettools#305)", repo); err != nil {
		return "claim refused: " + strings.TrimSpace(out), nil
	}

	dir := filepath.Join(o.root, strings.Replace(repo, "/", "-", 1))
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err := call("", "git", "clone", "--branch", pv.HeadRefName,
			"https://github.com/"+repo+".git", dir); err != nil {
			return "", err
		}
	} else {
		if st, err := call(dir, "git", "status", "--porcelain"); err != nil || strings.TrimSpace(st) != "" {
			return "", fmt.Errorf("clone %s is dirty or unreadable; not touching it", dir)
		}
		if _, err := call(dir, "git", "fetch", "origin", pv.HeadRefName); err != nil {
			return "", err
		}
		if _, err := call(dir, "git", "switch", "-C", pv.HeadRefName, "origin/"+pv.HeadRefName); err != nil {
			return "", err
		}
	}

	if pinned(dir) {
		return "RENOVATE_VERSION pin present: align both halves separately", nil
	}
	cfg := filepath.Join(dir, "config.js")
	src, err := os.ReadFile(cfg)
	if err != nil {
		return "", err
	}
	next, changed, err := migrate(string(src))
	if err != nil {
		return "", err
	}
	if !changed {
		return "config.js already carries the object", nil
	}
	if err := os.WriteFile(cfg, []byte(next), 0o644); err != nil {
		return "", err
	}
	msg := filepath.Join(dir, ".git", "dashfix-msg")
	body := "fix: write dependencyDashboard as an object\n\n" +
		"Upstream Renovate migrated the key; renovate-config-validator --strict exits 1 on the boolean.\n" +
		"Refs go-fleettools/fleettools#305\n\n" +
		"Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>\n"
	if err := os.WriteFile(msg, []byte(body), 0o644); err != nil {
		return "", err
	}
	for _, step := range [][]string{
		{"git", "add", "config.js"},
		{"git", "commit", "-F", msg},
		{"gitpush", "origin", pv.HeadRefName},
	} {
		if _, err := call(dir, step[0], step[1:]...); err != nil {
			return "", err
		}
	}
	sha, err := call(dir, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if err := rollout.HeadIs(repo, number, strings.TrimSpace(sha)); err != nil {
		return "", err
	}
	if _, err := call(dir, "prwait", "-repo", repo, number); err != nil {
		return "", fmt.Errorf("not green, not merging: %v", err)
	}
	if _, err := call(dir, "ghmerge", repo, number); err != nil {
		return "", err
	}
	if err := o.ledgerFile().Record(repo + "#" + number); err != nil {
		return "", err
	}
	return "", nil
}

// pinned reports whether a workflow carries the RENOVATE_VERSION half of the
// two-halves pin.
func pinned(dir string) bool {
	files, _ := filepath.Glob(filepath.Join(dir, ".github", "workflows", "*.yml"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && bytes.Contains(b, []byte("RENOVATE_VERSION")) {
			return true
		}
	}
	return false
}
