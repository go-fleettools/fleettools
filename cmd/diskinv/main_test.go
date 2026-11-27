package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

func drive(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := cli.Drive(RootCmd(), args, &out, &errb)
	return out.String(), errb.String(), code
}

func write(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if o, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, o)
	}
}

func TestTopRanksByAllocatedSizeAndReportsAge(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "big", "a.bin"), 1<<20)
	write(t, filepath.Join(root, "small", "b.bin"), 10)
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "big", "a.bin"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(root, "big"), old, old); err != nil {
		t.Fatal(err)
	}
	out, _, code := drive(t, "top", root)
	if code != 0 {
		t.Fatalf("status %d\n%s", code, out)
	}
	bi, si := strings.Index(out, "/big"), strings.Index(out, "/small")
	if bi < 0 || si < 0 || bi > si {
		t.Fatalf("big must come before small:\n%s", out)
	}
	if !strings.Contains(out, "3d00h") {
		t.Fatalf("age of the newest mtime inside big not shown:\n%s", out)
	}
	if !strings.Contains(out, "unreadable: 0") || !strings.Contains(out, "NOT checked") {
		t.Fatalf("missing statements:\n%s", out)
	}
}

func TestTopLimitsDepthAndCount(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "d", "e", "f.bin"), 4096)
	write(t, filepath.Join(root, "d", "g.bin"), 4096)
	out, _, _ := drive(t, "top", "--depth", "2", root)
	if !strings.Contains(out, "/d/e") || !strings.Contains(out, "/d/g.bin") || strings.Contains(out, "f.bin") {
		t.Fatalf("depth 2 rows wrong:\n%s", out)
	}
	out, _, _ = drive(t, "top", "-n", "1", "--depth", "2", root)
	if !strings.Contains(out, "shown 1 of 2") {
		t.Fatalf("-n not honoured:\n%s", out)
	}
	// the single-dash spelling the fleet used to type keeps working
	out, _, code := drive(t, "top", "-depth", "2", root)
	if code != 0 || !strings.Contains(out, "/d/e") {
		t.Fatalf("-depth: %d\n%s", code, out)
	}
}

func TestTopNeverFollowsASymlink(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	write(t, filepath.Join(other, "huge.bin"), 4<<20)
	if err := os.Symlink(other, filepath.Join(root, "link")); err != nil {
		t.Skip("no symlinks:", err)
	}
	out, _, _ := drive(t, "top", root)
	if strings.Contains(out, "MiB") {
		t.Fatalf("followed the link:\n%s", out)
	}
}

func TestTopCountsAHardLinkOnce(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a"), 1<<20)
	if err := os.Link(filepath.Join(root, "a"), filepath.Join(root, "b")); err != nil {
		t.Skip("no hard links:", err)
	}
	out, _, _ := drive(t, "top", root)
	if !strings.Contains(out, "1.0 MiB allocated") && !strings.Contains(out, "1.0 MiB of") {
		t.Fatalf("hard link counted twice:\n%s", out)
	}
}

func TestTopSaysWhatItCouldNotRead(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	root := t.TempDir()
	write(t, filepath.Join(root, "locked", "x"), 4096)
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	out, _, code := drive(t, "top", root)
	if code != 1 || !strings.Contains(out, "UNREADABLE: 1") || !strings.Contains(out, "locked") {
		t.Fatalf("a blind scan was reported as complete (status %d):\n%s", code, out)
	}
}

func TestTopRefusesBadInput(t *testing.T) {
	for _, args := range [][]string{
		{"top"}, {"top", "/nonexistent-diskinv"}, {"top", "--depth", "0", t.TempDir()}, {"top", "-n", "0", t.TempDir()}, {},
	} {
		if _, _, code := drive(t, args...); code != 2 {
			t.Errorf("%v: status %d, want 2", args, code)
		}
	}
	f := filepath.Join(t.TempDir(), "f")
	write(t, f, 1)
	if _, _, code := drive(t, "top", f); code != 2 {
		t.Errorf("a file as root: status %d, want 2", code)
	}
}

func TestClonesReportsUnpushedDirtyStashAndWorktrees(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	work := filepath.Join(root, "work")
	git(t, root, "init", "-q", "--bare", "-b", "main", bare)
	git(t, root, "clone", "-q", bare, work)
	write(t, filepath.Join(work, "f.txt"), 100)
	git(t, work, "add", ".")
	git(t, work, "commit", "-q", "-m", "one")
	git(t, work, "push", "-q", "origin", "main")
	write(t, filepath.Join(work, "g.txt"), 100)
	git(t, work, "add", ".")
	git(t, work, "commit", "-q", "-m", "two") // not on any remote
	write(t, filepath.Join(work, "dirty.txt"), 5)
	git(t, work, "stash", "-u", "-q")
	write(t, filepath.Join(work, "dirty.txt"), 5) // dirty again, one stash kept
	git(t, work, "worktree", "add", "-q", "-b", "side", filepath.Join(root, "side"))
	write(t, filepath.Join(root, "plain", "p"), 3<<20)

	out, errs, code := drive(t, "clones", root)
	if code != 0 {
		t.Fatalf("status %d\n%s\n%s", code, out, errs)
	}
	line := func(name string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasSuffix(strings.TrimSpace(l), " "+name) {
				return l
			}
		}
		t.Fatalf("no row for %s:\n%s", name, out)
		return ""
	}
	w := strings.Fields(line("work"))
	// SIZE(2 fields) KIND BRANCH DIRTY UNPUSHED STASH WT...
	if w[2] != "repo" || w[3] != "main" || !strings.HasPrefix(w[4], "yes(") || w[5] != "1" || w[6] != "1" || !strings.Contains(line("work"), "+1 linked") {
		t.Errorf("work row wrong: %q", line("work"))
	}
	s := line("side")
	if !strings.Contains(s, "worktree") || !strings.Contains(s, "linked of work") || !strings.Contains(s, " side ") {
		t.Errorf("side row wrong: %q", s)
	}
	if p := line("plain"); !strings.Contains(p, "dir") || !strings.Contains(p, "MiB") {
		t.Errorf("plain row wrong: %q", p)
	}
	// the biggest comes first: the 3 MiB plain directory
	if strings.Index(out, " plain\n") > strings.Index(out, " work\n") {
		t.Errorf("not sorted by size:\n%s", out)
	}
	if !strings.Contains(out, "deletes nothing") {
		t.Errorf("missing statement:\n%s", out)
	}
	// a bare repository has no .git inside: it is a plain directory here.
	if !strings.Contains(line("bare.git"), "dir") {
		t.Errorf("bare row: %q", line("bare.git"))
	}
}

func TestClonesLimitAndAGitFailureIsNotAZero(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", filepath.Join(root, "empty"))
	write(t, filepath.Join(root, "x", "f"), 10)
	out, _, code := drive(t, "clones", "-n", "1", root)
	if code != 0 || !strings.Contains(out, "shown 1 of 2") || !strings.Contains(out, "none") && !strings.Contains(out, "x") {
		t.Fatalf("status %d\n%s", code, out)
	}
	old := runGit
	defer func() { runGit = old }()
	runGit = func(string, ...string) (string, error) { return "", os.ErrPermission }
	out, _, code = drive(t, "clones", root)
	if code != 1 || !strings.Contains(out, "INCOMPLETE") || !strings.Contains(out, "?") {
		t.Fatalf("an unanswered git question was reported as complete (status %d):\n%s", code, out)
	}
}

func TestRefusalsSayWhyAndCountOperands(t *testing.T) {
	d := t.TempDir()
	for _, sub := range []string{"top", "clones"} {
		for _, c := range []struct {
			args []string
			want string
		}{{[]string{sub}, "arg(s)"}, {[]string{sub, d, d}, "arg(s)"}, {[]string{sub, "-n", "0", d}, "diskinv " + sub}} {
			out, errs, code := drive(t, c.args...)
			if code != 2 || out != "" || !strings.Contains(errs, c.want) {
				t.Errorf("%v: status %d, stdout %q, stderr %q", c.args, code, out, errs)
			}
		}
		_, errs, code := drive(t, sub, "/nonexistent-diskinv")
		if code != 2 || !strings.Contains(errs, "no such file") {
			t.Errorf("%s nonexistent: status %d, stderr %q", sub, code, errs)
		}
		f := filepath.Join(d, "afile")
		write(t, f, 1)
		_, errs, code = drive(t, sub, f)
		if code != 2 || !strings.Contains(errs, "is not a directory") {
			t.Errorf("%s on a file: status %d, stderr %q", sub, code, errs)
		}
	}
}

func TestClonesOnAnUnreadableDirectoryIsRefused(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	d := t.TempDir()
	locked := filepath.Join(d, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	out, errs, code := drive(t, "clones", locked)
	if code != 2 || out != "" || !strings.Contains(errs, "permission denied") {
		t.Fatalf("status %d, stdout %q, stderr %q", code, out, errs)
	}
}

func TestAScanOfAPathThatIsGoneIsRecordedNotZero(t *testing.T) {
	s := newScan()
	b, n := s.run(filepath.Join(t.TempDir(), "gone"), 1, nil)
	if b != 0 || !n.IsZero() || s.unreadable != 1 || len(s.errs) != 1 || !strings.Contains(s.errs[0], "gone") {
		t.Fatalf("bytes %d newest %v unreadable %d errs %v", b, n, s.unreadable, s.errs)
	}
	c := &clone{name: "gone"}
	inspect(filepath.Join(t.TempDir(), "gone"), 0, c)
	if c.kind != "file" || c.scanN != 1 || len(c.errs) != 1 || c.bytes != 0 {
		t.Fatalf("%+v", c)
	}
	if got := linkedOf(filepath.Join(t.TempDir(), "no.git")); got != "linked of ?" {
		t.Fatalf("linkedOf a missing file = %q", got)
	}
}

func TestRunGitReturnsOutputAndTheReasonForAFailure(t *testing.T) {
	if o, err := runGit(t.TempDir(), "--version"); err != nil || !strings.HasPrefix(o, "git version") {
		t.Fatalf("%q %v", o, err)
	}
	_, err := runGit(t.TempDir(), "rev-parse", "--verify", "HEAD")
	if err == nil || !strings.Contains(err.Error(), "rev-parse --verify HEAD") {
		t.Fatalf("%v", err)
	}
}

// Every git question that fails is named in the report, shown as ? in its own
// column, and makes the status 1 -- one at a time, so that a question whose
// failure is swallowed cannot hide behind the others.
func TestEachGitQuestionThatFailsIsNamed(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "r")
	git(t, root, "init", "-q", "-b", "main", repo)
	write(t, filepath.Join(repo, "f"), 1)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "one")
	real := runGit
	defer func() { runGit = real }()
	for _, q := range []struct{ sub, name string }{
		{"branch", "branch"}, {"status", "status"}, {"rev-list", "unpushed"},
		{"stash", "stash"}, {"log", "last commit"}, {"worktree", "worktrees"},
	} {
		runGit = func(dir string, args ...string) (string, error) {
			if args[0] == q.sub {
				return "", os.ErrPermission
			}
			return real(dir, args...)
		}
		out, _, code := drive(t, "clones", root)
		if code != 1 || !strings.Contains(out, "INCOMPLETE") || !strings.Contains(out, "r: "+q.name+": ") || !strings.Contains(out, "?") {
			t.Errorf("%s: status %d\n%s", q.sub, code, out)
		}
	}
	runGit = real
	if out, _, code := drive(t, "clones", root); code != 0 || strings.Contains(out, "INCOMPLETE") || strings.Contains(out, "?") {
		t.Errorf("control: status %d\n%s", code, out)
	}
}

func TestClonesShowsASymlinkAsTheLinkAndAFileAsAFile(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	write(t, filepath.Join(other, "huge"), 4<<20)
	if err := os.Symlink(other, filepath.Join(root, "ln")); err != nil {
		t.Skip("no symlinks:", err)
	}
	write(t, filepath.Join(root, "afile"), 2<<20)
	out, _, code := drive(t, "clones", root)
	if code != 0 || !strings.Contains(out, "symlink") || !strings.Contains(out, "file") || strings.Contains(out, "4.0 MiB") {
		t.Fatalf("status %d\n%s", code, out)
	}
}

func TestHumanAndAge(t *testing.T) {
	for in, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 3 << 30: "3.0 GiB", 1 << 40: "1.0 TiB"} {
		if got := human(in); got != want {
			t.Errorf("human(%d) = %q, want %q", in, got, want)
		}
	}
	n := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{30 * time.Minute: "30m", 5*time.Hour + 7*time.Minute: "5h07m", 50 * time.Hour: "2d02h", -time.Hour: "0m"} {
		if got := age(n, n.Add(-d)); got != want {
			t.Errorf("age(%v) = %q, want %q", d, got, want)
		}
	}
	if age(n, time.Time{}) != "?" {
		t.Error("zero time must read ?")
	}
}
