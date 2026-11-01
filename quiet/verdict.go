package quiet

import (
	"math"
	"sort"
	"time"
)

// Verdict is what to DO about a runner, not how bad it is. Four of these are
// four different fixes, and a single count would hide all four:
//
//	Archived            no pull request can fix it; the repository is frozen
//	DisabledInactivity  re-enable it -- and it expires again in 60 days
//	DisabledManually    someone turned it off; that may be entirely legitimate
//	Overdue/NeverFired  the runner itself has stopped
type Verdict string

const (
	// Healthy: it ran, and the next occurrence it owes is not yet due.
	Healthy Verdict = "healthy"
	// Broken: the schedule fires on time and every run it produces FAILS.
	//
	// ⛔⛔ THIS TOOL REPORTED SIXTEEN OF THESE AS `healthy`. On 2026-10-31,
	// sixteen organisations had their Renovate runner failing in about ten
	// seconds every night -- `RENOVATE_TOKEN` was never set, and an unset secret
	// expands to the empty string -- so NONE of them had ever received a
	// dependency update. go-approx's entire history was 11 runs, 11 failures,
	// zero successes. This file asked WHEN the last run started and never what
	// became of it, so all sixteen were timely, and timely read as fine.
	//
	// ⭐ THE LESSON IS THE MEASUREMENT'S SUBJECT, NOT THE MISSING FIELD.
	// `redscan` caught them only incidentally, because the runner's workflow
	// happens to live on a default branch. The specialised sweep -- the one whose
	// whole job is Renovate runners -- was the one that said nothing. Activity is
	// not health, and a runner that fails fast is the most active-looking
	// failure there is.
	Broken Verdict = "broken"
	// NotYetDue: it has never run, but its first scheduled occurrence has not
	// arrived. This is NOT the same as never having fired, and conflating the
	// two is what produced a false alarm about 26 dead runners that were merely
	// queued behind a schedule they had not reached.
	NotYetDue Verdict = "not_yet_due"
	// NeverFired: it has never run and its first occurrence is long past.
	NeverFired Verdict = "never_fired"
	// Overdue: it ran once, and the occurrence after that is long past.
	Overdue Verdict = "overdue"
	// DisabledInactivity: GitHub switched the schedule off after 60 days with
	// no push. Re-enabling starts the same 60-day clock again.
	DisabledInactivity Verdict = "disabled_inactivity"
	// DisabledManually: a person switched it off. One runner in this fleet is
	// deliberately retired, so this is reported and never paged on.
	DisabledManually Verdict = "disabled_manually"
	// Archived: the repository is archived. Nothing can be pushed to it.
	Archived Verdict = "archived"
	// NoSchedule: the workflow exists but carries no cron -- dispatch-only, so
	// nothing will ever start it on its own.
	NoSchedule Verdict = "no_schedule"
	// Covered: this organisation has no runner of its own and does not need
	// one -- a shared runner's autodiscoverFilter reaches it, or the Renovate
	// App is installed on it. Reporting these as missing runners is how a
	// watcher earns 199 lines nobody reads.
	Covered Verdict = "covered"
	// CoverageUnknown: no runner and no filter reaches it, and whether the
	// Renovate App does could not be read. An unread installation is unread,
	// not absent -- the same rule as an autodiscoverFilter that will not parse.
	CoverageUnknown Verdict = "coverage_unknown"
	// NoRunner: no runner reaches this organisation at all -- neither one of
	// its own nor any shared runner's filter. A recency check over runners
	// structurally cannot see this: there is no runner to be quiet.
	//
	// Whether the organisation holds an empty `.github` repository or none at
	// all is a detail of the same gap, so it is said in the line and not split
	// across two verdicts. One number that means one thing is the point.
	NoRunner Verdict = "no_runner"
	// Unreadable: the API would not say. Reported, never silently counted as
	// healthy -- a channel that cannot answer must not read as "fine".
	Unreadable Verdict = "unreadable"
)

// Pages reports whether this verdict is worth waking someone for. Archived and
// a manual disable are real findings with real fixes, but neither is a runner
// that stopped without anyone deciding it should.
func (v Verdict) Pages() bool {
	switch v {
	case Broken, Overdue, NeverFired, DisabledInactivity, NoSchedule, Unreadable:
		return true
	}
	return false
}

// InactivityLimit is GitHub's rule for public repositories: 60 days with no
// repository activity and scheduled workflows are switched off.
const InactivityLimit = 60 * 24 * time.Hour

// DefaultSlack is how far behind its own schedule a run may be before the
// runner counts as stopped.
//
// Twelve hours is not a round number chosen for comfort. GitHub's scheduler was
// measured on this fleet running 1h59m to 7h56m behind its crons and still
// growing; a one-hour tolerance would have paged on 113 runners that were
// working perfectly, and eight hours left four minutes of headroom against the
// worst lag observed. A pager that goes off on a fleet with nothing wrong is
// the same failure as an informational section nobody reads, and it costs more.
//
// quietscan prints the observed lag distribution on every pass, and says so out
// loud when the worst lag passes three quarters of this, so the number keeps
// being re-measured rather than becoming folklore.
const DefaultSlack = 12 * time.Hour

// DefaultPushWarn is when to start warning about the 60-day clock. All runner
// repositories here are public, and a .github repository whose only content is
// the runner receives no pushes except Renovate's own -- so if Renovate stops,
// the repository goes quiet, and at day 60 GitHub disables the schedule, which
// makes the silence permanent. Fifteen days of warning before that latch.
const DefaultPushWarn = 45 * 24 * time.Hour

// Runner is everything read about one organisation's runner. Every field is
// filled from a GET; nothing here can start a run.
type Runner struct {
	Org string
	// Repo is which repository holds the runner: `.github` for an
	// organisation's own, `renovate-runner` for a shared one. Empty when the
	// organisation holds no runner anywhere.
	Repo          string
	RepoExists    bool
	Archived      bool
	PushedAt      time.Time // .github repository's last push; drives the 60-day clock
	WorkflowFound bool
	WorkflowState string // active | disabled_inactivity | disabled_manually
	Born          time.Time
	BornSource    string // what Born was keyed on, so a wrong clock is visible
	Schedule      Schedule
	LastRun       time.Time // zero means: no scheduled run has ever completed
	// LastConclusion is what became of that run: GitHub's `conclusion`, one of
	// success, failure, timed_out, startup_failure, cancelled, skipped, neutral,
	// stale, action_required.
	//
	// ⚠ EMPTY MEANS NOT READ, AND MUST NOT READ AS EITHER VERDICT. A reader that
	// leaves this blank gets the old timeliness-only classification rather than
	// a silent `broken` -- an unread field is unread, the same rule that makes a
	// refused call Unreadable instead of healthy.
	LastConclusion string
	// ConsecutiveFailures is how many of the most recent scheduled runs failed
	// in a row, and RunsRead how many were on the page they were counted from.
	// SawSuccess says the streak ENDED inside that window -- that a run in
	// living memory actually worked.
	//
	// ⭐ THIS IS THE DIFFERENCE BETWEEN "IT BROKE LAST NIGHT" AND "IT HAS NEVER
	// WORKED", and the two take different fixes. What made the sixteen
	// diagnosable on 2026-10-31 was not the failure, it was the streak:
	// go-approx had 11 runs, 11 failures, zero successes, so the runner was
	// never configured rather than recently regressed. It costs nothing to know
	// -- the same single request returns the page.
	ConsecutiveFailures int
	RunsRead            int
	SawSuccess          bool
	// Covers is the runner's autodiscoverFilter: the organisations it watches,
	// which are NOT the organisation it lives in.
	Covers     []string
	FilterNote string // why Covers could not be read, if it could not
	ReadError  string
}

// Result is one runner's verdict and the numbers behind it.
type Result struct {
	Runner
	Verdict Verdict
	// Due is the occurrence the runner owes: the next one after its last run,
	// or after its birth if it has never run.
	Due time.Time
	// Late is how far past Due+slack the clock now is. Positive only when the
	// verdict pages.
	Late time.Duration
	// Lag is how far behind its own cron the last run actually started. This is
	// the measurement that justifies the slack, so it is collected on healthy
	// runners too.
	Lag      time.Duration
	HasLag   bool
	DaysIdle float64
	PushWarn bool
	// CoveredBy is the shared runner that reaches this organisation, filled in
	// fleet-wide by ApplyCoverage.
	CoveredBy        string
	CoveredByVerdict Verdict
	// UnknownWhy says what could not be read, when the verdict is
	// CoverageUnknown. A watcher that cannot say WHY it does not know is not
	// much better than one that guesses.
	UnknownWhy string
}

// Failed says whether a run's `conclusion` means the runner did not do its job.
//
// It is an ALLOWLIST OF FAILURES, not "anything that is not success", and the
// difference is the point: `cancelled` is usually a person, `skipped` is a
// condition that did not hold, `neutral` is a run that declined to judge, and
// paging on any of those is how a watcher earns being ignored. They fall through
// to the timeliness verdicts and the printed line still shows the word, so a
// reader can see a cancellation without being woken for it.
//
// An empty conclusion is NOT a failure: it means nobody read the field.
func Failed(conclusion string) bool {
	switch conclusion {
	case "failure", "timed_out", "startup_failure":
		return true
	}
	return false
}

// RunRecord is one scheduled run, reduced to what a health check needs.
type RunRecord struct {
	Status     string // queued | in_progress | completed
	Conclusion string
}

// Streak counts, newest first, how many runs failed in a row, and says whether
// a success appears anywhere in the window at all.
//
// A run still in flight is SKIPPED rather than counted either way: it has not
// failed, and it is not a success that would end the streak. A cancellation
// ends the streak without being part of it -- the run did not fail, so claiming
// an unbroken run of failures through it would overstate the evidence.
func Streak(runs []RunRecord) (failures int, sawSuccess bool) {
	for _, r := range runs {
		if r.Status != "completed" {
			continue
		}
		if r.Conclusion == "success" {
			return failures, true
		}
		if !Failed(r.Conclusion) {
			return failures, false
		}
		failures++
	}
	return failures, false
}

// Classify decides one runner's verdict.
//
// The birth date is keyed on the workflow's own created_at, NEVER on the last
// commit that touched the workflow file. A healthy Renovate rewrites that file
// whenever it bumps its own action pin, so a monitor keyed on file mtime resets
// its own clock every time Renovate works -- and goes blind precisely when
// things are fine.
func Classify(r Runner, now time.Time, slack, pushWarn time.Duration) Result {
	res := Result{Runner: r}
	if !r.PushedAt.IsZero() {
		res.DaysIdle = now.Sub(r.PushedAt).Hours() / 24
		// The 60-day rule disables SCHEDULED WORKFLOWS in the repository that
		// holds them. A repository holding no workflow has no schedule to lose,
		// so an idle one is not a latch risk however quiet it is -- and warning
		// about twenty of those buries the one repository whose idleness would
		// actually silence a whole family.
		res.PushWarn = r.WorkflowFound && now.Sub(r.PushedAt) >= pushWarn
	}

	switch {
	case r.ReadError != "":
		res.Verdict = Unreadable
		return res
	case !r.RepoExists:
		res.Verdict = NoRunner
		return res
	case r.Archived:
		// Checked before the workflow: an archived repository accepts no push,
		// so no pull request can fix anything found below this line.
		res.Verdict = Archived
		return res
	case !r.WorkflowFound:
		res.Verdict = NoRunner
		return res
	case r.WorkflowState == "disabled_inactivity":
		res.Verdict = DisabledInactivity
		return res
	case r.WorkflowState == "disabled_manually":
		res.Verdict = DisabledManually
		return res
	case len(r.Schedule) == 0:
		res.Verdict = NoSchedule
		return res
	}

	if !r.LastRun.IsZero() {
		if prev, ok := r.Schedule.Prev(r.LastRun); ok {
			res.Lag, res.HasLag = r.LastRun.Sub(prev), true
		}
	}

	// The clock starts at the last run, and only at the birth date when there
	// has never been one. Taking the LATER of the two would let a fresh birth
	// date clear a stale run -- the same blindness as keying on the workflow
	// file's mtime, arriving by a different door.
	from := r.LastRun
	if from.IsZero() {
		from = r.Born
	}
	due, ok := r.Schedule.Next(from)
	if !ok {
		// A schedule with no reachable occurrence will never start anything.
		res.Verdict = NoSchedule
		return res
	}
	res.Due = due
	res.Late = now.Sub(due.Add(slack))
	switch {
	// ⛔ A FAILING RUN OUTRANKS A LATE ONE, because it carries its own
	// diagnosis. A runner that is both failing and overdue is reported broken:
	// the failure says WHY, while "overdue" sends the reader to look at GitHub's
	// scheduler, which is working perfectly. The line still prints the lateness.
	case Failed(r.LastConclusion):
		res.Verdict = Broken
	case res.Late <= 0 && r.LastRun.IsZero():
		res.Verdict = NotYetDue
	case res.Late <= 0:
		res.Verdict = Healthy
	case r.LastRun.IsZero():
		res.Verdict = NeverFired
	default:
		res.Verdict = Overdue
	}
	return res
}

// LagStats summarises how far behind their crons the runs actually started.
// Printing this every pass is what keeps the slack an observation rather than a
// belief: if the distribution creeps towards the threshold, the threshold is
// wrong before it pages on healthy runners.
type LagStats struct {
	N                     int
	Min, Median, P90, Max time.Duration
}

// Lags summarises the lag of every result that has one.
func Lags(rs []Result) LagStats {
	var v []time.Duration
	for _, r := range rs {
		if r.HasLag {
			v = append(v, r.Lag)
		}
	}
	if len(v) == 0 {
		return LagStats{}
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	pick := func(q float64) time.Duration {
		i := int(math.Round(q * float64(len(v)-1)))
		return v[i]
	}
	return LagStats{N: len(v), Min: v[0], Median: pick(0.5), P90: pick(0.9), Max: v[len(v)-1]}
}
