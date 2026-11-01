package quiet

import (
	"testing"
	"time"
)

// A runner shaped exactly like the fleet's: born yesterday morning, a daily
// cron of its own, last run this morning six and a half hours behind it.
func healthyRunner() Runner {
	s, _ := ParseSchedule(realWorkflow) // 44 16 * * *
	return Runner{
		Org:           "go-widgets",
		RepoExists:    true,
		PushedAt:      at("2026-08-31T10:33:41Z"),
		WorkflowFound: true,
		WorkflowState: "active",
		Born:          at("2026-08-30T08:31:48Z"),
		BornSource:    "workflow.created_at",
		Schedule:      s,
		LastRun:       at("2026-08-30T22:12:00Z"),
	}
}

const now = "2026-08-31T14:00:00Z"

func classify(r Runner, at_ string) Result {
	return Classify(r, at(at_), DefaultSlack, DefaultPushWarn)
}

func TestHealthy(t *testing.T) {
	got := classify(healthyRunner(), now)
	if got.Verdict != Healthy {
		t.Fatalf("verdict = %s, want healthy", got.Verdict)
	}
	if got.Late > 0 {
		t.Errorf("Late = %v, want <= 0", got.Late)
	}
	// Lag is measured against the occurrence the run belongs to, not against a
	// nominal midnight: 22:12 against a 16:44 cron is 5h28m behind.
	if !got.HasLag || got.Lag != 5*time.Hour+28*time.Minute {
		t.Errorf("Lag = %v (has=%v), want 5h28m", got.Lag, got.HasLag)
	}
	if got.PushWarn {
		t.Error("PushWarn on a repo pushed today")
	}
	if got.Verdict.Pages() {
		t.Error("healthy must not page")
	}
}

// ⛔⛔ THE SIXTEEN. On 2026-10-31 this tool reported sixteen organisations as
// `healthy` whose Renovate runner failed in about ten seconds every single
// night -- RENOVATE_TOKEN was never set, and an unset secret expands to the
// empty string. go-approx's whole history was 11 runs, 11 failures, zero
// successes, so it had NEVER received a dependency update; the sweep whose only
// job is Renovate runners called it fine, because it asked when the last run
// started and never what became of it.
//
// This is that runner: perfectly timely, and failing.
func TestATimelyRunnerThatFailsIsNotHealthy(t *testing.T) {
	r := healthyRunner()
	r.LastConclusion = "failure"

	got := classify(r, now)
	if got.Verdict != Broken {
		t.Fatalf("verdict = %s, want broken", got.Verdict)
	}
	// The control that matters: the OLD classification is still reachable on
	// the same runner, so this test proves the conclusion is what moved it.
	r.LastConclusion = "success"
	if was := classify(r, now); was.Verdict != Healthy {
		t.Fatalf("with conclusion=success the same runner is %s, want healthy -- "+
			"so this test is not measuring the conclusion", was.Verdict)
	}
	if !Broken.Pages() {
		t.Error("broken must page: sixteen of these went unreported for ten days")
	}
}

// ⚠ AN UNREAD CONCLUSION IS UNREAD. A reader that does not fill the field --
// an older fixture, a run still in flight -- must get the timeliness verdict it
// always got, never a silent `broken`. The same rule that makes a refused call
// Unreadable instead of healthy, in the other direction.
func TestAnEmptyConclusionChangesNothing(t *testing.T) {
	r := healthyRunner()
	r.LastConclusion = ""
	if got := classify(r, now); got.Verdict != Healthy {
		t.Errorf("verdict = %s, want healthy: an unread conclusion is not a failure", got.Verdict)
	}
}

// Failed is an allowlist of failures, not "anything but success". A watcher that
// pages on a cancellation is a watcher people learn to ignore.
func TestOnlyRealFailuresCountAsBroken(t *testing.T) {
	for _, c := range []struct {
		conclusion string
		want       Verdict
	}{
		{"failure", Broken},
		{"timed_out", Broken},
		{"startup_failure", Broken},
		{"success", Healthy},
		// A person stopped it; that is not the runner failing.
		{"cancelled", Healthy},
		// A condition did not hold, so nothing ran and nothing broke.
		{"skipped", Healthy},
		{"neutral", Healthy},
	} {
		r := healthyRunner()
		r.LastConclusion = c.conclusion
		if got := classify(r, now); got.Verdict != c.want {
			t.Errorf("conclusion %q -> %s, want %s", c.conclusion, got.Verdict, c.want)
		}
	}
}

// ⛔ FAILING AND LATE AT ONCE READS AS FAILING, because the failure carries its
// own diagnosis while "overdue" sends the reader to GitHub's scheduler, which
// is working perfectly. The lateness is not lost -- it stays on the Result, and
// the printed line shows it.
func TestAFailingRunnerThatIsAlsoOverdueReadsAsBroken(t *testing.T) {
	r := healthyRunner()
	r.LastConclusion = "failure"

	// Three days past the occurrence it owed.
	got := classify(r, "2026-09-03T14:00:00Z")
	if got.Verdict != Broken {
		t.Fatalf("verdict = %s, want broken", got.Verdict)
	}
	if got.Late <= 0 {
		t.Errorf("Late = %v, want > 0: the lateness must survive the verdict", got.Late)
	}
	// And the control: without the failure, the same clock gives overdue.
	r.LastConclusion = "success"
	if was := classify(r, "2026-09-03T14:00:00Z"); was.Verdict != Overdue {
		t.Fatalf("without the failure the same runner is %s, want overdue", was.Verdict)
	}
}

// ⭐ THE STREAK IS THE DIAGNOSIS, not the failure. "It broke last night" and
// "it has never once worked" are the same red tick and different fixes, and on
// 2026-10-31 it was the streak -- go-approx, 11 runs, 11 failures, zero
// successes -- that showed the sixteen had never been configured at all rather
// than having regressed.
func TestStreak(t *testing.T) {
	ok := RunRecord{Status: "completed", Conclusion: "success"}
	bad := RunRecord{Status: "completed", Conclusion: "failure"}
	slow := RunRecord{Status: "in_progress"}
	stopped := RunRecord{Status: "completed", Conclusion: "cancelled"}

	for _, c := range []struct {
		name         string
		runs         []RunRecord
		wantFailures int
		wantSuccess  bool
	}{
		{"never once worked", []RunRecord{bad, bad, bad, bad}, 4, false},
		{"broke last night", []RunRecord{bad, ok, ok}, 1, true},
		{"failing for three, green before", []RunRecord{bad, bad, bad, ok}, 3, true},
		{"working", []RunRecord{ok, ok}, 0, true},
		// A run in flight has not failed and is not a success either: it must
		// not break the streak nor be counted into it.
		{"a run in flight is skipped, not judged", []RunRecord{slow, bad, bad}, 2, false},
		{"in flight above a success still sees the success", []RunRecord{slow, ok}, 0, true},
		// A cancellation ends the streak without joining it -- claiming an
		// unbroken run of failures through it would overstate the evidence.
		{"a cancellation ends the streak without being one", []RunRecord{bad, stopped, bad}, 1, false},
		{"nothing read at all", nil, 0, false},
	} {
		gotF, gotS := Streak(c.runs)
		if gotF != c.wantFailures || gotS != c.wantSuccess {
			t.Errorf("%s: Streak = (%d, %v), want (%d, %v)",
				c.name, gotF, gotS, c.wantFailures, c.wantSuccess)
		}
	}
}

// A slack of one hour would page on this healthy runner. That is the measured
// mistake the eight-hour default exists to avoid, so it is asserted, not
// remembered.
func TestSlackTooTightPagesOnHealthy(t *testing.T) {
	r := healthyRunner()
	// 16:44 was due; the scheduler is 1h16m behind it, well inside the
	// 1h59m-7h56m measured on this fleet.
	const late = "2026-08-31T18:00:00Z"
	if v := Classify(r, at(late), time.Hour, DefaultPushWarn).Verdict; v != Overdue {
		t.Fatalf("with 1h slack, verdict = %s; the default is not idle", v)
	}
	if v := Classify(r, at(late), DefaultSlack, DefaultPushWarn).Verdict; v != Healthy {
		t.Fatalf("with the default slack, verdict = %s", v)
	}
	// Eight hours left four minutes of headroom against the worst lag observed.
	// Twelve is the margin, and the lag distribution printed every pass is what
	// keeps it under review rather than turning it into folklore.
	if DefaultSlack != 12*time.Hour {
		t.Errorf("DefaultSlack = %v, want 12h", DefaultSlack)
	}
	if worst := 7*time.Hour + 56*time.Minute; worst > DefaultSlack*3/4 {
		t.Error("the measured worst lag should sit inside three quarters of the slack, or the NOTE fires on a healthy fleet")
	}
}

// The break-it cases. Each is a doctored copy of the healthy runner, changed in
// exactly one way, so the verdict names what was changed.
func TestBrokenRunners(t *testing.T) {
	for _, tc := range []struct {
		name   string
		doctor func(*Runner)
		want   Verdict
		pages  bool
	}{
		{"overdue: last run three days ago", func(r *Runner) {
			r.LastRun = at("2026-08-28T16:50:00Z")
		}, Overdue, true},
		{"never fired: born a week ago, no run at all", func(r *Runner) {
			r.Born = at("2026-08-24T08:00:00Z")
			r.LastRun = time.Time{}
		}, NeverFired, true},
		{"not yet due: born this morning, no run at all", func(r *Runner) {
			r.Born = at("2026-08-31T09:00:00Z")
			r.LastRun = time.Time{}
		}, NotYetDue, false},
		{"archived", func(r *Runner) { r.Archived = true }, Archived, false},
		{"disabled for inactivity", func(r *Runner) {
			r.WorkflowState = "disabled_inactivity"
		}, DisabledInactivity, true},
		{"disabled by a person", func(r *Runner) {
			r.WorkflowState = "disabled_manually"
		}, DisabledManually, false},
		{"no cron at all", func(r *Runner) { r.Schedule = nil }, NoSchedule, true},
		{"a cron that never comes round", func(r *Runner) {
			c, _ := ParseCron("0 0 30 2 *")
			r.Schedule = Schedule{c}
			r.LastRun = time.Time{}
		}, NoSchedule, true},
		{"no runner in the .github repo", func(r *Runner) {
			r.WorkflowFound = false
		}, NoRunner, false},
		{"no .github repo at all", func(r *Runner) {
			r.RepoExists = false
		}, NoRunner, false},
		{"the API would not say", func(r *Runner) {
			r.ReadError = "503"
		}, Unreadable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := healthyRunner()
			tc.doctor(&r)
			got := classify(r, now)
			if got.Verdict != tc.want {
				t.Fatalf("verdict = %s, want %s", got.Verdict, tc.want)
			}
			if got.Verdict.Pages() != tc.pages {
				t.Errorf("Pages() = %v, want %v", got.Verdict.Pages(), tc.pages)
			}
			if tc.pages && tc.want == Overdue && got.Late <= 0 {
				t.Errorf("Late = %v, want > 0", got.Late)
			}
		})
	}
}

// Archived is decided before the workflow state, because an archived repository
// accepts no push: naming it "disabled, re-enable it" would send someone to a
// fix that cannot be applied.
func TestArchivedBeatsDisabled(t *testing.T) {
	r := healthyRunner()
	r.Archived = true
	r.WorkflowState = "disabled_inactivity"
	if v := classify(r, now).Verdict; v != Archived {
		t.Fatalf("verdict = %s, want archived", v)
	}
}

// The false alarm this tool exists not to repeat: a runner rolled out at 10:31
// with a cron of 16:44 has not fired, is not late, and is not broken.
func TestNotYetDueIsNotNeverFired(t *testing.T) {
	r := healthyRunner()
	r.Born = at("2026-08-30T08:31:48Z")
	r.LastRun = time.Time{}
	if v := classify(r, "2026-08-30T12:00:00Z").Verdict; v != NotYetDue {
		t.Fatalf("two hours after rollout: %s", v)
	}
	// Its first occurrence, and the slack, must both pass before it pages.
	if v := classify(r, "2026-08-30T17:30:00Z").Verdict; v != NotYetDue {
		t.Fatalf("45 minutes past the first cron: %s", v)
	}
	// Only once the whole slack has also passed does silence become a page.
	if v := classify(r, "2026-08-31T04:00:00Z").Verdict; v != NotYetDue {
		t.Fatalf("eleven hours past the first cron, inside the slack: %s", v)
	}
	if v := classify(r, "2026-08-31T06:00:00Z").Verdict; v != NeverFired {
		t.Fatalf("thirteen hours past the first cron: %s", v)
	}
	// And the first-due time is reported, so "not yet due" can be checked.
	got := classify(r, "2026-08-30T12:00:00Z")
	if !got.Due.Equal(at("2026-08-30T16:44:00Z")) {
		t.Fatalf("Due = %v", got.Due)
	}
}

// A monitor keyed on the last commit to the workflow file goes blind exactly
// when Renovate is working, because Renovate bumps its own action pin in that
// file. Born is keyed on the workflow's creation instead, so a fresh file mtime
// cannot rescue a runner that has stopped.
func TestBirthDoesNotResetWhenTheFileIsRewritten(t *testing.T) {
	r := healthyRunner()
	r.LastRun = at("2026-08-20T16:50:00Z") // stopped eleven days ago
	if v := classify(r, now).Verdict; v != Overdue {
		t.Fatalf("verdict = %s, want overdue", v)
	}
	// Had Born been keyed on a file rewritten an hour ago, the same runner
	// would have read as not-yet-due.
	r.Born = at("2026-08-31T13:00:00Z")
	if v := classify(r, now).Verdict; v != Overdue {
		t.Fatalf("a fresh birth date must not clear a stale last run: %s", v)
	}
}

// The 60-day latch: a quiet repository gets its schedule switched off, and the
// switch-off makes the quiet permanent. The warning has to arrive well before.
func TestPushWarn(t *testing.T) {
	r := healthyRunner()
	r.PushedAt = at("2026-07-01T00:00:00Z") // 61 days
	got := classify(r, now)
	if !got.PushWarn {
		t.Error("61 days idle: want a push warning")
	}
	if int(got.DaysIdle) != 61 {
		t.Errorf("DaysIdle = %.1f, want 61", got.DaysIdle)
	}
	r.PushedAt = at("2026-08-01T00:00:00Z") // 30 days
	if classify(r, now).PushWarn {
		t.Error("30 days idle: no warning yet")
	}
	// 45 days is the threshold, 15 days of margin before GitHub's 60.
	if DefaultPushWarn+15*24*time.Hour != InactivityLimit {
		t.Error("the warning must leave margin before the 60-day latch")
	}
	r.PushedAt = time.Time{}
	if got := classify(r, now); got.PushWarn || got.DaysIdle != 0 {
		t.Error("an unknown push date is not a warning")
	}
}

func TestLags(t *testing.T) {
	mk := func(d time.Duration) Result { return Result{Lag: d, HasLag: true} }
	rs := []Result{
		mk(2 * time.Hour), mk(3 * time.Hour), mk(4 * time.Hour),
		mk(5 * time.Hour), mk(6 * time.Hour), mk(7*time.Hour + 42*time.Minute),
		{Verdict: NotYetDue}, // no run, so no lag: must not be counted as zero
	}
	s := Lags(rs)
	if s.N != 6 {
		t.Fatalf("N = %d, want 6", s.N)
	}
	if s.Min != 2*time.Hour || s.Max != 7*time.Hour+42*time.Minute {
		t.Errorf("min/max = %v/%v", s.Min, s.Max)
	}
	if s.Median != 5*time.Hour {
		t.Errorf("median = %v", s.Median)
	}
	if s.P90 != 7*time.Hour+42*time.Minute {
		t.Errorf("p90 = %v", s.P90)
	}
	if got := Lags(nil); got.N != 0 {
		t.Errorf("empty = %+v", got)
	}
}
