package main

import (
	"strings"
	"testing"
)

const goatErr = "go: github.com/ajroetker/go-highway/hwy/goat@v0.0.0: invalid version: unknown revision hwy/goat/v0.0.0"

func TestOnlyAGraphRefusalIsHandedToAPIDiff(t *testing.T) {
	for err, want := range map[string]bool{
		goatErr:                           true,
		"repo /x has uncommitted changes": false,
		"cannot suggest a version: Incompatible changes were detected.": false,
		"said nothing this could read":                                  false,
		"go: updates to go.mod needed":                                  false,
	} {
		if got := graphUnreadable(err); got != want {
			t.Errorf("%q: got %v want %v", err, got, want)
		}
	}
}

func withSeams(t *testing.T, gr func(dir, base, goos string) reading, ad func(gitDir, tree, base, goos string) reading) {
	t.Helper()
	g, a := askGorelease, askAPIDiff
	t.Cleanup(func() { askGorelease, askAPIDiff = g, a })
	askGorelease, askAPIDiff = gr, ad
}

func TestAGraphRefusalIsReadByAPIDiffOnEveryPlatform(t *testing.T) {
	calls := 0
	withSeams(t,
		func(_, _, goos string) reading { return reading{goos: goos, err: goatErr} },
		func(_, _, base, goos string) reading {
			calls++
			return reading{goos: goos, via: "apidiff", suggested: "v0.329.0"}
		})
	v := read("o/r", ".", ".", "v0.328.0", []string{"linux", "darwin", "windows"})
	if calls != 3 || len(v.unread) != 0 || v.next != "v0.329.0" || v.via != "apidiff" || v.viaCount != 3 {
		t.Fatalf("calls %d, verdict %+v", calls, v)
	}
	if v.graphErr != goatErr {
		t.Fatalf("the reason gorelease gave is kept for the report: %q", v.graphErr)
	}
}

func TestOtherRefusalsAreNeverHandedToAPIDiff(t *testing.T) {
	withSeams(t,
		func(_, _, goos string) reading { return reading{goos: goos, err: "repo /x has uncommitted changes"} },
		func(_, _, _, _ string) reading {
			t.Fatal("apidiff must not stand in for a dirty tree")
			return reading{}
		})
	v := read("o/r", ".", ".", "v0.1.0", []string{"linux"})
	if len(v.unread) != 1 || v.via != "" {
		t.Fatalf("%+v", v)
	}
}

func TestAFallbackThatFailsStaysUnreadWithBothReasons(t *testing.T) {
	withSeams(t,
		func(_, _, goos string) reading { return reading{goos: goos, err: goatErr} },
		func(_, _, _, goos string) reading { return reading{goos: goos, err: "apidiff: not installed"} })
	v := read("o/r", ".", ".", "v0.1.0", []string{"linux"})
	if len(v.unread) != 1 || !strings.Contains(v.unread[0], "invalid version") || !strings.Contains(v.unread[0], "not installed") {
		t.Fatalf("%+v", v.unread)
	}
}

func TestAnAPIDiffVerdictIsNeverSafe(t *testing.T) {
	v := verdict{base: "v0.4.0", next: "v0.4.1", via: "apidiff"}
	if v.safe() {
		t.Fatal("a patch apidiff derived must be asked for, never applied as safe")
	}
	v.via = ""
	if !v.safe() {
		t.Fatal("control: the same verdict from gorelease is safe")
	}
}

func TestParseAPIDiffCountsChangesNotHeadings(t *testing.T) {
	out := `Ignoring internal package github.com/go-widgets/toolkit/internal/formula
Incompatible changes:
- Old: removed
Compatible changes:
- ContrastInk: added
- Table.CellFill: added
- Viridis: added
`
	if b, g := parseAPIDiff(out); b != 1 || g != 3 {
		t.Fatalf("breaks %d grew %d", b, g)
	}
	if b, g := parseAPIDiff("Ignoring internal package x\n"); b != 0 || g != 0 {
		t.Fatalf("no change: %d %d", b, g)
	}
	if b, g := parseAPIDiff("Compatible changes:\n\n"); g != 0 || b != 0 {
		t.Fatalf("a heading with nothing under it: %d %d", b, g)
	}
}

func TestRequirementsChangedIsGoreleasesRule(t *testing.T) {
	base := "module m\n\ngo 1.27.1\n\nrequire (\n\ta.example/x v0.3.0\n\tb.example/y v1.2.3\n)\n"
	for name, c := range map[string]struct {
		head string
		want bool
	}{
		"unchanged":          {base, false},
		"a patch moved":      {strings.Replace(base, "v1.2.3", "v1.2.9", 1), false},
		"a minor moved":      {strings.Replace(base, "v0.3.0", "v0.4.0", 1), true},
		"a major moved":      {strings.Replace(base, "v1.2.3", "v2.0.0", 1), true},
		"go directive rose":  {strings.Replace(base, "go 1.27.1", "go 1.27.2", 1), true},
		"a requirement came": {strings.Replace(base, ")\n", "\tc.example/z v0.1.0\n)\n", 1), true},
		"one went away":      {strings.Replace(base, "\tb.example/y v1.2.3\n", "", 1), false},
		"unparsable head":    {"module m\nrequire (\n", true},
	} {
		if got := requirementsChanged([]byte(base), []byte(c.head)); got != c.want {
			t.Errorf("%s: got %v want %v", name, got, c.want)
		}
	}
}

func TestSuggestFollowsGorelease(t *testing.T) {
	for _, c := range []struct {
		base               string
		breaks, grew, reqs bool
		want, err          string
	}{
		{"v0.328.0", false, false, false, "v0.328.1", ""},
		{"v0.328.0", false, false, true, "v0.329.0", ""},
		{"v0.328.0", false, true, false, "v0.329.0", ""},
		{"v0.328.0", true, false, false, "v0.329.0", ""}, // below v1 a break is a minor
		{"v1.4.2", false, true, false, "v1.5.0", ""},
		{"v1.4.2", true, false, false, "", "Incompatible"},
		{"v0.4.0-rc9", false, false, false, "", "not a plain"},
		{"master", false, false, false, "", "not a plain"},
	} {
		got, err := suggest(c.base, c.breaks, c.grew, c.reqs)
		if got != c.want || (c.err == "") != (err == "") || !strings.Contains(err, c.err) {
			t.Errorf("%+v: got %q %q", c, got, err)
		}
	}
}
