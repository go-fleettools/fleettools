package main

import (
	"strings"
	"testing"
)

// TestADirtyTreeIsAnErrorNotASilentPatch is the founding case.
//
// ⛔ gorelease refuses an unclean tree and prints nothing else. A probe that
// grepped for "Suggested" read that as a module with nothing to report, and
// would have published a patch for a module it never looked at.
func TestADirtyTreeIsAnErrorNotASilentPatch(t *testing.T) {
	r := parseGorelease("gorelease: repo /tmp/x has uncommitted changes\n", "linux")
	if r.err == "" {
		t.Fatalf("a refusal must read as an error, got %+v", r)
	}
	if r.suggested != "" {
		t.Fatalf("a refusal must suggest nothing, got %q", r.suggested)
	}
}

func TestOutputWithNoSuggestionIsUnreadNotUnchanged(t *testing.T) {
	r := parseGorelease("some unexpected chatter\n", "linux")
	if r.err == "" {
		t.Fatalf("output this cannot read must be an error, got %+v", r)
	}
}

func TestParseGoreleaseReadsWhatGoreleaseActuallyPrints(t *testing.T) {
	// Copied from a real run against go-xrkit/depth3d with New removed.
	const out = "# summary\n## incompatible changes\nNew: removed\nSuggested version: v0.2.0\n"
	r := parseGorelease(out, "linux")
	if r.err != "" {
		t.Fatalf("unexpected error %q", r.err)
	}
	if r.suggested != "v0.2.0" {
		t.Errorf("suggested = %q, want v0.2.0", r.suggested)
	}
	if r.breaks != 1 {
		t.Errorf("breaks = %d, want 1", r.breaks)
	}
}

// TestOnePlatformDisagreeingIsNotOverruled replays the measurement this whole
// command exists for: on a darwin host the break was invisible.
func TestOnePlatformDisagreeingIsNotOverruled(t *testing.T) {
	saved := askGorelease
	defer func() { askGorelease = saved }()
	askGorelease = func(_, _, goos string) reading {
		if goos == "darwin" {
			return reading{goos: goos, suggested: "v0.1.1"}
		}
		return reading{goos: goos, suggested: "v0.2.0", breaks: 1}
	}
	v := read("go-xrkit/depth3d", "/nowhere", "v0.1.0", []string{"darwin", "linux", "windows"})
	if v.next != "v0.2.0" {
		t.Errorf("next = %q, want the HIGHEST suggestion v0.2.0", v.next)
	}
	if v.breaks != 2 {
		t.Errorf("breaks = %d, want 2", v.breaks)
	}
	if !v.disagree {
		t.Error("platforms suggested different versions; that must be recorded")
	}
	if v.safe() {
		t.Error("a module that breaks on two platforms must never be called safe")
	}
}

// TestAnUnreadPlatformMakesTheModuleUnread: a verdict folded from a partial
// reading is the defect this repository keeps meeting.
func TestAnUnreadPlatformMakesTheModuleUnread(t *testing.T) {
	saved := askGorelease
	defer func() { askGorelease = saved }()
	askGorelease = func(_, _, goos string) reading {
		if goos == "windows" {
			return reading{goos: goos, err: "build constraints exclude all Go files"}
		}
		return reading{goos: goos, suggested: "v0.1.1"}
	}
	v := read("o/r", "/nowhere", "v0.1.0", []string{"linux", "darwin", "windows"})
	if len(v.unread) != 1 {
		t.Fatalf("unread = %v, want one entry", v.unread)
	}
	if v.safe() {
		t.Error("a module one platform could not be read for must not be called safe")
	}
}

func TestSafeIsOnlyEverAPatchThatEverybodyAgreedOn(t *testing.T) {
	for _, c := range []struct {
		name string
		v    verdict
		want bool
	}{
		{"a clean patch", verdict{base: "v0.1.0", next: "v0.1.1"}, true},
		{"a minor bump is the API moving", verdict{base: "v0.1.0", next: "v0.2.0"}, false},
		{"a major bump", verdict{base: "v1.2.3", next: "v2.0.0"}, false},
		{"a patch that broke something", verdict{base: "v0.1.0", next: "v0.1.1", breaks: 1}, false},
		{"a patch the platforms disagreed on", verdict{base: "v0.1.0", next: "v0.1.1", disagree: true}, false},
		{"a patch with a platform unread", verdict{base: "v0.1.0", next: "v0.1.1", unread: []string{"windows: x"}}, false},
		{"no change at all", verdict{base: "v0.1.0", next: "v0.1.0"}, false},
		// ⛔ A prerelease base cannot be compared field by field, so it is never
		// safe: openweft carries v0.3.0-rc11 and v0.4.0-rc8 tags.
		{"a prerelease base", verdict{base: "v0.4.0-rc8", next: "v0.4.0"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.v.safe(); got != c.want {
				t.Errorf("safe() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestHigherOrdersNumericallyNotAsText(t *testing.T) {
	// ⛔ "v0.10.0" < "v0.9.0" as strings. go-tex/engine is at v0.228.0 and
	// go-widgets/toolkit at v0.321.2, so this is not hypothetical.
	for _, c := range [][3]string{
		{"v0.9.0", "v0.10.0", "v0.10.0"},
		{"v0.228.0", "v0.99.0", "v0.228.0"},
		{"v0.321.2", "v0.321.10", "v0.321.10"},
		{"v1.0.0", "v0.999.999", "v1.0.0"},
	} {
		if got := higher(c[0], c[1]); got != c[2] {
			t.Errorf("higher(%s, %s) = %s, want %s", c[0], c[1], got, c[2])
		}
		if got := higher(c[1], c[0]); got != c[2] {
			t.Errorf("higher(%s, %s) = %s, want %s (not order-dependent)", c[1], c[0], got, c[2])
		}
	}
}

func TestSplitSemverRefusesWhatCannotBeCompared(t *testing.T) {
	for _, bad := range []string{"0.1.0", "v0.1", "v0.1.0.1", "v0.1.0-rc1", "v0.1.0+meta", "vx.y.z", "v0..1", ""} {
		if splitSemver(bad) != nil {
			t.Errorf("%q must not parse as a comparable version", bad)
		}
	}
	if got := splitSemver("v0.321.2"); len(got) != 3 || got[1] != "321" {
		t.Errorf("v0.321.2 = %v", got)
	}
}

func TestGoreleaseOutputIsReadLineByLineNotSearched(t *testing.T) {
	// A changelog body mentioning the words must not be mistaken for a verdict.
	const out = "# summary\nThe docs now say: Suggested version: v9.9.9 is wrong.\nSuggested version: v0.1.1\n"
	r := parseGorelease(out, "linux")
	if r.suggested != "v0.1.1" {
		t.Errorf("suggested = %q; only a line that STARTS with the prefix counts", r.suggested)
	}
	if strings.Contains(r.suggested, "9") {
		t.Errorf("prose was read as a verdict: %q", r.suggested)
	}
}

// TestAFrozenBaseIsNeverSafe pins the one-way risk. A module whose tag sits on
// a rewritten history was first filed under "never tagged", and
// go-filesystems/xfs v0.1.0 is published and required by six repositories --
// "never tagged" is an invitation to cut it a second time.
func TestAFrozenBaseIsNeverSafe(t *testing.T) {
	v := verdict{base: "v0.1.0", next: "v0.1.1", frozen: true}
	if v.safe() {
		t.Error("a frozen base must never be called safe, however small the bump")
	}
	v.frozen = false
	if !v.safe() {
		t.Error("the control: the same verdict unfrozen IS safe, so frozen is what decided it")
	}
}

// TestAMinorWithNoChangesSectionIsNotAnAddition is the correction that cost the
// most to find.
//
// ⛔ gorelease suggests a minor for THREE reasons, and only two of them are the
// API: its requirementsChanged() also fires when a dependency moves up a minor,
// when one is added, or when the go directive rises. go-simd/floats suggests
// v0.2.0 printing NO changes section, and its only cause is
// golang.org/x/sys v0.46.0 -> v0.48.0. Labelling that "additions only" was a
// story this tool invented to fill a column.
func TestAMinorWithNoChangesSectionIsNotAnAddition(t *testing.T) {
	// Exactly what gorelease printed for go-simd/floats.
	r := parseGorelease("# summary\nSuggested version: v0.2.0\n", "linux")
	if r.err != "" {
		t.Fatalf("unexpected error %q", r.err)
	}
	if r.breaks != 0 || r.grew != 0 {
		t.Fatalf("no section was printed, so neither counter may move: breaks=%d grew=%d", r.breaks, r.grew)
	}
}

func TestCompatibleAndIncompatibleAreCountedApart(t *testing.T) {
	for _, c := range []struct {
		name                string
		out                 string
		wantGrew, wantBreak int
	}{
		{"additions", "## compatible changes\nF: added\nSuggested version: v0.2.0\n", 1, 0},
		{"a removal", "## incompatible changes\nF: removed\nSuggested version: v0.2.0\n", 0, 1},
		{"both at once", "## incompatible changes\nF: removed\n## compatible changes\nG: added\nSuggested version: v0.2.0\n", 1, 1},
		{"neither", "Suggested version: v0.1.1\n", 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := parseGorelease(c.out, "linux")
			if r.grew != c.wantGrew || r.breaks != c.wantBreak {
				t.Errorf("grew=%d breaks=%d, want grew=%d breaks=%d", r.grew, r.breaks, c.wantGrew, c.wantBreak)
			}
		})
	}
}

// TestGrewIsFoldedPerPlatform: a module whose API grew on one platform only is
// still a module whose API grew.
func TestGrewIsFoldedPerPlatform(t *testing.T) {
	saved := askGorelease
	defer func() { askGorelease = saved }()
	askGorelease = func(_, _, goos string) reading {
		if goos == "linux" {
			return reading{goos: goos, suggested: "v0.2.0", grew: 1}
		}
		return reading{goos: goos, suggested: "v0.1.1"}
	}
	v := read("o/r", "/nowhere", "v0.1.0", []string{"linux", "darwin"})
	if v.grew != 1 {
		t.Errorf("grew = %d, want 1", v.grew)
	}
	if !v.disagree {
		t.Error("the platforms disagreed and that must be recorded")
	}
	if v.safe() {
		t.Error("must not be safe")
	}
}

// TestARefusalKeepsGoreleasesOwnWords: eleven modules sat in the unread pile
// reported as "said nothing this could read", with their cause printed in full
// one line above, thrown away by the parser.
func TestARefusalKeepsGoreleasesOwnWords(t *testing.T) {
	const out = "# summary\nCannot suggest a release version.\n" +
		"Can only suggest a release version when compared against the most recent version of this major: v0.2.0.\n"
	r := parseGorelease(out, "linux")
	if r.err == "" {
		t.Fatal("a refusal must be an error")
	}
	if strings.Contains(r.err, "said nothing") {
		t.Errorf("gorelease said plenty; the parser threw it away: %q", r.err)
	}
	if !strings.Contains(r.err, "most recent version of this major") {
		t.Errorf("err = %q, want gorelease's own sentence", r.err)
	}
	if r.wantBase != "v0.2.0" {
		t.Errorf("wantBase = %q, want v0.2.0 -- gorelease names the base it will accept", r.wantBase)
	}
}

// TestTheNamedBaseIsRetriedOnce. latestTag asks git which tag is REACHABLE;
// gorelease asks the proxy for the most recent PUBLISHED one. When they
// disagree, gorelease refuses and names what it wants.
func TestTheNamedBaseIsRetriedOnce(t *testing.T) {
	saved := askGorelease
	defer func() { askGorelease = saved }()
	var asked []string
	askGorelease = func(_, base, goos string) reading {
		asked = append(asked, base)
		if base == "v0.1.1" {
			return reading{goos: goos, err: "...most recent version of this major: v0.2.0", wantBase: "v0.2.0"}
		}
		return reading{goos: goos, suggested: "v0.2.1"}
	}
	v := read("o/r", "/nowhere", "v0.1.1", []string{"linux"})
	if len(v.unread) != 0 {
		t.Fatalf("the retry should have succeeded: %v", v.unread)
	}
	if v.base != "v0.2.0" {
		t.Errorf("base = %q; the verdict must report the base actually compared against", v.base)
	}
	if v.next != "v0.2.1" {
		t.Errorf("next = %q", v.next)
	}
	if len(asked) != 2 || asked[0] != "v0.1.1" || asked[1] != "v0.2.0" {
		t.Errorf("asked = %v, want exactly one retry with the named base", asked)
	}
}

// TestARetryThatAlsoFailsIsStillUnread: it must not loop, and it must not
// pretend.
func TestARetryThatAlsoFailsIsStillUnread(t *testing.T) {
	saved := askGorelease
	defer func() { askGorelease = saved }()
	n := 0
	askGorelease = func(_, base, goos string) reading {
		n++
		return reading{goos: goos, err: "nope", wantBase: "v9.9.9"}
	}
	v := read("o/r", "/nowhere", "v0.1.1", []string{"linux"})
	if len(v.unread) != 1 {
		t.Fatalf("unread = %v", v.unread)
	}
	if n != 2 {
		t.Errorf("asked %d times, want exactly 2 -- one retry, never a loop", n)
	}
}

// TestEveryRefusalReasonSurvives. gorelease has at least four, and the useful
// part is always the line AFTER "Cannot suggest a release version."
func TestEveryRefusalReasonSurvives(t *testing.T) {
	for _, c := range []struct{ reason, wantBase string }{
		{"Errors were found.", ""},
		{"Incompatible changes were detected.", ""},
		{"Base module path is different from release.", ""},
		{"Can only suggest a release version when compared against the most recent version of this major: v0.22.0.", "v0.22.0"},
	} {
		r := parseGorelease("# summary\nCannot suggest a release version.\n"+c.reason+"\n", "linux")
		if !strings.Contains(r.err, c.reason) {
			t.Errorf("err = %q, want it to carry %q", r.err, c.reason)
		}
		if r.wantBase != c.wantBase {
			t.Errorf("wantBase = %q, want %q", r.wantBase, c.wantBase)
		}
	}
}
