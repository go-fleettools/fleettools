package quiet

import "testing"

// The three real configurations on this fleet, verbatim in shape.
const (
	rubyConfig = `module.exports = {
  platform: 'github',
  autodiscover: true,
  autodiscoverFilter: ['go-ruby-*/**'],
  onboarding: false,
};`
	pdfConfig = `module.exports = {
  autodiscover: true,
  autodiscoverFilter: ['go-pdfkit/**', 'go-gfx/**', 'go-opentype/**', 'go-widgets/**'],
};`
	// go-attest builds its filter from an inline list sliced by an env var.
	// No string scan can know what it resolves to, and pretending it resolves
	// to nothing would be the same mistake in a smaller coat.
	attestConfig = `const orgs = ['libfw', 'libhcl'];
const mine = orgs.slice(0, 1);
module.exports = {
  autodiscover: true,
  autodiscoverFilter: mine.map((o) => ` + "`${o}/**`" + `),
};`
	ownConfig = `module.exports = { autodiscover: true, autodiscoverFilter: ['go-widgets/**'] };`
)

func TestParseAutodiscoverFilter(t *testing.T) {
	got, note := ParseAutodiscoverFilter(rubyConfig)
	if note != "" || len(got) != 1 || got[0] != "go-ruby-*/**" {
		t.Fatalf("ruby = %v %q", got, note)
	}
	got, note = ParseAutodiscoverFilter(pdfConfig)
	if note != "" || len(got) != 4 || got[3] != "go-widgets/**" {
		t.Fatalf("pdf = %v %q", got, note)
	}
	got, note = ParseAutodiscoverFilter(ownConfig)
	if note != "" || len(got) != 1 {
		t.Fatalf("own = %v %q", got, note)
	}
}

// Everything that cannot be read must SAY it cannot be read.
func TestParseAutodiscoverFilterUnreadable(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"computed", attestConfig, "computed"},
		{"no filter at all", `module.exports = { autodiscover: true };`, "no filter"},
		{"not an assignment", `// autodiscoverFilter is documented here`, "not an assignment"},
		{"unterminated", `autodiscoverFilter: ['go-ruby-*/**'`, "unterminated"},
		{"no string literal", `autodiscoverFilter: [],`, "no string literal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, note := ParseAutodiscoverFilter(tc.src)
			if got != nil {
				t.Errorf("patterns = %v, want none", got)
			}
			if note == "" || !contains(note, tc.want) {
				t.Errorf("note = %q, want it to mention %q", note, tc.want)
			}
		})
	}
	// A config with no autodiscover at all watches only itself: nothing to
	// report, and nothing to claim.
	if got, note := ParseAutodiscoverFilter(`module.exports = { platform: 'github' };`); got != nil || note != "" {
		t.Errorf("no autodiscover = %v %q", got, note)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestMatchesOrg(t *testing.T) {
	for _, tc := range []struct {
		pattern, org string
		want         bool
	}{
		{"go-ruby-*/**", "go-ruby-facter", true},
		{"go-ruby-*/**", "go-ruby-stdlib", true},
		{"go-ruby-*/**", "go-rubyish", false}, // the hyphen is part of the prefix
		{"go-ruby-*/**", "go-widgets", false},
		{"go-ruby-*/**", "openweft", false},
		{"go-widgets/**", "go-widgets", true},
		{"go-widgets/**", "go-widgets-extra", false},
		{"go-pdfkit/**", "go-gfx", false},
		{"openweft", "openweft", true}, // a pattern with no slash is all owner
		{"*/**", "anything", true},
		{"go-*-stdlib/**", "go-ruby-stdlib", true},
		{"go-*-stdlib/**", "go-ruby-parser", false},
	} {
		if got := MatchesOrg(tc.pattern, tc.org); got != tc.want {
			t.Errorf("MatchesOrg(%q, %q) = %v, want %v", tc.pattern, tc.org, got, tc.want)
		}
	}
}

func covResult(org, repo string, v Verdict, covers ...string) Result {
	return Result{Runner: Runner{Org: org, Repo: repo, WorkflowFound: repo != "", Covers: covers}, Verdict: v}
}

func TestApplyCoverage(t *testing.T) {
	rs := ApplyCoverage([]Result{
		covResult("go-ruby-stdlib", "renovate-runner", Healthy, "go-ruby-*/**"),
		covResult("go-ruby-facter", "", NoRunner),
		covResult("go-ruby-hiera", "", NoRunner),
		covResult("openweft", "", NoRunner),
		covResult("go-widgets", ".github", Healthy, "go-widgets/**"),
	})
	want := map[string]struct {
		v  Verdict
		by string
	}{
		"go-ruby-stdlib": {Healthy, ""}, // covers others, not itself
		"go-ruby-facter": {Covered, "go-ruby-stdlib/renovate-runner"},
		"go-ruby-hiera":  {Covered, "go-ruby-stdlib/renovate-runner"},
		"openweft":       {NoRunner, ""}, // no filter reaches it: the real gap
		"go-widgets":     {Healthy, ""},
	}
	for _, r := range rs {
		w := want[r.Org]
		if r.Verdict != w.v || r.CoveredBy != w.by {
			t.Errorf("%s: verdict %s by %q, want %s by %q", r.Org, r.Verdict, r.CoveredBy, w.v, w.by)
		}
	}
	// A coverage gap is a standing condition, not a runner that stopped. It is
	// printed first among the findings and ALWAYS printed, zero included, but
	// it does not page: a watcher that exits 1 every day for a known, accepted
	// state teaches people to ignore its exit code, and then the real page goes
	// with it.
	if NoRunner.Pages() || Covered.Pages() {
		t.Error("a coverage gap is a finding, not a page")
	}
}

// A retired runner covers nothing. go-attest/renovate-runner is
// disabled_manually, and reading it as still covering 109 organisations would
// report a gap as if it were filled.
func TestRetiredRunnerCoversNothing(t *testing.T) {
	for _, v := range []Verdict{DisabledManually, DisabledInactivity, Archived, NoSchedule, Unreadable} {
		if v.Live() {
			t.Errorf("%s must not count as live coverage", v)
		}
		rs := ApplyCoverage([]Result{
			covResult("go-attest", "renovate-runner", v, "libfw/**"),
			covResult("libfw", "", NoRunner),
		})
		for _, r := range rs {
			if r.Org == "libfw" && r.Verdict != NoRunner {
				t.Errorf("%s: libfw = %s, want no_runner", v, r.Verdict)
			}
		}
	}
	for _, v := range []Verdict{Healthy, NotYetDue, Overdue, NeverFired} {
		if !v.Live() {
			t.Errorf("%s is enabled and must count as coverage", v)
		}
	}
}

// Being covered by a runner that has itself stopped is not coverage anyone
// should read past, so it is named.
func TestCoveredByAPagingRunnerSaysSo(t *testing.T) {
	rs := ApplyCoverage([]Result{
		covResult("go-ruby-stdlib", "renovate-runner", Overdue, "go-ruby-*/**"),
		covResult("go-ruby-hiera", "", NoRunner),
	})
	for _, r := range rs {
		if r.Org == "go-ruby-hiera" {
			if r.Verdict != Covered || !r.CoveredByVerdict.Pages() {
				t.Fatalf("hiera = %s covered by %s (%s)", r.Verdict, r.CoveredBy, r.CoveredByVerdict)
			}
		}
	}
	// A healthy runner is preferred over a stopped one when both reach it.
	rs = ApplyCoverage([]Result{
		covResult("a-runner", "renovate-runner", Overdue, "go-ruby-*/**"),
		covResult("b-runner", "renovate-runner", Healthy, "go-ruby-*/**"),
		covResult("go-ruby-hiera", "", NoRunner),
	})
	for _, r := range rs {
		if r.Org == "go-ruby-hiera" && r.CoveredBy != "b-runner/renovate-runner" {
			t.Errorf("covered by %q, want the healthy runner", r.CoveredBy)
		}
	}
}

// A runner with an unreadable filter claims no coverage -- and the command
// prints it in its own section, so it is never silently zero.
func TestUnreadableFilterClaimsNothing(t *testing.T) {
	rs := ApplyCoverage([]Result{
		covResult("go-attest", "renovate-runner", Healthy), // no Covers at all
		covResult("libfw", "", NoRunner),
	})
	for _, r := range rs {
		if r.Org == "libfw" && r.Verdict != NoRunner {
			t.Errorf("libfw = %s", r.Verdict)
		}
	}
}

// The latch is on the repository holding the schedule. A repository with no
// workflow has no schedule to lose, however quiet it gets.
func TestPushWarnOnlyWhereThereIsASchedule(t *testing.T) {
	old := at("2026-06-01T00:00:00Z")
	nowT := at("2026-08-31T14:00:00Z")

	holds := Runner{Org: "go-ruby-stdlib", Repo: "renovate-runner", RepoExists: true,
		PushedAt: old, WorkflowFound: true, WorkflowState: "active"}
	if got := Classify(holds, nowT, DefaultSlack, DefaultPushWarn); !got.PushWarn {
		t.Error("a repository holding a scheduled workflow must warn")
	}

	holdsNone := Runner{Org: "go-ruby-facter", Repo: ".github", RepoExists: true, PushedAt: old}
	got := Classify(holdsNone, nowT, DefaultSlack, DefaultPushWarn)
	if got.PushWarn {
		t.Error("a repository holding no workflow cannot latch and must not warn")
	}
	// The idle days are still computed -- the fact is reported, the false
	// alarm is not raised.
	if int(got.DaysIdle) != 91 {
		t.Errorf("DaysIdle = %.0f, want 91", got.DaysIdle)
	}
}

// A filter with a middle wildcard, and an unterminated quote. Neither shape is
// on the fleet today; both are one edit away, and a glob that silently says
// "no" is how an organisation stops being watched without anyone noticing.
func TestGlobMiddleWildcardAndRaggedQuotes(t *testing.T) {
	for _, tc := range []struct {
		pattern, org string
		want         bool
	}{
		{"go-*-ruby-*/**", "go-embedded-ruby-x", true},
		{"go-*-ruby-*/**", "go-embedded-rust-x", false},
		{"a*b*c/**", "axxbyyc", true},
		{"a*b*c/**", "axxc", false},
	} {
		if got := MatchesOrg(tc.pattern, tc.org); got != tc.want {
			t.Errorf("MatchesOrg(%q, %q) = %v, want %v", tc.pattern, tc.org, got, tc.want)
		}
	}
	// An opening quote with no partner yields what was readable, not a panic.
	got, note := ParseAutodiscoverFilter(`autodiscoverFilter: ['go-ruby-*/**', 'ragged],`)
	if note != "" || len(got) != 1 || got[0] != "go-ruby-*/**" {
		t.Errorf("ragged quotes = %v %q", got, note)
	}
}

// The third form of coverage. openweft has no runner, no filter reaches it, and
// the Renovate App is installed with 16 open pull requests to show for it. A
// census of workflows structurally cannot see that -- there is no workflow, no
// cron and no repository to be quiet.
func TestApplyAppCoverage(t *testing.T) {
	in := []Result{
		{Runner: Runner{Org: "openweft"}, Verdict: NoRunner},
		{Runner: Runner{Org: "go-gitsafe"}, Verdict: NoRunner},
		{Runner: Runner{Org: "go-gtk"}, Verdict: NoRunner},
		{Runner: Runner{Org: "go-widgets", Repo: ".github", WorkflowFound: true}, Verdict: Healthy},
	}
	out := ApplyAppCoverage(in, map[string]AppCoverage{
		"openweft":   AppPresent,
		"go-gitsafe": AppAbsent,
		"go-gtk":     AppUnread,
	})
	want := map[string]struct {
		v  Verdict
		by string
	}{
		"openweft":   {Covered, AppCoveredBy},
		"go-gitsafe": {NoRunner, ""},        // read, and genuinely nothing
		"go-gtk":     {CoverageUnknown, ""}, // refused: unread is not absent
		"go-widgets": {Healthy, ""},
	}
	for _, r := range out {
		w := want[r.Org]
		if r.Verdict != w.v || r.CoveredBy != w.by {
			t.Errorf("%s: %s by %q, want %s by %q", r.Org, r.Verdict, r.CoveredBy, w.v, w.by)
		}
	}
	if CoverageUnknown.Pages() || CoverageUnknown.Live() {
		t.Error("an unknown is a line to read, not a page, and covers nothing")
	}
}

// The App is asked about ONLY where it can change an answer. An organisation
// already covered by a filter is not re-labelled as covered by the App.
func TestAppCoverageDoesNotOverrideAFilter(t *testing.T) {
	in := []Result{{Runner: Runner{Org: "go-ruby-aasm"}, Verdict: Covered,
		CoveredBy: "go-ruby-stdlib/renovate-runner", CoveredByVerdict: Healthy}}
	out := ApplyAppCoverage(in, map[string]AppCoverage{"go-ruby-aasm": AppPresent})
	if out[0].CoveredBy != "go-ruby-stdlib/renovate-runner" {
		t.Errorf("covered by %q -- a filter answer must not be overwritten", out[0].CoveredBy)
	}
	// And an organisation never asked about is left exactly as it was.
	in = []Result{{Runner: Runner{Org: "never-asked"}, Verdict: NoRunner}}
	if out := ApplyAppCoverage(in, map[string]AppCoverage{}); out[0].Verdict != NoRunner {
		t.Errorf("unasked = %s, want no_runner unchanged", out[0].Verdict)
	}
}

// The rule the coordinator's rate-limit outage made concrete: a failed read
// must never become a verdict. If a live runner's filter could not be read, an
// organisation "no filter reaches" might in fact be reached by it.
func TestUnreadFilterMakesCoverageUnknownNotAGap(t *testing.T) {
	live := Result{Runner: Runner{Org: "go-ruby-stdlib", Repo: "renovate-runner",
		WorkflowFound: true, FilterNote: "config.js: HTTP 403"}, Verdict: Healthy}
	gap := Result{Runner: Runner{Org: "go-ruby-aasm"}, Verdict: NoRunner}

	unread := UnreadFilters([]Result{live, gap})
	if len(unread) != 1 || unread[0] != "go-ruby-stdlib/renovate-runner" {
		t.Fatalf("UnreadFilters = %v", unread)
	}
	out := ApplyUncertainty([]Result{live, gap}, unread)
	if out[1].Verdict != CoverageUnknown {
		t.Fatalf("gap = %s, want coverage_unknown", out[1].Verdict)
	}
	if !contains(out[1].UnknownWhy, "go-ruby-stdlib/renovate-runner") {
		t.Errorf("UnknownWhy = %q, should name what could not be read", out[1].UnknownWhy)
	}

	// A DISABLED runner's unread filter creates no doubt: it covers nothing
	// either way, so the gap stays a gap and the strong statement survives.
	dead := live
	dead.Verdict = DisabledManually
	if u := UnreadFilters([]Result{dead, gap}); len(u) != 0 {
		t.Errorf("a retired runner's unread filter should raise no doubt: %v", u)
	}
	if out := ApplyUncertainty([]Result{dead, gap}, nil); out[1].Verdict != NoRunner {
		t.Errorf("gap = %s, want no_runner", out[1].Verdict)
	}

	// A runner whose filter WAS read raises no doubt either.
	ok := live
	ok.FilterNote = ""
	ok.Covers = []string{"go-ruby-*/**"}
	if u := UnreadFilters([]Result{ok, gap}); len(u) != 0 {
		t.Errorf("a readable filter should raise no doubt: %v", u)
	}
}

// A narrowed pass cannot see the filter that covers an organisation, so it must
// not claim nothing does. `quietscan -orgs go-ruby-aasm` reads no_runner for an
// organisation go-ruby-stdlib/renovate-runner covers perfectly well.
func TestNarrowedPassCannotClaimAGap(t *testing.T) {
	in := []Result{
		{Runner: Runner{Org: "go-ruby-aasm"}, Verdict: NoRunner},
		{Runner: Runner{Org: "openweft"}, Verdict: Covered, CoveredBy: AppCoveredBy},
		{Runner: Runner{Org: "go-widgets", Repo: ".github", WorkflowFound: true}, Verdict: Healthy},
	}
	out := ApplyNarrowed(in)
	if out[0].Verdict != CoverageUnknown || !contains(out[0].UnknownWhy, "outside -orgs") {
		t.Errorf("gap = %s (%q), want coverage_unknown naming the narrowing", out[0].Verdict, out[0].UnknownWhy)
	}
	// An answer a narrowed pass CAN establish is untouched: an App installation
	// and a runner's own health are properties of the organisation itself.
	if out[1].Verdict != Covered || out[2].Verdict != Healthy {
		t.Errorf("narrowing changed what it could actually see: %s %s", out[1].Verdict, out[2].Verdict)
	}
}
