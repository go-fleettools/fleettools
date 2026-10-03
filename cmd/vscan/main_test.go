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
