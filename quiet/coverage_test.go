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
