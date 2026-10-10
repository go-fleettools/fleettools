package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestMain makes the real runner a panic. Every test drives a fake: the
// previous fleet tool whose tests started real processes became a fork bomb
// (load 583, 10894 processes), and a test that forgets to inject must fail,
// not spawn.
func TestMain(m *testing.M) {
	realRunner = func(context.Context, string, []string, string, ...string) ([]byte, []byte, error) {
		panic("a test reached a real process")
	}
	preflight = func(string) error { return nil }
	os.Exit(m.Run())
}

// answer is what the fake govulncheck says for one module directory.
type answer struct {
	stdout, stderr []byte
	err            error
}

// fake stands in for git and govulncheck. repos maps org/repo to the files a
// clone produces (nil = the clone fails); scans maps "org/repo/dir" to what
// govulncheck answers there.
type fake struct {
	mu     sync.Mutex
	repos  map[string]map[string]string
	scans  map[string]answer
	clones []string
	runs   []fakeRun
}

type fakeRun struct {
	key string
	env []string
}

func (f *fake) run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name == "git" {
		url, dest := args[len(args)-2], args[len(args)-1]
		repo := strings.TrimSuffix(strings.TrimPrefix(url, "https://github.com/"), ".git")
		f.clones = append(f.clones, repo)
		files, ok := f.repos[repo]
		if !ok || files == nil {
			return nil, []byte("remote: Repository not found.\nfatal: repository '" + url + "' not found\n"), errors.New("exit status 128")
		}
		for p, body := range files {
			full := filepath.Join(dest, p)
			os.MkdirAll(filepath.Dir(full), 0o755)
			os.WriteFile(full, []byte(body), 0o644)
		}
		os.MkdirAll(dest, 0o755)
		return nil, nil, nil
	}
	// govulncheck: identify the module by its path below the job directory,
	// which is <work>/<n>/<org>/<repo>/<dir>.
	parts := strings.Split(filepath.ToSlash(dir), "/")
	key := ""
	for i := range parts {
		if _, ok := f.repos[strings.Join(parts[i:min(i+2, len(parts))], "/")]; ok && i+2 <= len(parts) {
			key = strings.Join(parts[i:], "/")
			break
		}
	}
	f.runs = append(f.runs, fakeRun{key, env})
	a, ok := f.scans[key]
	if !ok {
		return fixture("imported.json"), nil, nil
	}
	return a.stdout, a.stderr, a.err
}

const gomod = "module example.com/m\n\ngo 1.27.1\n"

func sweep(t *testing.T, f *fake, list string, args ...string) (string, report, int) {
	t.Helper()
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "out.json")
	var out, errb strings.Builder
	// The fixtures were recorded under go1.27.1; a later -go in args wins.
	all := append([]string{"-go", "go1.27.1", "-json", jsonPath, "-work", filepath.Join(dir, "work")}, args...)
	code := run(strings.NewReader(list), &out, &errb, all, f.run)
	var rep report
	if b, err := os.ReadFile(jsonPath); err == nil {
		json.Unmarshal(b, &rep)
	}
	if errb.Len() > 0 {
		t.Logf("stderr: %s", errb.String())
	}
	return out.String(), rep, code
}

func byRepo(rep report) map[string]result {
	m := map[string]result{}
	for _, r := range rep.Repos {
		m[r.Repo] = r
	}
	return m
}

// TestAKnownPositiveIsCalled is the reason the tool can be trusted at all: a
// judge that never says CALLED reports a quiet fleet whatever the fleet is.
func TestAKnownPositiveIsCalled(t *testing.T) {
	s := judge(fixture("called.json"), nil, nil, "")
	if s.Status != Called {
		t.Fatalf("status %s, want CALLED", s.Status)
	}
	if len(s.Called) != 1 {
		t.Fatalf("called = %+v, want exactly GO-2026-6603 in x/net", s.Called)
	}
	c := s.Called[0]
	if c.ID != "GO-2026-6603" || c.Module != "golang.org/x/net" || c.Fixed != "v0.60.0" || c.Found != "v0.59.0" {
		t.Errorf("called = %+v", c)
	}
	// GO-2026-6604 is imported (package os) and never called; GO-2026-6599 is
	// only required. Neither may be counted as called.
	if s.Imported != 1 || s.Required != 1 {
		t.Errorf("imported only %d, required only %d; want 1 and 1", s.Imported, s.Required)
	}
}

// TestImportedButNotCalledIsClean: govulncheck reports every advisory at
// module, package and symbol level, and only the symbol level is reachable.
func TestImportedButNotCalledIsClean(t *testing.T) {
	s := judge(fixture("imported.json"), nil, nil, "")
	if s.Status != Clean || len(s.Called) != 0 {
		t.Fatalf("status %s with %d called, want CLEAN", s.Status, len(s.Called))
	}
	if s.Imported != 2 || s.Required != 1 {
		t.Errorf("imported only %d, required only %d; want 2 and 1", s.Imported, s.Required)
	}
}

// TestALoadFailureIsUnreadNotClean: the recorded stream of a load failure has
// a config and no finding at all. Read as JSON alone, it is a clean module.
func TestALoadFailureIsUnreadNotClean(t *testing.T) {
	s := judge(fixture("loadfail.json"), fixture("loadfail.stderr"), errors.New("exit status 1"), "")
	if s.Status != Unread {
		t.Fatalf("status %s, want UNREAD", s.Status)
	}
	if !strings.Contains(s.Err, "replacement directory ../sib does not exist") {
		t.Errorf("the reason was lost: %q", s.Err)
	}
	// The same stream with the exit status lost -- a wrapper that swallowed
	// it -- must still not read as clean: no SBOM, no load.
	s = judge(fixture("loadfail.json"), nil, nil, "")
	if s.Status != Unread {
		t.Errorf("a stream without an SBOM read as %s", s.Status)
	}
	// And output that does not decode is not an empty answer either.
	if s := judge([]byte(`{"config":`), nil, nil, ""); s.Status != Unread {
		t.Errorf("a torn stream read as %s", s.Status)
	}
}

// TestAScanUnderAnotherToolchainIsRefused: standard-library findings are
// decided by the one Go version the scan runs under. A scan that judged it as
// another answered a different question.
func TestAScanUnderAnotherToolchainIsRefused(t *testing.T) {
	s := judge(fixture("called.json"), nil, nil, "go1.27.2")
	if s.Status != Unread || !strings.Contains(s.Err, "go1.27.1") {
		t.Errorf("status %s (%s), want UNREAD naming the version actually used", s.Status, s.Err)
	}
	if s := judge(fixture("called.json"), nil, nil, "go1.27.1"); s.Status != Called {
		t.Errorf("the matching toolchain was refused: %s %s", s.Status, s.Err)
	}
}

func TestTheSelftestPasses(t *testing.T) {
	var out strings.Builder
	if code := selftest(&out); code != 0 {
		t.Errorf("selftest failed:\n%s", out.String())
	}
}

// TestTheFourStatusesStayApart drives a whole sweep: one repository of each
// kind, plus one that cannot even be cloned.
func TestTheFourStatusesStayApart(t *testing.T) {
	f := &fake{
		repos: map[string]map[string]string{
			"acme/called": {"go.mod": gomod},
			"acme/clean":  {"go.mod": gomod},
			"acme/docs":   {"README.md": "no Go here"},
			"acme/broken": {"go.mod": gomod},
			"acme/gone":   nil,
		},
		scans: map[string]answer{
			"acme/called": {stdout: fixture("called.json")},
			"acme/broken": {fixture("loadfail.json"), fixture("loadfail.stderr"), errors.New("exit status 1")},
		},
	}
	out, rep, code := sweep(t, f, "# a comment\nacme/called\nacme/clean\n\nacme/docs  # trailing\nacme/broken\nacme/gone\n")
	got := byRepo(rep)
	for repo, want := range map[string]string{
		"acme/called": Called, "acme/clean": Clean, "acme/docs": NoGo, "acme/broken": Unread, "acme/gone": Unread,
	} {
		if got[repo].Status != want {
			t.Errorf("%s: %s, want %s", repo, got[repo].Status, want)
		}
	}
	if rep.Totals[Called] != 1 || rep.Totals[Clean] != 1 || rep.Totals[NoGo] != 1 || rep.Totals[Unread] != 2 {
		t.Errorf("totals %v", rep.Totals)
	}
	if code != 2 {
		t.Errorf("exit %d, want 2: two repositories could not be read", code)
	}
	unreadLines := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "] UNREAD") {
			unreadLines++
			if strings.Contains(l, "0 called") || !strings.Contains(l, "could not scan:") {
				t.Errorf("an UNREAD line reads like an answer: %q", l)
			}
		}
	}
	if unreadLines != 2 {
		t.Errorf("%d UNREAD lines, want 2:\n%s", unreadLines, out)
	}
	if !strings.Contains(out, "INCOMPLETE") {
		t.Errorf("a sweep with unread repositories did not say so:\n%s", out)
	}
	if !strings.Contains(got["acme/broken"].Err, "replacement directory") || !strings.Contains(got["acme/gone"].Err, "not found") {
		t.Errorf("reasons lost: broken=%q gone=%q", got["acme/broken"].Err, got["acme/gone"].Err)
	}
	c := got["acme/called"].Called
	if len(c) != 1 || c[0].Fixed != "v0.60.0" || c[0].Module != "golang.org/x/net" {
		t.Errorf("called = %+v", c)
	}
}

// TestTheScanRunsAsLinuxUnderTheAskedToolchain: darwin hides Linux-only files
// (nineteen findings were hidden that way), and the host's toolchain is not
// the one CI builds with.
func TestTheScanRunsAsLinuxUnderTheAskedToolchain(t *testing.T) {
	f := &fake{repos: map[string]map[string]string{"acme/a": {"go.mod": gomod}}}
	var out, errb strings.Builder
	run(strings.NewReader("acme/a\n"), &out, &errb, []string{"-work", t.TempDir()}, f.run) // every default
	if len(f.runs) != 1 {
		t.Fatalf("%d scans, want 1", len(f.runs))
	}
	env := strings.Join(f.runs[0].env, " ")
	for _, want := range []string{"GOOS=linux", "GOTOOLCHAIN=go1.27.2", "GOARCH=amd64"} {
		if !strings.Contains(env, want) {
			t.Errorf("env %q lacks %s", env, want)
		}
	}

	f = &fake{repos: map[string]map[string]string{"acme/a": {"go.mod": gomod}}}
	sweep(t, f, "acme/a\n", "-go", "go1.27.1", "-goos", "linux,darwin")
	var gooses []string
	for _, r := range f.runs {
		gooses = append(gooses, strings.Join(r.env, " "))
	}
	if len(f.runs) != 2 || !strings.Contains(gooses[0], "GOOS=linux") || !strings.Contains(gooses[1], "GOOS=darwin") ||
		!strings.Contains(gooses[1], "GOTOOLCHAIN=go1.27.1") {
		t.Errorf("scans: %v", gooses)
	}
}

// TestEveryModuleIsScannedButNotFixtures: a nested go.mod is a separate build;
// a go.mod under testdata or vendor is a fixture -- unless it replaces ../,
// which makes it a module built on the repository's own code.
func TestEveryModuleIsScannedButNotFixtures(t *testing.T) {
	f := &fake{repos: map[string]map[string]string{"acme/a": {
		"go.mod":                    gomod,
		"tools/go.mod":              gomod,
		"testdata/fixture/go.mod":   gomod,
		"testdata/example/go.mod":   gomod + "\nreplace example.com/m => ../..\n",
		"vendor/x/go.mod":           gomod,
		"node_modules/thing/go.mod": gomod,
	}}}
	_, rep, _ := sweep(t, f, "acme/a\n")
	var dirs []string
	for _, m := range rep.Repos[0].Modules {
		dirs = append(dirs, m.Dir)
	}
	want := ". testdata/example tools"
	if strings.Join(dirs, " ") != want {
		t.Errorf("modules scanned: %v, want %s", dirs, want)
	}
}

// TestASiblingNamedByReplaceIsCloned: without the sibling, `go list` fails and
// the repository is UNREAD -- for a reason that is the sweep's, not its own.
func TestASiblingNamedByReplaceIsCloned(t *testing.T) {
	f := &fake{repos: map[string]map[string]string{
		"acme/app":  {"go.mod": gomod + "\nreplace example.com/lib => ../lib\n", "sub/go.mod": gomod + "\nreplace example.com/x => ../../lib/x\n"},
		"acme/lib":  {"go.mod": gomod + "\nreplace example.com/deep => ../deep\n", "x/go.mod": gomod},
		"acme/deep": {"go.mod": gomod},
		"acme/out":  {"go.mod": gomod + "\nreplace example.com/o => ../../other/thing\n"},
	}}
	_, rep, _ := sweep(t, f, "acme/app\nacme/out\n")
	got := byRepo(rep)
	if s := strings.Join(got["acme/app"].Siblings, " "); s != "acme/deep acme/lib" {
		t.Errorf("siblings cloned: %q, want lib and, through it, deep", s)
	}
	n := 0
	for _, c := range f.clones {
		if c == "acme/lib" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("acme/lib cloned %d times for one repository, want 1", n)
	}
	if notes := strings.Join(got["acme/out"].Notes, " "); !strings.Contains(notes, "leaves the organisation") {
		t.Errorf("a replace outside the organisation was not reported: %q", notes)
	}
}

// TestASweepResumesFromItsState: the fleet takes hours; a sweep killed halfway
// must not start over, and must not re-run what it already answered.
func TestASweepResumesFromItsState(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.jsonl")
	mk := func() *fake {
		return &fake{
			repos: map[string]map[string]string{"acme/a": {"go.mod": gomod}, "acme/b": {"go.mod": gomod}, "acme/gone": nil},
		}
	}
	f := mk()
	sweep(t, f, "acme/a\nacme/gone\n", "-state", state)
	if len(f.clones) != 2 {
		t.Fatalf("first pass cloned %v", f.clones)
	}
	f = mk()
	_, rep, _ := sweep(t, f, "acme/a\nacme/b\nacme/gone\n", "-state", state)
	if strings.Join(f.clones, " ") != "acme/b" {
		t.Errorf("second pass cloned %v, want only the new acme/b", f.clones)
	}
	if rep.Totals[Clean] != 2 || rep.Totals[Unread] != 1 {
		t.Errorf("the report forgot what the state held: %v", rep.Totals)
	}
	f = mk()
	sweep(t, f, "acme/a\nacme/b\nacme/gone\n", "-state", state, "-retry-unread")
	if strings.Join(f.clones, " ") != "acme/gone" {
		t.Errorf("-retry-unread cloned %v, want only acme/gone", f.clones)
	}
	f = mk()
	sweep(t, f, "acme/a\nacme/b\n", "-state", state, "-rescan")
	if len(f.clones) != 2 {
		t.Errorf("-rescan cloned %v, want both", f.clones)
	}
}

// TestTheCloneIsRemovedUnlessKept: two thousand clones left behind is a disk.
func TestTheCloneIsRemovedUnlessKept(t *testing.T) {
	for _, keep := range []bool{false, true} {
		f := &fake{repos: map[string]map[string]string{"acme/a": {"go.mod": gomod}}}
		work := filepath.Join(t.TempDir(), "w")
		args := []string{"-work", work}
		if keep {
			args = append(args, "-keep")
		}
		var out, errb strings.Builder
		run(strings.NewReader("acme/a\n"), &out, &errb, args, f.run)
		_, err := os.Stat(filepath.Join(work, "0", "acme", "a", "go.mod"))
		if keep != (err == nil) {
			t.Errorf("-keep=%v: clone present=%v", keep, err == nil)
		}
	}
}

func TestRefusals(t *testing.T) {
	f := &fake{}
	var out, errb strings.Builder
	if code := run(strings.NewReader("acme/a\n"), &out, &errb, []string{"-j", "4"}, f.run); code != 2 {
		t.Errorf("-j 4 accepted (exit %d)", code)
	}
	if code := run(strings.NewReader("not a repo\n"), &out, &errb, nil, f.run); code != 2 {
		t.Errorf("a malformed list was accepted (exit %d)", code)
	}
	if code := run(strings.NewReader("# nothing\n"), &out, &errb, nil, f.run); code != 2 {
		t.Errorf("an empty list was accepted (exit %d)", code)
	}
	t.Setenv(childMarker, "1")
	if code := run(strings.NewReader("acme/a\n"), &out, &errb, nil, f.run); code != 2 || !strings.Contains(errb.String(), childMarker) {
		t.Errorf("ran under %s (exit %d)", childMarker, code)
	}
	if len(f.clones) != 0 {
		t.Errorf("a refused sweep cloned %v", f.clones)
	}
}

// TestAPartialScanStillSaysSo: CALLED wins the status, but the module that
// could not be read is named, so a partial answer never reads as a whole one.
func TestAPartialScanStillSaysSo(t *testing.T) {
	f := &fake{
		repos: map[string]map[string]string{"acme/a": {"go.mod": gomod, "sub/go.mod": gomod}},
		scans: map[string]answer{
			"acme/a":     {stdout: fixture("called.json")},
			"acme/a/sub": {fixture("loadfail.json"), fixture("loadfail.stderr"), errors.New("exit status 1")},
		},
	}
	out, rep, _ := sweep(t, f, "acme/a\n")
	r := rep.Repos[0]
	if r.Status != Called || !strings.Contains(r.Err, "1 of 2 module scans UNREAD") {
		t.Errorf("status %s, err %q", r.Status, r.Err)
	}
	if !strings.Contains(out, "PARTIAL") {
		t.Errorf("the line hides the unread module:\n%s", out)
	}
}

func TestCIGoVersionsAreRecorded(t *testing.T) {
	f := &fake{repos: map[string]map[string]string{"acme/a": {
		"go.mod": gomod,
		".github/workflows/ci.yml": "jobs:\n  t:\n    steps:\n      - uses: actions/setup-go@v7\n        with:\n          go-version: '1.27.1'\n" +
			"      - uses: actions/setup-go@v7\n        with:\n          go-version-file: go.mod\n",
	}}}
	_, rep, _ := sweep(t, f, "acme/a\n")
	if got := strings.Join(rep.Repos[0].CIGo, " "); got != "1.27.1 file:go.mod" {
		t.Errorf("ci_go = %q", got)
	}
}
