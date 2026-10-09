package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run1(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=A U Thor", "GIT_AUTHOR_EMAIL=a@example.org", "GIT_AUTHOR_DATE=1700000000 +0200",
		"GIT_COMMITTER_NAME=C O Mitter", "GIT_COMMITTER_EMAIL=c@example.org", "GIT_COMMITTER_DATE=1700000100 +0200",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name string, b []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture is the bridge case in small: a binary committed with the first
// release, kept through a second, deleted before a third. A commit message
// holds lines that read like stream commands, and a LATER file of the same
// name shows why the strip goes by ID and not by path.
type fixture struct {
	mirror, bin, laterBin string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	run1(t, ".", "init", "--quiet", "-b", "main", work)
	write(t, work, "a.go", []byte("package a\n"))
	run1(t, work, "add", ".")
	run1(t, work, "commit", "--quiet", "-m", "start")
	big := make([]byte, 64<<10)
	rand.Read(big)
	write(t, work, "bridge", big)
	run1(t, work, "add", ".")
	run1(t, work, "commit", "--quiet", "-m", "oops: the binary came along")
	run1(t, work, "tag", "v0.1.0")
	write(t, work, "a.go", []byte("package a\n\nvar X = 1\n"))
	run1(t, work, "add", ".")
	run1(t, work, "commit", "--quiet", "-m", "a message that\nM 100644 :1 a.go\ndata 3\nlooks like a stream\n")
	run1(t, work, "tag", "-a", "-m", "annotated", "v0.2.0")
	run1(t, work, "rm", "--quiet", "bridge")
	run1(t, work, "commit", "--quiet", "-m", "delete the binary")
	run1(t, work, "tag", "v0.3.0")
	bin := run1(t, work, "rev-parse", "v0.1.0:bridge")
	// The same path again, later, holding something that belongs there.
	write(t, work, "bridge", []byte("#!/bin/sh\necho a legitimate script\n"))
	run1(t, work, "add", ".")
	run1(t, work, "commit", "--quiet", "-m", "a script named bridge")
	later := run1(t, work, "rev-parse", "HEAD:bridge")
	run1(t, work, "switch", "--quiet", "-c", "side", "v0.2.0")
	write(t, work, "b.go", []byte("package a\n"))
	run1(t, work, "add", ".")
	run1(t, work, "commit", "--quiet", "-m", "side work")
	run1(t, work, "switch", "--quiet", "main")
	run1(t, work, "merge", "--quiet", "--no-ff", "-m", "merge side", "side")
	mirror := filepath.Join(t.TempDir(), "mirror.git")
	run1(t, ".", "clone", "--quiet", "--mirror", work, mirror)
	return fixture{mirror: mirror, bin: bin, laterBin: later}
}

func TestScanNamesTheBinaryAndTheTagsItTouches(t *testing.T) {
	fx := newFixture(t)
	var out, errb bytes.Buffer
	if c := run([]string{"scan", "-min", "32KiB", fx.mirror}, &out, &errb); c != 0 {
		t.Fatalf("exit %d: %s", c, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, fx.bin) || !strings.Contains(s, "in the trees of 2 tags: v0.1.0 .. v0.2.0") {
		t.Errorf("scan said:\n%s", s)
	}
	if strings.Contains(s, fx.laterBin) {
		t.Errorf("reported a blob HEAD holds:\n%s", s)
	}
	if !strings.Contains(s, "1 of at least 32768 bytes") {
		t.Errorf("summary:\n%s", s)
	}
}

func TestStripRewritesAndProvesIt(t *testing.T) {
	fx := newFixture(t)
	before := run1(t, fx.mirror, "rev-parse", "v0.3.0^{tree}", "main^{tree}")
	dst := filepath.Join(t.TempDir(), "new.git")
	var out, errb bytes.Buffer
	if c := run([]string{"strip", "-blob", fx.bin[:12], "-to", dst, fx.mirror}, &out, &errb); c != 0 {
		t.Fatalf("exit %d: %s\n%s", c, errb.String(), out.String())
	}
	t.Log(out.String())
	if exec.Command("git", "-C", dst, "cat-file", "-e", fx.bin).Run() == nil {
		t.Fatal("the binary is still there")
	}
	// The trees made after the deletion are byte-identical, so the module
	// hashes of those releases do not move.
	if after := run1(t, dst, "rev-parse", "v0.3.0^{tree}", "main^{tree}"); after != before {
		t.Errorf("trees moved:\n%s\n%s", before, after)
	}
	// Stripping by ID left the later file of the same name alone.
	if got := run1(t, dst, "rev-parse", "main:bridge"); got != fx.laterBin {
		t.Errorf("main:bridge = %s, want %s", got, fx.laterBin)
	}
	if got := run1(t, dst, "log", "-1", "--format=%B", "v0.2.0~0^{commit}"); !strings.Contains(got, "M 100644 :1 a.go\ndata 3") {
		t.Errorf("message mangled: %q", got)
	}
	if run1(t, dst, "cat-file", "-t", "v0.2.0") != "tag" {
		t.Error("the annotated tag was not kept annotated")
	}
	if run1(t, dst, "symbolic-ref", "HEAD") != "refs/heads/main" {
		t.Error("HEAD does not name main")
	}
	for _, want := range []string{"2 trees unchanged; 3 changed", "side: -bridge", "v0.1.0: -bridge", "v0.2.0: -bridge", "nothing was pushed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
	if fsck := exec.Command("git", "-C", dst, "fsck", "--strict", "--no-dangling"); fsck.Run() != nil {
		t.Error("fsck fails on the new repository")
	}
}

// The verification is the point, so it must catch what it claims to: these
// are rewrites that are wrong in the ways that matter.
func TestVerifyCatchesAWrongRewrite(t *testing.T) {
	fx := newFixture(t)
	strip := map[string]bool{fx.bin: true}
	dst := filepath.Join(t.TempDir(), "new.git")
	if _, err := rewrite(fx.mirror, dst, strip); err != nil {
		t.Fatal(err)
	}
	if _, err := verify(fx.mirror, dst, strip); err != nil {
		t.Fatalf("a correct rewrite fails: %v", err)
	}
	// Claiming nothing was stripped: v0.1.0's tree lost a file unexplained.
	if _, err := verify(fx.mirror, dst, map[string]bool{}); err == nil || !strings.Contains(err.Error(), "not the old one minus") {
		t.Errorf("an unexplained missing file passed: %v", err)
	}
	// The blob is still in the "new" repository.
	if _, err := verify(fx.mirror, fx.mirror, strip); err == nil {
		t.Error("a repository still holding the blob passed")
	}
	// A message changed.
	bad := filepath.Join(t.TempDir(), "bad")
	run1(t, ".", "clone", "--quiet", "--mirror", dst, bad)
	work := filepath.Join(t.TempDir(), "w")
	run1(t, ".", "clone", "--quiet", bad, work)
	run1(t, work, "commit", "--quiet", "--amend", "-m", "merge side, reworded")
	run1(t, work, "push", "--quiet", "--force", "origin", "main")
	if _, err := verify(fx.mirror, bad, strip); err == nil || !strings.Contains(err.Error(), "what a person wrote") {
		t.Errorf("a reworded message passed: %v", err)
	}
	// A ref went missing.
	run1(t, bad, "tag", "-d", "v0.3.0")
	if _, err := verify(fx.mirror, bad, strip); err == nil || !strings.Contains(err.Error(), "refs differ") {
		t.Errorf("a missing tag passed: %v", err)
	}
}

func TestStripRefuses(t *testing.T) {
	fx := newFixture(t)
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
		say  string
	}{
		{"a blob HEAD holds", []string{"strip", "-blob", fx.laterBin, "-to", filepath.Join(dir, "a"), fx.mirror}, "still in HEAD's tree"},
		{"not a blob", []string{"strip", "-blob", "0123456789abcdef0123456789abcdef01234567", "-to", filepath.Join(dir, "b"), fx.mirror}, "is not a blob"},
		{"an existing target", []string{"strip", "-blob", fx.bin, "-to", dir, fx.mirror}, "already exists"},
		{"no blob", []string{"strip", "-to", filepath.Join(dir, "c"), fx.mirror}, "usage"},
	} {
		var out, errb bytes.Buffer
		if c := run(tc.args, &out, &errb); c != 2 || !strings.Contains(errb.String(), tc.say) {
			t.Errorf("%s: exit %d, said %q", tc.name, c, errb.String())
		}
	}
}

// A scan that read nothing says so rather than reporting a clean zero.
func TestScanOfNothingIsNotClean(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "e.git")
	run1(t, ".", "init", "--quiet", "--bare", empty)
	var out, errb bytes.Buffer
	if c := run([]string{"scan", empty}, &out, &errb); c == 0 {
		t.Errorf("exit 0 on an empty repository: %s", out.String())
	}
}

func TestFilterCopiesDataWithoutReadingIt(t *testing.T) {
	in := "blob\nmark :1\noriginal-oid aaaa\ndata 4\nGONE\n" +
		"blob\nmark :2\noriginal-oid bbbb\ndata 4\nKEPT\n" +
		"commit refs/heads/main\nmark :3\ndata 23\nM 100644 :1 x\nblob\ndata\nM 100644 :1 x\nM 100644 :2 y\n\ndone\n"
	f := newFilter(map[string]bool{"aaaa": true})
	var out bytes.Buffer
	if err := f.run(bufio.NewReader(strings.NewReader(in)), &out); err != nil {
		t.Fatal(err)
	}
	want := "blob\nmark :2\noriginal-oid bbbb\ndata 4\nKEPT\n" +
		"commit refs/heads/main\nmark :3\ndata 23\nM 100644 :1 x\nblob\ndata\nM 100644 :2 y\n\ndone\n"
	if out.String() != want {
		t.Errorf("got\n%q\nwant\n%q", out.String(), want)
	}
	if f.stats.blobs != 1 || f.stats.entries != 1 || !f.found["aaaa"] {
		t.Errorf("stats %+v found %v", f.stats, f.found)
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "1234": 1234, "512KiB": 512 << 10, "16M": 16 << 20, "1GiB": 1 << 30} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("%s: %d %v", in, got, err)
		}
	}
	for _, in := range []string{"", "x", "-1", "1TiB"} {
		if _, err := parseSize(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}
