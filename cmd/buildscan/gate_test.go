// Copyright (c) the go-fleettools authors.
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
)

func TestPlatformReason(t *testing.T) {
	for _, c := range []struct {
		name, path, src, want string // want "" = neutral
	}{
		{"plain", "a.go", "package a\n", ""},
		{"plain test", "a_test.go", "package a\n", ""},
		{"go:build", "a.go", "//go:build !js\n\npackage a\n", "build constraint"},
		{"plus build", "a.go", "// +build linux\n\npackage a\n", "build constraint"},
		{"constraint after a licence", "a.go", "// Copyright x\n\n//go:build linux\n\npackage a\n", "build constraint"},
		{"go:build text AFTER package is not a constraint", "a.go", "package a\n\n//go:build linux\nvar x = 1\n", ""},
		{"os suffix", "a_windows.go", "package a\n", "operating system windows"},
		{"arch suffix", "a_arm64.go", "package a\n", "architecture arm64"},
		{"os_arch suffix", "a_linux_amd64.go", "package a\n", "architecture amd64"},
		{"suffix before _test", "a_linux_test.go", "package a\n", "operating system linux"},
		{"bare os name has no stem", "linux.go", "package a\n", ""},
		{"os word inside the stem", "linux_helper.go", "package a\n", ""},
		{"syscall", "a.go", "package a\n\nimport \"syscall\"\n\nvar _ = syscall.Getpid\n", "imports syscall"},
		{"unsafe", "a.go", "package a\n\nimport (\n\t\"fmt\"\n\t\"unsafe\"\n)\n\nvar _ = fmt.Sprint(unsafe.Sizeof(0))\n", "imports unsafe"},
		{"x/sys", "a.go", "package a\n\nimport \"golang.org/x/sys/unix\"\n\nvar _ = unix.Getpid\n", "imports golang.org/x/sys/unix"},
		{"x/sys root", "a.go", "package a\n\nimport _ \"golang.org/x/sys\"\n", "imports golang.org/x/sys"},
		{"a lookalike import is neutral", "a.go", "package a\n\nimport _ \"example.com/syscall\"\nimport _ \"example.com/unsafe\"\n", ""},
		{"a syscall NAME in a string is neutral", "a.go", "package a\n\nvar s = \"syscall\"\n", ""},
		{"does not parse", "a.go", "this is not go", "does not parse"},
	} {
		got := platformReason(c.path, []byte(c.src))
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: platformReason = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPushesIn(t *testing.T) {
	for _, c := range []struct {
		name, cmd string
		want      []push
	}{
		{"none", "go test ./...", nil},
		{"gitpush", "gitpush origin b", []push{{dir: "/w"}}},
		{"git push", "git push", []push{{dir: "/w"}}},
		{"absolute cd", "cd /r && gitpush", []push{{dir: "/r"}}},
		{"relative cd", "cd sub && gitpush", []push{{dir: "/w/sub"}}},
		{"git -C", "git -C /r push", []push{{dir: "/r"}}},
		{"git -c is not a dir", "git -c a=b push", []push{{dir: "/w"}}},
		{"waiver", "BUILDSCAN_SKIP=1 gitpush", []push{{dir: "/w", skip: true}}},
		{"another variable is not the waiver", "MUTSWEEP_SKIP=1 gitpush", []push{{dir: "/w"}}},
		{"waiver must be =1", "BUILDSCAN_SKIP=0 gitpush", []push{{dir: "/w"}}},
		{"commit then push", "git commit -m x && gitpush", []push{{dir: "/w", moved: "git commit"}}},
		{"git status moves nothing", "git status && gitpush", []push{{dir: "/w"}}},
		{"two pushes", "gitpush a; cd /r; gitpush b", []push{{dir: "/w"}, {dir: "/r"}}},
		{"git log mentioning push", "git log --grep push", nil},
	} {
		got := pushesIn(c.cmd, "/w")
		if len(got) != len(c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: push %d = %+v, want %+v", c.name, i, got[i], c.want[i])
			}
		}
	}
}

func TestRecordKeyedByTree(t *testing.T) {
	t.Setenv(recordsEnv, t.TempDir())
	const old = "1111111111111111111111111111111111111111"
	const cur = "2222222222222222222222222222222222222222"
	if _, ok, err := lookupRecord(cur); ok || err != nil {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	if err := storeRecord(passRecord{Tree: old, Configs: []string{"linux/amd64"}}); err != nil {
		t.Fatal(err)
	}
	if r, ok, err := lookupRecord(old); !ok || err != nil || r.Tree != old {
		t.Fatalf("the tree that was vetted has no record: %+v ok=%v err=%v", r, ok, err)
	}
	// The control of the whole feature: a pass for an OLDER tree does not count.
	if _, ok, err := lookupRecord(cur); ok || err != nil {
		t.Fatalf("a record for another tree vouched for this one: ok=%v err=%v", ok, err)
	}
}

func TestRecordRefusesWhatItCannotTrust(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(recordsEnv, dir)
	const a = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const b = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := os.WriteFile(filepath.Join(dir, a+".json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := lookupRecord(a); ok || err == nil {
		t.Errorf("a corrupt record: ok=%v err=%v, want an error", ok, err)
	}
	// A record moved under another tree's name must not vouch for it.
	if err := storeRecord(passRecord{Tree: a}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, a+".json"), filepath.Join(dir, b+".json")); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := lookupRecord(b); ok || err == nil {
		t.Errorf("a renamed record: ok=%v err=%v, want an error", ok, err)
	}
	// A tree is a file name: nothing but a hash may become one.
	for _, bad := range []string{"", "../x", "abc", strings.Repeat("g", 40), a + "/.."} {
		if err := storeRecord(passRecord{Tree: bad}); err == nil {
			t.Errorf("storeRecord(%q) was accepted", bad)
		}
		if _, _, err := lookupRecord(bad); err == nil {
			t.Errorf("lookupRecord(%q) was accepted", bad)
		}
	}
}

// ---- a real repository ------------------------------------------------------

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitIn(dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.test",
		"-c", "commit.gpgsign=false"}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

const ciYML = "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go vet ./...\n"

// newClone makes a repository whose origin/main is its first commit, so a later
// commit is "the push". withCI false leaves it without workflows.
func newClone(t *testing.T, withCI bool) string {
	t.Helper()
	t.Setenv(recordsEnv, t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")
	if withCI {
		write(t, dir, ".github/workflows/ci.yml", ciYML)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	return dir
}

func commit(t *testing.T, dir, rel, body string) {
	t.Helper()
	write(t, dir, rel, body)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "change "+rel)
}

func pushOf(dir string) payload {
	var p payload
	p.ToolName = "Bash"
	p.Cwd = dir
	p.ToolInput.Command = "gitpush origin b"
	return p
}

func TestNeutralPushIsNotRefused(t *testing.T) {
	dir := newClone(t, true)
	commit(t, dir, "plain.go", "package a\n\nfunc F() {}\n")
	if d := decide(pushOf(dir)); d.deny || d.reason != "" {
		t.Fatalf("a platform-neutral push: %+v, want a silent allow", d)
	}
}

func TestPlatformPushWithoutAPassIsRefused(t *testing.T) {
	for _, c := range []struct{ name, rel, body, in string }{
		{"constraint", "x.go", "//go:build !js\n\npackage a\n", "x.go"},
		{"suffix", "x_windows.go", "package a\n", "x_windows.go"},
		{"syscall in a test", "x_test.go", "package a\n\nimport \"syscall\"\n\nvar _ = syscall.Getpid\n", "x_test.go"},
	} {
		dir := newClone(t, true)
		commit(t, dir, c.rel, c.body)
		d := decide(pushOf(dir))
		if !d.deny {
			t.Errorf("%s: not refused: %+v", c.name, d)
			continue
		}
		tree := git(t, dir, "rev-parse", "HEAD^{tree}")
		for _, want := range []string{c.in, tree[:12], "buildscan --dir " + dir + " --vet", skipEnv} {
			if !strings.Contains(d.reason, want) {
				t.Errorf("%s: the refusal does not name %q:\n%s", c.name, want, d.reason)
			}
		}
	}
}

func TestChangingAnExistingPlatformFileIsAsRefused(t *testing.T) {
	dir := newClone(t, true)
	commit(t, dir, "x_linux.go", "package a\n")
	git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD") // already pushed
	if d := decide(pushOf(dir)); d.deny {
		t.Fatalf("nothing to push but refused: %+v", d)
	}
	commit(t, dir, "x_linux.go", "package a\n\nvar v = 1\n")
	if d := decide(pushOf(dir)); !d.deny {
		t.Fatalf("a modified platform file was not refused: %+v", d)
	}
}

func TestAPassForThisTreeAllowsAndAnOlderOneDoesNot(t *testing.T) {
	dir := newClone(t, true)
	commit(t, dir, "x_windows.go", "package a\n")
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")
	if err := storeRecord(passRecord{Tree: tree, Configs: []string{"linux/amd64"}}); err != nil {
		t.Fatal(err)
	}
	if d := decide(pushOf(dir)); d.deny || d.reason != "" {
		t.Fatalf("a recorded pass for this tree: %+v, want a silent allow", d)
	}
	// One more commit: the tree moves, and the old record no longer counts.
	commit(t, dir, "y.go", "package a\n\nfunc Y() {}\n")
	if d := decide(pushOf(dir)); !d.deny {
		t.Fatalf("a pass for an older tree vouched for the new one: %+v", d)
	}
}

func TestAWaiverAllowsAndIsShown(t *testing.T) {
	dir := newClone(t, true)
	commit(t, dir, "x_windows.go", "package a\n")
	p := pushOf(dir)
	p.ToolInput.Command = "BUILDSCAN_SKIP=1 gitpush origin b"
	d := decide(p)
	if d.deny {
		t.Fatalf("a waived push was refused: %+v", d)
	}
	for _, want := range []string{"WAIVED", skipEnv, "x_windows.go"} {
		if !strings.Contains(d.reason, want) {
			t.Errorf("the waiver does not say %q: %q", want, d.reason)
		}
	}
}

func TestWhatItCannotReadIsAllowedAndSaid(t *testing.T) {
	t.Run("not a repository", func(t *testing.T) {
		t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
		d := decide(pushOf(t.TempDir()))
		if d.deny || !strings.Contains(d.reason, "NOT JUDGED") {
			t.Fatalf("%+v, want an allow that says NOT JUDGED", d)
		}
	})
	t.Run("no origin", func(t *testing.T) {
		dir := newClone(t, true)
		commit(t, dir, "x_windows.go", "package a\n")
		git(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
		d := decide(pushOf(dir))
		if d.deny || !strings.Contains(d.reason, "NOT JUDGED") {
			t.Fatalf("%+v, want an allow that says NOT JUDGED", d)
		}
	})
	t.Run("CI declares nothing to vet", func(t *testing.T) {
		dir := newClone(t, false)
		commit(t, dir, "x_windows.go", "package a\n")
		d := decide(pushOf(dir))
		if d.deny || !strings.Contains(d.reason, "declares no configuration") {
			t.Fatalf("%+v, want an allow that says the CI declares no configuration", d)
		}
	})
	t.Run("a corrupt record is not a pass", func(t *testing.T) {
		dir := newClone(t, true)
		commit(t, dir, "x_windows.go", "package a\n")
		tree := git(t, dir, "rev-parse", "HEAD^{tree}")
		rd, _ := recordDir()
		if err := os.MkdirAll(rd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rd, tree+".json"), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		if d := decide(pushOf(dir)); !d.deny {
			t.Fatalf("a corrupt record vouched: %+v", d)
		}
	})
	t.Run("not a Bash call", func(t *testing.T) {
		dir := newClone(t, true)
		commit(t, dir, "x_windows.go", "package a\n")
		p := pushOf(dir)
		p.ToolName = "Read"
		if d := decide(p); d.deny || d.reason != "" {
			t.Fatalf("%+v", d)
		}
	})
}

func TestACommitInTheSameLineIsRefusedWhereThereIsGo(t *testing.T) {
	dir := newClone(t, true)
	p := pushOf(dir)
	p.ToolInput.Command = "git commit -m x && gitpush origin b"
	d := decide(p)
	if !d.deny || !strings.Contains(d.reason, "git commit") {
		t.Fatalf("%+v, want a refusal naming git commit", d)
	}
	p.ToolInput.Command = "BUILDSCAN_SKIP=1 git commit -m x && BUILDSCAN_SKIP=1 gitpush origin b"
	if d := decide(p); d.deny || !strings.Contains(d.reason, "WAIVED") {
		t.Fatalf("a waived push after a commit: %+v", d)
	}
	// A repository with no Go is none of this gate's business.
	empty, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, empty, "init", "-q", "-b", "main")
	write(t, empty, "README", "x\n")
	git(t, empty, "add", "-A")
	git(t, empty, "commit", "-q", "-m", "base")
	git(t, empty, "update-ref", "refs/remotes/origin/main", "HEAD")
	p = pushOf(empty)
	p.ToolInput.Command = "git commit -m x && gitpush origin b"
	if d := decide(p); d.deny || d.reason != "" {
		t.Fatalf("a repository with no Go: %+v", d)
	}
}

func TestHookModeSpeaksTheProtocol(t *testing.T) {
	dir := newClone(t, true)
	commit(t, dir, "x_windows.go", "package a\n")
	in, _ := json.Marshal(pushOf(dir))
	var out, errOut bytes.Buffer
	if code := hookMode(&out, &errOut, bytes.NewReader(in)); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		H map[string]string `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%v: %q", err, out.String())
	}
	if got.H["permissionDecision"] != "deny" || got.H["hookEventName"] != "PreToolUse" {
		t.Errorf("%v", got.H)
	}

	out.Reset()
	errOut.Reset()
	if code := hookMode(&out, &errOut, strings.NewReader("{")); code != 0 || out.Len() != 0 ||
		!strings.Contains(errOut.String(), "allowed") {
		t.Errorf("an unreadable payload: exit %d out=%q err=%q, want a said allow", code, out.String(), errOut.String())
	}
}

// ---- --vet leaves the record the gate reads ---------------------------------

func vetRun(t *testing.T, dir string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	run([]string{"--dir", dir, "--vet"}, &out, &errOut)
	return errOut.String()
}

func TestVetRecordsAPassForTheHeadTree(t *testing.T) {
	dir := newClone(t, true)
	commit(t, dir, "x_windows.go", "package a\n")
	if d := decide(pushOf(dir)); !d.deny {
		t.Fatalf("not refused before the vet: %+v", d)
	}
	msg := vetRun(t, dir)
	if !strings.Contains(msg, "recorded a vet pass") {
		t.Fatalf("no record announced: %q", msg)
	}
	if d := decide(pushOf(dir)); d.deny || d.reason != "" {
		t.Fatalf("refused after a passing vet: %+v", d)
	}
}

// ⛔ `buildscan --dir . --vet` from the repository root is the way most people
// type it. The root was compared with the directory AS GIVEN, so "." was never the
// root and a passing vet was not recorded (fleettools#407).
func TestVetRecordsAPassForARelativeDirAndASymlink(t *testing.T) {
	dir := newClone(t, true)
	commit(t, dir, "x_windows.go", "package a\n")
	t.Chdir(dir)
	if msg := vetRun(t, "."); !strings.Contains(msg, "recorded a vet pass") {
		t.Fatalf("--dir . from the root: %q", msg)
	}

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if msg := vetRun(t, link); !strings.Contains(msg, "recorded a vet pass") {
		t.Fatalf("--dir through a symlink: %q", msg)
	}
}

func TestVetRecordsNothingForADirtyTree(t *testing.T) {
	dir := newClone(t, true)
	commit(t, dir, "x_windows.go", "package a\n")
	write(t, dir, "untracked.go", "package a\n")
	msg := vetRun(t, dir)
	if !strings.Contains(msg, "uncommitted") || strings.Contains(msg, "recorded a vet pass") {
		t.Fatalf("a dirty tree: %q", msg)
	}
	if d := decide(pushOf(dir)); !d.deny {
		t.Fatalf("a vet of a dirty tree vouched for HEAD: %+v", d)
	}
}

func TestVetRecordsNothingWhenItFails(t *testing.T) {
	dir := newClone(t, true)
	commit(t, dir, "bad.go", "package a\n\nimport _ \"syscall\"\n\nvar v int = \"not an int\"\n")
	msg := vetRun(t, dir)
	if strings.Contains(msg, "recorded a vet pass") {
		t.Fatalf("a failing vet was recorded: %q", msg)
	}
	if d := decide(pushOf(dir)); !d.deny {
		t.Fatalf("a failing vet vouched: %+v", d)
	}
}

// ---- what the record store does when it cannot -----------------------------

func TestTheRecordStoreFailsLoudly(t *testing.T) {
	const tree = "cccccccccccccccccccccccccccccccccccccccc"
	t.Run("no config directory", func(t *testing.T) {
		if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
			t.Skip("UserConfigDir reads other variables here")
		}
		t.Setenv(recordsEnv, "")
		t.Setenv("HOME", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		// The precondition asks the platform, NOT recordDir: a skip decided by the
		// code under test hides the guard it exists to exercise.
		if _, err := os.UserConfigDir(); err == nil {
			t.Skip("this platform finds a config directory without HOME")
		}
		if _, err := recordDir(); err == nil {
			t.Error("recordDir found a directory the platform could not")
		}
		if err := storeRecord(passRecord{Tree: tree}); err == nil {
			t.Error("storeRecord with no directory to write to succeeded")
		}
		if _, ok, err := lookupRecord(tree); ok || err == nil {
			t.Errorf("lookupRecord with no directory: ok=%v err=%v, want an error", ok, err)
		}
	})
	t.Run("the store path is a file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv(recordsEnv, filepath.Join(f, "sub"))
		if err := storeRecord(passRecord{Tree: tree}); err == nil {
			t.Error("storeRecord under a file succeeded")
		}
	})
	t.Run("the store is read-only", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("a read-only directory is writable here")
		}
		d := t.TempDir()
		if err := os.Chmod(d, 0o500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(d, 0o755)
		t.Setenv(recordsEnv, d)
		if err := storeRecord(passRecord{Tree: tree}); err == nil {
			t.Error("storeRecord into a read-only directory succeeded")
		}
	})
	t.Run("the record is a directory", func(t *testing.T) {
		d := t.TempDir()
		if err := os.Mkdir(filepath.Join(d, tree+".json"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(recordsEnv, d)
		if _, ok, err := lookupRecord(tree); ok || err == nil {
			t.Errorf("an unreadable record: ok=%v err=%v, want an error", ok, err)
		}
	})
}

func TestTheHookSaysWhenItCannotReadItsInput(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := hookMode(&out, &errOut, iotest.ErrReader(errors.New("boom"))); code != 0 || out.Len() != 0 ||
		!strings.Contains(errOut.String(), "could not read the hook payload") {
		t.Errorf("exit %d out=%q err=%q", code, out.String(), errOut.String())
	}
}

func TestACorruptRecordIsNamedInTheRefusal(t *testing.T) {
	dir := newClone(t, true)
	commit(t, dir, "x_windows.go", "package a\n")
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")
	rd, _ := recordDir()
	write(t, rd, tree+".json", "{")
	if d := decide(pushOf(dir)); !d.deny || !strings.Contains(d.reason, "cannot be read") {
		t.Fatalf("%+v, want a refusal that says the record cannot be read", d)
	}
}

func TestNoOriginIsNamedInTheNotice(t *testing.T) {
	dir := newClone(t, true)
	commit(t, dir, "x_windows.go", "package a\n")
	git(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
	if d := decide(pushOf(dir)); d.deny || !strings.Contains(d.reason, "no origin/HEAD") {
		t.Fatalf("%+v, want an allow that names the missing origin", d)
	}
}

func TestTestdataAndVendorAreNotJudged(t *testing.T) {
	for _, rel := range []string{"testdata/x_windows.go", "vendor/m/x_windows.go", "a/testdata/b/x.go"} {
		dir := newClone(t, true)
		body := "//go:build windows\n\npackage a\n"
		commit(t, dir, rel, body)
		if d := decide(pushOf(dir)); d.deny || d.reason != "" {
			t.Errorf("%s: %+v, want a silent allow", rel, d)
		}
	}
}

// ---- --vet that must NOT leave a record -------------------------------------

func TestVetRecordsNothingWhenTheCIDeclaresNothing(t *testing.T) {
	dir := newClone(t, false)
	msg := vetRun(t, dir)
	if !strings.Contains(msg, "declares no configuration") || strings.Contains(msg, "recorded a vet pass") {
		t.Fatalf("%q", msg)
	}
}

func TestVetRecordsNothingWithoutAModule(t *testing.T) {
	dir := newClone(t, true)
	git(t, dir, "rm", "-q", "go.mod")
	git(t, dir, "commit", "-q", "-m", "no module")
	msg := vetRun(t, dir)
	if !strings.Contains(msg, "no module") || strings.Contains(msg, "recorded a vet pass") {
		t.Fatalf("%q", msg)
	}
}

func TestVetRecordsNothingForASubdirectoryOfARepository(t *testing.T) {
	dir := newClone(t, false)
	sub := filepath.Join(dir, "sub")
	write(t, sub, "go.mod", "module example.test/sub\n\ngo 1.27\n")
	write(t, sub, "b.go", "package b\n")
	write(t, sub, ".github/workflows/ci.yml", ciYML)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "sub")
	msg := vetRun(t, sub)
	if !strings.Contains(msg, "not the repository root") || strings.Contains(msg, "recorded a vet pass") {
		t.Fatalf("%q", msg)
	}
}

func TestVetOutsideARepositoryIsSilentAboutRecords(t *testing.T) {
	t.Setenv(recordsEnv, t.TempDir())
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")
	write(t, dir, ".github/workflows/ci.yml", ciYML)
	if msg := vetRun(t, dir); strings.Contains(msg, "no record") || strings.Contains(msg, "recorded") {
		t.Fatalf("a directory with no git has no HEAD to key a pass by, and nothing to say: %q", msg)
	}
}

func TestVetRecordsNothingBeforeTheFirstCommit(t *testing.T) {
	t.Setenv(recordsEnv, t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, ".git/info/exclude", "*\n") // clean, yet there is no HEAD
	write(t, dir, "go.mod", "module example.test\n\ngo 1.27\n")
	write(t, dir, "a.go", "package a\n")
	write(t, dir, ".github/workflows/ci.yml", ciYML)
	msg := vetRun(t, dir)
	if !strings.Contains(msg, "no record") || strings.Contains(msg, "recorded a vet pass") {
		t.Fatalf("%q", msg)
	}
}

func TestVetSaysWhenItCannotStoreTheRecord(t *testing.T) {
	dir := newClone(t, true)
	f := filepath.Join(t.TempDir(), "file")
	write(t, filepath.Dir(f), "file", "")
	t.Setenv(recordsEnv, filepath.Join(f, "sub"))
	msg := vetRun(t, dir)
	if !strings.Contains(msg, "no record") || strings.Contains(msg, "recorded a vet pass") {
		t.Fatalf("%q", msg)
	}
}

// --hook reaches the gate from the command line, not only from the function.
func TestTheHookFlagAnswersThroughTheCommand(t *testing.T) {
	dir := newClone(t, true)
	commit(t, dir, "x_windows.go", "package a\n")
	in, _ := json.Marshal(pushOf(dir))
	var out, errOut bytes.Buffer
	c := RootCmd()
	c.SetIn(bytes.NewReader(in))
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetArgs([]string{"--hook"})
	if err := c.Execute(); err != nil {
		t.Fatalf("%v (stderr %q)", err, errOut.String())
	}
	if !strings.Contains(out.String(), `"permissionDecision":"deny"`) {
		t.Fatalf("--hook did not deny: out=%q err=%q", out.String(), errOut.String())
	}
}

// ⛔ fleettools#408: a package the CI vets with a check turned off was vetted
// here with every check on, so a green CI read as a vet failure and the only way
// to push was BUILDSCAN_SKIP.
const seekGo = "package seeker\n\ntype F struct{}\n\n// Seek has the shape a third-party interface mandates; stdmethods objects.\nfunc (F) Seek(off int64, whence int) (int64, int) { return 0, 0 }\n"

func ciWithVetLine(vetLine string) string {
	return "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n          go vet $(go list ./... | grep -v '/seeker$')\n" + vetLine
}

func TestVetHonoursTheFlagsTheCIPassesForAPackage(t *testing.T) {
	dir := newClone(t, false)
	write(t, dir, ".github/workflows/ci.yml", ciWithVetLine("          go vet -stdmethods=false ./seeker\n"))
	write(t, dir, "seeker/seeker.go", seekGo)
	commit(t, dir, "x_windows.go", "package a\n")
	msg := vetRun(t, dir)
	if !strings.Contains(msg, "recorded a vet pass") {
		t.Fatalf("the CI's own vet flags were not honoured: %q", msg)
	}
}

func TestVetStillFailsWithoutTheCIExemption(t *testing.T) {
	dir := newClone(t, false)
	write(t, dir, ".github/workflows/ci.yml", ciWithVetLine("          go test ./...\n"))
	write(t, dir, "seeker/seeker.go", seekGo)
	commit(t, dir, "x_windows.go", "package a\n")
	msg := vetRun(t, dir)
	if strings.Contains(msg, "recorded a vet pass") {
		t.Fatalf("a package the CI does not exempt was vetted with a check off: %q", msg)
	}
}
