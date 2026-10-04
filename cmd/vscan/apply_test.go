package main

import (
	"strings"
	"testing"
)

const headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func stubGitHub(t *testing.T, head string, existing map[string]bool, created map[string]string) {
	t.Helper()
	sh, se, st, sc := remoteHead, tagExists, tagTarget, createTag
	t.Cleanup(func() { remoteHead, tagExists, tagTarget, createTag = sh, se, st, sc })
	remoteHead = func(string, string) (string, error) { return head, nil }
	tagExists = func(_, tag string) (bool, error) { return existing[tag], nil }
	createTag = func(repo, tag, sha string) error { created[repo+" "+tag] = sha; return nil }
	tagTarget = func(repo, tag string) (string, error) { return created[repo+" "+tag], nil }
}

// TestNoFlagEverTagsAnIncompatibleChange is the one this file exists for.
//
// ⛔ There is deliberately no -apply value for an incompatible change, and this
// asserts the absence rather than trusting it: a removal or a changed signature
// is a decision about consumers, and nothing derives it. If someone adds that
// flag later, this fails.
func TestNoFlagEverTagsAnIncompatibleChange(t *testing.T) {
	broken := verdict{repo: "o/r", base: "v0.1.0", next: "v0.2.0", breaks: 1, grew: 1}
	all := applyPlan{safe: true, noAPI: true, additions: true, frozen: true}
	if all.wants(broken) {
		t.Fatal("an incompatible verdict must be in NO category, even with every flag set")
	}
	// The control: the same verdict without the break IS wanted, so breaks is
	// what decided it.
	broken.breaks = 0
	if !all.wants(broken) {
		t.Fatal("the control failed: without the break it should be wanted")
	}
}

func TestParseApplyHasNoWordForIncompatible(t *testing.T) {
	if _, err := parseApply("incompatible"); err == nil {
		t.Fatal(`"incompatible" must not be an accepted category`)
	}
	if _, err := parseApply("safe,frozen"); err != nil {
		t.Fatalf("safe,frozen: %v", err)
	}
	p, err := parseApply("")
	if err != nil || p.any() {
		t.Fatalf("an empty -apply must select nothing: %+v %v", p, err)
	}
}

func TestEachCategoryIsSelectedOnlyByItsOwnFlag(t *testing.T) {
	safe := verdict{base: "v0.1.0", next: "v0.1.1"}
	noAPI := verdict{base: "v0.1.0", next: "v0.2.0"}
	adds := verdict{base: "v0.1.0", next: "v0.2.0", grew: 1}
	froz := verdict{base: "v0.1.0", next: "v0.2.0", frozen: true}
	for _, c := range []struct {
		flag string
		v    verdict
		name string
	}{{"safe", safe, "safe"}, {"no-api-change", noAPI, "no-api-change"}, {"additions", adds, "additions"}, {"frozen", froz, "frozen"}} {
		p, err := parseApply(c.flag)
		if err != nil {
			t.Fatal(err)
		}
		if !p.wants(c.v) {
			t.Errorf("-apply=%s must select its own category", c.flag)
		}
		for _, other := range []verdict{safe, noAPI, adds, froz} {
			if sameCategory(c.v, other) {
				continue
			}
			if p.wants(other) {
				t.Errorf("-apply=%s also selected %+v", c.flag, other)
			}
		}
	}
}

func sameCategory(a, b verdict) bool {
	return a.frozen == b.frozen && a.grew == b.grew && a.safe() == b.safe()
}

// TestABranchThatMovedIsSkippedNotTagged: the suggestion described the tree at
// the commit the scan read. If the branch moved, it describes nothing here.
func TestABranchThatMovedIsSkippedNotTagged(t *testing.T) {
	created := map[string]string{}
	stubGitHub(t, headB, nil, created) // remote is at B
	o := applyOne(verdict{repo: "o/r", next: "v0.1.1", sha: headA, branch: "main"}, false)
	if o.ok {
		t.Fatal("a moved branch must not be tagged")
	}
	if !strings.Contains(o.note, "moved since the scan") {
		t.Errorf("note = %q", o.note)
	}
	if len(created) != 0 {
		t.Errorf("nothing may be created: %v", created)
	}
}

func TestAnExistingTagIsNeverOverwritten(t *testing.T) {
	created := map[string]string{}
	stubGitHub(t, headA, map[string]bool{"v0.1.1": true}, created)
	o := applyOne(verdict{repo: "o/r", next: "v0.1.1", sha: headA, branch: "main"}, false)
	if o.ok || o.fatal || !strings.Contains(o.note, "already exists") {
		t.Fatalf("o = %+v", o)
	}
	if len(created) != 0 {
		t.Errorf("nothing may be created: %v", created)
	}
}

// TestATagThatCannotBeReadBackIsAFailure: a 201 says the request was accepted.
func TestATagThatCannotBeReadBackIsAFailure(t *testing.T) {
	created := map[string]string{}
	stubGitHub(t, headA, nil, created)
	createTag = func(string, string, string) error { return nil } // accepted, writes nothing
	o := applyOne(verdict{repo: "o/r", next: "v0.1.1", sha: headA, branch: "main"}, false)
	if o.ok {
		t.Fatal("a tag that reads back as something else must be a failure")
	}
	if !strings.Contains(o.note, "points at") {
		t.Errorf("note = %q", o.note)
	}
}

func TestADryRunCreatesNothing(t *testing.T) {
	created := map[string]string{}
	stubGitHub(t, headA, nil, created)
	o := applyOne(verdict{repo: "o/r", next: "v0.1.1", sha: headA, branch: "main"}, true)
	if !o.ok || !strings.Contains(o.note, "would tag") {
		t.Fatalf("o = %+v", o)
	}
	if len(created) != 0 {
		t.Errorf("a dry run created %v", created)
	}
}

// TestItStopsAtAFailureButNotAtASkip is the distinction that cost a whole dry
// run: one orphaned clone of a deleted repository ended the pass after 34 of
// 231. A tag that already exists, a branch that moved and a repository that is
// gone all mean "leave this one alone", not "stop".
func TestItStopsAtAFailureButNotAtASkip(t *testing.T) {
	created := map[string]string{}
	stubGitHub(t, headA, map[string]bool{"v0.0.2": true}, created)
	vs := []verdict{
		{repo: "o/first", base: "v0.0.0", next: "v0.0.1", sha: headA, branch: "main"},
		{repo: "o/second", base: "v0.0.1", next: "v0.0.2", sha: headA, branch: "main"}, // exists -> SKIP
		{repo: "o/third", base: "v0.0.2", next: "v0.0.3", sha: headA, branch: "main"},
	}
	var b strings.Builder
	done, skipped, failed := applyTags(&b, vs, applyPlan{safe: true}, 0, false)
	if failed != nil {
		t.Fatalf("a skip must not stop the run: %+v", failed)
	}
	if len(done) != 2 || done[0].repo != "o/first" || done[1].repo != "o/third" {
		t.Fatalf("done = %+v -- the third must still have been reached", done)
	}
	if len(skipped) != 1 || skipped[0].repo != "o/second" {
		t.Fatalf("skipped = %+v", skipped)
	}

	// The control: a real FAILURE in the same position does stop it.
	created = map[string]string{}
	stubGitHub(t, headA, nil, created)
	createTag = func(repo, tag, sha string) error {
		if repo == "o/second" {
			return errString("boom")
		}
		created[repo+" "+tag] = sha
		return nil
	}
	done, _, failed = applyTags(&b, vs, applyPlan{safe: true}, 0, false)
	if failed == nil || failed.repo != "o/second" {
		t.Fatalf("a failed write must stop the run: %+v", failed)
	}
	if len(done) != 1 {
		t.Fatalf("done = %+v", done)
	}
	if _, reached := created["o/third v0.0.3"]; reached {
		t.Error("the third was attempted after a failure")
	}
}

// TestADryRunStopsForNothing: it writes nothing, so there is no half-finished
// state to protect, and its whole purpose is to show every problem at once.
func TestADryRunStopsForNothing(t *testing.T) {
	created := map[string]string{}
	stubGitHub(t, headA, nil, created)
	remoteHead = func(repo, _ string) (string, error) {
		if repo == "o/gone" {
			return "", errString("gh: Not Found (HTTP 404)")
		}
		return headA, nil
	}
	createTag = func(string, string, string) error { return errString("boom") }
	vs := []verdict{
		{repo: "o/first", base: "v0.0.0", next: "v0.0.1", sha: headA, branch: "main"},
		{repo: "o/gone", base: "v0.0.1", next: "v0.0.2", sha: headA, branch: "main"},
		{repo: "o/third", base: "v0.0.2", next: "v0.0.3", sha: headA, branch: "main"},
	}
	var b strings.Builder
	done, skipped, failed := applyTags(&b, vs, applyPlan{safe: true}, 0, true)
	if failed != nil {
		t.Fatalf("a dry run must never stop: %+v", failed)
	}
	if len(done) != 2 || len(skipped) != 1 {
		t.Fatalf("done=%d skipped=%d, want 2 and 1 -- every repository must be reported on", len(done), len(skipped))
	}
	if len(created) != 0 {
		t.Fatalf("a dry run created %v", created)
	}
}

// TestADeletedRepositoryIsSkippedNotFatal names the real case:
// go-compressions/matchlen, whose repository git reports as "not found".
func TestADeletedRepositoryIsSkippedNotFatal(t *testing.T) {
	created := map[string]string{}
	stubGitHub(t, headA, nil, created)
	re := repoExists
	t.Cleanup(func() { repoExists = re })
	remoteHead = func(string, string) (string, error) { return "", errString("gh: Not Found (HTTP 404)") }
	repoExists = func(string) (string, error) { return "", errString("gh: Not Found (HTTP 404)") }
	o := applyOne(verdict{repo: "go-compressions/matchlen", next: "v0.1.2", sha: headA, branch: "main"}, false)
	if o.fatal {
		t.Error("a repository that is gone must not end the run")
	}
	if o.ok || !strings.Contains(o.note, "no longer exists") {
		t.Errorf("note = %q", o.note)
	}

	// The control: any OTHER error from the same call IS fatal, because it
	// says nothing about whether the next repository can be read.
	remoteHead = func(string, string) (string, error) { return "", errString("no route to host") }
	o = applyOne(verdict{repo: "o/r", next: "v0.1.1", sha: headA, branch: "main"}, false)
	if !o.fatal {
		t.Error("an unreachable GitHub must stop the run")
	}
}

func TestAVerdictWithNoRecordedCommitIsRefused(t *testing.T) {
	created := map[string]string{}
	stubGitHub(t, headA, nil, created)
	o := applyOne(verdict{repo: "o/r", next: "v0.1.1", branch: "main"}, false)
	if o.ok || !strings.Contains(o.note, "no commit") {
		t.Fatalf("o = %+v", o)
	}
}

// errString is an error that is just its message, so a test can hand the code
// under test the exact wording GitHub produces.
type errString string

func (e errString) Error() string { return string(e) }

// TestA404OnTheBranchIsNotA404OnTheRepository is go-richdoc/markdown.
//
// ⛔ It exists. Its head read 404s because the local checkout's origin/HEAD
// still names a branch deleted months ago -- so the version had been derived
// from the WRONG branch, and a message blaming "a stale local clone" would
// have hidden the one fact worth knowing.
func TestA404OnTheBranchIsNotA404OnTheRepository(t *testing.T) {
	created := map[string]string{}
	stubGitHub(t, headA, nil, created)
	re := repoExists
	t.Cleanup(func() { repoExists = re })
	remoteHead = func(string, string) (string, error) { return "", errString("gh: Not Found (HTTP 404)") }
	repoExists = func(string) (string, error) { return "main", nil } // the repository is there

	o := applyOne(verdict{repo: "go-richdoc/markdown", next: "v0.7.1", sha: headA, branch: "markdown-converter"}, false)
	if o.ok || o.fatal {
		t.Fatalf("o = %+v", o)
	}
	if !strings.Contains(o.note, "WRONG branch") {
		t.Errorf("the message must name what is actually wrong, got %q", o.note)
	}
	if strings.Contains(o.note, "no longer exists") {
		t.Errorf("it must NOT claim the repository is gone: %q", o.note)
	}
}

// TestAnArchivedRepositoryIsSkippedNotFatal: GitHub refuses writes to an
// archived repository with 404, the same status as "no such thing".
// go-composites/nonnil ended a run after 22 of 227 tags that way.
func TestAnArchivedRepositoryIsSkippedNotFatal(t *testing.T) {
	created := map[string]string{}
	stubGitHub(t, headA, nil, created)
	ar := archived
	t.Cleanup(func() { archived = ar })
	createTag = func(string, string, string) error { return errString("gh: Not Found (HTTP 404)") }

	archived = func(string) bool { return true }
	o := applyOne(verdict{repo: "go-composites/nonnil", next: "v0.1.1", sha: headA, branch: "main"}, false)
	if o.fatal {
		t.Error("an archived repository must not end the run")
	}
	if o.ok || !strings.Contains(o.note, "ARCHIVED") {
		t.Errorf("note = %q", o.note)
	}

	// The control: the same 404 on a repository that is NOT archived is fatal.
	// A write refused for a reason nobody identified must stop the run.
	archived = func(string) bool { return false }
	o = applyOne(verdict{repo: "o/r", next: "v0.1.1", sha: headA, branch: "main"}, false)
	if !o.fatal {
		t.Error("a 404 write failure that is not an archive must stop the run")
	}
}

// TestAPrereleaseBaseIsNeverPromoted. openweft/weft-ha-irods is at v0.4.0-rc9
// and gorelease suggests v0.4.0 -- arithmetically a tiny step, in fact the
// decision to call a release candidate finished. Nothing in an API diff knows.
func TestAPrereleaseBaseIsNeverPromoted(t *testing.T) {
	all := applyPlan{safe: true, noAPI: true, additions: true, frozen: true}
	rc := verdict{repo: "openweft/weft-ha-irods", base: "v0.4.0-rc9", next: "v0.4.0"}
	if all.wants(rc) {
		t.Error("promoting a release candidate is an editorial decision, not a derivation")
	}
	// The control: the same shape from a plain version IS wanted, so the
	// prerelease is what decided it.
	rc.base, rc.next = "v0.3.9", "v0.4.0"
	if !all.wants(rc) {
		t.Error("the control failed")
	}
}

// TestADryRunDoesNotPromiseAnImpossibleWrite. The dry run said "would tag" for
// two archived repositories and the real run then skipped both. A dry run
// exists to say what will happen; a confident wrong answer is the one thing it
// must not give.
func TestADryRunDoesNotPromiseAnImpossibleWrite(t *testing.T) {
	created := map[string]string{}
	stubGitHub(t, headA, nil, created)
	ar := archived
	t.Cleanup(func() { archived = ar })

	archived = func(string) bool { return true }
	o := applyOne(verdict{repo: "go-freedesktop/dbus", next: "v0.1.2", sha: headA, branch: "main"}, true)
	if o.ok || !strings.Contains(o.note, "ARCHIVED") {
		t.Errorf("a dry run must not promise a write GitHub will refuse: %+v", o)
	}

	// The control: a live repository still reads as "would tag".
	archived = func(string) bool { return false }
	o = applyOne(verdict{repo: "o/r", next: "v0.1.1", sha: headA, branch: "main"}, true)
	if !o.ok || !strings.Contains(o.note, "would tag") {
		t.Errorf("the control failed: %+v", o)
	}
	if len(created) != 0 {
		t.Errorf("a dry run created %v", created)
	}
}
