package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// config is every knob of one run.
type config struct {
	version, from, list, state         string
	apply, claim, follow, guard, gomod bool
	prefilter, noAlias                 bool
	pause, quotaWait, ciTimeout        time.Duration
	limit, passes                      int
}

// exitErr carries the process status out of RunE. msg, when set, is printed.
type exitErr struct {
	code int
	msg  string
}

func (e *exitErr) Error() string { return e.msg }

const longHelp = `gopin pins the Go version in a repository's GitHub workflows, and moves a
pin that is already explicit after a Go security release.

  gopin --from 1.27.2 --version 1.27.3 --list repos.txt                  report
  gopin --from 1.27.2 --version 1.27.3 --list repos.txt --apply          open the pull requests
  gopin --from 1.27.2 --version 1.27.3 --list repos.txt --apply --claim --follow --state S.json

A campaign flag set (--claim --follow --state) makes a run survive what
killed the shell-loop campaign of 2026-10-10:

  --claim   agentsync claim before editing a repository, release after; a
            repository another session holds is skipped and REPORTED.
  --guard   a REAL API call before each repository. GitHub's quota is
            per account and "gh api rate_limit" can read 5000/5000 while
            every real call is a 403, so that endpoint is never trusted. A
            quota 403 is "wait", never a result: it sleeps (to the reset, at
            most --quota-wait) and retries, and a repository that never got
            an answer is DEFERRED to a later --passes pass, never counted done.
  --follow  after opening, wait for the checks the way prwait does (no checks
            is not a pass) and merge with ghmerge when green. Red pull
            requests are listed in the summary.
  --state   a JSON file (repository -> status). A rerun skips finished
            repositories and resumes the pull requests still being followed.
  --gomod   also move CI that reads its version from go.mod (go-version-file):
            raises a go directive that is EXACTLY --from, runs go mod tidy,
            commits go.mod and go.sum by name.

The single-dash spellings (-from, -version, -apply ...) still work.

Exits 0 when nothing is left to do, 1 when something is unread, deferred, red
or failed, 2 on a usage error.`

// longFlags are the flags that used to be spelled with one dash (package flag).
var longFlags = map[string]bool{
	"version": true, "from": true, "list": true, "apply": true, "pause": true, "limit": true,
	"claim": true, "follow": true, "guard": true, "gomod": true, "state": true,
	"prefilter": true, "no-alias": true, "quota-wait": true, "ci-timeout": true, "passes": true, "quota-retries": true,
}

// legacyArgs rewrites `-from x` to `--from x`. pflag would read `-claim` as the
// shorthands c, l, a, i, m, and every script and note written for the flag
// package would break silently into something else.
func legacyArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
			name, _, _ := strings.Cut(a[1:], "=")
			if longFlags[name] {
				a = "-" + a
			}
		}
		out = append(out, a)
	}
	return out
}

// RootCmd is the command, for tests to drive with SetArgs/SetOut/SetErr.
func RootCmd() *cobra.Command {
	var cfg config
	c := &cobra.Command{
		Use:           "gopin --version V [--from X] [--list FILE] [--apply] [flags]",
		Short:         "Pin the Go version in workflows, or move an explicit pin after a security release",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		return runConfig(cfg, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	}
	fl := c.Flags()
	fl.StringVar(&cfg.version, "version", "", "the Go version to pin, e.g. 1.27.1 (required)")
	fl.StringVar(&cfg.from, "from", "", "also move workflows pinned to EXACTLY this version to --version, e.g. --from 1.27.1 --version 1.27.2 after a Go security release; an exact match, so a repository held back on purpose at some other version is not a candidate")
	fl.StringVar(&cfg.list, "list", "", "file of org/repo lines, # comments; default: read stdin")
	fl.BoolVar(&cfg.apply, "apply", false, "open pull requests; without it this only reports")
	fl.DurationVar(&cfg.pause, "pause", 3*time.Second, "wait this long between repositories; GitHub's SECONDARY limit brakes a burst and waiting is the whole remedy")
	fl.IntVar(&cfg.limit, "limit", 0, "stop after this many repositories that needed a change (0: no limit)")
	fl.BoolVar(&cfg.claim, "claim", false, "agentsync claim each repository before editing it, release after; skip (and report) one another session holds")
	fl.BoolVar(&cfg.guard, "guard", false, "make a real API call before each repository and wait out a quota 403 (implied by --follow)")
	fl.BoolVar(&cfg.follow, "follow", false, "after opening, wait for checks like prwait (no checks is not a pass) and merge with ghmerge when green")
	fl.StringVar(&cfg.state, "state", "", "resumable state file (repository -> status); a rerun skips finished repositories")
	fl.BoolVar(&cfg.gomod, "gomod", false, "with --from: also raise a go.mod read through go-version-file whose go directive is exactly --from, and tidy")
	fl.BoolVar(&cfg.prefilter, "prefilter", false, "print only the listed repositories whose workflows name something to move, read through GraphQL (its own budget, 20 repositories a call) instead of ~6 REST calls each; a repository it cannot read is passed through, never dropped")
	fl.BoolVar(&cfg.noAlias, "no-alias", false, "leave `stable` aliases alone: a security-release campaign moves explicit pins, and pinning an alias is a different change with a different review")
	fl.DurationVar(&cfg.quotaWait, "quota-wait", 5*time.Minute, "longest sleep after a quota 403 before retrying")
	fl.IntVar(&quotaRetries, "quota-retries", 12, "how many quota sleeps one API call may take before the repository is deferred (0: report the 403 as an error)")
	fl.IntVar(&cfg.passes, "passes", 3, "how many passes over repositories deferred by the quota")
	fl.DurationVar(&cfg.ciTimeout, "ci-timeout", 30*time.Minute, "give up waiting for one pull request's checks after this")
	return c
}

// execute runs c and turns the outcome into a status.
func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	var ee *exitErr
	if errors.As(err, &ee) {
		if ee.msg != "" {
			fmt.Fprintf(c.ErrOrStderr(), "gopin: %s\n", ee.msg)
		}
		return ee.code
	}
	fmt.Fprintf(c.ErrOrStderr(), "gopin: %v\n", err)
	return 2
}

func main() {
	// Every command here does this first: a binary built from an older
	// checkout than HEAD says so rather than quietly doing an older thing.
	// It matters more for this one than most, since it writes to other
	// people's repositories.
	fleet.WarnIfStale(os.Stderr)
	osExit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is the testable entry: arguments in, status out.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c := RootCmd()
	c.SetArgs(legacyArgs(args))
	c.SetIn(stdin)
	c.SetOut(stdout)
	c.SetErr(stderr)
	return execute(c)
}

// runConfig validates cfg and sweeps.
func runConfig(cfg config, stdin io.Reader, stdout, stderr io.Writer) error {
	usage := func(format string, a ...any) error { return &exitErr{2, fmt.Sprintf(format, a...)} }
	if cfg.version == "" {
		return usage("--version is required: there is no safe default, and a default would be the same unreviewable choice as `stable`")
	}
	target, err := parseGoVersion(cfg.version)
	if err != nil {
		return usage("%v", err)
	}
	if cfg.from != "" {
		fromV, err := parseGoVersion(cfg.from)
		if err != nil {
			return usage("--from %v", err)
		}
		if cfg.from == cfg.version {
			return usage("--from and --version name the same version, so there is nothing to move")
		}
		// ⛔ FORWARD ONLY. The reason to move an explicit pin is a release
		// that fixes something; moving one BACKWARDS is a different act and
		// not one to perform across a fleet by accident.
		if target.olderThan(fromV) {
			return usage("--version %s is older than --from %s; this moves pins forward, and going back is not a sweep", cfg.version, cfg.from)
		}
	}
	if cfg.gomod && cfg.from == "" {
		return usage("--gomod needs --from: it raises a go directive that is exactly that version")
	}
	if (cfg.claim || cfg.follow) && !cfg.apply {
		return usage("--claim and --follow act on pull requests: they need --apply")
	}
	if cfg.passes < 1 {
		cfg.passes = 1
	}
	if cfg.follow {
		cfg.guard = true
	}
	repos, err := readList(cfg.list, stdin)
	if err != nil {
		return usage("%v", err)
	}
	// A sweep with an empty corpus reports "nothing to do" exactly like one
	// that read everything.
	if len(repos) == 0 {
		return &exitErr{1, "the list is empty — that is a failure, not a clean fleet"}
	}
	if cfg.prefilter {
		if cfg.apply || cfg.claim || cfg.follow {
			return usage("--prefilter only reads: it cannot be combined with --apply, --claim or --follow")
		}
		quotaWaitMax = cfg.quotaWait
		return prefilter(cfg, repos, stdout, stderr)
	}
	st, err := loadState(cfg.state, cfg.from, cfg.version)
	if err != nil {
		return usage("%v", err)
	}
	if code := (&sweeper{cfg: cfg, target: target, out: stdout, st: st}).sweep(repos); code != 0 {
		return &exitErr{code: code}
	}
	return nil
}
