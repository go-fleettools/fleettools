package main

import (
	"regexp"
	"strings"
	"testing"

	"golang.org/x/mod/semver"
)

// A date is written into a claim whole, so only an exact YYYY-MM-DD is one this
// command could be given; anything else is normalised to "", which is the
// production state of a caller that passes no date.
var fuzzDateRE = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// ⛔ THIS COMMAND IS A PARSER THAT WRITES FILES, so it is fuzzed. Until
// 2026-11-22 it only moved a version number and nothing it read could reach
// anything but that number; now the text inside a claim's parentheses drives a
// substitution whose result is written back over the page, and the shape of
// that text comes from a repository anyone can send a pull request to.
//
// The two properties asserted here are the ones a crash cannot state:
//
//  1. ⛔ ONE PASS IS ENOUGH. Rewriting the output again must change nothing.
//     That is the whole contract in one line: a claim the first pass moved is
//     current, and a claim it refused is untouched -- so a second pass has
//     nothing to do either way. A half-written claim fails it, because the
//     second pass finds the version already current and the parentheses still
//     stale, and moves one of them.
//  2. ⛔ THE REFUSALS ARE STABLE. The same page, the same release, the same
//     date must refuse the same number of claims. A refusal that appears or
//     disappears on a second reading of its own output means the page, not the
//     release, decided it.
//
// The fuzzer's tag, commit and date are NORMALISED rather than skipped:
// `highest` yields a canonical vX.Y.Z and a 40-character object id or none, and
// `run` always has a date, so an input outside those shapes is not a page this
// command can meet -- but "" IS a production state for both the commit (a line
// it could not parse) and the date (a caller that passes none), and both are
// kept. Skipping would throw away most of the corpus to test shapes that cannot
// occur.
func FuzzOnePassIsEnoughOrNothingMoved(f *testing.F) {
	f.Add(sourced, "v0.154.0", newCommit, "2026-11-22", false)
	f.Add(sourced, "v0.154.0", "", "2026-11-22", false)
	f.Add(startsLine, "v0.9.0", newCommit, "2026-12-01", false)
	f.Add(sameLine, "v1.0.0", newCommit, "", false)
	f.Add(nextLine, "v0.15.0", newCommit, "2026-11-22", false)
	// A parenthesis that opens and never closes, one that nests, and a claim
	// whose parentheses hold two commits and two tags.
	f.Add("[R](https://github.com/go-fleettools/a) Latest tag: **v0.1.0** ((`deadbeef`\n", "v0.2.0", newCommit, "2026-11-22", false)
	f.Add("[R](https://github.com/go-fleettools/a) Latest tag: **v0.1.0** (`deadbeef` (v0.1.0) `cafed00d1`)\n", "v0.2.0", newCommit, "2026-11-22", false)
	f.Add("[[params.repos]]\n  name = \"a\"\n  label = \"v0.1.0\"\n", "v0.2.0", newCommit, "2026-11-22", true)

	f.Fuzz(func(t *testing.T, src, tag, commit, date string, toml bool) {
		if !semver.IsValid(tag) || semver.Canonical(tag) != tag || semver.Prerelease(tag) != "" {
			tag = "v9.9.9"
		}
		if !oidRE.MatchString(commit) {
			commit = ""
		}
		if !fuzzDateRE.MatchString(date) {
			date = ""
		}
		want := map[string]Release{}
		for _, c := range Find(src, "go-fleettools", toml) {
			want[c.Module] = Release{Tag: tag, Commit: commit}
		}

		cells := Find(src, "go-fleettools", toml)
		out, refused := Rewrite(src, cells, want, date)

		// Nothing may be lost: every rewrite is a substitution of one token for
		// another, so the page keeps its shape around them. A page that lost
		// its claim line entirely would still satisfy the two properties below.
		if strings.Count(out, "\n") != strings.Count(src, "\n") {
			t.Fatalf("the line count changed, %d -> %d:\n%s", strings.Count(src, "\n"), strings.Count(out, "\n"), out)
		}

		out2, refused2 := Rewrite(out, Find(out, "go-fleettools", toml), want, date)
		if out2 != out {
			t.Fatalf("a second pass moved something, so the first left a claim half written:\nfirst:\n%s\nsecond:\n%s", out, out2)
		}
		if len(refused2) != len(refused) {
			t.Fatalf("%d refusal(s) then %d on the same page and release", len(refused), len(refused2))
		}
	})
}
