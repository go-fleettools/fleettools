package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// lsRemote is the real shape of `git ls-remote --tags --refs` output: a SHA, a
// tab, then refs/tags/<tag>. Copied from go-xrkit/player on 2026-11-15, with
// the two kinds of line that must NOT be taken as the repository's version
// added: a nested module's tag and a prerelease.
const lsRemote = "" +
	"4538b33a07c10d649e6519cda7da3a824bf293a9\trefs/tags/v0.2.0\n" +
	"ae7a62011cb4b9a4f6d56d7e2a1a8f2cf7d0a111\trefs/tags/v0.1.0\n" +
	"b1c2d3e4f5061728394a5b6c7d8e9f0a1b2c3d4e\trefs/tags/tools/v9.9.9\n" +
	"c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5e\trefs/tags/v0.3.0-rc1\n"

// TestTheTagScannedIsTheOneAConsumerResolves pins the choice itself. The whole
// flag exists because the default branch is the tree nobody imports; picking
// the wrong tag would move the measurement to a different tree nobody has,
// which is the same defect wearing the remedy's clothes.
func TestTheTagScannedIsTheOneAConsumerResolves(t *testing.T) {
	for _, c := range []struct {
		name string
		out  string
		want string
	}{
		{"the highest release, not the newest line", lsRemote, "v0.2.0"},
		{
			// ⛔ `sub/v1.2.3` versions the module in that directory. Taking it
			// would scan a tree whose ROOT module is at some other version.
			name: "a nested module's tag is not this module's version",
			out:  "aaa\trefs/tags/tools/v9.9.9\nbbb\trefs/tags/v1.0.0\n",
			want: "v1.0.0",
		},
		{
			// The same rule with NOTHING else to fall back on, which is the case
			// the line above cannot make: if a nested tag were accepted, this
			// would answer tools/v9.9.9 instead of "". Mutating the filter away
			// is caught here and nowhere else.
			name: "a nested module's tag alone leaves nothing to scan",
			out:  "aaa\trefs/tags/tools/v9.9.9\n",
			want: "",
		},
		{
			// ⛔ The proxy serves the highest RELEASE and falls back to a
			// prerelease only when there is no release at all. A sweep that
			// read v2.0.0-rc1 while consumers were served v1.9.0 would be
			// measuring a tree nobody has.
			name: "a release beats a higher prerelease",
			out:  "aaa\trefs/tags/v2.0.0-rc1\nbbb\trefs/tags/v1.9.0\n",
			want: "v1.9.0",
		},
		{
			name: "a prerelease is taken only when nothing is released",
			out:  "aaa\trefs/tags/v2.0.0-rc1\nbbb\trefs/tags/v2.0.0-rc2\n",
			want: "v2.0.0-rc2",
		},
		{
			name: "semantic order, not lexical",
			out:  "aaa\trefs/tags/v0.9.0\nbbb\trefs/tags/v0.10.0\n",
			want: "v0.10.0",
		},
		{
			name: "a tag that is not a version at all is ignored",
			out:  "aaa\trefs/tags/release-2024\nbbb\trefs/tags/latest\nccc\trefs/tags/v0.1.0\n",
			want: "v0.1.0",
		},
		{
			// The NO-TAG case: nothing published, which is a state of its own.
			name: "no version tag leaves nothing to scan",
			out:  "aaa\trefs/tags/latest\n",
			want: "",
		},
		{"no tags at all", "", ""},
	} {
		if got := highestTag(c.out); got != c.want {
			t.Errorf("%s: highestTag = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestLatestTagRefusesToGuessWhenGitFails: ls-remote failing is a question
// without an answer, never "this repository publishes nothing". Reading it as
// NO-TAG would turn a transient network refusal into a measurement, and NO-TAG
// is not an alarm -- so the error has to come back.
func TestLatestTagRefusesToGuessWhenGitFails(t *testing.T) {
	run := func(context.Context, string, []string, string, ...string) ([]byte, []byte, error) {
		return nil, []byte(paced), errors.New("exit status 128")
	}
	if _, err := latestTag(context.Background(), run, "go-images/gif"); err == nil {
		t.Fatal("a failed ls-remote came back as success, so the repository would read as publishing nothing")
	}
}

// TestLatestTagAsksTheRemoteAndNotTheAPI: this runs inside a sweep whose own
// doctrine is that it must not spend the hourly REST budget the fleet's
// Renovate runners share. git over https is metered apart from it, so the
// command matters and not only the answer.
func TestLatestTagAsksTheRemoteAndNotTheAPI(t *testing.T) {
	var got []string
	run := func(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, []byte, error) {
		got = append([]string{name}, args...)
		return []byte(lsRemote), nil, nil
	}
	tag, err := latestTag(context.Background(), run, "go-xrkit/player")
	if err != nil {
		t.Fatalf("latestTag: %v", err)
	}
	if tag != "v0.2.0" {
		t.Errorf("tag = %q, want v0.2.0", tag)
	}
	line := strings.Join(got, " ")
	if !strings.HasPrefix(line, "git ls-remote --tags --refs ") {
		t.Errorf("ran %q, want git ls-remote --tags --refs", line)
	}
	if !strings.HasSuffix(line, "https://github.com/go-xrkit/player.git") {
		t.Errorf("ran %q, want the https clone URL with no credential in it", line)
	}
}

// TestCloningATagNamesIt is the other half: resolving the right tag and then
// cloning the default branch anyway would produce a measurement labelled with a
// version it was not taken at -- worse than no flag, because the label would be
// believed.
func TestCloningATagNamesIt(t *testing.T) {
	noBackoff(t)
	var got []string
	run := func(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, []byte, error) {
		got = append([]string{name}, args...)
		return nil, nil, nil
	}
	if _, err := clone(context.Background(), run, "go-xrkit/player", t.TempDir(), "v0.1.0"); err != nil {
		t.Fatalf("clone at a tag: %v", err)
	}
	line := strings.Join(got, " ")
	if !strings.Contains(line, "--branch v0.1.0") {
		t.Errorf("ran %q, want --branch v0.1.0", line)
	}
	if !strings.Contains(line, "--depth 1") {
		t.Errorf("ran %q: a tag is still a shallow clone", line)
	}
}

// TestCloningWithoutARefStillTakesTheDefaultBranch is the control that keeps
// the change from being invisible: without it, a --branch passed unconditionally
// (or never) would pass the test above and nobody would notice that every
// ordinary sweep had changed meaning.
func TestCloningWithoutARefStillTakesTheDefaultBranch(t *testing.T) {
	noBackoff(t)
	var got []string
	run := func(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, []byte, error) {
		got = append([]string{name}, args...)
		return nil, nil, nil
	}
	if _, err := clone(context.Background(), run, "go-xrkit/player", t.TempDir(), ""); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if line := strings.Join(got, " "); strings.Contains(line, "--branch") {
		t.Errorf("ran %q: no ref was asked for, so no --branch may be passed", line)
	}
}

// TestANoTagLineSaysNothingWasPublishedAndNotThatSomethingFailed: found by
// RUNNING the flag, not by reading it. Against two untagged repositories the
// line came out as `NO-TAG go-xrkit/.github  could not scan: ` -- the error
// template, with an empty reason, because NO-TAG fell through to the default
// case. Nothing failed: the repository publishes nothing. The tests asserted
// the summary and the annotation and never this line, which is why a real run
// belongs beside them.
func TestANoTagLineSaysNothingWasPublishedAndNotThatSomethingFailed(t *testing.T) {
	got := line(result{
		Repo:   "go-xrkit/.github",
		Status: NoTag,
		Notes:  []string{"no vX.Y.Z tag on the repository: nothing published for a consumer to resolve"},
	})
	if strings.Contains(got, "could not scan") {
		t.Errorf("a repository that publishes nothing reads as a failed scan:\n%s", got)
	}
	if !strings.Contains(got, "nothing published") {
		t.Errorf("the line does not say why there is no measurement:\n%s", got)
	}
	// And with no note at all it must still say something rather than trail off.
	bare := line(result{Repo: "acme/x", Status: NoTag})
	if !strings.Contains(bare, "nothing published to scan") {
		t.Errorf("a bare NO-TAG line says nothing:\n%s", bare)
	}
}

// TestNoTagIsCountedAndNotFoldedIntoTheTotal: a repository in no bucket makes
// "N listed, M scanned" disagree with itself, which is exactly how a sweep hides
// what it did not answer about -- the defect this tool's own history is made of.
func TestNoTagIsCountedAndNotFoldedIntoTheTotal(t *testing.T) {
	var sb strings.Builder
	rep := report{
		Listed: 3,
		Totals: map[string]int{Called: 0, Clean: 1, NoGo: 0, Unread: 0, NoTag: 2},
	}
	summary(&sb, rep)
	out := sb.String()
	if !strings.Contains(out, "3 listed, 3 scanned") {
		t.Errorf("the totals do not add up:\n%s", out)
	}
	if !strings.Contains(out, "NO-TAG 2") {
		t.Errorf("NO-TAG is not named, so two unmeasured repositories read as nothing:\n%s", out)
	}
}
