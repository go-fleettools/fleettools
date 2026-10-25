package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

const commit = "dfef8eb8272d707a3f6614fd1dca882690e00430"

func sumOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestParseSumsReadsSha256sumsFormat(t *testing.T) {
	a, b := sumOf([]byte("a")), sumOf([]byte("b"))
	got, err := parseSums([]byte(a + "  tool-linux-amd64\n" + strings.ToUpper(b) + " *tool-windows-amd64.exe\r\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got["tool-linux-amd64"] != a || got["tool-windows-amd64.exe"] != b || len(got) != 2 {
		t.Errorf("%v", got)
	}
}

func TestParseSumsRefusesWhatItCannotTrust(t *testing.T) {
	a := sumOf([]byte("a"))
	for name, in := range map[string]string{
		"a path":           a + "  ../../etc/passwd\n",
		"a subdirectory":   a + "  bin/tool\n",
		"a backslash":      a + `  ..\tool` + "\n",
		"short sum":        a[:60] + "  tool\n",
		"not hex":          strings.Repeat("z", 64) + "  tool\n",
		"one space":        a + " tool\n",
		"listed twice":     a + "  tool\n" + a + "  tool\n",
		"nothing":          "\n",
		"a dot-dot itself": a + "  ..\n",
	} {
		if _, err := parseSums([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCheckBuildWantsTheTagsCommitAndACleanTree(t *testing.T) {
	good := []debug.BuildSetting{{Key: "vcs.revision", Value: commit}, {Key: "vcs.modified", Value: "false"}}
	if p := checkBuild("v1.2.3", good, "v1.2.3", commit); p != "" {
		t.Errorf("a correct binary: %s", p)
	}
	for name, c := range map[string]struct {
		version  string
		settings []debug.BuildSetting
		want     string
	}{
		"another version": {"v1.2.2", good, "module version v1.2.2"},
		"a devel build":   {"(devel)", good, "module version (devel)"},
		"another commit":  {"v1.2.3", []debug.BuildSetting{{Key: "vcs.revision", Value: "0000"}, {Key: "vcs.modified", Value: "false"}}, "vcs.revision 0000"},
		"a modified tree": {"v1.2.3", []debug.BuildSetting{{Key: "vcs.revision", Value: commit}, {Key: "vcs.modified", Value: "true"}}, "vcs.modified=true"},
		// go install from the proxy records no vcs.* at all: not the release
		// workflow's build, so not what a release publishes.
		"no vcs stamp": {"v1.2.3", nil, "no vcs.revision"},
	} {
		if p := checkBuild(c.version, c.settings, "v1.2.3", commit); !strings.Contains(p, c.want) {
			t.Errorf("%s: %q, want it to say %q", name, p, c.want)
		}
	}
}

func TestTagRunsKeepsOnlyThisTagAtThisCommit(t *testing.T) {
	runs := tagRuns([]run{
		{Name: "Release", HeadBranch: "v1.2.3", HeadSHA: commit},
		{Name: "Release", HeadBranch: "v1.2.3", HeadSHA: "old"},
		{Name: "ci", HeadBranch: "main", HeadSHA: commit},
	}, "v1.2.3", commit)
	if len(runs) != 1 || runs[0].HeadSHA != commit {
		t.Errorf("%v", runs)
	}
}

// fakeClock makes waitForRuns' minutes pass in a test.
func fakeClock(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	cur := t0
	now = func() time.Time { return cur }
	sleep = func(d time.Duration) { cur = cur.Add(d) }
	t.Cleanup(func() { now, sleep = time.Now, time.Sleep })
}

func fakeGH(t *testing.T, answer func(args []string) ([]byte, error)) *[]string {
	var calls []string
	gh = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return answer(args)
	}
	t.Cleanup(func() { gh = defaultGH })
	return &calls
}

var defaultGH = gh

// The defect this command exists for: a tag nothing builds was waited on
// forever. It must be an answer, and a prompt one.
func TestATagNothingBuildsIsAnAnswer(t *testing.T) {
	fakeClock(t)
	calls := fakeGH(t, func(args []string) ([]byte, error) {
		if args[0] == "release" {
			return nil, errors.New("release not found")
		}
		return []byte(`{"workflow_runs":[]}`), nil
	})
	_, err := waitForRuns(&bytes.Buffer{}, "o/r", "v1.2.3", commit, time.Hour)
	if !errors.Is(err, errUnverifiable) || !strings.Contains(err.Error(), "nothing builds this tag") {
		t.Fatalf("%v", err)
	}
	if len(*calls) > 20 {
		t.Errorf("%d calls before saying so", len(*calls))
	}
}

// A release made without a workflow run is still checked, not refused: its
// missing attestations are what will say so.
func TestAReleaseWithoutARunIsStillChecked(t *testing.T) {
	fakeClock(t)
	fakeGH(t, func(args []string) ([]byte, error) {
		if args[0] == "release" {
			return []byte(`{"tagName":"v1.2.3"}`), nil
		}
		return []byte(`{"workflow_runs":[]}`), nil
	})
	var out bytes.Buffer
	runs, err := waitForRuns(&out, "o/r", "v1.2.3", commit, time.Hour)
	if err != nil || len(runs) != 0 || !strings.Contains(out.String(), "as published") {
		t.Fatalf("runs %v err %v: %s", runs, err, out.String())
	}
}

func TestItWaitsForRunsInProgress(t *testing.T) {
	fakeClock(t)
	n := 0
	fakeGH(t, func([]string) ([]byte, error) {
		n++
		status := "in_progress"
		if n >= 4 {
			status = "completed"
		}
		return fmt.Appendf(nil, `{"workflow_runs":[{"name":"Release","status":%q,"conclusion":"success","head_branch":"v1.2.3","head_sha":%q}]}`, status, commit), nil
	})
	runs, err := waitForRuns(&bytes.Buffer{}, "o/r", "v1.2.3", commit, time.Hour)
	if err != nil || len(runs) != 1 || n != 4 {
		t.Fatalf("runs %v err %v after %d polls", runs, err, n)
	}
}

func TestItGivesUpAfterWait(t *testing.T) {
	fakeClock(t)
	fakeGH(t, func([]string) ([]byte, error) {
		return fmt.Appendf(nil, `{"workflow_runs":[{"name":"Release","status":"queued","head_branch":"v1.2.3","head_sha":%q}]}`, commit), nil
	})
	if _, err := waitForRuns(&bytes.Buffer{}, "o/r", "v1.2.3", commit, 10*time.Minute); !errors.Is(err, errUnverifiable) {
		t.Fatalf("%v", err)
	}
}

func TestAnAnnotatedTagIsFollowedToItsCommit(t *testing.T) {
	fakeGH(t, func(args []string) ([]byte, error) {
		switch args[1] {
		case "repos/o/r/git/ref/tags/v1.2.3":
			return []byte(`{"object":{"type":"tag","sha":"t1"}}`), nil
		case "repos/o/r/git/tags/t1":
			return fmt.Appendf(nil, `{"object":{"type":"commit","sha":%q}}`, commit), nil
		}
		return nil, errors.New("unexpected " + strings.Join(args, " "))
	})
	if c, err := tagCommit("o/r", "v1.2.3"); err != nil || c != commit {
		t.Fatalf("%s %v", c, err)
	}
}

// checkFiles over a real directory: one good file, one whose sum is wrong, one
// published but not listed, one listed but not published, one the attestation
// refuses.
func TestCheckFilesHoldsEveryFileToEveryCheck(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("good.deb", "good")
	write("tampered.deb", "tampered")
	write("unlisted.deb", "unlisted")
	write("unattested.deb", "unattested")
	write("SHA256SUMS", sumOf([]byte("good"))+"  good.deb\n"+
		sumOf([]byte("original"))+"  tampered.deb\n"+
		sumOf([]byte("unattested"))+"  unattested.deb\n"+
		sumOf([]byte("lost"))+"  lost.deb\n")
	fakeGH(t, func(args []string) ([]byte, error) {
		if args[0] == "attestation" && strings.HasSuffix(args[2], "unattested.deb") {
			return nil, errors.New("Error: verifying with issuer \"sigstore.dev\"\nmore")
		}
		return nil, nil
	})
	var out bytes.Buffer
	files, err := checkFiles(&out, "o/r", "v1.2.3", commit, dir, skipScan)
	if err != nil {
		t.Fatal(err)
	}
	verdict := map[string]bool{}
	for _, f := range files {
		verdict[f.name] = f.ok()
	}
	want := map[string]bool{"good.deb": true, "tampered.deb": false, "unlisted.deb": false, "unattested.deb": false, "lost.deb": false}
	for name, ok := range want {
		if v, seen := verdict[name]; !seen || v != ok {
			t.Errorf("%s: ok=%v seen=%v, want ok=%v", name, v, seen, ok)
		}
	}
	for _, s := range []string{"not listed in SHA256SUMS", "SHA256SUMS says", "not published", "sigstore.dev"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("the report does not say %q:\n%s", s, out.String())
		}
	}
}

func TestAWorkTreeIsFound(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := insideWorkTree(filepath.Join(root, "a", "b")); got != root {
		t.Errorf("inside: %q", got)
	}
	if got := insideWorkTree(t.TempDir()); got != "" {
		t.Errorf("outside: %q", got)
	}
}

// A real Go binary: built here, its build information read back the way a
// release's is. A test build carries no module version and, outside a
// checkout, no vcs stamp, so it must be refused -- the point is that the
// reading works on a real file, not only on a struct.
func TestABinaryBuiltFromNoTagIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	write := filepath.Join(dir, "tool-linux-amd64")
	if err := os.WriteFile(write, b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sumOf(b)+"  tool-linux-amd64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeGH(t, func([]string) ([]byte, error) { return nil, nil })
	files, err := checkFiles(&bytes.Buffer{}, "o/r", "v1.2.3", commit, dir, skipScan)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !files[0].goBinary || files[0].ok() || !strings.Contains(files[0].build, "module version") {
		t.Errorf("%+v", files)
	}
}

// ⛔ THE `v` BELONGS TO THE TAG, NOT TO THE VERSION — and this tool used to
// insist on it. Measured 2026-10-20: go-pkgx/pkgx v0.23.2 verified on every
// file for sum, attestation and build info, and was then reported FAILED on
// `pkgs --version: "pkgs 0.23.2"`. That output is correct by the rule its own
// release workflow enforces (`${TAG#v}`); go-pkgx/bk prints the `v`. A
// verifier that insists on one spelling is borrowing a convention rather than
// checking a fact, and a tool that cries wolf about a correct release is one
// nobody runs.
//
// ⛔ AND THE CONTROLS ARE THE OTHER HALF. The check it replaces was
// `strings.Contains(line, tag)`, which is loose in the opposite direction:
// "0.23.25" contains "0.23.2". Accepting the bare spelling without matching a
// WORD would trade a tool that cries wolf for one that nods along, so the
// near-misses below are the point of this test.
func TestAVersionLineMayOrMayNotCarryTheV(t *testing.T) {
	const tag = "v0.23.2"
	for _, c := range []struct {
		line string
		want bool
	}{
		{"pkgs 0.23.2", true},                        // go-pkgx/pkgx, which stamps ${TAG#v}
		{"bk v0.23.2", true},                         // go-pkgx/bk, which stamps the tag
		{"fileshare version 0.23.2 (a1b2c3d)", true}, // a commit in parentheses
		{"tool 0.23.2, built 2026-10-20", true},      // trailing comma
		{"pkgs 0.23.25", false},                      // ⛔ the old Contains accepted this
		{"pkgs v0.23.25", false},
		{"pkgs 10.23.2", false},
		{"pkgs 0.23.2-rc1", false}, // a prerelease is a different version
		{"pkgs dev", false},        // the unstamped build this fleet has shipped twice
		{"", false},
	} {
		if got := saysVersion(c.line, tag); got != c.want {
			t.Errorf("saysVersion(%q, %q) = %v, want %v", c.line, tag, got, c.want)
		}
	}
	// An empty tag matches nothing, or every line would verify.
	if saysVersion("pkgs 0.23.2", "v") {
		t.Error("an empty tag matched a version line")
	}
}
