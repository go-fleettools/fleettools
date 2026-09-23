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

	"github.com/go-fleettools/fleettools/quiet"
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
		// The core budget will not refill inside this pass's backoff, so a
		// refusal for it fails fast and the verdict becomes "unreadable" --
		// which is exactly what a watcher should say when it could not look.
		if attempt < 5 && (transient || rateLimited(msg)) {
			time.Sleep(time.Duration(20*(attempt+1)) * time.Second)
			continue
		}
		return nil, fmt.Errorf("%s", strings.TrimSpace(msg))
	}
}

// isNotFound requires the 404 itself, not the words around it. A rate-limited
// or forbidden response that happens to say "Not Found" must never be read as
// "this organisation has no runner": that is a verdict derived from a call that
// did not answer.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "404")
}

var calls struct {
	sync.Mutex
	n       int
	limited string // the first rate-limit refusal seen, verbatim
}

// rateLimited latches the first hard rate-limit refusal of the pass.
//
// /rate_limit cannot be trusted to notice: it was observed reporting core
// 5000/5000 while a real request returned 403 with X-RateLimit-Remaining: 0. So
// the budget is judged by what the API actually did, not by what it says about
// itself. Once latched, calls stop sleeping through five retries each -- 1300
// calls x 300 seconds of backoff is not a pass, it is a hang -- and the pass
// reports itself as incomplete.
// It returns whether to back off and retry, which is NOT the same question as
// whether the budget is gone. A secondary rate limit is genuinely transient and
// worth twenty seconds. The core budget is not: it refills on the hour, so
// every one of 1300 calls would pay five sleeps to learn the same thing, and
// what should be a pass becomes a hang.
func rateLimited(msg string) (retry bool) {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "secondary rate") || strings.Contains(low, "abuse") ||
		strings.Contains(low, "too quickly"):
		return true
	case strings.Contains(low, "rate limit") || strings.Contains(low, "ratelimit-remaining: 0"):
		calls.Lock()
		if calls.limited == "" {
			calls.limited = strings.TrimSpace(msg)
		}
		calls.Unlock()
		return false
	}
	return false
}

func budgetExhausted() string {
	calls.Lock()
	defer calls.Unlock()
	return calls.limited
}

func counted(args ...string) ([]byte, error) {
	calls.Lock()
	calls.n++
	calls.Unlock()
	return gh(args...)
}

var (
	slack     = flag.Duration("slack", quiet.DefaultSlack, "how far behind its own cron a run may be before the runner counts as stopped")
	pushWarn  = flag.Int("push-warn", int(quiet.DefaultPushWarn/(24*time.Hour)), "warn when the runner repository has had no push for this many days")
	repoNames = flag.String("repos", ".github,renovate-runner", "comma-separated repositories, in each organisation, that may hold a runner")
	wfFile    = flag.String("workflow", "renovate.yml", "runner workflow file name")
	all       = flag.Bool("all", false, "list every runner, not only the findings")
	fixture   = flag.String("fixture", "", "read runners from a JSON file instead of the API; makes no network call at all")
	workers   = flag.Int("workers", 6, "concurrent API readers")
	// A full pass is ~1300 calls, a quarter of an hour's budget, and this fleet
	// routinely has eight sessions on it. Checking one organisation should not
	// cost the same as checking all of them.
	only = flag.String("orgs", "", "comma-separated organisations to check instead of the whole fleet")
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

// read gathers every runner an organisation holds. Every call here is a GET.
//
// Both candidate repositories are probed in every organisation, not just until
// one answers. A shared runner lives in `renovate-runner` and an organisation
// may hold BOTH -- go-attest holds an active `.github` runner and the retired
// shared one, and stopping at the first would have hidden the retirement that
// left 109 organisations to be re-covered.
func read(org string) []quiet.Runner {
	var found []quiet.Runner
	var empty quiet.Runner
	empty.Org = org
	for _, name := range strings.Split(*repoNames, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		r, exists := readRepo(org, name)
		if exists && !empty.RepoExists {
			// Keep the facts of the first candidate that existed. Reporting
			// "last push never" for a repository that is simply runner-less
			// invents a fact rather than reading one.
			empty.RepoExists = true
			empty.PushedAt = r.PushedAt
			empty.Repo = name
		}
		if r.WorkflowFound || r.ReadError != "" {
			found = append(found, r)
		}
	}
	if len(found) > 0 {
		return found
	}
	// No runner anywhere in this organisation. Whether a candidate repository
	// merely existed is still worth saying: an empty `.github` holding only a
	// Renovate preset is a different thing from no repository at all.
	return []quiet.Runner{empty}
}

// readRepo reads one candidate repository. The bool says whether it exists at
// all, which is not the same as whether it holds a runner.
func readRepo(org, name string) (quiet.Runner, bool) {
	r := quiet.Runner{Org: org, Repo: name}

	b, err := counted("api", fmt.Sprintf("repos/%s/%s", org, name))
	if isNotFound(err) {
		return r, false
	}
	if err != nil {
		r.ReadError = err.Error()
		return r, true
	}
	var repo struct {
		Archived bool   `json:"archived"`
		PushedAt string `json:"pushed_at"`
	}
	if json.Unmarshal(b, &repo) != nil {
		r.ReadError = "repo: unparseable"
		return r, true
	}
	r.RepoExists = true
	r.Archived = repo.Archived
	r.PushedAt = parseTime(repo.PushedAt)
	if r.Archived {
		// Nothing below this line can be fixed by a pull request, so nothing
		// below this line is worth an API call. An archived repository that
		// holds a runner is still reported: it is frozen, not absent.
		r.WorkflowFound = true
		return r, true
	}

	b, err = counted("api", fmt.Sprintf("repos/%s/%s/actions/workflows?per_page=100", org, name))
	if isNotFound(err) {
		return r, true
	}
	if err != nil {
		r.ReadError = err.Error()
		return r, true
	}
	var wfs struct {
		Workflows []wfEntry `json:"workflows"`
	}
	if json.Unmarshal(b, &wfs) != nil {
		r.ReadError = "workflows: unparseable"
		return r, true
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
		return r, true // repository exists, runner does not
	}
	r.WorkflowFound = true
	r.WorkflowState = wf.State
	// The birth date is the WORKFLOW's creation, never the last commit to its
	// file: Renovate rewrites that file whenever it bumps its own action pin,
	// so a clock keyed on the file resets itself every time Renovate works.
	r.Born = parseTime(wf.CreatedAt)
	r.BornSource = "workflow.created_at"

	// What this runner WATCHES, which is not the organisation it lives in.
	// Read even for a disabled runner: knowing that the retired go-attest
	// runner used to cover 109 organisations is the point of reading it.
	if b, err := counted("api", "-H", "Accept: application/vnd.github.raw",
		fmt.Sprintf("repos/%s/%s/contents/config.js", org, name)); err == nil {
		r.Covers, r.FilterNote = quiet.ParseAutodiscoverFilter(string(b))
	} else if isNotFound(err) {
		r.FilterNote = "no config.js"
	} else {
		r.FilterNote = "config.js: " + err.Error()
	}

	if wf.State != "active" {
		return r, true
	}

	// The cron, from the workflow's own file: the fleet's runners are staggered
	// across 24 hours, so there is no constant period to compare against.
	if b, err := counted("api", "-H", "Accept: application/vnd.github.raw",
		fmt.Sprintf("repos/%s/%s/contents/%s", org, name, wf.Path)); err == nil {
		sched, errs := quiet.ParseSchedule(string(b))
		r.Schedule = sched
		if len(sched) == 0 && len(errs) > 0 {
			r.ReadError = errs[0].Error()
			return r, true
		}
	} else {
		r.ReadError = "workflow file: " + err.Error()
		return r, true
	}

	// event=schedule, because a manual dispatch is not evidence the schedule
	// still fires -- and a person poking a dead runner is exactly how this
	// failure hides.
	b, err = counted("api", fmt.Sprintf("repos/%s/%s/actions/workflows/%d/runs?event=schedule&per_page=1", org, name, wf.ID))
	if err != nil {
		r.ReadError = "runs: " + err.Error()
		return r, true
	}
	var runs struct {
		Runs []struct {
			CreatedAt string `json:"created_at"`
		} `json:"workflow_runs"`
	}
	if json.Unmarshal(b, &runs) != nil {
		r.ReadError = "runs: unparseable"
		return r, true
	}
	if len(runs.Runs) > 0 {
		r.LastRun = parseTime(runs.Runs[0].CreatedAt)
	}
	return r, true
}

// readApp asks whether the Renovate App is installed on an organisation.
//
// A GitHub App covers an organisation through a mechanism no workflow census
// can see: no runner, no cron, no repository to be quiet, and pull requests all
// the same. The read can be refused where the token cannot see installations,
// and a refusal is AppUnread -- never AppAbsent.
func readApp(org string) quiet.AppCoverage {
	b, err := counted("api", fmt.Sprintf("orgs/%s/installations?per_page=100", org))
	if err != nil {
		return quiet.AppUnread
	}
	var v struct {
		Installations []struct {
			AppSlug string `json:"app_slug"`
		} `json:"installations"`
	}
	if json.Unmarshal(b, &v) != nil {
		return quiet.AppUnread
	}
	for _, in := range v.Installations {
		if strings.Contains(strings.ToLower(in.AppSlug), "renovate") {
			return quiet.AppPresent
		}
	}
	return quiet.AppAbsent
}

// recheckCovering re-reads the state of every runner that other organisations
// are counted as covered by, and re-resolves coverage if one moved.
//
// A coverage map is a snapshot, and a runner can leave the live set while the
// pass that read it is still running: go-pdfkit/renovate-runner was retired
// between two passes an evening apart, and nothing says it could not have been
// retired between two calls of the same pass. Leaving an organisation counted
// as covered by something that stopped covering it is the attribution mistake
// this tool exists not to make.
func recheckCovering(rs []quiet.Result, now time.Time) ([]quiet.Result, []string) {
	covering := map[string]bool{}
	for _, r := range rs {
		if r.CoveredBy != "" && r.CoveredBy != quiet.AppCoveredBy {
			covering[r.CoveredBy] = true
		}
	}
	var moved []string
	changed := false
	out := make([]quiet.Result, len(rs))
	copy(out, rs)
	for i := range out {
		if !covering[label(out[i])] {
			continue
		}
		state, ok := readWorkflowState(out[i].Org, out[i].Repo)
		if !ok || state == out[i].WorkflowState {
			continue
		}
		moved = append(moved, fmt.Sprintf("%s  %s -> %s during the pass", label(out[i]), out[i].WorkflowState, state))
		out[i].WorkflowState = state
		out[i] = quiet.Classify(out[i].Runner, now, *slack, time.Duration(*pushWarn)*24*time.Hour)
		changed = true
	}
	if !changed {
		return rs, nil
	}
	// Coverage is resolved from scratch, not patched: a runner leaving the live
	// set can hand its organisations to another runner that also reaches them.
	for i := range out {
		out[i].CoveredBy, out[i].CoveredByVerdict = "", ""
		if out[i].Verdict == quiet.Covered {
			out[i].Verdict = quiet.NoRunner
		}
	}
	return quiet.ApplyCoverage(out), moved
}

func readWorkflowState(org, repo string) (string, bool) {
	b, err := counted("api", fmt.Sprintf("repos/%s/%s/actions/workflows?per_page=100", org, repo))
	if err != nil {
		return "", false
	}
	var wfs struct {
		Workflows []wfEntry `json:"workflows"`
	}
	if json.Unmarshal(b, &wfs) != nil {
		return "", false
	}
	for _, w := range wfs.Workflows {
		if strings.HasSuffix(w.Path, "/"+*wfFile) || w.Path == *wfFile {
			return w.State, true
		}
	}
	return "", false
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
		Repo          string   `json:"repo"`
		Covers        []string `json:"covers"`
		FilterNote    string   `json:"filter_note"`
		App           string   `json:"app"` // "present", "absent", "unread"
		ReadError     string   `json:"read_error"`
	} `json:"runners"`
}

func loadFixture(path string) ([]quiet.Runner, time.Time, map[string]quiet.AppCoverage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	var f fixtureFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, time.Time{}, nil, err
	}
	now := time.Now().UTC()
	if f.Now != "" {
		if t := parseTime(f.Now); !t.IsZero() {
			now = t
		} else {
			return nil, time.Time{}, nil, fmt.Errorf("fixture now=%q is not a timestamp", f.Now)
		}
	}
	app := map[string]quiet.AppCoverage{}
	var rs []quiet.Runner
	for _, in := range f.Runners {
		r := quiet.Runner{
			Org: in.Org, RepoExists: in.RepoExists, Archived: in.Archived,
			PushedAt: parseTime(in.PushedAt), WorkflowFound: in.WorkflowFound,
			WorkflowState: in.WorkflowState, Born: parseTime(in.Born),
			BornSource: "fixture", LastRun: parseTime(in.LastRun),
			Repo: in.Repo, Covers: in.Covers, FilterNote: in.FilterNote,
			ReadError: in.ReadError,
		}
		for _, c := range in.Crons {
			p, err := quiet.ParseCron(c)
			if err != nil {
				return nil, time.Time{}, nil, fmt.Errorf("%s: %w", in.Org, err)
			}
			r.Schedule = append(r.Schedule, p)
		}
		switch in.App {
		case "present":
			app[in.Org] = quiet.AppPresent
		case "absent":
			app[in.Org] = quiet.AppAbsent
		case "unread":
			app[in.Org] = quiet.AppUnread
		case "":
		default:
			return nil, time.Time{}, nil, fmt.Errorf("%s: app=%q is not present/absent/unread", in.Org, in.App)
		}
		rs = append(rs, r)
	}
	return rs, now, app, nil
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
	started := time.Now().UTC()
	now := started
	var runners []quiet.Runner
	app := map[string]quiet.AppCoverage{}

	if *fixture != "" {
		var err error
		runners, now, app, err = loadFixture(*fixture)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fixture:", err)
			os.Exit(2)
		}
		started = now
		fmt.Printf("fixture: %s   as of %s\n", *fixture, stamp(now))
	} else {
		var list []string
		if *only != "" {
			for _, o := range strings.Split(*only, ",") {
				if o = strings.TrimSpace(o); o != "" {
					list = append(list, o)
				}
			}
			sort.Strings(list)
			fmt.Printf("NARROWED to %d organisations: filters and Apps outside this list are NOT read, so no gap can be established here.\n", len(list))
		} else {
			var err error
			if list, err = orgs(); err != nil {
				fmt.Fprintln(os.Stderr, "orgs:", err)
				os.Exit(2)
			}
		}
		// Driven from the ORGANISATION list, not the runner list. A recency
		// check over runners structurally cannot see an organisation that has
		// no runner to be quiet.
		// A coverage map is a snapshot, so it carries the window it was read
		// in. A runner can be retired between two calls of one pass.
		fmt.Printf("orgs: %d   reading from %s\n", len(list), stamp(started))
		per := make([][]quiet.Runner, len(list))
		sem := make(chan struct{}, *workers)
		var wg sync.WaitGroup
		for i, o := range list {
			wg.Add(1)
			go func(i int, o string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				per[i] = read(o)
			}(i, o)
		}
		wg.Wait()
		for _, p := range per {
			runners = append(runners, p...)
		}
		now = time.Now().UTC()
	}

	results := make([]quiet.Result, 0, len(runners))
	for _, r := range runners {
		results = append(results, quiet.Classify(r, now, *slack, time.Duration(*pushWarn)*24*time.Hour))
	}
	// Fleet-wide, because coverage is not a property any single organisation
	// can answer: it is decided by some OTHER organisation's runner filter.
	results = quiet.ApplyCoverage(results)

	// A runner may have left the live set while this pass was reading. Confirm
	// every runner that others are counted as covered by is still in the state
	// it was read in, and re-resolve coverage from scratch if one moved.
	var moved []string
	if *fixture == "" {
		results, moved = recheckCovering(results, now)

		// The third form of coverage, asked only where it can change an answer:
		// an organisation with no runner and no filter reaching it. The
		// Renovate App leaves no workflow, no cron and no repository to be
		// quiet, and opens pull requests all the same.
		for _, r := range results {
			if r.Verdict == quiet.NoRunner {
				app[r.Org] = readApp(r.Org)
			}
		}
	}
	results = quiet.ApplyAppCoverage(results, app)
	// A failed read must never become a verdict. If a live runner's filter
	// could not be read, an organisation "no filter reaches" might in fact be
	// reached by it.
	results = quiet.ApplyUncertainty(results, quiet.UnreadFilters(results))
	if *only != "" {
		// Safe by construction, not safe if someone read the banner.
		results = quiet.ApplyNarrowed(results)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Org != results[j].Org {
			return results[i].Org < results[j].Org
		}
		return results[i].Repo < results[j].Repo
	})

	by := map[quiet.Verdict][]quiet.Result{}
	pages := 0
	for _, r := range results {
		by[r.Verdict] = append(by[r.Verdict], r)
		if r.Verdict.Pages() {
			pages++
		}
	}
	withRunner, shared := 0, 0
	for _, r := range results {
		if r.WorkflowFound {
			withRunner++
			if len(r.Covers) > 1 || (len(r.Covers) == 1 && !quiet.MatchesOrg(r.Covers[0], r.Org)) {
				shared++
			}
		}
	}
	if *fixture == "" {
		fmt.Printf("runners: %d (%d shared)   API calls: %d   snapshot closed %s\n",
			withRunner, shared, calls.n, stamp(time.Now().UTC()))
	}
	for _, m := range moved {
		fmt.Printf("  COVERAGE SNAPSHOT MOVED: %s -- coverage re-resolved\n", m)
	}

	// The observed lag, every pass. The threshold below is a measurement that
	// has to keep being re-taken: GitHub's scheduler was 2.0 to 7.7 hours
	// behind when this was written and still growing, and a slack that stops
	// covering it pages on a fleet that is working perfectly.
	if msg := budgetExhausted(); msg != "" {
		fmt.Printf("\n! RATE LIMITED DURING THIS PASS -- the verdicts below are INCOMPLETE.\n"+
			"  %s\n"+
			"  Every refused call reads as `unreadable`, never as a stopped runner. Re-run when the budget refills.\n", msg)
	}

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
		{quiet.NoRunner, "NOTHING reaches this organisation: no runner of its own, no filter, no App"},
		{quiet.CoverageUnknown, "coverage could not be ESTABLISHED -- an unread filter or an unread App installation, not an absent one"},
		{quiet.Covered, "no runner of its own, and none needed: a shared runner's filter reaches it"},
		{quiet.NotYetDue, "born, not yet due; nothing to do"},
		{quiet.Healthy, ""},
	}
	for _, o := range order {
		rs := by[o.v]
		// no_runner is printed even at zero. "No organisation in this fleet is
		// unwatched" is a strong statement and the only place it can be made;
		// a section that vanishes when it is empty cannot make it.
		if len(rs) == 0 && o.v != quiet.NoRunner {
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
		// Healthy and covered are counts, not lists, unless asked for. Two
		// hundred lines saying "this is fine" is how the one line that is not
		// gets scrolled past.
		if (o.v == quiet.Healthy || o.v == quiet.Covered) && !*all {
			continue
		}
		for _, r := range rs {
			fmt.Println(strings.TrimRight(fmt.Sprintf("    %-42s %s", label(r), detail(r)), " "))
		}
	}

	// The 60-day latch, which is why detection has to fire early: these
	// repositories are public and receive no push except Renovate's own, so if
	// Renovate stops the repository goes quiet, at day 60 GitHub disables the
	// schedule, and the disablement makes the silence permanent.
	// The 60-day latch, and WHERE it actually applies. GitHub disables the
	// scheduled workflows of a quiet repository, so the risk belongs to the
	// repository holding the schedule -- not to the organisations downstream of
	// it. Warning about twenty idle `go-ruby-*/.github` repositories that hold
	// no workflow at all buries the one repository whose idleness would silence
	// all two hundred of them.
	var idle []quiet.Result
	for _, r := range results {
		if r.PushWarn {
			idle = append(idle, r)
		}
	}
	if len(idle) > 0 {
		sort.Slice(idle, func(i, j int) bool { return idle[i].DaysIdle > idle[j].DaysIdle })
		fmt.Printf("\n! runners going quiet (no push for %d+ days; GitHub disables the schedule at %d): %d\n",
			*pushWarn, int(quiet.InactivityLimit/(24*time.Hour)), len(idle))
		for _, r := range idle {
			line := fmt.Sprintf("    %-42s %.0f days idle, latches %s", label(r), r.DaysIdle,
				r.PushedAt.Add(quiet.InactivityLimit).Format("2006-01-02"))
			if n := coveredCount(results, r); n > 0 {
				line += fmt.Sprintf("   -- would silence %d organisations", n)
			}
			fmt.Println(line)
		}
	}

	// A filter that could not be read is reported, never counted as zero
	// coverage: that is the same mistake as calling an unanswered API healthy.
	var opaque []quiet.Result
	for _, r := range results {
		if r.WorkflowFound && r.FilterNote != "" {
			opaque = append(opaque, r)
		}
	}
	if len(opaque) > 0 {
		fmt.Printf("\n  runners whose coverage could not be read: %d\n", len(opaque))
		for _, r := range opaque {
			fmt.Printf("    %-42s %s\n", label(r), r.FilterNote)
		}
	}

	if pages > 0 {
		os.Exit(1)
	}
}

// label names the runner, not the organisation -- but only when there IS one.
// Printing "org/.github" for an organisation that holds no runner reads as a
// runner that exists and is broken, which is the attribution mistake this whole
// change is about.
func label(r quiet.Result) string {
	if r.Repo == "" || !r.WorkflowFound {
		return r.Org
	}
	return r.Org + "/" + r.Repo
}

// coveredCount is the blast radius: how many organisations this runner is the
// only live answer for.
func coveredCount(all []quiet.Result, r quiet.Result) int {
	name := label(r)
	seen := map[string]bool{}
	for _, o := range all {
		if o.CoveredBy == name && !seen[o.Org] {
			seen[o.Org] = true
		}
	}
	return len(seen)
}

func detail(r quiet.Result) string {
	switch r.Verdict {
	case quiet.Unreadable:
		return r.ReadError
	case quiet.Covered:
		s := "covered by " + r.CoveredBy
		if r.CoveredByVerdict.Pages() {
			s += fmt.Sprintf(" -- WHICH IS ITSELF %s", strings.ToUpper(string(r.CoveredByVerdict)))
		}
		return s
	case quiet.CoverageUnknown:
		if r.UnknownWhy != "" {
			return r.UnknownWhy
		}
		return "coverage unknown (App visibility): orgs/" + r.Org + "/installations could not be read"
	case quiet.NoRunner:
		if r.RepoExists {
			return fmt.Sprintf("a repository, but no runner in it, no filter reaching it and no App (last push %s)", stamp(r.PushedAt))
		}
		return "no runner repository, no filter reaching it and no App"
	case quiet.Archived, quiet.DisabledManually, quiet.DisabledInactivity:
		// No run history is fetched for a runner that cannot run, so none is
		// claimed. "last run never" would be a fact nobody read.
		s := fmt.Sprintf("last push %s", stamp(r.PushedAt))
		if len(r.Covers) > 0 {
			s += fmt.Sprintf("   used to watch %s", strings.Join(r.Covers, " "))
		}
		return s
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
