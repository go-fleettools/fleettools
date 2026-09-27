package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// repo writes a throwaway checkout: test files at the given package paths,
// plus zero or more workflows.
func repo(t *testing.T, root, name string, testPkgs []string, workflows map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range testPkgs {
		d := filepath.Join(dir, p)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "x_test.go"), []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
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

// TestNamedPackagesDoNotCoverTheRest is the founding case: openweft/weft ran
// `go test -tags=integration ./floatingipnat/` and three siblings, and nothing
// else. Every lane was green, about four packages out of dozens.
func TestNamedPackagesDoNotCoverTheRest(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/weft",
		[]string{"floatingipnat", "portsec", "agent", "cluster", "etcdcoord"},
		map[string]string{"ci.yml": `jobs:
  a:
    steps:
      - run: sudo -E env "PATH=$PATH" go test -tags=integration -v -count=1 ./floatingipnat/
  b:
    steps:
      - run: sudo -E env "PATH=$PATH" go test -tags=integration -v -count=1 ./portsec/
`})

	f, has := scan(root, "o/weft")
	if !has {
		t.Fatal("expected a repository with tests")
	}
	if f.covered() {
		t.Error("two named packages must not count as covering five")
	}
	if f.withTests != 5 || f.testedPkgs != 2 {
		t.Errorf("tested %d of %d, want 2 of 5", f.testedPkgs, f.withTests)
	}
}

// TestCatchAllCovers is the other direction: `go test ./...` covers whatever
// exists, and must never be reported.
func TestCatchAllCovers(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/fine",
		[]string{"a", "b", "c/d"},
		map[string]string{"ci.yml": "steps:\n  - run: go test -race ./...\n"})

	f, _ := scan(root, "o/fine")
	if !f.covered() {
		t.Errorf("./... must cover everything; tested %d of %d", f.testedPkgs, f.withTests)
	}
}

// TestFlagValuesAreNotPackages pins the parse. `-timeout 20m` and `-run TestX`
// take a separate value, and reading those as package paths would make a
// covered repository look uncovered -- or worse, the reverse.
func TestFlagValuesAreNotPackages(t *testing.T) {
	got := packageArgs(" -short -timeout 20m -run TestFoo ./pkg/ ")
	if len(got) != 1 || got[0] != "./pkg" {
		t.Errorf("packageArgs = %q, want [./pkg]", got)
	}
}

// TestSubdirectoriesOfANamedPathCount: naming ./cmd/... covers cmd/weft and
// cmd/weft/plugin both.
func TestSubdirectoriesOfANamedPathCount(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/sub",
		[]string{"cmd/weft", "cmd/weft/plugin", "internal/api"},
		map[string]string{"ci.yml": "steps:\n  - run: go test ./cmd/...\n"})

	f, _ := scan(root, "o/sub")
	if f.testedPkgs != 2 {
		t.Errorf("tested %d, want 2 (both under cmd/)", f.testedPkgs)
	}
	if f.covered() {
		t.Error("internal/api is untested; this is not covered")
	}
}

// TestNoWorkflowsIsNotZeroPackages separates "no CI" from "CI without go
// test": they need different fixes, and a failed read must never look like
// either.
func TestNoWorkflowsIsNotZeroPackages(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/bare", []string{"a"}, nil)

	f, _ := scan(root, "o/bare")
	if !f.noCI {
		t.Error("a repository with no workflows must be reported as having no CI")
	}
	if f.bytesRead != 0 || f.workflows != 0 {
		t.Errorf("claimed to have read %d bytes in %d files", f.bytesRead, f.workflows)
	}
}

// TestScriptDeferralIsReportedNotGuessed: what a called script runs is not
// visible here. Saying "no go test" would be a confident falsehood.
func TestScriptDeferralIsReportedNotGuessed(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/scripted", []string{"a"},
		map[string]string{"ci.yml": "steps:\n  - run: bash scripts/ci/test.sh\n"})

	f, _ := scan(root, "o/scripted")
	if !f.noGoTest || !f.viaScript {
		t.Errorf("a script call must be reported as such: noGoTest=%v viaScript=%v", f.noGoTest, f.viaScript)
	}
}

// TestBuildTaggedFilesAreCounted: `go test ./...` does not compile files
// behind //go:build integration, so a repo can cover every package and still
// not run those tests. The tool cannot resolve it; it must say so.
func TestBuildTaggedFilesAreCounted(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "o/tagged")
	repo(t, root, "o/tagged", []string{"a"},
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})
	if err := os.WriteFile(filepath.Join(dir, "a", "live_test.go"),
		[]byte("//go:build integration\n\npackage p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _ := scan(root, "o/tagged")
	if f.tagged != 1 {
		t.Errorf("tagged = %d, want 1", f.tagged)
	}
}

// TestWorktreesAreNotSeparateRepositories: a worktree's .git is a FILE, so
// counting it makes one repository look like several.
func TestWorktreesAreNotSeparateRepositories(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/real", []string{"a"}, nil)
	wt := filepath.Join(root, "o", "real.bump")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
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

// TestTestdataIsNotAPackage: a fixture tree can hold _test.go files that are
// data, not tests of this repository.
func TestTestdataIsNotAPackage(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/fix", []string{"a"},
		map[string]string{"ci.yml": "steps:\n  - run: go test ./...\n"})
	td := filepath.Join(root, "o/fix", "testdata", "golden")
	if err := os.MkdirAll(td, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(td, "y_test.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _ := scan(root, "o/fix")
	if f.withTests != 1 {
		t.Errorf("withTests = %d, want 1: testdata/ is not a package", f.withTests)
	}
}

// TestRootPackageOnly: `go test .` names the root package and nothing under it.
func TestRootPackageOnly(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/rootonly", []string{".", "sub"},
		map[string]string{"ci.yml": "steps:\n  - run: go test .\n"})

	f, _ := scan(root, "o/rootonly")
	if f.testedPkgs != 1 || f.covered() {
		t.Errorf("tested %d of %d; `go test .` must not cover sub/", f.testedPkgs, f.withTests)
	}
}

// TestComputedPackageSetIsNotZeroCoverage pins the false positive the first
// fleet run produced for three dozen repositories. Packages can come from a
// variable or a generated list:
//
//	go test $PURE_GO_PKGS
//
// The set is real; it is simply not readable here. Calling that "0 of N
// tested" is a confident falsehood, and it is the WORSE direction: it sends
// someone to fix a repository that is fine.
func TestComputedPackageSetIsNotZeroCoverage(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/doom", []string{"a", "b", "c"},
		map[string]string{"ci.yml": "steps:\n  - run: go test $PURE_GO_PKGS\n"})

	f, _ := scan(root, "o/doom")
	if !f.dynamic {
		t.Error("a $VARIABLE package set must be flagged as computed")
	}
	if f.testedPkgs != 0 || f.covered() {
		t.Errorf("tested=%d covered=%v: neither a count nor a verdict is available here",
			f.testedPkgs, f.covered())
	}
}

// TestGoListFeedingGoTestIsFullCoverage: `go list ./...` piped into a test run
// IS everything, minus whatever the pipeline filters out on purpose. This is
// the shape openweft/weft now uses, and the first run reported it as "4 of 83"
// — the very repository the tool's author had just fixed.
func TestGoListFeedingGoTestIsFullCoverage(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/weft2", []string{"a", "b", "c"},
		map[string]string{"unit.yml": `steps:
  - run: |
      go list ./... | grep -v '/cmd/weft/plugin$' > "$RUNNER_TEMP/pkgs.txt"
      xargs go test -short < "$RUNNER_TEMP/pkgs.txt"
`})

	f, _ := scan(root, "o/weft2")
	if !f.covered() {
		t.Errorf("go list ./... feeding go test is full coverage; got %d of %d", f.testedPkgs, f.withTests)
	}
}

// TestWrittenOutPackagesStillCount is the control for both: a workflow that
// names its packages literally must still be measured, not excused as dynamic.
func TestWrittenOutPackagesStillCount(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/named", []string{"a", "b", "c"},
		map[string]string{"ci.yml": "steps:\n  - run: go test ./a/ ./b/\n"})

	f, _ := scan(root, "o/named")
	if f.dynamic {
		t.Error("literal package paths are not a computed set")
	}
	if f.testedPkgs != 2 || f.covered() {
		t.Errorf("tested %d of %d, want 2 of 3 and not covered", f.testedPkgs, f.withTests)
	}
}

// TestTaskDelegationIsFollowedNotGuessed pins the third false-positive class.
// go-fde/fde and go-filesystems/interface run `task ci`, and their Taskfile's
// ci target does `go test -race ./...`. Reporting "no go test in CI" pointed
// at two repositories that are fully covered.
//
// The delegate is READ, not assumed: a repository whose Taskfile really has no
// tests must still be reported.
func TestTaskDelegationIsFollowedNotGuessed(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/fde", []string{"a", "b"},
		map[string]string{"ci.yml": "steps:\n  - run: task ci\n"})
	if err := os.WriteFile(filepath.Join(root, "o/fde", "Taskfile.yml"),
		[]byte("tasks:\n  ci:\n    cmds:\n      - go test -race ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _ := scan(root, "o/fde")
	if !f.covered() {
		t.Errorf("a Taskfile running go test ./... is coverage; got %d of %d, noGoTest=%v",
			f.testedPkgs, f.withTests, f.noGoTest)
	}
}

// TestDelegationToATaskfileWithoutTestsIsStillAGap is the control: following
// the delegation must be able to come back empty-handed.
func TestDelegationToATaskfileWithoutTestsIsStillAGap(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/notests", []string{"a"},
		map[string]string{"ci.yml": "steps:\n  - run: task build\n"})
	if err := os.WriteFile(filepath.Join(root, "o/notests", "Taskfile.yml"),
		[]byte("tasks:\n  build:\n    cmds:\n      - go build ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _ := scan(root, "o/notests")
	if !f.noGoTest || !f.viaScript {
		t.Errorf("a Taskfile with no go test is still a gap: noGoTest=%v viaScript=%v",
			f.noGoTest, f.viaScript)
	}
}

// TestStalenessIsMeasuredNotAssumed pins the failure that produced three
// redundant pull requests on 2026-09-22. This tool reads the WORKING TREE, so
// a checkout that has not fetched in weeks yields a weeks-old diagnosis that
// is indistinguishable from a current one.
//
// Three openweft repositories were reported as having no test lane. All three
// had gained ci.yml weeks earlier; the local clones predated it. Refreshing
// the clones before branching made the finding look confirmed — the finding
// itself was never re-derived.
func TestStalenessIsMeasuredNotAssumed(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/old", []string{"a"},
		map[string]string{"ci.yml": "steps:\n  - run: go build ./...\n"})

	// No FETCH_HEAD at all: unknown must report as unknown, never as fresh.
	f, _ := scan(root, "o/old")
	if f.fetchKnown {
		t.Error("a clone that has never fetched must not report a known age")
	}

	// With one, the age is the file's mtime and must be readable.
	fh := filepath.Join(root, "o/old", ".git", "FETCH_HEAD")
	if err := os.WriteFile(fh, []byte("abc123\tbranch 'main'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(fh, old, old); err != nil {
		t.Fatal(err)
	}
	f, _ = scan(root, "o/old")
	if !f.fetchKnown {
		t.Fatal("a clone with FETCH_HEAD must report a known age")
	}
	if f.fetchAge < 29*24*time.Hour {
		t.Errorf("fetchAge = %v, want about 30 days", f.fetchAge)
	}
}

// TestRemoteModeReadsTheRefNotTheDisk pins the correction that matters most.
// The working tree is a cache; on this machine it is routinely weeks stale. Of
// thirteen repositories reported as "no CI at all" from the disk, ELEVEN
// already had a workflow on origin/main.
//
// Here the checkout has no workflow and the ref does. Disk mode must report a
// gap; ref mode must not.
func TestRemoteModeReadsTheRefNotTheDisk(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "o", "stale")
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "x_test.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", "-A")
	run("commit", "-q", "-m", "tests, no CI")

	// A "remote" branch that HAS a workflow, which the checkout does not.
	run("checkout", "-q", "-b", "withci")
	if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".github", "workflows", "ci.yml"),
		[]byte("steps:\n  - run: go test ./...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "add CI")
	run("update-ref", "refs/remotes/origin/main", "refs/heads/withci")
	run("checkout", "-q", "main")

	// Disk mode sees the stale checkout: a gap.
	f, has := scan(root, filepath.Join("o", "stale"))
	if !has || !f.noCI {
		t.Errorf("disk mode: noCI=%v has=%v, want a reported gap", f.noCI, has)
	}

	// Ref mode sees what the remote holds: covered.
	f, has = scanRef(root, filepath.Join("o", "stale"))
	if !has {
		t.Fatal("ref mode found no tests")
	}
	if f.noCI || !f.covered() {
		t.Errorf("ref mode: noCI=%v covered=%v, want covered — the ref has the workflow",
			f.noCI, f.covered())
	}
}

// TestNoRemoteIsItsOwnVerdict: cloud-boot/init has nine packages with tests and
// no origin at all. "No CI" understates it; the repository exists nowhere but
// this disk, and that is the thing worth saying.
func TestNoRemoteIsItsOwnVerdict(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/local-only", []string{"a"}, nil)

	f, has := scanRef(root, "o/local-only")
	if !has || !f.noRemote {
		t.Errorf("noRemote=%v has=%v, want a repository with no remote to say so", f.noRemote, has)
	}
}

// TestLineContinuationKeepsThePackages pins the fifth false-positive class. A
// trailing backslash continues the command, and the packages are routinely on
// the continued line:
//
//	go test -mod=mod -timeout 600s -covermode=atomic \
//	  -coverprofile=cover.out ./...
//
// Stopping at the first newline read that as naming no packages at all, so
// grpc-transports/webrtc — fully covered — reported as "0 of 1 tested".
func TestLineContinuationKeepsThePackages(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/cont", []string{"a", "b"},
		map[string]string{"ci.yml": `steps:
  - run: |
      go test -mod=mod -timeout 600s -covermode=atomic \
        -coverprofile=cover.out ./...
      go tool cover -func=cover.out | tail -1
`})

	f, _ := scan(root, "o/cont")
	if !f.covered() {
		t.Errorf("./... on the continued line must count; got %d of %d", f.testedPkgs, f.withTests)
	}
}

// TestContinuationDoesNotSwallowTheNextCommand is the control: the capture
// must stop at a line that is NOT continued, or a later `go build ./...` would
// be read as this invocation's package list.
func TestContinuationDoesNotSwallowTheNextCommand(t *testing.T) {
	root := t.TempDir()
	repo(t, root, "o/stop", []string{"a", "b"},
		map[string]string{"ci.yml": `steps:
  - run: |
      go test ./a/
      go build ./...
`})

	f, _ := scan(root, "o/stop")
	if f.covered() {
		t.Errorf("a following `go build ./...` must not count as tested packages; got %d of %d",
			f.testedPkgs, f.withTests)
	}
}

// TestNoRemoteReachesTheOutput closes a gap this suite had itself: the
// noRemote field was set correctly, TestNoRemoteIsItsOwnVerdict asserted it,
// and nothing ever printed it — cloud-boot/init read as "no CI at all" for the
// whole sweep. Testing a field does not test that anybody reads it.
func TestNoRemoteReachesTheOutput(t *testing.T) {
	got := render(finding{repo: "o/local-only", noRemote: true, noCI: true, withTests: 9})
	if !strings.Contains(got, "NO REMOTE") {
		t.Errorf("render = %q, want it to say NO REMOTE", got)
	}
	if strings.Contains(got, "no CI at all") {
		t.Errorf("render = %q: noRemote must win over noCI, it is the sharper fact", got)
	}
}

// TestWhyNotOursNamesTheReasonAndOnlyWhenAnswered. The failure mode that
// matters is the quiet one: an unanswered question must never read as "not
// ours", because that deletes a real finding — and the minutes when the API
// refuses are exactly the minutes a sweep is running.
func TestWhyNotOursNamesTheReasonAndOnlyWhenAnswered(t *testing.T) {
	for name, c := range map[string]struct {
		in   ownership
		want string
	}{
		"a fork":                     {ownership{fork: true, ok: true}, "a fork"},
		"not on GitHub":              {ownership{missing: true, ok: true}, "not on GitHub"},
		"ours":                       {ownership{ok: true}, ""},
		"the question failed":        {ownership{}, ""},
		"failed, looked like a fork": {ownership{fork: true}, ""},
	} {
		if got := whyNotOurs(c.in); got != c.want {
			t.Errorf("%s: whyNotOurs = %q, want %q", name, got, c.want)
		}
	}
}

// TestSiftKeepsWhatItCannotAskAbout — fail open, in one place, with a control
// that a repository it CAN place is still dropped.
func TestSiftKeepsWhatItCannotAskAbout(t *testing.T) {
	// The question never answers: nothing may be dropped on either ground.
	unanswerable := func(string) ownership { return ownership{} }
	kept, dropped := sift([]finding{{repo: "someone/else"}}, nil, false, unanswerable)
	if len(kept) != 1 || len(dropped) != 0 {
		t.Errorf("an unanswered owner list dropped a finding: kept=%v dropped=%v", len(kept), dropped)
	}
	// Owners known: a repository outside them goes, one inside stays.
	owners := map[string]bool{"go-encryptions": true}
	kept, dropped = sift([]finding{{repo: "usbarmory/tamago"}}, owners, true, unanswerable)
	if len(kept) != 0 || len(dropped) != 1 {
		t.Errorf("a checkout under somebody else's account was kept: kept=%v dropped=%v", len(kept), dropped)
	}
	if !strings.Contains(dropped[0], "not one of our accounts") {
		t.Errorf("the reason is not named: %q", dropped[0])
	}
	// A fork under an account that IS ours still goes, on the other ground.
	kept, dropped = sift([]finding{{repo: "go-encryptions/ccm"}}, owners, true,
		func(string) ownership { return ownership{fork: true, ok: true} })
	if len(kept) != 0 || len(dropped) != 1 || !strings.Contains(dropped[0], "a fork") {
		t.Errorf("a fork in one of our own organisations was kept: kept=%v dropped=%v", len(kept), dropped)
	}
	// And a plain repository of ours survives both tests.
	kept, dropped = sift([]finding{{repo: "go-encryptions/ccm"}}, owners, true,
		func(string) ownership { return ownership{ok: true} })
	if len(kept) != 1 || len(dropped) != 0 {
		t.Errorf("one of ours was dropped: kept=%v dropped=%v", len(kept), dropped)
	}
}

func TestOwnerOf(t *testing.T) {
	for in, want := range map[string]string{
		"go-encryptions/ccm": "go-encryptions",
		"tannevaled/hcl":     "tannevaled",
		"nameonly":           "nameonly",
	} {
		if got := ownerOf(in); got != want {
			t.Errorf("ownerOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// gitRepo builds a real clone with a real remote, so the distance measured is
// git's own and not a string this test and the code agree on.
func gitRepo(t *testing.T, commitsAhead int) string {
	t.Helper()
	base := t.TempDir()
	origin := filepath.Join(base, "origin")
	clone := filepath.Join(base, "clone")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	run(origin, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "a.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(origin, "add", "-A")
	run(origin, "commit", "-qm", "first")

	run(base, "clone", "-q", origin, clone)

	for i := range commitsAhead {
		if err := os.WriteFile(filepath.Join(origin, "a.txt"), []byte{byte('a' + i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		run(origin, "commit", "-qam", "later")
	}
	// A FETCH, not a pull: this is the whole point. It refreshes FETCH_HEAD
	// and the remote refs and leaves the working tree where it was, which is
	// the state that made five findings wrong. Always, including when there is
	// nothing new — otherwise the level-with-its-remote case has no FETCH_HEAD
	// and SKIPS, and a skipped control is the one direction that mattered.
	run(clone, "fetch", "-q", "origin")
	return clone
}

// TestStalenessCountsCommitsAndNotMinutes. The old version returned a
// hard-coded 0 for the distance while its doc said it reported one, so the
// only signal was the fetch age — and a clone fetched seconds ago can be
// twenty commits behind.
func TestStalenessCountsCommitsAndNotMinutes(t *testing.T) {
	clone := gitRepo(t, 3)
	behind, age, ok := staleness(clone)
	if !ok {
		t.Fatal("a clone that has fetched reported unknown")
	}
	if behind != 3 {
		t.Errorf("behind = %d, want 3", behind)
	}
	// And the age says "fresh", which is exactly why it is the wrong signal.
	if age > time.Hour {
		t.Errorf("the fetch was seconds ago and the age reads %v", age)
	}
}

// TestACloneLevelWithItsRemoteIsNotBehind — the other direction, or a function
// returning any positive number would pass the test above.
func TestACloneLevelWithItsRemoteIsNotBehind(t *testing.T) {
	behind, _, ok := staleness(gitRepo(t, 0))
	if !ok {
		t.Fatal("a clone that has fetched reported unknown")
	}
	if behind != 0 {
		t.Errorf("a clone level with its remote reads %d behind", behind)
	}
}

// TestSomethingThatIsNotARepositoryIsUnknownNotZero. Unknown must not read as
// up to date: that is how a directory this cannot measure would slip through
// looking fresh.
func TestSomethingThatIsNotARepositoryIsUnknownNotZero(t *testing.T) {
	if _, _, ok := staleness(t.TempDir()); ok {
		t.Error("a directory with no .git reported a known staleness")
	}
}
