package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitArms puts extra case arms first in the fake git.
func (w *world) gitArms(arms string) {
	w.t.Helper()
	p := filepath.Join(w.bin, "git")
	b, err := os.ReadFile(p)
	if err != nil {
		w.t.Fatal(err)
	}
	s := strings.Replace(string(b), "case \"$*\" in\n", "case \"$*\" in\n"+arms, 1)
	if s == string(b) {
		w.t.Fatal("no case in the fake git")
	}
	if err := os.WriteFile(p, []byte(s), 0o755); err != nil {
		w.t.Fatal(err)
	}
}

const leftoverSHA = "abc123" // what the fake gh says the pull request head is

// remoteBranch makes the branch exist on the remote at sha, ahead commits
// ahead of the default branch, whose file is baseSrc; the branch's is branchSrc.
func (w *world) remoteBranch(sha, ahead, baseSrc, branchSrc string) {
	w.t.Helper()
	bf, rf := filepath.Join(w.dir, "base.txt"), filepath.Join(w.dir, "remote.txt")
	for p, c := range map[string]string{bf: baseSrc, rf: branchSrc} {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			w.t.Fatal(err)
		}
	}
	w.gitArms(fmt.Sprintf("  *for-each-ref*) echo '%s refs/remotes/origin/%s' ;;\n  *rev-list*) echo %s ;;\n"+
		"  *\"show origin/main:\"*) cat %s ;;\n  *\"show %s:\"*) cat %s ;;\n", sha, branch, ahead, bf, sha, rf))
}

func edited() string { return strings.ReplaceAll(target1, oldText, newText) }

// The run stopped after its push and before the pull request: the branch is
// ours, one commit ahead with exactly the edit, and is reused, not pushed over.
func TestOurOwnLeftoverBranchIsReusedAndNothingIsPushed(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	w.remoteBranch(leftoverSHA, "1", target1, edited())
	st, out, errOut := w.drive("--orgs", "go-a")
	if st != 0 || !strings.Contains(out, "merged    go-a/.github#7") {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
	l := w.logged()
	for _, forbidden := range []string{"gitpush", "git commit", "git switch", "git add"} {
		if strings.Contains(l, forbidden) {
			t.Fatalf("%q ran on a reused leftover:\n%s", forbidden, l)
		}
	}
	if !strings.Contains(l, "pr create") || !strings.Contains(l, "ghmerge go-a/.github 7") {
		t.Fatalf("the pull request was not opened and merged:\n%s", l)
	}
}

func TestABranchOnTheRemoteThatIsNotOurLeftoverStopsTheRunNamingItsCommit(t *testing.T) {
	for name, tc := range map[string]struct{ ahead, base, branch, want string }{
		"two commits":   {"2", target1, edited(), "2 commit(s) ahead of origin/main, not 1"},
		"none":          {"0", target1, edited(), "0 commit(s) ahead of origin/main, not 1"},
		"other content": {"1", target1, edited() + "# more\n", "is not what the edit produces"},
		"unedited":      {"1", target1, target1, "is not what the edit produces"},
	} {
		w := newWorld(t, map[string]answer{"go-a": ok(target1), "go-b": ok(target1)})
		w.remoteBranch("deadbeef", tc.ahead, tc.base, tc.branch)
		st, _, errOut := w.drive()
		l := w.logged()
		if st != 1 || !strings.Contains(errOut, "STOPPED at go-a/.github") || !strings.Contains(errOut, "at deadbeef") ||
			!strings.Contains(errOut, tc.want) || !strings.Contains(errOut, "not forcing, not deleting") {
			t.Errorf("%s: status %d: %s", name, st, errOut)
		}
		for _, forbidden := range []string{"gitpush", "git commit", "pr create", "ghmerge", "go-b", "--force", "push --delete"} {
			if strings.Contains(l, forbidden) {
				t.Errorf("%s: %q ran:\n%s", name, forbidden, l)
			}
		}
	}
}

func TestADefaultBranchThatIsNoLongerATargetStopsTheLeftoverCheck(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	w.remoteBranch(leftoverSHA, "1", "nothing to do\n", edited())
	st, _, errOut := w.drive("--orgs", "go-a")
	if st != 1 || !strings.Contains(errOut, "is no longer a target") || strings.Contains(w.logged(), "pr create") {
		t.Fatalf("status %d: %s", st, errOut)
	}
}

func TestEachProgramOfTheLeftoverCheckFailingStopsTheRunWithItsWords(t *testing.T) {
	for _, pattern := range []string{"for-each-ref", "rev-list", "show origin/main:", "show " + leftoverSHA + ":"} {
		w := newWorld(t, map[string]answer{"go-a": ok(target1)})
		w.remoteBranch(leftoverSHA, "1", target1, edited())
		word := "boom" + strings.NewReplacer(" ", "", ":", "").Replace(pattern)
		w.armFirst("git", pattern, word)
		st, _, errOut := w.drive("--orgs", "go-a")
		if st != 1 || !strings.Contains(errOut, word) || !strings.Contains(errOut, "git ") || strings.Contains(errOut, "is not what the edit") ||
			strings.Contains(errOut, "no longer a target") || strings.Contains(w.logged(), "pr create") {
			t.Errorf("%s: status %d: %s", pattern, st, errOut)
		}
	}
}

// Only the branch itself counts: a remote ref beneath it is another branch.
func TestARefBeneathTheBranchIsNotTheBranch(t *testing.T) {
	w := newWorld(t, map[string]answer{"go-a": ok(target1)})
	w.gitArms("  *for-each-ref*) echo 'deadbeef refs/remotes/origin/" + branch + "/child' ;;\n")
	st, out, errOut := w.drive("--orgs", "go-a")
	if st != 0 || !strings.Contains(w.logged(), "gitpush origin "+branch) {
		t.Fatalf("status %d: %s %s", st, out, errOut)
	}
}

// A pull request from the branch that is not open is never re-opened and
// never doubled: the run stops before touching anything.
func TestAClosedOrMergedPullRequestFromTheBranchStopsTheRun(t *testing.T) {
	for _, state := range []string{"CLOSED", "MERGED", ""} {
		w := newWorld(t, map[string]answer{"go-a": ok(target1), "go-b": ok(target1)})
		list := fmt.Sprintf(`[{"number":5,"headRefOid":"abc123","state":%q}]`, state)
		if err := os.WriteFile(filepath.Join(w.dir, "prlist-go-a_.github"), []byte(list), 0o644); err != nil {
			t.Fatal(err)
		}
		st, _, errOut := w.drive()
		l := w.logged()
		if st != 1 || !strings.Contains(errOut, "STOPPED at go-a/.github: pull request #5 from "+branch+" is "+fmt.Sprintf("%q", state)) {
			t.Errorf("%q: status %d: %s", state, st, errOut)
		}
		for _, forbidden := range []string{"git ", "gitpush", "pr create", "prwait", "ghmerge", "go-b"} {
			if strings.Contains(l, forbidden) {
				t.Errorf("%q: %q ran:\n%s", state, forbidden, l)
			}
		}
	}
}

// A body the checks refuse must leave nothing behind: no clone, no branch, no push.
func TestARefusedBodyLeavesNoBranchBehind(t *testing.T) {
	for _, prog := range []string{"prbody", "paritycheck"} {
		w := newWorld(t, map[string]answer{"go-a": ok(target1), "go-b": ok(target1)})
		w.fake(prog, "echo "+prog+" refused >&2; exit 1")
		st, _, errOut := w.drive()
		l := w.logged()
		if st != 1 || !strings.Contains(errOut, "STOPPED at go-a/.github") || !strings.Contains(errOut, prog+" refused") {
			t.Errorf("%s: status %d: %s", prog, st, errOut)
		}
		for _, forbidden := range []string{"git ", "gitpush", "pr create", "go-b"} {
			if strings.Contains(l, forbidden) {
				t.Errorf("%s: %q ran after a refused body:\n%s", prog, forbidden, l)
			}
		}
	}
}
