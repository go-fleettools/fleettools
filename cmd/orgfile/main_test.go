package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// answer is what the fake GitHub says for one contents path.
type answer struct {
	status int
	body   string
}

// fake serves /user/orgs and the contents paths in files; any other path is a
// 404. remaining, when non-nil, is sent as X-RateLimit-Remaining and decremented
// by one per call.
type fake struct {
	orgs      []string
	files     map[string]answer
	remaining *atomic.Int64
	hits      atomic.Int64
	mu        sync.Mutex
	paths     []string
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.hits.Add(1)
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.mu.Unlock()
	if f.remaining != nil {
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprint(f.remaining.Add(-1)))
	}
	if r.URL.Path == "/user/orgs" {
		parts := make([]string, len(f.orgs))
		for i, o := range f.orgs {
			parts[i] = fmt.Sprintf(`{"login":%q}`, o)
		}
		fmt.Fprintf(w, "[%s]", strings.Join(parts, ","))
		return
	}
	if a, ok := f.files[r.URL.Path]; ok {
		w.WriteHeader(a.status)
		fmt.Fprint(w, a.body)
		return
	}
	http.NotFound(w, r)
}

func drive(t *testing.T, f *fake, args ...string) (code int, out, errOut string) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	sweepBase = srv.URL
	oldTok := readToken
	readToken = func(string) (string, error) { return "test-token", nil }
	t.Cleanup(func() { sweepBase = ""; readToken = oldTok })
	var o, e bytes.Buffer
	code = cli.Drive(RootCmd(), args, &o, &e)
	return code, o.String(), e.String()
}

const cfg = "/repos/%s/%s/contents/config.js"

func p(org, repo string) string { return fmt.Sprintf(cfg, org, repo) }

func TestMatch(t *testing.T) {
	f := &fake{orgs: []string{"a-org"}, files: map[string]answer{
		p("a-org", ".github"): {200, "dependencyDashboard: { enabled: true }"},
	}}
	code, out, _ := drive(t, f, "--file", "config.js", "--contains", "enabled: true")
	if code != 0 || !strings.Contains(out, "match    a-org/.github") || !strings.Contains(out, "match 1, nomatch 0, nofile 0, unread 0") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestNoMatchSaysWhichString(t *testing.T) {
	f := &fake{orgs: []string{"a-org"}, files: map[string]answer{
		p("a-org", ".github"): {200, "dependencyDashboard: true"},
	}}
	code, out, _ := drive(t, f, "--file", "config.js", "--list",
		"--contains", "enabled: true", "--contains", "dependencyDashboard: true")
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	for _, want := range []string{
		"nomatch  a-org/.github", `"enabled: true"=no`, `"dependencyDashboard: true"=yes`,
		`contains "enabled: true": 0 of 1`, `contains "dependencyDashboard: true": 1 of 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestNoMatchHiddenWithoutList(t *testing.T) {
	f := &fake{orgs: []string{"a-org"}, files: map[string]answer{p("a-org", ".github"): {200, "x"}}}
	_, out, _ := drive(t, f, "--file", "config.js", "--contains", "y")
	if strings.Contains(out, "nomatch  a-org") || !strings.Contains(out, "nomatch 1") {
		t.Fatalf("a nomatch line without --list, or an uncounted nomatch:\n%s", out)
	}
}

func TestNoFileOnBothRepos(t *testing.T) {
	f := &fake{orgs: []string{"a-org"}}
	code, out, _ := drive(t, f, "--file", "config.js", "--list", "--contains", "x")
	if code != 0 || !strings.Contains(out, "nofile   a-org") || !strings.Contains(out, "nofile 1") {
		t.Fatalf("code %d\n%s", code, out)
	}
	if n := f.hits.Load(); n != 3 { // the listing and one call per repository
		t.Errorf("%d calls, want 3", n)
	}
}

func TestFallsBackToTheSecondRepo(t *testing.T) {
	f := &fake{orgs: []string{"a-org"}, files: map[string]answer{
		p("a-org", "renovate-runner"): {200, "enabled: true"},
	}}
	code, out, _ := drive(t, f, "--file", "config.js", "--contains", "enabled: true")
	if code != 0 || !strings.Contains(out, "match    a-org/renovate-runner") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestFirstRepoWins(t *testing.T) {
	f := &fake{orgs: []string{"a-org"}, files: map[string]answer{
		p("a-org", ".github"):         {200, "old"},
		p("a-org", "renovate-runner"): {200, "enabled: true"},
	}}
	_, out, _ := drive(t, f, "--file", "config.js", "--list", "--contains", "enabled: true")
	if !strings.Contains(out, "nomatch  a-org/.github") || strings.Contains(out, "renovate-runner") {
		t.Fatalf("the second repository was read or reported:\n%s", out)
	}
}

// unreadScenario is the case the unread tests share: one organisation answers
// 403, another 500, a third is fine.
func unreadScenario(t *testing.T) (int, string, string) {
	f := &fake{orgs: []string{"fine", "forbidden", "broken"}, files: map[string]answer{
		p("fine", ".github"):      {200, "enabled: true"},
		p("forbidden", ".github"): {403, "rate limited"},
		p("broken", ".github"):    {500, "boom"},
		// A second repository holding the file must NOT answer for a repository
		// that failed: the failed one might have it too.
		p("broken", "renovate-runner"): {200, "enabled: true"},
	}}
	return drive(t, f, "--file", "config.js", "--contains", "enabled: true")
}

// checkUnread returns what is wrong with the output of unreadScenario, or "".
func checkUnread(code int, out string) string {
	var bad []string
	if code != 1 {
		bad = append(bad, fmt.Sprintf("exit %d, want 1", code))
	}
	for _, want := range []string{"unread   forbidden", "HTTP 403", "unread   broken", "HTTP 500", "unread 2"} {
		if !strings.Contains(out, want) {
			bad = append(bad, "missing "+want)
		}
	}
	if strings.Contains(out, "broken/renovate-runner") {
		bad = append(bad, "fell back past a failed repository")
	}
	return strings.Join(bad, "; ")
}

func TestForbiddenAndServerErrorAreUnread(t *testing.T) {
	code, out, errOut := unreadScenario(t)
	if bad := checkUnread(code, out); bad != "" {
		t.Fatalf("%s\n%s", bad, out)
	}
	if !strings.Contains(errOut, "unread") {
		t.Errorf("nothing on stderr: %q", errOut)
	}
}

// TestUnreadTestFailsWhenErrorsBecomeNofile is the mutation check: with the
// error branch turned into nofile, the test above must fail. If this passes the
// test above is vacuous.
func TestUnreadTestFailsWhenErrorsBecomeNofile(t *testing.T) {
	failVerdict = vNoFile
	t.Cleanup(func() { failVerdict = vUnread })
	code, out, _ := unreadScenario(t)
	if checkUnread(code, out) == "" {
		t.Fatalf("the unread test still passes with errors read as nofile:\n%s", out)
	}
}

func TestReserveStopIsAFloor(t *testing.T) {
	files := map[string]answer{}
	var orgs []string
	for i := 0; i < 6; i++ {
		o := fmt.Sprintf("org%d", i)
		orgs = append(orgs, o)
		files[p(o, ".github")] = answer{200, "enabled: true"}
	}
	rem := &atomic.Int64{}
	rem.Store(1002) // the listing leaves 1001; two reads leave 999 < 1000
	f := &fake{orgs: orgs, files: files, remaining: rem}
	code, out, errOut := drive(t, f, "--workers", "1", "--file", "config.js", "--contains", "enabled: true")
	if code != 1 {
		t.Fatalf("code %d\n%s", code, out)
	}
	if !strings.Contains(errOut, "FLOOR") || !strings.Contains(errOut, "stopped at the reserve") {
		t.Errorf("no floor message: %q", errOut)
	}
	if !strings.Contains(out, "match 2") || strings.Contains(out, "match 6") {
		t.Errorf("wrong counts:\n%s", out)
	}
	if n := f.hits.Load(); n > 4 {
		t.Errorf("%d calls: kept reading past the reserve", n)
	}
}

func TestHelpMakesNoAPICall(t *testing.T) {
	f := &fake{orgs: []string{"a"}}
	called := false
	srv := httptest.NewServer(f)
	defer srv.Close()
	sweepBase = srv.URL
	old := readToken
	readToken = func(string) (string, error) { called = true; return "x", nil }
	defer func() { sweepBase = ""; readToken = old }()
	var o, e bytes.Buffer
	if code := cli.Drive(RootCmd(), []string{"-h"}, &o, &e); code != 0 {
		t.Fatalf("code %d", code)
	}
	if f.hits.Load() != 0 || called || o.Len() != 0 {
		t.Fatalf("-h touched the API (%d hits), the token (%v) or stdout", f.hits.Load(), called)
	}
	if !strings.Contains(e.String(), "--contains") {
		t.Errorf("no help on stderr: %q", e.String())
	}
}

func TestUsageErrorsAreExit2AndCallNothing(t *testing.T) {
	for _, args := range [][]string{
		{"--contains", "x"},                                         // no --file
		{"--file", "config.js"},                                     // no --contains
		{"--file", "../x", "--contains", "x"},                       // traversal
		{"--file", "config.js", "--contains", ""},                   // empty needle
		{"--file", "config.js", "--contains", "x", "--orgs", "a/b"}, // not a name
		{"--file", "config.js", "--contains", "x", "--workers", "0"},
	} {
		f := &fake{orgs: []string{"a"}}
		if code, _, _ := drive(t, f, args...); code != 2 || f.hits.Load() != 0 {
			t.Errorf("%v: code %d, %d calls", args, code, f.hits.Load())
		}
	}
}

func TestOrgsNarrowsTheListing(t *testing.T) {
	f := &fake{orgs: []string{"a", "b"}, files: map[string]answer{p("b", ".github"): {200, "x"}}}
	code, out, _ := drive(t, f, "--orgs", "b", "--file", "config.js", "--contains", "x")
	if code != 0 || !strings.Contains(out, "1 organisations") {
		t.Fatalf("code %d\n%s", code, out)
	}
	for _, path := range f.paths {
		if path == "/user/orgs" {
			t.Error("listed the organisations although --orgs was given")
		}
	}
}

func TestEmptyOrgListIsNotClean(t *testing.T) {
	f := &fake{}
	code, out, _ := drive(t, f, "--file", "config.js", "--contains", "x")
	if code != 2 || strings.Contains(out, "totals") {
		t.Fatalf("code %d\n%s", code, out)
	}
}

func TestListingFailureIsExit2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	sweepBase = srv.URL
	old := readToken
	readToken = func(string) (string, error) { return "x", nil }
	defer func() { sweepBase = ""; readToken = old }()
	var o, e bytes.Buffer
	if code := cli.Drive(RootCmd(), []string{"--file", "config.js", "--contains", "x"}, &o, &e); code != 2 || strings.Contains(o.String(), "totals") {
		t.Fatalf("code %d, out %q", code, o.String())
	}
}
