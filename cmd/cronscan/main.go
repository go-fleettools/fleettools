// Command cronscan asks whether an organisation's SCHEDULED workflows
// actually fire, by counting the firings their cron expressions ask for
// against the `schedule` runs GitHub recorded.
//
//	cronscan go-pkgx --days 14
//
// # WHY IT EXISTS
//
// A scheduled job that never runs fails in the quietest way there is:
// nothing is red, nothing is logged, and a capability nobody invokes is a
// capability the fleet does not have. Every other scanner here reads what
// DID happen — prscan and redscan read runs and requests, wfaudit reads a
// workflow's security — and none of them can see an absence.
//
// ⛔ THE LISTING LIES BY TRUNCATION. On 2026-10-19 I believed go-pkgx's
// schedules were half broken: `gh run list --limit 6` on its versioncheck
// showed six runs, all `push` or `pull_request`, with no `schedule` among
// them. It was a busy afternoon of pushes and the scheduled runs were simply
// further down. cronscan said 14 of 14. Counting needs the whole list and a
// denominator; a glance gives neither.
//
// ⛔ AND THE DENOMINATOR MUST RESPECT THE WORKFLOW'S AGE. Asking "how many
// firings does a daily cron want in 14 days" answers 14 for a file that has
// existed for one. The first version of this printed `want 14 got 0` for
// three jobs merged the previous evening, beside `want 14 got 14` for the
// established ones — which reads as an organisation whose schedules are half
// dead. The window's floor is whichever is later, the span asked for or the
// workflow's own created_at.
//
// ⛔ AND ITS CEILING MUST RESPECT THE DELAY. A firing due two hours ago has
// not been missed on a service measured to run five and eight hours late; it
// is pending. Run at 07:18 UTC on 2026-10-20 without this, the pass reported
// `3 of 52 firing(s) did not happen` — three one-day-old daily jobs whose
// morning run had simply not arrived — and a scanner that is red every
// morning on a healthy fleet is a scanner nobody reads. So the window ends at
// now minus --grace (default 9h, from the 8h worst case measured here), and
// runs recorded inside the grace window still count: this errs toward
// believing the fleet, because the alternative errs toward crying wolf daily.
//
// # WHAT IT FOUND, AND THE FACT WORTH KNOWING
//
// Over 14 days go-pkgx asked for 49 firings and got 49. The schedules are
// reliable — but they arrive HOURS LATE: a job with `37 5 * * *` fired at
// 11:13 UTC, and a Monday `0 5 * * 1` at 12:56 UTC. So "my cron should have
// run by now" is wrong before about 14:00 UTC, and somebody checking the
// Actions tab in the morning concludes the job is dead. Measure at the end
// of the day, or over days.
//
// A commented-out cron is not counted: `# - cron: "0 6 * * *"` is how this
// fleet disables one, and counting it would report a workflow as failing to
// run a schedule nobody asked for.
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

// failure is a scan that could not complete (exit 1). Anything else out of
// the command is a usage error (exit 2).
type failure struct{ err error }

func (f *failure) Error() string { return f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

func failf(format string, a ...any) error { return &failure{fmt.Errorf(format, a...)} }

type workflow struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	State   string `json:"state"`
	ID      int64  `json:"id"`
	Created string `json:"created_at"`
}

type runRec struct {
	Created string `json:"created_at"`
}

const longHelp = `cronscan counts the scheduled firings an organisation's workflows ASK FOR
against the schedule-triggered runs GitHub recorded, so that a cron which
silently never fires stops being invisible.

For each active workflow it reads the cron expressions out of the workflow
file, counts their firings in the window, and counts the recorded runs. The
window starts at the workflow's own created_at when that is later than the
span asked for, because a daily cron cannot have fired fourteen times in a
file that is one day old.

Scheduled runs on GitHub arrive late — measured at five and eight hours on
this fleet — so the window ENDS --grace hours before now (default 9h): a
firing due two hours ago is pending, not missed, and counting it as missed
makes this red every morning on a healthy fleet. Runs recorded inside the
grace window still count.

Exits 1 when fewer firings were recorded than asked for, 2 on a usage error.`

func RootCmd() *cobra.Command {
	var days int
	var grace time.Duration
	c := &cobra.Command{
		Use:           "cronscan ORG [flags]",
		Short:         "Did the scheduled workflows actually fire?",
		Long:          longHelp,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if days <= 0 {
			return errors.New("--days must be a positive number of days")
		}
		if grace < 0 {
			return errors.New("--grace must not be negative")
		}
		return run(cmd, args[0], days, grace)
	}
	c.Flags().IntVarP(&days, "days", "d", 14, "how many days back to count")
	c.Flags().DurationVarP(&grace, "grace", "g", 9*time.Hour,
		"ignore firings due less than this long ago; GitHub's schedules were measured 8h late here")
	return c
}

func execute(c *cobra.Command) int {
	err := c.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.ErrOrStderr(), "cronscan: %v\n", err)
	var f *failure
	if errors.As(err, &f) {
		return 1
	}
	return 2
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	osExit(execute(RootCmd()))
}

func run(cmd *cobra.Command, org string, days int, grace time.Duration) error {
	since := now().AddDate(0, 0, -days)
	// ⛔ The ceiling, not now: a firing due inside the grace window has not
	// been missed, it has not arrived yet.
	until := now().Add(-grace)
	rs, err := repos(org)
	if err != nil {
		return failf("listing %s: %w", org, err)
	}
	out := cmd.OutOrStdout()
	var want, got, scanned int
	for _, r := range rs {
		wfs, err := workflowsOf(r.FullName)
		if err != nil {
			return failf("%s: %w", r.FullName, err)
		}
		for _, w := range wfs {
			if w.State != "active" {
				continue
			}
			crons := cronsIn(r.FullName, w.Path)
			if len(crons) == 0 {
				continue
			}
			scanned++
			from, age := windowFrom(since, w.Created)
			n := 0
			for _, c := range crons {
				n += firings(c, from, until)
			}
			recorded, err := scheduledRuns(r.FullName, w.ID, from)
			if err != nil {
				return failf("%s %s: %w", r.FullName, w.Name, err)
			}
			want += n
			got += recorded
			fmt.Fprintf(out, "%-24s %-42s want %3d  got %3d%s\n",
				short(r.FullName), trim(w.Name, 42), n, recorded, age)
		}
	}
	// ⛔ NEVER A PERCENTAGE OF NOTHING, and never silence either. A run that
	// found no cron at all must say so in words: "100%" of zero and "every
	// schedule fired" are the same string to a reader and opposite facts.
	if scanned == 0 {
		fmt.Fprintf(out, "no active workflow in %s declares a cron\n", org)
		return nil
	}
	fmt.Fprintf(out, "\nover %d day(s), up to %s ago: %d scheduled firing(s) asked for, %d recorded\n",
		days, grace, want, got)
	if got < want {
		return failf("%d of %d firing(s) did not happen, and they were already %s overdue when this ran", want-got, want, grace)
	}
	return nil
}

// The seams. now, because a test that asks how many Mondays are in a window
// cannot depend on which day it is run; gh and repos, because the claim worth
// testing is not that the arithmetic is right — that is tested on its own —
// but that the arithmetic the flags ask for is the arithmetic run performs.
// A ceiling the counting loop does not use passes every arithmetic test ever
// written about it.
var (
	now   = func() time.Time { return time.Now().UTC() }
	gh    = fleet.GH
	repos = fleet.Repos
)

// windowFrom is the floor of the counting window, and the note explaining it.
func windowFrom(since time.Time, created string) (time.Time, string) {
	t, err := time.Parse(time.RFC3339, created)
	if err != nil || !t.After(since) {
		return since, ""
	}
	return t, fmt.Sprintf("  (added %s ago)", now().Sub(t).Round(time.Hour))
}

func workflowsOf(repo string) ([]workflow, error) {
	b, err := gh("api", "repos/"+repo+"/actions/workflows?per_page=100", "--jq", ".workflows")
	if err != nil {
		return nil, err
	}
	var out []workflow
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("reading the workflow list: %w", err)
	}
	return out, nil
}

func scheduledRuns(repo string, id int64, from time.Time) (int, error) {
	b, err := gh("api",
		fmt.Sprintf("repos/%s/actions/workflows/%d/runs?per_page=100&event=schedule", repo, id),
		"--jq", ".workflow_runs")
	if err != nil {
		return 0, err
	}
	var runs []runRec
	if err := json.Unmarshal(b, &runs); err != nil {
		return 0, fmt.Errorf("reading the run list: %w", err)
	}
	n := 0
	for _, r := range runs {
		if t, err := time.Parse(time.RFC3339, r.Created); err == nil && t.After(from) {
			n++
		}
	}
	return n, nil
}

// cronsIn reads the cron expressions out of a workflow file.
//
// A file that cannot be read yields none rather than an error: a workflow
// listed by the API and absent from the default branch is a real state —
// a run from a deleted branch — and it is not what this command is about.
func cronsIn(repo, path string) []string {
	b, err := gh("api", "repos/"+repo+"/contents/"+path, "--jq", ".content")
	if err != nil {
		return nil
	}
	body, err := decodeBase64(string(b))
	if err != nil {
		return nil
	}
	return cronLines(body)
}

// cronLines is the parsing half, split out so a test can hand it a file
// instead of a repository.
func cronLines(body string) []string {
	var crons []string
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "- cron:") {
			continue
		}
		v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "- cron:")), `"'`)
		if v != "" {
			crons = append(crons, v)
		}
	}
	return crons
}

// firings counts how many times a cron expression fires in a window.
//
// Minute by minute rather than clever: the window is days, so this is a few
// hundred thousand comparisons, and a hand-rolled "every N days" shortcut is
// exactly where a counter like this goes quietly wrong. A counter that is
// wrong reports a crisis and an all-clear with equal confidence.
func firings(spec string, from, to time.Time) int {
	f := strings.Fields(spec)
	if len(f) != 5 {
		return 0
	}
	n := 0
	for t := from.Truncate(time.Minute); t.Before(to); t = t.Add(time.Minute) {
		if match(f[0], t.Minute()) && match(f[1], t.Hour()) &&
			match(f[2], t.Day()) && match(f[3], int(t.Month())) &&
			matchDow(f[4], int(t.Weekday())) {
			n++
		}
	}
	return n
}

func match(field string, v int) bool {
	if field == "*" {
		return true
	}
	for _, part := range strings.Split(field, ",") {
		if step, ok := strings.CutPrefix(part, "*/"); ok {
			if s, err := strconv.Atoi(step); err == nil && s > 0 && v%s == 0 {
				return true
			}
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, e1 := strconv.Atoi(lo)
			b, e2 := strconv.Atoi(hi)
			if e1 == nil && e2 == nil && v >= a && v <= b {
				return true
			}
			continue
		}
		if a, err := strconv.Atoi(part); err == nil && a == v {
			return true
		}
	}
	return false
}

// matchDow treats 7 as Sunday, as cron does and as Go's Weekday does not.
func matchDow(field string, v int) bool {
	return match(field, v) || (v == 0 && match(field, 7))
}

func decodeBase64(s string) (string, error) {
	s = strings.Trim(strings.ReplaceAll(strings.TrimSpace(s), "\n", ""), `"`)
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}

func short(full string) string {
	if _, name, ok := strings.Cut(full, "/"); ok {
		return name
	}
	return full
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
