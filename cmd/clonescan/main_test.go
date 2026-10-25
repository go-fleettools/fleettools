package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolate keeps the machine's git configuration out of the tests: a global
// core.hooksPath (this fleet installs a pre-push guard) or a signing setting
// would make a fixture behave differently from one machine to the next.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
	t.Setenv("GH_TOKEN", "") // never reach GitHub from a test
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture is a bare origin and a clone of it under root/clone, with main
// pushed. It returns root and the clone.
func fixture(t *testing.T) (string, string) {
	t.Helper()
	isolate(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin := filepath.Join(base, "origin.git")
	run(t, base, "init", "-q", "--bare", "-b", "main", origin)
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(root, "clone")
	run(t, base, "clone", "-q", origin, clone)
	run(t, clone, "checkout", "-q", "-b", "main")
	writeFile(t, filepath.Join(clone, "a.txt"), "a\n")
	run(t, clone, "add", "a.txt")
	run(t, clone, "commit", "-q", "-m", "a")
	run(t, clone, "push", "-q", "-u", "origin", "main")
	return root, clone
}

// noPRs fails the test if GitHub is asked at all.
func noPRs(t *testing.T) {
	t.Helper()
	mergedPRs = func(slug, branch string) ([]pullRequest, error) {
		t.Errorf("GitHub asked about %s %s", slug, branch)
		return nil, errors.New("not in this test")
	}
	t.Cleanup(func() { mergedPRs = defaultPRs })
}

var defaultPRs = mergedPRs

func scanOut(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&errb)
	code := execute(c, args)
	return code, out.String() + errb.String()
}

func TestACleanPushedCloneIsSafe(t *testing.T) {
	root, clone := fixture(t)
	noPRs(t)
	code, out := scanOut(t, "--recent", "0", root)
	if code != 0 || !strings.Contains(out, "SAFE "+clone) || !strings.Contains(out, "branch main: on a remote-tracking ref") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// ⛔ A SQUASH MERGE MAKES "UNPUSHED" MEANINGLESS: the branch's commits are on
// no remote once GitHub deletes it, and the work is in main. The merged pull
// request's head at the SAME tip is what makes it safe -- and a tip that moved
// on after the merge is not.
func TestASquashMergedBranchIsSafeOnlyAtTheMergedTip(t *testing.T) {
	root, clone := fixture(t)
	run(t, clone, "checkout", "-q", "-b", "feat")
	writeFile(t, filepath.Join(clone, "b.txt"), "b\n")
	run(t, clone, "add", "b.txt")
	run(t, clone, "commit", "-q", "-m", "b")
	merged := run(t, clone, "rev-parse", "HEAD")
	run(t, clone, "checkout", "-q", "main")
	run(t, clone, "remote", "set-url", "origin", "https://github.com/o/r.git")

	var asked []string
	answer := func(prs []pullRequest, err error) {
		mergedPRs = func(slug, branch string) ([]pullRequest, error) {
			asked = append(asked, slug+" "+branch)
			return prs, err
		}
	}
	t.Cleanup(func() { mergedPRs = defaultPRs })

	answer([]pullRequest{{Number: 7, HeadRefOid: merged}}, nil)
	code, out := scanOut(t, "--recent", "0", root)
	if code != 0 || !strings.Contains(out, "merged PR #7 at the same tip") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if len(asked) != 1 || asked[0] != "o/r feat" {
		t.Errorf("asked %v; main is on the remote and must not be asked about", asked)
	}

	for name, c := range map[string]struct {
		prs  []pullRequest
		err  error
		want string
	}{
		"no merged pull request":    {nil, nil, "no merged pull request at this tip"},
		"GitHub cannot be asked":    {nil, errors.New("HTTP 502"), "GitHub could not be asked"},
		"a pull request elsewhere":  {[]pullRequest{{Number: 8, HeadRefOid: "0123456789012345678901234567890123456789"}}, nil, "no merged pull request at this tip"},
		"the merged one, then more": {[]pullRequest{{Number: 7, HeadRefOid: merged}}, nil, "1 commit(s) after merged PR #7"},
	} {
		t.Run(name, func(t *testing.T) {
			if name == "the merged one, then more" {
				run(t, clone, "checkout", "-q", "feat")
				writeFile(t, filepath.Join(clone, "c.txt"), "c\n")
				run(t, clone, "add", "c.txt")
				run(t, clone, "commit", "-q", "-m", "c")
				run(t, clone, "checkout", "-q", "main")
			}
			answer(c.prs, c.err)
			code, out := scanOut(t, "--recent", "0", root)
			if code != 1 || !strings.Contains(out, "KEEP "+clone) || !strings.Contains(out, c.want) {
				t.Fatalf("exit %d, want KEEP saying %q\n%s", code, c.want, out)
			}
		})
	}
}

// ⛔ THREE "CLONES" WERE WORKTREES. A worktree found alone is reported under
// its main repository, once; the main is never SAFE while it hangs off it, and
// becomes SAFE once it is removed -- the control that the KEEP is the
// worktree's doing.
func TestAWorktreeIsReportedUnderItsMain(t *testing.T) {
	root, clone := fixture(t)
	noPRs(t)
	other := filepath.Join(filepath.Dir(root), "elsewhere")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(other, "wt")
	run(t, clone, "worktree", "add", "-q", "--detach", wt)

	code, out := scanOut(t, "--recent", "0", other)
	if code != 1 || !strings.Contains(out, "KEEP "+clone) || !strings.Contains(out, "found through its worktree "+wt) ||
		!strings.Contains(out, "worktree SAFE "+wt) || !strings.Contains(out, "1 linked worktree(s) hang off it") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if strings.Contains(out, "SAFE "+wt+"\n") || strings.HasPrefix(out, "SAFE") {
		t.Errorf("the worktree was reported as a clone of its own:\n%s", out)
	}

	// Both directories: still one repository.
	_, out = scanOut(t, "--recent", "0", root, other)
	if n := strings.Count(out, clone+"\n"); n != 1 {
		t.Errorf("the repository was reported %d times:\n%s", n, out)
	}

	run(t, clone, "worktree", "remove", wt)
	if code, out := scanOut(t, "--recent", "0", root); code != 0 {
		t.Fatalf("without its worktree the clone should be SAFE: exit %d\n%s", code, out)
	}
}

func TestADirtyWorktreeKeepsItsMain(t *testing.T) {
	root, clone := fixture(t)
	noPRs(t)
	wt := filepath.Join(root, "wt")
	run(t, clone, "worktree", "add", "-q", "--detach", wt)
	writeFile(t, filepath.Join(wt, "new.txt"), "work\n")
	code, out := scanOut(t, "--recent", "0", root)
	if code != 1 || !strings.Contains(out, "worktree KEEP "+wt) || !strings.Contains(out, "1 uncommitted path(s)") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// The disaster itself: the main repository deleted first leaves a worktree
// git cannot read. It is KEEP, never skipped.
func TestAnOrphanedWorktreeIsKept(t *testing.T) {
	root, clone := fixture(t)
	noPRs(t)
	other := filepath.Join(filepath.Dir(root), "elsewhere")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(other, "wt")
	run(t, clone, "worktree", "add", "-q", "--detach", wt)
	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	code, out := scanOut(t, "--recent", "0", other)
	if code != 1 || !strings.Contains(out, "KEEP "+wt) || !strings.Contains(out, "main repository is gone") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestAStashIsKept(t *testing.T) {
	root, clone := fixture(t)
	noPRs(t)
	writeFile(t, filepath.Join(clone, "a.txt"), "changed\n")
	run(t, clone, "stash", "-q")
	code, out := scanOut(t, "--recent", "0", root)
	if code != 1 || !strings.Contains(out, "1 stash(es)") || strings.Contains(out, "uncommitted") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestUncommittedPathsAreKept(t *testing.T) {
	root, clone := fixture(t)
	noPRs(t)
	writeFile(t, filepath.Join(clone, "a.txt"), "changed\n")
	writeFile(t, filepath.Join(clone, "untracked.txt"), "x\n")
	code, out := scanOut(t, "--recent", "0", root)
	if code != 1 || !strings.Contains(out, "2 uncommitted path(s)") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// A HEAD that just moved is somebody's: KEEP within --recent, and the same
// clone is SAFE with --recent 0.
func TestARecentHeadIsKept(t *testing.T) {
	root, _ := fixture(t)
	noPRs(t)
	code, out := scanOut(t, "--recent", "1h", root)
	if code != 1 || !strings.Contains(out, "within --recent 1h0m0s") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// Detached commits on no branch and no remote are kept.
func TestDetachedCommitsAreKept(t *testing.T) {
	root, clone := fixture(t)
	noPRs(t)
	run(t, clone, "checkout", "-q", "--detach")
	writeFile(t, filepath.Join(clone, "d.txt"), "d\n")
	run(t, clone, "add", "d.txt")
	run(t, clone, "commit", "-q", "-m", "d")
	code, out := scanOut(t, "--recent", "0", root)
	if code != 1 || !strings.Contains(out, "detached HEAD has 1 commit(s) on no branch and no remote") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// A bundle verifies, its heads are the repository's, and a clone of it gives
// the branches back.
func TestABundleVerifiesAndRestores(t *testing.T) {
	root, clone := fixture(t)
	noPRs(t)
	run(t, clone, "branch", "side")
	out := filepath.Join(filepath.Dir(root), "bundles")
	code, text := scanOut(t, "--recent", "0", "--bundle", out, root)
	if code != 0 || !strings.Contains(text, "(verified, heads match)") {
		t.Fatalf("exit %d\n%s", code, text)
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 1 {
		t.Fatalf("%v %v", entries, err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	run(t, filepath.Dir(restored), "clone", "-q", "--mirror", filepath.Join(out, entries[0].Name()), restored)
	want := run(t, clone, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads")
	got := run(t, restored, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads")
	if got != want || !strings.Contains(got, "refs/heads/side") {
		t.Fatalf("restored\n%s\nwant\n%s", got, want)
	}
}

// --bundle inside a work tree is refused before anything is written, however
// deep below the .git it is.
func TestABundleInsideAWorkTreeIsRefused(t *testing.T) {
	root, clone := fixture(t)
	noPRs(t)
	inside := filepath.Join(clone, "deep", "bundles")
	code, out := scanOut(t, "--recent", "0", "--bundle", inside, root)
	if code != 2 || !strings.Contains(out, "inside the git work tree "+clone) {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, err := os.Stat(inside); err == nil {
		t.Error("the refused directory was created")
	}
}

func TestSameHeadsSeesEveryDifference(t *testing.T) {
	repo := map[string]string{"refs/heads/main": "a", "refs/heads/x": "b"}
	if err := sameHeads(map[string]string{"refs/heads/main": "a", "refs/heads/x": "b"}, repo); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string]map[string]string{
		"a head moved":   {"refs/heads/main": "a", "refs/heads/x": "c"},
		"a head missing": {"refs/heads/main": "a"},
		"an extra head":  {"refs/heads/main": "a", "refs/heads/x": "b", "refs/heads/y": "d"},
	} {
		if sameHeads(b, repo) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseWorktreesReadsThePorcelainFormat(t *testing.T) {
	// The example of git-worktree(1), NUL-terminated as -z prints it.
	in := strings.Join([]string{
		"worktree /path/to/bare-source", "bare", "",
		"worktree /path/to/linked-worktree", "HEAD abcd1234abcd1234abcd1234abcd1234abcd1234", "branch refs/heads/master", "",
		"worktree /path/to/other-linked-worktree", "HEAD 1234abc1234abc1234abc1234abc1234abc1234a", "detached", "",
		"worktree /path/to/linked-worktree-locked-with-reason", "HEAD 3456def3456def3456def3456def3456def3456b", "branch refs/heads/locked-with-reason", "locked reason why is locked", "",
		"worktree /path/to/linked-worktree-prunable", "HEAD 1233def1234def1234def1234def1234def1234b", "detached", "prunable gitdir file points to non-existent location", "",
	}, "\x00")
	got := parseWorktrees(in)
	if len(got) != 5 || !got[0].bare || got[1].branch != "master" || !got[2].detached ||
		!got[3].locked || got[3].lockReason != "reason why is locked" || !got[4].prunable {
		t.Fatalf("%+v", got)
	}
}

func TestCountStatusCountsARenameOnce(t *testing.T) {
	in := " M a.txt\x00R  new.txt\x00old.txt\x00?? u.txt\x00"
	if n := countStatus(in); n != 3 {
		t.Fatalf("%d", n)
	}
}

func TestGithubSlug(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/o/r.git":     "o/r",
		"https://github.com/o/r":         "o/r",
		"git@github.com:o/r.git":         "o/r",
		"ssh://git@github.com/o/r.x.git": "o/r.x",
		"https://gitlab.com/o/r.git":     "",
		"/tmp/origin.git":                "",
		"https://github.com/o/r/extra":   "",
	} {
		if got := githubSlug(in); got != want {
			t.Errorf("githubSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUsage(t *testing.T) {
	isolate(t)
	for args, want := range map[string]int{"-h": 0, "--help": 0, "": 2, "--depth -1 .": 2} {
		if code, _ := scanOut(t, strings.Fields(args)...); code != want {
			t.Errorf("clonescan %s: exit %d, want %d", args, code, want)
		}
	}
	empty := t.TempDir()
	if code, out := scanOut(t, empty); code != 2 || !strings.Contains(out, "no git repository") {
		t.Errorf("an empty directory: exit %d\n%s", code, out)
	}
}
