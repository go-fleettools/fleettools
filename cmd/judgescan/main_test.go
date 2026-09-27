package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repo writes a throwaway checkout: one _test.go and zero or more workflows.
func repo(t *testing.T, root, name, testGo string, workflows map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(testGo), 0o644); err != nil {
		t.Fatal(err)
	}
	if workflows == nil {
		return
	}
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	for n, body := range workflows {
		if err := os.WriteFile(filepath.Join(wf, n), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestPackageNameCountsAsInstalled pins the false positive that made the first
// two versions of this scan useless. A workflow installs `poppler-utils`; the
// test looks for `pdftoppm`. Matching only the binary name calls every poppler
// judge in the fleet missing -- and a scan whose every answer is "missing" is
// indistinguishable from one that read nothing.
func TestPackageNameCountsAsInstalled(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/ops",
		`package p
func f() { exec.LookPath("pdftoppm") }`,
		map[string]string{"ci.yml": "steps:\n  - run: sudo apt-get install -y poppler-utils\n"})

	f, ok := scan(root, "o/ops")
	if !ok {
		t.Fatal("expected the repository to name a tool")
	}
	if len(f.missing) != 0 {
		t.Errorf("pdftoppm reported missing though poppler-utils is installed: %v", f.missing)
	}
	if f.workflows != 1 {
		t.Errorf("read %d workflow files, want 1", f.workflows)
	}
}

// TestMissingToolIsReported is the other direction: the judge really is absent.
func TestMissingToolIsReported(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/luks",
		`package p
func f() { exec.LookPath("cryptsetup") }`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	f, _ := scan(root, "o/luks")
	if len(f.missing) != 1 || f.missing[0] != "cryptsetup" {
		t.Errorf("missing = %v, want [cryptsetup]", f.missing)
	}
}

// TestNoWorkflowsIsNotTwentyMissingTools separates "no CI" from "CI without
// the tool". They need different fixes -- one is a missing lane, the other a
// missing step -- and the shell version conflated them by letting a failed
// glob produce an empty haystack.
func TestNoWorkflowsIsNotTwentyMissingTools(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/bare",
		`package p
func f() { exec.LookPath("swtpm") }`, nil)

	f, _ := scan(root, "o/bare")
	if f.noCI == "" {
		t.Error("a repository with no workflows must be reported as having no CI")
	}
	if f.bytesRead != 0 || f.workflows != 0 {
		t.Errorf("claimed to have read %d bytes in %d files", f.bytesRead, f.workflows)
	}
}

// TestUbiquitousToolsAreNotJudges keeps the output readable: `sh` witnesses
// nothing, and `go` is the subject, not the judge.
func TestUbiquitousToolsAreNotJudges(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/noise",
		`package p
func f() { exec.LookPath("sh"); exec.LookPath("go"); exec.LookPath("qpdf") }`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	f, _ := scan(root, "o/noise")
	if len(f.missing) != 1 || f.missing[0] != "qpdf" {
		t.Errorf("missing = %v, want [qpdf] only", f.missing)
	}
}

// TestDynamicLookPathIsFlaggedNotGuessed: a LookPath over a variable names its
// tool somewhere this scan does not read. Inventing a name would be worse than
// admitting the gap, so the finding carries a flag instead.
func TestDynamicLookPathIsFlaggedNotGuessed(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/dyn",
		`package p
func f(bin string) { exec.LookPath(bin); exec.LookPath("zdb") }`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	f, _ := scan(root, "o/dyn")
	if !f.dynamic {
		t.Error("a non-literal LookPath must be flagged")
	}
	if len(f.missing) != 1 || f.missing[0] != "zdb" {
		t.Errorf("missing = %v, want the literal one only", f.missing)
	}
}

// TestTestdataIsNotScanned: a fixture tree can hold Go sources that are data,
// not tests of this repository.
func TestTestdataIsNotScanned(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/fix",
		`package p
func f() {}`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})
	td := filepath.Join(root, "o/fix", "testdata")
	if err := os.MkdirAll(td, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(td, "y_test.go"), []byte(`exec.LookPath("imaginary")`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, ok := scan(root, "o/fix"); ok {
		t.Error("a tool named only under testdata/ must not count")
	}
}

// TestFindReposWantsAGitDir keeps stray directories out of a fleet walk.
func TestFindReposWantsAGitDir(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/real", "package p", nil)
	if err := os.MkdirAll(filepath.Join(root, "o", "notarepo"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findRepos(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != filepath.Join("o", "real") {
		t.Errorf("findRepos = %v, want [o/real]", got)
	}
}

// TestMacOSRunnerCountsAsInstalling: hdiutil is not installed by anybody, it
// arrives with the runner. Naming a macOS runner is how a workflow "installs"
// it, and a scan that does not know this reports every macOS judge as absent.
func TestMacOSRunnerCountsAsInstalling(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/dmg",
		`package p
func f() { exec.LookPath("hdiutil") }`,
		map[string]string{"ci.yml": "jobs:\n  mac:\n    runs-on: macos-15\n"})

	f, _ := scan(root, "o/dmg")
	if len(f.missing) != 0 {
		t.Errorf("hdiutil reported missing on a macOS runner: %v", f.missing)
	}
}

// TestAliasTableNamesRealBinaries guards the table itself: a typo in a key is
// invisible, because an unknown key just means "no alias" and the tool reads
// as missing. Every key must be a plausible binary, never a package name.
func TestAliasTableNamesRealBinaries(t *testing.T) {
	for bin, pkgs := range packageOf {
		if strings.ContainsAny(bin, " \t") {
			t.Errorf("key %q is not a binary name", bin)
		}
		if len(pkgs) == 0 {
			t.Errorf("key %q maps to nothing, which is the same as being absent", bin)
		}
		for _, p := range pkgs {
			if p == "" {
				t.Errorf("key %q has an empty alias, which matches every workflow", bin)
			}
		}
	}
}

// TestCheckoutPathIsNotAnInstall pins the false NEGATIVE that would have
// erased the finding this tool was written for. go-filesystems/btrfs checks
// itself out with `path: btrfs`; a whole-file search finds "btrfs", concludes
// btrfs-progs is installed, and drops the one repository that provoked the
// scan. A missing finding leaves nothing to notice.
func TestCheckoutPathIsNotAnInstall(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/btrfs",
		`package p
func f() { exec.LookPath("btrfs") }`,
		map[string]string{"ci.yml": `jobs:
  native:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          path: btrfs
      - name: Test
        working-directory: btrfs
        run: go test ./...
`})

	f, _ := scan(root, "o/btrfs")
	if len(f.missing) != 1 || f.missing[0] != "btrfs" {
		t.Errorf("missing = %v, want [btrfs]: a checkout path is not an install", f.missing)
	}
}

// TestRunStepStillCounts is the positive control for the line filter: once
// the step really installs it, the finding must disappear.
func TestRunStepStillCounts(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/btrfs2",
		`package p
func f() { exec.LookPath("btrfs") }`,
		map[string]string{"ci.yml": `jobs:
  native:
    steps:
      - name: btrfs-progs, as a foreign reader
        run: sudo apt-get install -y -qq btrfs-progs
`})

	f, _ := scan(root, "o/btrfs2")
	if len(f.missing) != 0 {
		t.Errorf("missing = %v, want none: the run step installs it", f.missing)
	}
}

// TestDiscardedLookPathErrorIsNotAGate pins a false positive found by reading
// what the scan reported. go-filesystems/ext4 and /xfs do
//
//	mockPath, _ := exec.LookPath("mock")
//
// and fall back to ./bin/mock in the repository. The error goes nowhere, so
// the absence of a system `mock` gates nothing and judges nothing. Reporting
// it as a missing judge is noise that makes the real findings harder to see.
func TestDiscardedLookPathErrorIsNotAGate(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/fetch",
		`package p
func f() {
	mockPath, _ := exec.LookPath("mock")
	_ = mockPath
}`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	if f, ok := scan(root, "o/fetch"); ok {
		t.Errorf("a discarded LookPath error was treated as a gate: %v", f.missing)
	}
}

// TestHandledLookPathErrorIsStillAGate is the positive control: the same tool,
// with its error actually used, must still be reported.
func TestHandledLookPathErrorIsStillAGate(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/gated",
		`package p
func f(t *testing.T) {
	p, err := exec.LookPath("mock")
	if err != nil {
		t.Skip("no mock")
	}
	_ = p
}`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	f, ok := scan(root, "o/gated")
	if !ok || len(f.missing) != 1 || f.missing[0] != "mock" {
		t.Errorf("missing = %v (ok=%v), want [mock]", f.missing, ok)
	}
}

// TestMacOSRunnerInAMatrixCountsToo pins the second false positive found by
// checking a finding before acting on it. go-macos/appbundle picks its runner
// from a matrix:
//
//	runs-on: ${{ matrix.os }}
//	...
//	os: [ubuntu-latest, macos-latest, windows-latest]
//
// The `runs-on:` line names no image, and the matrix line is a plain mapping
// that the install-line filter drops -- so codesign read as never installed on
// a repository that has been running its codesign judge all along.
func TestMacOSRunnerInAMatrixCountsToo(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/appbundle",
		`package p
func f(t *testing.T) {
	if _, err := exec.LookPath("codesign"); err != nil {
		t.Skip("no codesign")
	}
}`,
		map[string]string{"ci.yml": `jobs:
  test:
    runs-on: ${{ matrix.os }}
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    steps:
      - run: go test ./...
`})

	f, _ := scan(root, "o/appbundle")
	if len(f.missing) != 0 {
		t.Errorf("codesign reported missing though the matrix names macos-latest: %v", f.missing)
	}
}

// TestLinuxOnlyMatrixStillReportsAMacTool is the control: the same shape with
// no macOS leg must still be reported, or the fix above would silence every
// macOS finding instead of the wrong one.
func TestLinuxOnlyMatrixStillReportsAMacTool(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/dmg",
		`package p
func f(t *testing.T) {
	if _, err := exec.LookPath("hdiutil"); err != nil {
		t.Skip("no hdiutil")
	}
}`,
		map[string]string{"ci.yml": `jobs:
  test:
    runs-on: ${{ matrix.runner }}
    strategy:
      matrix:
        include:
          - runner: ubuntu-latest
    steps:
      - run: go test ./...
`})

	f, _ := scan(root, "o/dmg")
	if len(f.missing) != 1 || f.missing[0] != "hdiutil" {
		t.Errorf("missing = %v, want [hdiutil]: this matrix has no macOS leg", f.missing)
	}
}

// TestWorktreesAreNotSeparateRepositories pins a counting error: a git
// worktree's .git is a FILE holding `gitdir: ...`, not a directory. Counting
// it makes one repository look like several and prints each of its findings
// once per worktree -- openweft/weft-loom-server appeared three times, which
// reads as three repositories needing the same fix.
func TestWorktreesAreNotSeparateRepositories(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/real", "package p", nil)

	wt := filepath.Join(root, "o", "real.bump")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+filepath.Join(root, "o", "real", ".git", "worktrees", "real.bump")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findRepos(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != filepath.Join("o", "real") {
		t.Errorf("findRepos = %v, want [o/real]: a worktree is the same repository", got)
	}
}

// TestFallbackLookupIsNotAGate pins a sixth false-positive class. In
// go-tex/go-tex.github.io the lookup is
//
//	if p, err := exec.LookPath("git-http-backend"); err == nil {
//		return p
//	}
//	// otherwise ask `git --exec-path`
//
// The tool being absent changes which path is taken, not whether anything is
// judged. Same family as a discarded error: what matters is never that
// LookPath was called, only what happens when it fails.
func TestFallbackLookupIsNotAGate(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/pages",
		`package p
func backend() string {
	if p, err := exec.LookPath("git-http-backend"); err == nil {
		return p
	}
	return "/usr/lib/git-core/git-http-backend"
}`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	if f, ok := scan(root, "o/pages"); ok {
		t.Errorf("a fallback lookup was treated as a gate: %v", f.missing)
	}
}

// TestSkippingLookupIsStillAGate is the control: the same tool, with the error
// leading to a skip, must still be reported.
func TestSkippingLookupIsStillAGate(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/gated2",
		`package p
func f(t *testing.T) {
	if _, err := exec.LookPath("git-http-backend"); err != nil {
		t.Skip("absent")
	}
}`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	f, ok := scan(root, "o/gated2")
	if !ok || len(f.missing) != 1 || f.missing[0] != "git-http-backend" {
		t.Errorf("missing = %v (ok=%v), want [git-http-backend]", f.missing, ok)
	}
}

// TestAStringThatLooksLikeACallIsNotOne. This tool reported ITSELF: its own
// fixtures hold Go source inside backquoted strings, and a whole-file search
// found nine tools in them. The parser does not, because a LookPath inside a
// string literal is not a call.
func TestAStringThatLooksLikeACallIsNotOne(t *testing.T) {
	src := []byte("package p\n\n" +
		"import \"testing\"\n\n" +
		"func TestFixture(t *testing.T) {\n" +
		"\tsrc := `package q\nfunc f() { exec.LookPath(\"pdftoppm\") }`\n" +
		"\t_ = src\n" +
		"}\n")
	gated, _, _, ok := lookPaths(src)
	if !ok {
		t.Fatal("the fixture did not parse")
	}
	if gated["pdftoppm"] {
		t.Error("a tool named inside a string literal was read as a call")
	}
	if len(gated) != 0 {
		t.Errorf("gated = %v, want none", gated)
	}
}

// TestARealCallIsStillFound — the other direction. Without it the rule above
// is satisfied by a reader that finds nothing at all.
func TestARealCallIsStillFound(t *testing.T) {
	src := []byte("package p\n\nimport \"os/exec\"\n\nfunc f() {\n" +
		"\tp, err := exec.LookPath(\"swtpm\")\n\t_, _ = p, err\n}\n")
	gated, _, _, ok := lookPaths(src)
	if !ok {
		t.Fatal("did not parse")
	}
	if !gated["swtpm"] {
		t.Errorf("a real call was missed: %v", gated)
	}
}

// TestADiscardedErrorIsNotAGate. `p, _ := exec.LookPath("x")` gates nothing,
// and the AST says so from the ASSIGNMENT rather than from the text around it.
func TestADiscardedErrorIsNotAGate(t *testing.T) {
	src := []byte("package p\n\nimport \"os/exec\"\n\nfunc f() {\n" +
		"\tp, _ := exec.LookPath(\"mock\")\n\t_ = p\n}\n")
	gated, tolerated, _, ok := lookPaths(src)
	if !ok {
		t.Fatal("did not parse")
	}
	if gated["mock"] {
		t.Error("a LookPath whose error is thrown away was called a gate")
	}
	if !tolerated["mock"] {
		t.Error("it was not reported as tolerated either — it has to be one or the other")
	}
}

// TestAFallbackIsNotAGate: `if p, err := exec.LookPath("x"); err == nil { … }`
// takes the tool when it is there and carries on when it is not.
func TestAFallbackIsNotAGate(t *testing.T) {
	src := []byte("package p\n\nimport \"os/exec\"\n\nfunc f() {\n" +
		"\tif p, err := exec.LookPath(\"git-http-backend\"); err == nil {\n\t\t_ = p\n\t}\n}\n")
	gated, tolerated, _, ok := lookPaths(src)
	if !ok {
		t.Fatal("did not parse")
	}
	if gated["git-http-backend"] {
		t.Error("a fallback was called a gate")
	}
	if !tolerated["git-http-backend"] {
		t.Error("the fallback was not recorded")
	}
}

// TestANonLiteralArgumentIsDynamicNotAbsent. A LookPath over a variable names
// a tool this cannot know, and saying nothing would read as "no tool needed".
func TestANonLiteralArgumentIsDynamicNotAbsent(t *testing.T) {
	src := []byte("package p\n\nimport \"os/exec\"\n\nfunc f(name string) {\n" +
		"\tp, _ := exec.LookPath(name)\n\t_ = p\n}\n")
	gated, _, dynamic, ok := lookPaths(src)
	if !ok {
		t.Fatal("did not parse")
	}
	if !dynamic {
		t.Error("a LookPath over a variable was not reported as dynamic")
	}
	if len(gated) != 0 {
		t.Errorf("it invented a name: %v", gated)
	}
}

// TestAFileThatDoesNotParseIsNotAFileWithNoTools. ok=false is the signal that
// sends the caller to the fallback; returning an empty set with ok=true would
// make an unreadable file look clean, which is the one answer that must never
// happen by accident.
func TestAFileThatDoesNotParseIsNotAFileWithNoTools(t *testing.T) {
	if _, _, _, ok := lookPaths([]byte("package p\nfunc f( {")); ok {
		t.Error("a file that does not parse reported a clean read")
	}
}

// TestAToolOnlyABenchmarkReachesIsLeftOut. `go test` does not run benchmarks,
// so a judge behind one was never going to run whatever the workflow installs.
// go-tpm2/tpm2 named swtpm through a helper called from six Benchmark
// functions and nothing else.
func TestAToolOnlyABenchmarkReachesIsLeftOut(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/benchonly",
		`package p

func startIt(tb testing.TB) string {
	bin, err := exec.LookPath("swtpm")
	if err != nil {
		tb.Skip("no swtpm")
	}
	return bin
}

func BenchmarkRoundTrip(b *testing.B) { _ = startIt(b) }`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	if f, ok := scan(root, "o/benchonly"); ok {
		t.Errorf("a benchmark-only judge was reported: %v", f.missing)
	}
}

// TestTheSameHelperBehindATestIsStillReported — the other direction, and the
// one that makes the rule worth having rather than just quieter. openweft/weft
// has this exact shape and its judge really has never run.
func TestTheSameHelperBehindATestIsStillReported(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/testtoo",
		`package p

func startIt(tb testing.TB) string {
	bin, err := exec.LookPath("swtpm")
	if err != nil {
		tb.Skip("no swtpm")
	}
	return bin
}

func BenchmarkRoundTrip(b *testing.B)  { _ = startIt(b) }
func TestRealHandshake(t *testing.T)   { _ = startIt(t) }`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	f, ok := scan(root, "o/testtoo")
	if !ok || len(f.missing) != 1 || f.missing[0] != "swtpm" {
		t.Errorf("missing = %v (ok=%v), want [swtpm] — a Test reaches it too", f.missing, ok)
	}
}

// TestAHelperNothingCallsIsStillReported. The call graph is name-based and so
// incomplete: a helper invoked through a function value or an interface has no
// edge this can see. It may therefore take things OFF the list and never be
// the reason something is on it.
func TestAHelperNothingCallsIsStillReported(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/orphan",
		`package p

func helper(t *testing.T) {
	if _, err := exec.LookPath("xorriso"); err != nil {
		t.Skip("absent")
	}
}`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	f, ok := scan(root, "o/orphan")
	if !ok || len(f.missing) != 1 || f.missing[0] != "xorriso" {
		t.Errorf("missing = %v (ok=%v), want [xorriso] — an unresolved caller must not delete a judge", f.missing, ok)
	}
}

// TestAHelperAcrossTwoFilesIsFollowed. The LookPath is almost never in the
// Test; reading one file at a time could say which tool was wanted and never
// by whom.
func TestAHelperAcrossTwoFilesIsFollowed(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "o", "twofiles")
	if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("helper_test.go", "package p\n\nfunc startIt(tb testing.TB) {\n"+
		"\tif _, err := exec.LookPath(\"swtpm\"); err != nil {\n\t\ttb.Skip(\"no\")\n\t}\n}\n")
	write("bench_test.go", "package p\n\nfunc BenchmarkA(b *testing.B) { startIt(b) }\n")
	if err := os.WriteFile(filepath.Join(dir, ".github", "workflows", "ci.yml"),
		[]byte("steps:\n  - run: go test ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, ok := scan(root, "o/twofiles"); ok {
		t.Errorf("the helper's only caller is in another file and was not followed: %v", f.missing)
	}
}

// TestErrIsNilInsideALargerConditionIsNotAGate. go-ansible/facts writes
//
//	if _, err := exec.LookPath("lsb_release"); err == nil && !anySeen {
//		t.Error(...)
//	}
//
// The tool being ABSENT costs an assertion and skips nothing. Read as a gate,
// it named a judge the CI never installs when there was no judge to install.
func TestErrIsNilInsideALargerConditionIsNotAGate(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/conj",
		`package p

func TestProbe(t *testing.T) {
	anySeen := false
	if _, err := exec.LookPath("lsb_release"); err == nil && !anySeen {
		t.Error("installed but nothing was reported")
	}
}`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	if f, ok := scan(root, "o/conj"); ok {
		t.Errorf("a strengthening check was treated as a gate: %v", f.missing)
	}
}

// TestASkipInsideTheSameShapeIsStillAGate — the other direction. The
// conjunction must not become a blanket excuse: a LookPath whose failure
// really does skip is still a judge.
func TestASkipInsideTheSameShapeIsStillAGate(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/stillgate",
		`package p

func TestProbe(t *testing.T) {
	if _, err := exec.LookPath("xorriso"); err != nil {
		t.Skip("absent")
	}
}`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	f, ok := scan(root, "o/stillgate")
	if !ok || len(f.missing) != 1 || f.missing[0] != "xorriso" {
		t.Errorf("missing = %v (ok=%v), want [xorriso]", f.missing, ok)
	}
}

// TestAToolProvenPresentIsNotReported. bzip2 is in the runner image's apt
// table and its judge was already running in CI.
func TestAToolProvenPresentIsNotReported(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/bz",
		`package p

func TestRef(t *testing.T) {
	bin, err := exec.LookPath("bzip2")
	if err != nil {
		t.Skip("no bzip2")
	}
	_ = bin
}`,
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})

	if f, ok := scan(root, "o/bz"); ok {
		t.Errorf("a tool the runner ships was reported: %v", f.missing)
	}
}

// ⛔ skipIsFatal is an EXCLUSION: a wrong `true` deletes a real finding and
// says nothing. So the cases that must stay findings are the important half,
// and they are first.

func TestASkipThatNothingActsOnIsStillSilent(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"no mention at all", "jobs:\n  t:\n    steps:\n      - run: go test ./...\n"},
		{"printed, not judged", "      - run: go test -v ./... | grep -- '--- SKIP' || true\n"},
		{
			// ::error:: writes an annotation. The step still succeeds, so the
			// skip stays green -- exactly the silence this tool reports.
			"an annotation with no exit",
			"      - run: |\n          case $out in\n            *\"--- SKIP\"*) echo \"::error::a judge skipped\" ;;\n          esac\n",
		},
		{
			// exit 0 is a guard written the wrong way round.
			"exit 0",
			"      - run: |\n          case $out in *\"--- SKIP\"*) exit 0 ;; esac\n",
		},
		{
			// The exit belongs to a later step, five lines down.
			"an exit too far away",
			"      - run: echo '--- SKIP'\n      - run: a\n      - run: b\n      - run: c\n      - run: d\n      - run: exit 1\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if skipIsFatal(tc.yaml) {
				t.Error("excluded a repository whose skip would stay green")
			}
		})
	}
}

func TestASkipTheRunRefusesIsNotSilent(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{
			// go-filesystems/unarchive, verbatim from the merged workflow.
			"the case form, on one line",
			"      - run: |\n          case \"$out\" in\n            *\"--- SKIP\"*) echo \"::error::a foreign judge skipped; its tool is not installed\"; exit 1 ;;\n          esac\n",
		},
		{
			"the grep form, spread over lines",
			"      - run: |\n          if grep -q -- \"--- SKIP\" out.txt; then\n            echo \"::error::skipped\"\n            exit 1\n          fi\n",
		},
		{"exit 2 counts as well", "          *\"--- SKIP\"*) exit 2 ;;\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !skipIsFatal(tc.yaml) {
				t.Error("a run that exits non-zero on --- SKIP was called silent")
			}
		})
	}
}

// TestTheTwoRepositoriesThatCausedThis pins the actual regression: a binary
// whose package is spelt differently, in a workflow that also refuses a skip.
// Both halves must hold, or the pair comes back.
func TestTheTwoRepositoriesThatCausedThis(t *testing.T) {
	for _, tc := range []struct{ tool, install string }{
		{"bsdcpio", "sudo apt-get install -y -qq libarchive-tools"},
		{"7zz", "sudo apt-get install -y -qq libarchive-tools xorriso 7zip-standalone"},
		{"bsdtar", "sudo apt-get install -y -qq libarchive-tools"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			if !mentioned(strings.ToLower(tc.install), tc.tool) {
				t.Errorf("%q reported missing from %q", tc.tool, tc.install)
			}
		})
	}
	// And the control: the alias must not swallow a tool that really is absent.
	if mentioned("sudo apt-get install -y -qq libarchive-tools", "xorriso") {
		t.Error("xorriso was found in a line that does not install it")
	}
}
