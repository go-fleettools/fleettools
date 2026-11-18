package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-fleettools/fleettools/internal/binnames"
	"github.com/go-fleettools/fleettools/internal/cli"
)

const mainSrc = "package main\n\nfunc main() {}\n"

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, files)
	return root
}

// drive runs binnames with an empty PATH unless the test set one, so the host
// the tests run on cannot decide a verdict.
func drive(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := cli.Drive(rootCmd(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func emptyPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	// git alone stays reachable: the fleet tests make real clones.
	if g, err := exec.LookPath("git"); err == nil {
		if err := os.Symlink(g, filepath.Join(dir, "git")); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestNoCollision(t *testing.T) {
	emptyPath(t)
	dir := fixture(t, map[string]string{"go.mod": "module github.com/o/r\n", "cmd/authn-kinit/main.go": mainSrc})
	code, out, errs := drive(t, dir)
	if code != 0 {
		t.Fatalf("status %d\n%s%s", code, out, errs)
	}
	if !strings.Contains(out, "-> authn-kinit") || !strings.Contains(out, "0 collisions, 0 unread") {
		t.Fatalf("%s", out)
	}
}

func TestACmdDirectoryCollision(t *testing.T) {
	emptyPath(t)
	dir := fixture(t, map[string]string{"go.mod": "module github.com/o/r\n", "cmd/ls/main.go": mainSrc})
	code, out, _ := drive(t, dir)
	if code != 1 {
		t.Fatalf("status %d\n%s", code, out)
	}
	for _, w := range []string{"COLLISION", `binary "ls"`, "cmd/ls", "coreutils", "source: https://"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// go-authn/kinit v0.1.0: main.go at the root of a module named .../kinit.
func TestTheKinitCase(t *testing.T) {
	emptyPath(t)
	dir := fixture(t, map[string]string{"go.mod": "module github.com/go-authn/kinit\n", "main.go": mainSrc})
	code, out, _ := drive(t, dir)
	if code != 1 || !strings.Contains(out, `binary "kinit"`) || !strings.Contains(out, "MIT krb5") {
		t.Fatalf("status %d\n%s", code, out)
	}
}

func TestAVersionedModuleIsNamedForItsPredecessor(t *testing.T) {
	emptyPath(t)
	dir := fixture(t, map[string]string{"go.mod": "module github.com/o/ssh/v2\n", "main.go": mainSrc})
	code, out, _ := drive(t, dir)
	if code != 1 || !strings.Contains(out, `binary "ssh"`) {
		t.Fatalf("status %d\n%s", code, out)
	}
}

func TestAllowedNameDoesNotCollide(t *testing.T) {
	emptyPath(t)
	dir := fixture(t, map[string]string{"go.mod": "module github.com/o/kinit\n", "main.go": mainSrc})
	if code, out, _ := drive(t, "--allow", "KINIT", dir); code != 0 {
		t.Fatalf("status %d\n%s", code, out)
	}
}

func TestThePathSupplementIsUsedAndSaysSo(t *testing.T) {
	host := fixture(t, map[string]string{"zzhosttool": "#!/bin/sh\n"})
	if err := os.Chmod(filepath.Join(host, "zzhosttool"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", host)
	dir := fixture(t, map[string]string{"go.mod": "module github.com/o/zzhosttool\n", "main.go": mainSrc})
	code, out, _ := drive(t, dir)
	if code != 1 || !strings.Contains(out, "host's PATH") || !strings.Contains(out, "PATH supplement: read 1 host PATH directories") {
		t.Fatalf("status %d\n%s", code, out)
	}
	if code, out, _ := drive(t, "--no-path", dir); code != 0 || !strings.Contains(out, "PATH supplement: off") {
		t.Fatalf("--no-path: status %d\n%s", code, out)
	}
	// The fleet's own install directory is not a collision with itself.
	if code, out, _ := drive(t, "--path-skip", host, dir); code != 0 {
		t.Fatalf("--path-skip: status %d\n%s", code, out)
	}
}

func TestUnreadIsNotClean(t *testing.T) {
	emptyPath(t)
	code, out, _ := drive(t, filepath.Join(t.TempDir(), "missing"))
	if code != 2 || !strings.Contains(out, "UNREAD") {
		t.Fatalf("status %d\n%s", code, out)
	}
	if code, _, _ := drive(t); code != 2 {
		t.Fatalf("no operand: status %d", code)
	}
}

func TestHelpWritesToStderrAndStartsNothing(t *testing.T) {
	code, out, errs := drive(t, "fleet", "-h")
	if code != 0 || out != "" || !strings.Contains(errs, "UNREAD") {
		t.Fatalf("status %d stdout %q stderr %q", code, out, errs)
	}
}

// git runs a command in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// mkClone makes a git clone with a github origin whose default branch holds files.
func mkClone(t *testing.T, root, name, origin string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	write(t, dir, files)
	git(t, dir, "init", "-q")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "x")
	git(t, dir, "remote", "add", "origin", origin)
	git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	return dir
}

func TestFleetReadsClonesAtTheDefaultBranchAndReportsWhatItCouldNotRead(t *testing.T) {
	emptyPath(t)
	root := t.TempDir()
	mkClone(t, root, "o-tidy", "https://github.com/o/tidy.git", map[string]string{"go.mod": "module github.com/o/tidy\n", "cmd/tidyfix/main.go": mainSrc})
	bad := mkClone(t, root, "o-bad", "git@github.com:o/bad.git", map[string]string{"go.mod": "module github.com/o/kinit\n", "main.go": mainSrc})
	// The working tree has since been fixed on a branch; the default branch has not.
	if err := os.Remove(filepath.Join(bad, "main.go")); err != nil {
		t.Fatal(err)
	}
	mkClone(t, root, "elsewhere", "https://gitlab.com/o/x.git", map[string]string{"main.go": mainSrc})
	if err := os.MkdirAll(filepath.Join(root, "not-a-clone"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A second clone of o/tidy is read once.
	mkClone(t, root, "o-tidy-copy", "https://github.com/o/tidy.git", map[string]string{"go.mod": "module github.com/o/tidy\n", "cmd/tidyfix/main.go": mainSrc})

	code, out, errs := drive(t, "fleet", "--clones", root, "--no-remote")
	if code != 1 {
		t.Fatalf("status %d\n%s%s", code, out, errs)
	}
	for _, w := range []string{"COLLISION o/bad .", `binary "kinit"`, "UNREAD elsewhere", "origin is not on github.com", "3 repositories"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "o/tidy") {
		t.Errorf("a clean repository is not listed without --list:\n%s", out)
	}
}

func TestFleetOnlyUnreadIsStatusTwo(t *testing.T) {
	emptyPath(t)
	root := t.TempDir()
	mkClone(t, root, "x", "https://gitlab.com/o/x.git", map[string]string{"main.go": mainSrc})
	if code, _, _ := drive(t, "fleet", "--clones", root, "--no-remote"); code != 2 {
		t.Fatalf("status %d: an unread repository is not a clean fleet", code)
	}
	if code, _, _ := drive(t, "fleet", "--clones", t.TempDir(), "--no-remote"); code != 2 {
		t.Fatalf("status %d: a sweep of nothing is not a clean one", code)
	}
}

func TestACloneWithoutADefaultRefIsUnread(t *testing.T) {
	emptyPath(t)
	root := t.TempDir()
	dir := mkClone(t, root, "o-r", "https://github.com/o/r.git", map[string]string{"main.go": mainSrc})
	git(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
	code, out, _ := drive(t, "fleet", "--clones", root, "--no-remote")
	if code != 2 || !strings.Contains(out, "UNREAD o/r") {
		t.Fatalf("status %d\n%s", code, out)
	}
}

func TestRemoteRepositoriesPayOneCallEachAndStopAtTheCap(t *testing.T) {
	emptyPath(t)
	ck, err := newChecker(&bytes.Buffer{}, opts{noPath: true})
	if err != nil {
		t.Fatal(err)
	}
	api := func(args ...string) ([]byte, error) {
		return []byte(`{"tree":[{"path":"go.mod","type":"blob"},{"path":"cmd/ls/main.go","type":"blob"}]}`), nil
	}
	raw := func(u string) ([]byte, error) {
		if strings.HasSuffix(u, "go.mod") {
			return []byte("module github.com/o/r\n"), nil
		}
		return []byte(mainSrc), nil
	}
	b := &binnames.Budget{Cap: 2}
	r1 := ck.remoteWith("o/a", "main", b, api, raw)
	r2 := ck.remoteWith("o/b", "main", b, api, raw)
	r3 := ck.remoteWith("o/c", "main", b, api, raw)
	if len(r1.collisions) != 1 || len(r2.collisions) != 1 {
		t.Fatalf("collisions: %v %v", r1.collisions, r2.collisions)
	}
	if len(r3.unread) != 1 || !strings.Contains(r3.unread[0].Why, "cap") || len(r3.collisions) != 0 {
		t.Fatalf("past the cap a repository must be UNREAD, got %+v", r3)
	}
	if b.Used != 2 {
		t.Fatalf("spent %d", b.Used)
	}
}

func TestFindClonesRedactsCredentialsInAnOrigin(t *testing.T) {
	root := t.TempDir()
	mkClone(t, root, "x", "https://user:secret@gitlab.com/o/x.git", map[string]string{"main.go": mainSrc})
	_, unread, err := findClones(root)
	if err != nil || len(unread) != 1 {
		t.Fatalf("%v %v", unread, err)
	}
	if strings.Contains(unread[0].Why, "secret") {
		t.Fatalf("credentials printed: %s", unread[0].Why)
	}
}

func TestGithubOriginSpellings(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/o/r.git":                   "o/r",
		"https://github.com/o/r":                       "o/r",
		"git@github.com:o/r.git":                       "o/r",
		"ssh://git@ssh.github.com:443/o/r.git":         "o/r",
		"ssh://ssh.github.com:443/go-ruby-cgi/cgi.git": "go-ruby-cgi/cgi",
		"https://github.com/o/r.v2.git":                "o/r.v2",
	} {
		m := githubOrigin.FindStringSubmatch(in)
		if m == nil || m[1]+"/"+m[2] != want {
			t.Errorf("%s: got %v want %s", in, m, want)
		}
	}
	if githubOrigin.MatchString("https://gitlab.com/o/r.git") {
		t.Error("gitlab is not github")
	}
}

func TestACloneWithoutADefaultRefIsRescuedByAnotherClone(t *testing.T) {
	emptyPath(t)
	root := t.TempDir()
	broken := mkClone(t, root, "o-r-topic", "https://github.com/o/r.git", map[string]string{"go.mod": "module github.com/o/kinit\n", "main.go": mainSrc})
	git(t, broken, "update-ref", "-d", "refs/remotes/origin/main")
	mkClone(t, root, "o-r", "https://github.com/o/r.git", map[string]string{"go.mod": "module github.com/o/kinit\n", "main.go": mainSrc})
	code, out, _ := drive(t, "fleet", "--clones", root, "--no-remote")
	if code != 1 || strings.Contains(out, "UNREAD") || !strings.Contains(out, "COLLISION o/r") {
		t.Fatalf("status %d\n%s", code, out)
	}
}
