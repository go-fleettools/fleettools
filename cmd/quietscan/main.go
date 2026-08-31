// Reports every organisation whose Renovate runner has stopped running.
//
// The failure it exists to catch is silent by construction. `redscan` asks
// whether the last run FAILED; a runner that stopped does not fail, it says
// nothing, and `status=completed&per_page=1` on a repository with no runs
// returns an empty object -- so a runner that has never fired is invisible to a
// scan that branches on `conclusion`. This is the other half of that file's own
// lesson: a red branch is not a broken one, and a green fleet is not a running
// one. Check WHEN it last ran.
//
// Two properties are structural, not conventions:
//
//   - It is not a scheduled GitHub Actions workflow. A watcher that shares the
//     failure mode it watches for is not a watcher. Run it from this machine
//     like its three siblings, or from launchd if it is to be unattended.
//   - It never dispatches a run. A dispatch opens real pull requests across an
//     organisation, so every call goes through readOnly, which refuses any
//     write verb and any path that could start one.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"fleet/quiet"
)

// readOnly refuses anything that is not a plain GET.
//
// The whole tool is one dangerous mistake away from being useful in the wrong
// direction: `actions/workflows/{id}/dispatches` is one path segment from the
// runs listing it reads, and a dispatch opens real pull requests across an
// organisation. So the refusal is a gate every call passes through, not a rule
// to be remembered at each call site.
func readOnly(args []string) error {
	// `gh api` is the ONLY subcommand allowed. `gh workflow run` carries no
	// write verb and no dispatches path, and is a dispatch all the same -- so
	// the gate is an allowlist of one, not a denylist of shapes I thought of.
	if len(args) == 0 || args[0] != "api" {
		return fmt.Errorf("quietscan reads only, via `gh api`: %v", args)
	}
	for i, a := range args {
		switch {
		case a == "-X" || a == "--method":
			if i+1 < len(args) && strings.EqualFold(args[i+1], "GET") {
				continue
			}
			return fmt.Errorf("quietscan issues no writes: %v", args)
		case a == "-f" || a == "--raw-field" || a == "-F" || a == "--field":
			// gh turns a field into a POST unless the method is forced.
			return fmt.Errorf("quietscan issues no writes: %v", args)
		case strings.Contains(a, "/dispatches") || strings.Contains(a, "dispatch"):
			return fmt.Errorf("quietscan never dispatches a run: %v", args)
		}
	}
	return nil
}

func gh(args ...string) ([]byte, error) {
	if err := readOnly(args); err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		cmd := exec.Command("gh", args...)
		var errb strings.Builder
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err == nil {
			return out, nil
		}
		msg := errb.String()
		// Same list as prmerge: a sweep that gives up on the first dial error
		// turns a network blip into a silent hole, and a hole in a watcher
		// reads as "all quiet".
		transient := strings.Contains(msg, "no route to host") ||
			strings.Contains(msg, "operation timed out") ||
			strings.Contains(msg, "connection reset") ||
			strings.Contains(msg, "i/o timeout") ||
			strings.Contains(msg, "TLS handshake timeout") ||
			strings.Contains(msg, "EOF") ||
			strings.Contains(msg, "error connecting to") ||
			strings.Contains(msg, "no such host") ||
			strings.Contains(msg, "check your internet connection")
		if attempt < 5 && (transient || strings.Contains(msg, "secondary rate") || strings.Contains(msg, "abuse") || strings.Contains(msg, "too quickly") || strings.Contains(msg, "rate limit")) {
			time.Sleep(time.Duration(20*(attempt+1)) * time.Second)
			continue
		}
		return nil, fmt.Errorf("%s", strings.TrimSpace(msg))
	}
}

func isNotFound(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "Not Found"))
}

var calls struct {
	sync.Mutex
	n int
}

func counted(args ...string) ([]byte, error) {
	calls.Lock()
	calls.n++
	calls.Unlock()
	return gh(args...)
}

var (
	slack    = flag.Duration("slack", quiet.DefaultSlack, "how far behind its own cron a run may be before the runner counts as stopped")
	pushWarn = flag.Int("push-warn", int(quiet.DefaultPushWarn/(24*time.Hour)), "warn when the runner repository has had no push for this many days")
	repoName = flag.String("repo", ".github", "repository, in each organisation, that holds the runner")
	wfFile   = flag.String("workflow", "renovate.yml", "runner workflow file name")
	all      = flag.Bool("all", false, "list every runner, not only the findings")
	fixture  = flag.String("fixture", "", "read runners from a JSON file instead of the API; makes no network call at all")
	workers  = flag.Int("workers", 6, "concurrent API readers")
)

type wfEntry struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// read gathers one organisation's runner. Every call here is a GET.
func read(org string) quiet.Runner {
	r := quiet.Runner{Org: org}

	b, err := counted("api", fmt.Sprintf("repos/%s/%s", org, *repoName))
	if isNotFound(err) {
		return r // no runner repository at all: a finding in its own right
	}
	if err != nil {
		r.ReadError = err.Error()
		return r
	}
	var repo struct {
		Archived bool   `json:"archived"`
		PushedAt string `json:"pushed_at"`
	}
	if json.Unmarshal(b, &repo) != nil {
		r.ReadError = "repo: unparseable"
		return r
	}
	r.RepoExists = true
	r.Archived = repo.Archived
	r.PushedAt = parseTime(repo.PushedAt)
	if r.Archived {
		// Nothing below this line can be fixed by a pull request, so nothing
		// below this line is worth an API call.
		return r
	}

	b, err = counted("api", fmt.Sprintf("repos/%s/%s/actions/workflows?per_page=100", org, *repoName))
	if isNotFound(err) {
		return r
	}
	if err != nil {
		r.ReadError = err.Error()
		return r
	}
	var wfs struct {
		Workflows []wfEntry `json:"workflows"`
	}
	if json.Unmarshal(b, &wfs) != nil {
		r.ReadError = "workflows: unparseable"
		return r
	}
	var wf *wfEntry
	for i := range wfs.Workflows {
		w := &wfs.Workflows[i]
		if strings.HasSuffix(w.Path, "/"+*wfFile) || w.Path == *wfFile {
			wf = w
			break
		}
	}
	if wf == nil {
		return r // repository exists, runner does not
	}
	r.WorkflowFound = true
	r.WorkflowState = wf.State
	// The birth date is the WORKFLOW's creation, never the last commit to its
	// file: Renovate rewrites that file whenever it bumps its own action pin,
	// so a clock keyed on the file resets itself every time Renovate works.
	r.Born = parseTime(wf.CreatedAt)
	r.BornSource = "workflow.created_at"
	if wf.State != "active" {
		return r
	}

	// The cron, from the workflow's own file: the fleet's runners are staggered
	// across 24 hours, so there is no constant period to compare against.
	if b, err := counted("api", "-H", "Accept: application/vnd.github.raw",
		fmt.Sprintf("repos/%s/%s/contents/%s", org, *repoName, wf.Path)); err == nil {
		sched, errs := quiet.ParseSchedule(string(b))
		r.Schedule = sched
		if len(sched) == 0 && len(errs) > 0 {
			r.ReadError = errs[0].Error()
			return r
		}
	} else {
		r.ReadError = "workflow file: " + err.Error()
		return r
	}

	// event=schedule, because a manual dispatch is not evidence the schedule
	// still fires -- and a person poking a dead runner is exactly how this
	// failure hides.
	b, err = counted("api", fmt.Sprintf("repos/%s/%s/actions/workflows/%d/runs?event=schedule&per_page=1", org, *repoName, wf.ID))
	if err != nil {
		r.ReadError = "runs: " + err.Error()
		return r
	}
	var runs struct {
		Runs []struct {
			CreatedAt string `json:"created_at"`
		} `json:"workflow_runs"`
	}
	if json.Unmarshal(b, &runs) != nil {
		r.ReadError = "runs: unparseable"
		return r
	}
	if len(runs.Runs) > 0 {
		r.LastRun = parseTime(runs.Runs[0].CreatedAt)
	}
	return r
}

// fixtureFile is the doctored input a watcher needs to be provable. Nothing in
// it touches the network, so a runner can be made to look overdue, archived or
// never-fired without doctoring a live organisation.
type fixtureFile struct {
	Now     string `json:"now"`
	Runners []struct {
		Org           string   `json:"org"`
		RepoExists    bool     `json:"repo_exists"`
		Archived      bool     `json:"archived"`
		PushedAt      string   `json:"pushed_at"`
		WorkflowFound bool     `json:"workflow_found"`
		WorkflowState string   `json:"workflow_state"`
		Born          string   `json:"born"`
		Crons         []string `json:"crons"`
		LastRun       string   `json:"last_run"`
		ReadError     string   `json:"read_error"`
	} `json:"runners"`
}

func loadFixture(path string) ([]quiet.Runner, time.Time, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	var f fixtureFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, time.Time{}, err
	}
	now := time.Now().UTC()
	if f.Now != "" {
		if t := parseTime(f.Now); !t.IsZero() {
			now = t
		} else {
			return nil, time.Time{}, fmt.Errorf("fixture now=%q is not a timestamp", f.Now)
		}
	}
	var rs []quiet.Runner
	for _, in := range f.Runners {
		r := quiet.Runner{
			Org: in.Org, RepoExists: in.RepoExists, Archived: in.Archived,
			PushedAt: parseTime(in.PushedAt), WorkflowFound: in.WorkflowFound,
			WorkflowState: in.WorkflowState, Born: parseTime(in.Born),
			BornSource: "fixture", LastRun: parseTime(in.LastRun),
			ReadError: in.ReadError,
		}
		for _, c := range in.Crons {
			p, err := quiet.ParseCron(c)
			if err != nil {
				return nil, time.Time{}, fmt.Errorf("%s: %w", in.Org, err)
			}
			r.Schedule = append(r.Schedule, p)
		}
		rs = append(rs, r)
	}
	return rs, now, nil
}

func orgs() ([]string, error) {
	out, err := counted("api", "user/orgs", "--paginate", "--jq", ".[].login")
	if err != nil {
		return nil, err
	}
	var o []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			o = append(o, l)
		}
	}
	sort.Strings(o)
	return o, nil
}

func hm(d time.Duration) string {
	if d < 0 {
		return "-" + hm(-d)
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d/time.Minute)%60)
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.UTC().Format("2006-01-02 15:04Z")
}

func main() {
	flag.Parse()
	now := time.Now().UTC()
	var runners []quiet.Runner

	if *fixture != "" {
		var err error
		runners, now, err = loadFixture(*fixture)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fixture:", err)
			os.Exit(2)
		}
		fmt.Printf("fixture: %s   as of %s\n", *fixture, stamp(now))
	} else {
		list, err := orgs()
		if err != nil {
			fmt.Fprintln(os.Stderr, "orgs:", err)
			os.Exit(2)
		}
		// Driven from the ORGANISATION list, not the runner list. A recency
		// check over runners structurally cannot see an organisation that has
		// no runner to be quiet.
		fmt.Printf("orgs: %d   as of %s\n", len(list), stamp(now))
		runners = make([]quiet.Runner, len(list))
		sem := make(chan struct{}, *workers)
		var wg sync.WaitGroup
		for i, o := range list {
			wg.Add(1)
			go func(i int, o string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				runners[i] = read(o)
			}(i, o)
		}
		wg.Wait()
	}

	results := make([]quiet.Result, 0, len(runners))
	for _, r := range runners {
		results = append(results, quiet.Classify(r, now, *slack, time.Duration(*pushWarn)*24*time.Hour))
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Org < results[j].Org })

	by := map[quiet.Verdict][]quiet.Result{}
	pages := 0
	for _, r := range results {
		by[r.Verdict] = append(by[r.Verdict], r)
		if r.Verdict.Pages() {
			pages++
		}
	}
	withRunner := len(results) - len(by[quiet.NoRunner]) - len(by[quiet.NoRunnerRepo])
	if *fixture == "" {
		fmt.Printf("runners: %d   API calls: %d\n", withRunner, calls.n)
	}

	// The observed lag, every pass. The threshold below is a measurement that
	// has to keep being re-taken: GitHub's scheduler was 2.0 to 7.7 hours
	// behind when this was written and still growing, and a slack that stops
	// covering it pages on a fleet that is working perfectly.
	l := quiet.Lags(results)
	if l.N > 0 {
		fmt.Printf("scheduler lag behind cron (n=%d): min %s  median %s  p90 %s  max %s   [slack %s]\n",
			l.N, hm(l.Min), hm(l.Median), hm(l.P90), hm(l.Max), hm(*slack))
		if l.Max > *slack*3/4 {
			fmt.Printf("  NOTE: the worst observed lag is past three quarters of the slack; raise -slack before it pages on healthy runners.\n")
		}
	}

	// Never a bare total: four verdicts are four different fixes, and one
	// number hides all four.
	fmt.Printf("\nPAGES: %d\n", pages)
	order := []struct {
		v   quiet.Verdict
		fix string
	}{
		{quiet.Overdue, "the runner has stopped; look at the last run's logs and the token"},
		{quiet.NeverFired, "it has never run at all; the schedule or the token is wrong"},
		{quiet.DisabledInactivity, "re-enable it -- and note it expires again 60 days later"},
		{quiet.NoSchedule, "the workflow has no cron; nothing will ever start it"},
		{quiet.Unreadable, "the API would not answer; re-run before believing anything else"},
		{quiet.DisabledManually, "someone switched it off; this may be entirely deliberate"},
		{quiet.Archived, "no pull request can fix this; the repository is frozen"},
		{quiet.NoRunner, "the repository exists but holds no runner"},
		{quiet.NoRunnerRepo, "no runner repository in this organisation"},
		{quiet.NotYetDue, "born, not yet due; nothing to do"},
		{quiet.Healthy, ""},
	}
	for _, o := range order {
		rs := by[o.v]
		if len(rs) == 0 {
			continue
		}
		mark := " "
		if o.v.Pages() {
			mark = "!"
		}
		fmt.Printf("\n%s %-20s %d", mark, string(o.v), len(rs))
		if o.fix != "" {
			fmt.Printf("   -- %s", o.fix)
		}
		fmt.Println()
		if o.v == quiet.Healthy && !*all {
			continue
		}
		for _, r := range rs {
			fmt.Println(strings.TrimRight(fmt.Sprintf("    %-38s %s", r.Org, detail(r)), " "))
		}
	}

	// The 60-day latch, which is why detection has to fire early: these
	// repositories are public and receive no push except Renovate's own, so if
	// Renovate stops the repository goes quiet, at day 60 GitHub disables the
	// schedule, and the disablement makes the silence permanent.
	var idle []quiet.Result
	for _, r := range results {
		if r.PushWarn && r.RepoExists {
			idle = append(idle, r)
		}
	}
	if len(idle) > 0 {
		sort.Slice(idle, func(i, j int) bool { return idle[i].DaysIdle > idle[j].DaysIdle })
		fmt.Printf("\n! quiet repositories (no push for %d+ days; GitHub disables the schedule at %d): %d\n",
			*pushWarn, int(quiet.InactivityLimit/(24*time.Hour)), len(idle))
		for _, r := range idle {
			fmt.Printf("    %-38s %.0f days idle, latches %s\n", r.Org, r.DaysIdle,
				r.PushedAt.Add(quiet.InactivityLimit).Format("2006-01-02"))
		}
	}

	if pages > 0 {
		os.Exit(1)
	}
}

func detail(r quiet.Result) string {
	switch r.Verdict {
	case quiet.Unreadable:
		return r.ReadError
	case quiet.NoRunnerRepo, quiet.NoRunner:
		if r.RepoExists {
			return fmt.Sprintf("last push %s (%.0f days)", stamp(r.PushedAt), r.DaysIdle)
		}
		return ""
	case quiet.Archived, quiet.DisabledManually, quiet.DisabledInactivity:
		return fmt.Sprintf("last run %s   last push %s", stamp(r.LastRun), stamp(r.PushedAt))
	case quiet.NoSchedule:
		if len(r.Schedule) == 0 {
			return "the workflow carries no cron at all"
		}
		return "cron " + r.Schedule.String() + " has no reachable occurrence"
	case quiet.NotYetDue:
		return fmt.Sprintf("born %s   first due %s   cron %s", stamp(r.Born), stamp(r.Due), r.Schedule)
	default:
		s := fmt.Sprintf("last run %s   due %s   late %s   cron %s",
			stamp(r.LastRun), stamp(r.Due), hm(r.Late), r.Schedule)
		if r.HasLag {
			s += fmt.Sprintf("   lag %s", hm(r.Lag))
		}
		return s
	}
}
