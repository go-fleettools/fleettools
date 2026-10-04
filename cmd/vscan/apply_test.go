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
	if o.ok || !strings.Contains(o.note, "already exists") {
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

// TestItStopsAtTheFirstFailure: 231 permanent writes that half-succeed leave
// nobody able to say which half.
func TestItStopsAtTheFirstFailure(t *testing.T) {
	created := map[string]string{}
	stubGitHub(t, headA, map[string]bool{"v0.0.2": true}, created)
	vs := []verdict{
		{repo: "o/first", base: "v0.0.0", next: "v0.0.1", sha: headA, branch: "main"},
		{repo: "o/second", base: "v0.0.1", next: "v0.0.2", sha: headA, branch: "main"}, // exists -> refused
		{repo: "o/third", base: "v0.0.2", next: "v0.0.3", sha: headA, branch: "main"},
	}
	var b strings.Builder
	done, failed := applyTags(&b, vs, applyPlan{safe: true}, 0, false)
	if len(done) != 1 || done[0].repo != "o/first" {
		t.Fatalf("done = %+v", done)
	}
	if failed == nil || failed.repo != "o/second" {
		t.Fatalf("failed = %+v", failed)
	}
	if _, reached := created["o/third v0.0.3"]; reached {
		t.Error("the third was attempted after a failure")
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
