// Command flakescan counts a named test's failures across a repository's CI
// history, by lane, with a denominator.
//
// ⛔ IT EXISTS BECAUSE THE SWEEP WAS DONE BY HAND THREE TIMES AND GAVE THREE
// ANSWERS. go-crdt/collab#204 is one flake, sampled on 2026-10-03 over "commits
// on main", on 2026-11-15 over "RED runs on main", and once over "the pull
// request I happened to be on". Each answered a different question, and the
// corrections recorded in that issue are the evidence: the commit count came
// out 7, then 8, then 6, then 9, and `conclusion` is EMPTY for a run that has
// not finished, so a filter of `!= "success"` counts queued runs as failures.
//
// ⛔ AND THE POPULATION IS THE WHOLE DIFFICULTY, not the counting. Measured on
// 2026-11-19, run 39069166645 of go-crdt/collab:
//
//	attempt 2, conclusion success, event pull_request
//	attempts/1/jobs -> one job not success: "test (ubuntu-latest)" failure
//
// That occurrence is invisible twice over -- it is not on the default branch,
// and the run it lives in is GREEN today, because somebody re-ran the job. A
// flake is precisely the failure that gets re-run, so "N failures in M runs"
// is a floor on the rate rather than the rate, and a census that reads only
// runs whose conclusion is `failure` cannot see the thing it is counting.
//
// So this reads RUNS x ATTEMPTS, prints the lane, and refuses to read a log it
// could not fetch as an absence.
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// A run is the subset of a workflow run this census needs.
type run struct {
	ID         int64     `json:"id"`
	RunAttempt int       `json:"run_attempt"`
	Event      string    `json:"event"`
	HeadBranch string    `json:"head_branch"`
	HeadSHA    string    `json:"head_sha"`
	Conclusion string    `json:"conclusion"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
}

// A job is one lane of one attempt.
type job struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Conclusion string    `json:"conclusion"`
	StartedAt  time.Time `json:"started_at"`
	Steps      []struct {
		Name       string `json:"name"`
		Conclusion string `json:"conclusion"`
	} `json:"steps"`
}

// An occurrence is one failure of the named test, bound to where it happened.
type occurrence struct {
	When     time.Time
	Run      int64
	Attempt  int
	Attempts int
	Event    string
	Branch   string
	SHA      string
	Lane     string
	Step     string
	Lines    []string
}

// unread is a log the census could not read. It is a first-class result, not a
// zero: see the package comment.
type unread struct {
	Run     int64
	Attempt int
	Lane    string
	Why     string
}

// needsLog reports whether this job's log can hold a test failure.
//
// ⛔ A FAILED STEP INSIDE A SUCCESSFUL JOB IS THE CASE THAT LOOKS LIKE NOTHING.
// `continue-on-error: true` leaves the job's own conclusion `success` while a
// step under it failed, so judging by the job alone would skip exactly the lane
// that was configured not to complain. The steps arrive with the jobs listing,
// so asking costs no extra call.
func needsLog(j job) bool {
	if j.Conclusion != "success" && j.Conclusion != "skipped" && j.Conclusion != "" {
		return true
	}
	for _, s := range j.Steps {
		if s.Conclusion == "failure" {
			return true
		}
	}
	return false
}

// failedStep names the step a reader should open, or "" when the job itself is
// what failed.
func failedStep(j job) string {
	for _, s := range j.Steps {
		if s.Conclusion == "failure" {
			return s.Name
		}
	}
	return ""
}

// stamped strips the RFC3339 timestamp GitHub puts in front of every line of a
// job log, and reports whether there was one.
//
// Without this, every line of a downloaded log is "more indented" than the one
// before by the width of a timestamp, and the block-collecting below reads the
// whole log as one failure.
// ⚠ `i <= 0` rather than `i < 0` is a FAST PATH and nothing more: a line
// beginning with a space gives i == 0, and `time.Parse` of the empty string
// fails, so both forms answer (line, false). Boundary mutation moves it to
// `i < 0` and no assertion can redden -- the two branches share their answer.
// Said here because an unfalsifiable guard looks exactly like an untested one.
func stamped(line string) (string, bool) {
	i := strings.IndexByte(line, ' ')
	if i <= 0 {
		return line, false
	}
	if _, err := time.Parse(time.RFC3339Nano, line[:i]); err != nil {
		return line, false
	}
	return line[i+1:], true
}

// failureBlocks returns one block per failure of test in log: the `--- FAIL`
// line and the indented lines under it.
//
// A subtest answers for its parent, the way `go test` reports it and the way
// failset reads it: `--- FAIL: TestA/case` is a failure of TestA. The converse
// is not true, so a request for TestA/case does NOT match a bare TestA.
func failureBlocks(log, test string, maxLines int) [][]string {
	var out [][]string
	var cur []string
	curIndent := -1
	flush := func() {
		if cur != nil {
			out = append(out, cur)
			cur = nil
			curIndent = -1
		}
	}
	for _, raw := range strings.Split(log, "\n") {
		line := strings.TrimRight(raw, "\r")
		line, _ = stamped(line)
		trimmed := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trimmed)
		if rest, ok := cutPrefix(trimmed, "--- FAIL: "); ok {
			name := rest
			if i := strings.IndexAny(name, " \t("); i >= 0 {
				name = name[:i]
			}
			mine := name == test || strings.HasPrefix(name, test+"/")
			// ⛔ A FAILING SUBTEST IS NOT A SECOND OCCURRENCE. `go test` prints
			// the parent's `--- FAIL: TestA` and, indented under it,
			// `--- FAIL: TestA/case`. Both match a request for TestA, so
			// starting a block at each counted ONE failure as two -- in a tool
			// whose entire output is a count. Deeper than the open block means
			// inside it: a continuation line, not a new failure.
			if mine && cur != nil && indent > curIndent {
				if len(cur) < maxLines {
					cur = append(cur, trimmed)
				}
				continue
			}
			if mine {
				flush()
				cur = []string{trimmed}
				curIndent = indent
				continue
			}
			// A different test's failure ends this block: its own lines are
			// not ours to report.
			flush()
			continue
		}
		if cur == nil {
			continue
		}
		if trimmed == "" || indent <= curIndent {
			flush()
			continue
		}
		if len(cur) < maxLines {
			cur = append(cur, trimmed)
		}
	}
	flush()
	return out
}

func cutPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// attemptsOf returns every attempt of a run, oldest first.
//
// ⛔ The newest attempt is the only one GitHub's own UI shows by default, and
// it is the one that hides a flake: a re-run that passed. All of them are read.
func attemptsOf(r run) []int {
	n := r.RunAttempt
	if n < 1 {
		n = 1
	}
	out := make([]int, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, i)
	}
	return out
}

// parseSince accepts a date or an instant, because a date is what a reader
// types and an instant is what a boundary needs.
//
// ⛔ A BARE DATE IS A BOUNDARY, AND #204 RECORDS IT GOING WRONG: "--since on a
// bare date, and an awk comparison on the wrong field, both quietly answered a
// different question than the one asked". A date here means midnight UTC of
// that day, stated rather than guessed.
func parseSince(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("--since %q is neither YYYY-MM-DD nor an RFC3339 instant", s)
}

// byLane counts occurrences per lane, which is the axis a platform hypothesis
// is about.
func byLane(occs []occurrence) []string {
	n := map[string]int{}
	for _, o := range occs {
		n[o.Lane]++
	}
	lanes := make([]string, 0, len(n))
	for l := range n {
		lanes = append(lanes, l)
	}
	sort.Slice(lanes, func(i, j int) bool {
		if n[lanes[i]] != n[lanes[j]] {
			return n[lanes[i]] > n[lanes[j]]
		}
		return lanes[i] < lanes[j]
	})
	out := make([]string, 0, len(lanes))
	for _, l := range lanes {
		out = append(out, fmt.Sprintf("  %-34s %d", l, n[l]))
	}
	return out
}

// api is the GitHub read surface, so that the judging above can be put a
// question without a network.
type api interface {
	get(path string) ([]byte, error)
	logText(repo string, jobID int64) (string, error)
}

// newAPI is what the command reads GitHub through, and it is a variable so a
// test cannot reach the forge even by accident.
//
// ⛔ MEASURED, NOT FEARED: with `--test`'s own refusal deleted, `mutsweep` ran
// this package's flag table against the REAL GitHub for 4 minutes 25 seconds --
// a hundred runs of go-crdt/collab, through the shared hourly budget, from a
// unit test. The guards held in the shipped code, so nothing was wrong with it;
// what was wrong is that the suite's only protection from the network was a
// guard under test. A test that can call out when an assertion moves is a test
// that will call out in CI one day.
var newAPI = func() api { return ghAPI{} }

type ghAPI struct{}

func (ghAPI) get(path string) ([]byte, error) { return fleet.GH("api", path) }

func (ghAPI) logText(repo string, jobID int64) (string, error) {
	b, err := fleet.GH("api", fmt.Sprintf("repos/%s/actions/jobs/%d/logs", repo, jobID))
	return string(b), err
}

type census struct {
	WindowRuns  int // total_count: how many runs the window holds
	RunsRead    int
	Attempts    int
	JobsRead    int
	LogsRead    int
	Occurrences []occurrence
	Unread      []unread
}

// perPage is GitHub's maximum page size, and the page size this asks for:
// fewer items than this in an answer means there is no page after it.
const perPage = 100

func listRuns(a api, repo, event, workflow, since string, max int) ([]run, int, error) {
	var out []run
	total, read := 0, 0
	for page := 1; len(out) < max; page++ {
		q := url.Values{}
		q.Set("per_page", fmt.Sprint(perPage))
		q.Set("page", fmt.Sprint(page))
		if event != "" {
			q.Set("event", event)
		}
		if since != "" {
			q.Set("created", ">="+since)
		}
		path := fmt.Sprintf("repos/%s/actions/runs?%s", repo, q.Encode())
		b, err := a.get(path)
		if err != nil {
			return nil, 0, cli.Unanswerable("listing runs of %s: %v", repo, err)
		}
		var answer struct {
			TotalCount int   `json:"total_count"`
			Runs       []run `json:"workflow_runs"`
		}
		if err := json.Unmarshal(b, &answer); err != nil {
			return nil, 0, cli.Unanswerable("runs of %s: %v", repo, err)
		}
		total = answer.TotalCount
		if len(answer.Runs) == 0 {
			break
		}
		for _, r := range answer.Runs {
			if workflow != "" && r.Name != workflow && !strings.HasSuffix(r.Path, "/"+workflow) {
				continue
			}
			out = append(out, r)
			if len(out) >= max {
				break
			}
		}
		// ⛔ A SHORT PAGE IS THE LAST PAGE. Stopping only on an EMPTY one spends
		// one request per census asking for a page that cannot exist -- against
		// the hourly budget this account shares with every Renovate runner in
		// the fleet. The sandbox caught it as a test failure; against the real
		// API it would have been invisible, since GitHub answers the extra call
		// with an empty list and the loop then stops.
		read += len(answer.Runs)
		if len(answer.Runs) < perPage || (total > 0 && read >= total) {
			break
		}
	}
	return out, total, nil
}

// jobsOf returns every job of one attempt, following the pages.
//
// ⛔ ONE PAGE IS NOT EVERY JOB, AND A MATRIX IS WHY. go-crdt/crdt's fuzz lane
// alone is twenty jobs and its test lane several more; a hundred is a ceiling a
// matrix reaches. Reading page one and stopping would drop the jobs past it
// SILENTLY -- the census would be short by exactly the lanes a big matrix adds,
// which are the lanes a flake hides in. The count GitHub reports is compared
// with the count read, and a short read is an error rather than a smaller
// answer.
func jobsOf(a api, repo string, runID int64, attempt int) ([]job, error) {
	var out []job
	total := -1
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("per_page", "100")
		q.Set("page", fmt.Sprint(page))
		b, err := a.get(fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d/jobs?%s", repo, runID, attempt, q.Encode()))
		if err != nil {
			return nil, err
		}
		var answer struct {
			TotalCount int   `json:"total_count"`
			Jobs       []job `json:"jobs"`
		}
		if err := json.Unmarshal(b, &answer); err != nil {
			return nil, err
		}
		if total < 0 {
			total = answer.TotalCount
		}
		out = append(out, answer.Jobs...)
		if len(answer.Jobs) == 0 || len(out) >= total {
			break
		}
	}
	// ⚠ `total > 0` only keeps an absent total_count from reading as "the
	// attempt has no jobs". Moving it to `>= 0` changes nothing an assertion
	// can see: with total == 0 the comparison beside it is `len(out) < 0`,
	// which no answer satisfies.
	if total > 0 && len(out) < total {
		return nil, fmt.Errorf("read %d of %d job(s)", len(out), total)
	}
	return out, nil
}

func scan(a api, repo, test string, runs []run, windowRuns, maxLines int) census {
	c := census{WindowRuns: windowRuns}
	for _, r := range runs {
		c.RunsRead++
		for _, at := range attemptsOf(r) {
			c.Attempts++
			jobs, err := jobsOf(a, repo, r.ID, at)
			if err != nil {
				c.Unread = append(c.Unread, unread{Run: r.ID, Attempt: at, Lane: "(jobs)", Why: err.Error()})
				continue
			}
			for _, j := range jobs {
				c.JobsRead++
				if !needsLog(j) {
					continue
				}
				log, err := a.logText(repo, j.ID)
				if err != nil {
					c.Unread = append(c.Unread, unread{Run: r.ID, Attempt: at, Lane: j.Name, Why: err.Error()})
					continue
				}
				c.LogsRead++
				for _, block := range failureBlocks(log, test, maxLines) {
					when := j.StartedAt
					if when.IsZero() {
						when = r.CreatedAt
					}
					c.Occurrences = append(c.Occurrences, occurrence{
						When: when, Run: r.ID, Attempt: at, Attempts: r.RunAttempt,
						Event: r.Event, Branch: r.HeadBranch, SHA: r.HeadSHA,
						Lane: j.Name, Step: failedStep(j), Lines: block,
					})
				}
			}
		}
	}
	sort.SliceStable(c.Occurrences, func(i, j int) bool {
		return c.Occurrences[i].When.Before(c.Occurrences[j].When)
	})
	return c
}

const long = `Counts a named test's failures across a repository's CI history, by lane, with
a denominator.

⛔ IT READS RUNS x ATTEMPTS, not runs. A flake is the failure somebody re-runs,
and a re-run moves the RUN's conclusion to success while the evidence stays in
the earlier attempt: go-crdt/collab run 39069166645 is green today and its
first attempt holds the failure this was written for. A census over runs whose
conclusion is "failure" therefore cannot see the thing it counts, and "N in M
runs" is a FLOOR on the rate rather than the rate.

⛔ AND AN UNREAD LOG IS NOT AN ABSENCE. GitHub discards job logs on a retention
window, so any census over a long window is partly unreadable by construction.
Every log that could not be fetched is named, and a run of ZERO occurrences
with anything unread exits 2 rather than 0: a scan that could not read must not
look like a clean bill.

A failed STEP inside a successful job is read too (continue-on-error leaves the
job green), and that costs no extra call: the steps arrive with the jobs.

Exit status: 0 the history was read and the test never failed, 1 it did, 2 the
question could not be put -- a log or a listing that could not be read, or a
window with no runs in it.`

// RootCmd is the command, driven by the tests through cli.Drive.
func RootCmd() *cobra.Command {
	c := cli.New("flakescan --repo OWNER/NAME --test TESTNAME [--since DATE] [--event EVENT] [--workflow NAME] [--max-runs N]",
		"Count a test's failures across a repository's CI history, by lane, with a denominator.",
		long)
	repo := c.Flags().String("repo", "", "the repository to read, OWNER/NAME (required)")
	test := c.Flags().String("test", "", "the test whose failures to count; a subtest answers for its parent (required)")
	since := c.Flags().String("since", "", "only runs created at or after this YYYY-MM-DD (midnight UTC) or RFC3339 instant")
	event := c.Flags().String("event", "", "only runs of this event (push, pull_request, schedule, workflow_dispatch)")
	workflow := c.Flags().String("workflow", "", "only runs of this workflow, by name or by file name")
	maxRuns := c.Flags().Int("max-runs", 100, "bound the runs read; the window's real size is printed beside it")
	maxLines := c.Flags().Int("max-lines", 40, "bound the lines quoted under one failure")
	c.RunE = func(c *cobra.Command, _ []string) error {
		if cli.HelpRequested(c) {
			return nil
		}
		sinceArg, err := check(*repo, *test, *maxRuns, *since)
		if err != nil {
			return err
		}
		return report(c, newAPI(), *repo, *test, *event, *workflow, sinceArg, *maxRuns, *maxLines)
	}
	return c
}

// check judges the arguments and returns the `created` bound to send, or the
// refusal naming the flag at fault.
//
// ⛔ IT IS A FUNCTION OF ITS OWN SO THAT A TEST CAN ASK IT WITHOUT A NETWORK.
// Driving the whole command to assert that `--max-runs 1` is ACCEPTED means
// everything after the check runs too — and that is a `gh api` call from a unit
// test, which took the package from 0.18 s to 9.5 s and would have put a
// network read in this repository's CI. The refusals could be driven end to end
// because they stop here; the acceptance cannot, and testing only the refusals
// is how an over-refusal survives.
func check(repo, test string, maxRuns int, since string) (string, error) {
	if repo == "" || !strings.Contains(repo, "/") {
		return "", cli.Unanswerable("--repo OWNER/NAME is required")
	}
	if test == "" {
		return "", cli.Unanswerable("--test TESTNAME is required: this counts one test, named")
	}
	if maxRuns < 1 {
		return "", cli.Unanswerable("--max-runs %d reads nothing", maxRuns)
	}
	sinceT, err := parseSince(since)
	if err != nil {
		return "", cli.Unanswerable("%v", err)
	}
	if sinceT.IsZero() {
		return "", nil
	}
	return sinceT.Format(time.RFC3339), nil
}

func report(c *cobra.Command, a api, repo, test, event, workflow, since string, maxRuns, maxLines int) error {
	runs, window, err := listRuns(a, repo, event, workflow, since, maxRuns)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		return cli.Unanswerable("no runs of %s matched: a window with no runs measures nothing", repo)
	}
	cen := scan(a, repo, test, runs, window, maxLines)
	out := cli.Out(c)
	for _, o := range cen.Occurrences {
		fmt.Fprintf(out, "%s  run %d attempt %d/%d  %-13s %-28s %s\n",
			o.When.UTC().Format(time.RFC3339), o.Run, o.Attempt, o.Attempts,
			o.Event, truncate(o.Branch, 28), o.Lane)
		if o.Step != "" {
			fmt.Fprintf(out, "    step: %s\n", o.Step)
		}
		for _, l := range o.Lines {
			fmt.Fprintf(out, "    %s\n", l)
		}
	}
	fmt.Fprintf(out, "%d occurrence(s) of %s in %d attempt(s) of %d run(s) read",
		len(cen.Occurrences), test, cen.Attempts, cen.RunsRead)
	if cen.WindowRuns > cen.RunsRead {
		fmt.Fprintf(out, ", out of %d in the window", cen.WindowRuns)
	}
	fmt.Fprintf(out, "; %d job(s) seen, %d log(s) read\n", cen.JobsRead, cen.LogsRead)
	for _, l := range byLane(cen.Occurrences) {
		fmt.Fprintln(out, l)
	}
	if len(cen.Unread) > 0 {
		fmt.Fprintf(cli.Err(c), "⛔ %d log(s) or listing(s) could not be read, so the denominator is partial:\n", len(cen.Unread))
		for _, u := range cen.Unread {
			fmt.Fprintf(cli.Err(c), "   run %d attempt %d %-28s %s\n", u.Run, u.Attempt, u.Lane, truncate(u.Why, 120))
		}
	}
	switch {
	case len(cen.Occurrences) > 0:
		return cli.Found("%s failed %d time(s) in the history read", test, len(cen.Occurrences))
	case len(cen.Unread) > 0:
		return cli.Unanswerable("%s was not seen to fail, but %d log(s) could not be read: that is not zero",
			test, len(cen.Unread))
	default:
		return nil
	}
}

func truncate(s string, n int) string {
	r := []rune(strings.ReplaceAll(strings.TrimSpace(s), "\n", " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}
