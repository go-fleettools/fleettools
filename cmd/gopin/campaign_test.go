package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// world is a fake GitHub plus fake agentsync/prwait/ghmerge. NOTHING here
// starts a process: every seam the sweep uses is replaced, because a previous
// fleet tool's tests became a fork bomb by re-executing themselves.
type world struct {
	t     *testing.T
	files map[string]map[string]string // repo -> path -> body
	// quota[repo] is how many more calls on that repo answer 403 "rate limit".
	quota map[string]int
	// rateLimitReads is what `gh api rate_limit` says: always full, which is
	// exactly the lie the 2026-10-10 campaign believed.
	prs      map[string]string // repo -> JSON for `pr list`
	claimed  map[string]string // repo -> holder note (claim refused)
	toolCode map[string]int    // "prwait"/"ghmerge" -> exit code
	reset    int64

	sleeps   []time.Duration
	tools    []string
	ghCalls  []string
	pushed   []string
	released []string
	created  []string
	dir      string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, files: map[string]map[string]string{}, quota: map[string]int{},
		prs: map[string]string{}, claimed: map[string]string{}, toolCode: map[string]int{}}
	pg, pp, pgh, pr, ps, pt, pn, pl, pq, pw, ptd := git, push, ghRaw, ghJSON, sleep, tool, now, logw, quotaRetries, quotaWaitMax, goTidy
	ghJSON = func(args ...string) ([]byte, error) { return callWithQuota(ghRaw, args...) }
	ghRaw = w.gh
	sleep = func(d time.Duration) { w.sleeps = append(w.sleeps, d) }
	now = func() time.Time { return time.Unix(1_000_000, 0) }
	logw = &strings.Builder{}
	tool = w.tool
	quotaRetries, quotaWaitMax = 3, 5*time.Minute
	git = func(dir string, args ...string) (string, error) {
		if args[0] == "clone" {
			repo := strings.TrimSuffix(strings.TrimPrefix(args[len(args)-2], "https://github.com/"), ".git")
			w.dir = args[len(args)-1]
			for p, body := range w.files[repo] {
				full := filepath.Join(w.dir, p)
				_ = os.MkdirAll(filepath.Dir(full), 0o755)
				_ = os.WriteFile(full, []byte(body), 0o644)
			}
		}
		w.ghCalls = append(w.ghCalls, "git "+strings.Join(args, " "))
		return "", nil
	}
	push = func(dir, branch string) (string, error) { w.pushed = append(w.pushed, branch); return "", nil }
	goTidy = func(dir string) (string, error) { return "", nil }
	t.Cleanup(func() {
		git, push, ghRaw, ghJSON, sleep, tool, now, logw, quotaRetries, quotaWaitMax, goTidy = pg, pp, pgh, pr, ps, pt, pn, pl, pq, pw, ptd
	})
	return w
}

func (w *world) gh(args ...string) ([]byte, error) {
	joined := strings.Join(args, " ")
	w.ghCalls = append(w.ghCalls, joined)
	if args[0] == "api" && args[1] == "rate_limit" {
		return []byte(strconv.FormatInt(w.reset, 10)), nil
	}
	repo, tail := "", ""
	switch args[0] {
	case "api":
		parts := strings.SplitN(strings.TrimPrefix(args[1], "repos/"), "/", 3)
		repo = parts[0] + "/" + parts[1]
		if len(parts) == 3 {
			tail = "/" + parts[2]
		}
	default:
		for i, a := range args {
			if a == "--repo" {
				repo = args[i+1]
			}
		}
	}
	if w.quota[repo] > 0 {
		w.quota[repo]--
		return nil, errors.New("HTTP 403: API rate limit exceeded for user ID 42. (https://api.github.com/repos/" + repo + ")")
	}
	switch {
	case args[0] == "pr" && args[1] == "list":
		if s, ok := w.prs[repo]; ok {
			return []byte(s), nil
		}
		return []byte("[]"), nil
	case args[0] == "pr" && args[1] == "create":
		w.created = append(w.created, repo)
		return []byte("https://github.com/" + repo + "/pull/7\n"), nil
	}
	if _, ok := w.files[repo]; !ok && args[0] == "api" {
		return nil, errors.New("HTTP 404: Not Found")
	}
	rest := tail
	switch {
	case rest == "":
		if strings.Contains(joined, ".id") {
			return []byte("1"), nil
		}
		return []byte("main\nfalse\n"), nil
	case rest == "/contents/.github/workflows":
		var names []string
		for p := range w.files[repo] {
			if n, ok := strings.CutPrefix(p, ".github/workflows/"); ok {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			return nil, errors.New("HTTP 404: Not Found")
		}
		return []byte(strings.Join(names, "\n")), nil
	}
	if body, ok := w.files[repo][strings.TrimPrefix(rest, "/contents/")]; ok {
		return []byte(base64.StdEncoding.EncodeToString([]byte(body))), nil
	}
	return nil, errors.New("HTTP 404: Not Found")
}

func (w *world) tool(name string, args ...string) (string, int) {
	w.tools = append(w.tools, name+" "+strings.Join(args, " "))
	switch name {
	case "agentsync":
		repo := args[len(args)-1]
		if args[0] == "release" {
			w.released = append(w.released, repo)
			return "", 0
		}
		if holder, ok := w.claimed[repo]; ok {
			return "REFUSED  " + repo + "\n         held by x\n         \"" + holder + "\"", 1
		}
		return "", 0
	}
	if c, ok := w.toolCode[name]; ok {
		return "line one\nthe last line", c
	}
	return "", 0
}

func (w *world) repo(name, workflow, gomod string) {
	w.files[name] = map[string]string{".github/workflows/ci.yml": workflow}
	if gomod != "" {
		w.files[name]["go.mod"] = gomod
	}
}

const pinned272 = "      - uses: actions/setup-go@v7\n        with:\n          go-version: '1.27.2'\n"

func (w *world) run(args ...string) (int, string, string) {
	w.t.Helper()
	var o, e strings.Builder
	code := run(args, strings.NewReader(""), &o, &e)
	return code, o.String(), e.String()
}

func (w *world) list(repos ...string) string {
	w.t.Helper()
	p := filepath.Join(w.t.TempDir(), "list.txt")
	if err := os.WriteFile(p, []byte(strings.Join(repos, "\n")+"\n"), 0o644); err != nil {
		w.t.Fatal(err)
	}
	return p
}

// REGRESSION 1 (2026-10-10): `gh api rate_limit` read 5000/5000 while every
// real call was a 403. A quota refusal is "wait", never a result.
func TestAQuotaRefusalIsWaitedOutNotReported(t *testing.T) {
	w := newWorld(t)
	w.repo("o/a", pinned272, "")
	w.quota["o/a"] = 2
	w.reset = 1_000_000 + 90 // rate_limit claims a reset in 90 s
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/a"))
	if code != 0 || !strings.Contains(out, "REPIN   o/a") || strings.Contains(out, "ERROR") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	if len(w.sleeps) != 2 || w.sleeps[0] != 92*time.Second {
		t.Fatalf("sleeps = %v; want two waits, the first until the advertised reset+2s", w.sleeps)
	}
}

func TestQuotaDelayBounds(t *testing.T) {
	w := newWorld(t)
	for _, tc := range []struct {
		reset int64
		want  time.Duration
	}{
		{1_000_000 + 10, time.Minute},       // floor
		{1_000_000 + 3600, 5 * time.Minute}, // ceiling
		{0, time.Minute},                    // already past: floor
	} {
		w.reset = tc.reset
		if got := quotaDelay(); got != tc.want {
			t.Errorf("reset %d: %v; want %v", tc.reset, got, tc.want)
		}
	}
	prev := ghRaw
	ghRaw = func(...string) ([]byte, error) { return nil, errors.New("boom") }
	if got := quotaDelay(); got != 5*time.Minute {
		t.Errorf("unreadable reset: %v; want the maximum", got)
	}
	ghRaw = func(...string) ([]byte, error) { return []byte("soon"), nil }
	if got := quotaDelay(); got != 5*time.Minute {
		t.Errorf("garbled reset: %v; want the maximum", got)
	}
	ghRaw = prev
}

func TestCallWithQuotaGivesUpAfterTheRetriesAndSaysWhy(t *testing.T) {
	newWorld(t)
	calls := 0
	_, err := callWithQuota(func(...string) ([]byte, error) {
		calls++
		return nil, errors.New("HTTP 403: You have exceeded a secondary rate limit")
	}, "api", "x")
	if err == nil || calls != quotaRetries+1 {
		t.Fatalf("err=%v calls=%d; want the error after %d calls", err, calls, quotaRetries+1)
	}
	// A non-quota error is an answer at once.
	calls = 0
	_, err = callWithQuota(func(...string) ([]byte, error) { calls++; return nil, errors.New("404 Not Found") }, "api", "x")
	if err == nil || calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	if !isQuota("abuse detection mechanism") || isQuota("HTTP 404") {
		t.Fatal("isQuota misclassifies")
	}
}

// REGRESSION 2: repositories hit by the quota were reported "unreadable" and
// counted as done. They must be DEFERRED and retried in a later pass.
func TestQuotaHitRepositoryIsRetriedInALaterPassNotCountedDone(t *testing.T) {
	w := newWorld(t)
	w.repo("o/a", pinned272, "")
	w.repo("o/b", pinned272, "")
	w.quota["o/b"] = 100 // the first pass cannot get through at all
	// The quota returns during the pause between passes (the only 5 minute
	// sleep: in-call waits floor at one minute here).
	prev := sleep
	sleep = func(d time.Duration) {
		prev(d)
		if d == 5*time.Minute {
			w.quota["o/b"] = 0
		}
	}
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/a", "o/b"), "--passes", "3")
	if !strings.Contains(out, "DEFER   o/b") || !strings.Contains(out, "-- pass 2") {
		t.Fatalf("o/b was not deferred and retried:\n%s", out)
	}
	if code != 0 || !strings.Contains(out, "REPIN   o/b") || strings.Contains(out, "DEFERRED by the quota") {
		t.Fatalf("o/b never completed (code %d):\n%s", code, out)
	}
}

func TestARepositoryThatNeverAnswersEndsDeferredAndFailsTheRun(t *testing.T) {
	w := newWorld(t)
	w.repo("o/a", pinned272, "")
	w.quota["o/a"] = 1000
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/a"), "--passes", "2")
	if code != 1 || !strings.Contains(out, "1 DEFERRED by the quota") || !strings.Contains(out, "needs attention: o/a (deferred)") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if strings.Contains(out, "unreadable") && !strings.Contains(out, "0 unreadable") {
		t.Fatalf("a quota refusal was counted as unreadable:\n%s", out)
	}
}

// REGRESSION 3: a missing field must never silently drop a row. Every listed
// repository ends in exactly one counted state.
func TestEveryRowIsAccountedFor(t *testing.T) {
	w := newWorld(t)
	for _, r := range []string{"o/a", "o/b", "o/c", "o/d"} {
		w.repo(r, pinned272, "")
	}
	// o/d has a workflow without a go-version line at all: a row with a
	// "missing field", which must still be counted.
	w.repo("o/d", "name: ci\n", "")
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/d", "o/a", "o/b", "o/c"), "-limit", "2")
	if code != 0 || strings.Contains(out, "ACCOUNTING FAILED") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if !strings.Contains(out, "4 read") || !strings.Contains(out, "1 already explicit") || !strings.Contains(out, "1 not reached") {
		t.Fatalf("o/d is explicit, o/c is not reached, none is dropped:\n%s", out)
	}
	s := &sweeper{cfg: config{}, out: &strings.Builder{}}
	if s.summary([]string{"o/a"}, map[string]result{"o/a": {st: "wat"}}) != 1 {
		t.Fatal("an unknown status must fail the accounting")
	}
}

func TestSummaryClosesForEveryStatus(t *testing.T) {
	var b strings.Builder
	s := &sweeper{cfg: config{apply: true, from: "1.27.2"}, out: &b}
	repos := []string{"1/a", "2/a", "3/a", "4/a", "5/a", "6/a", "7/a", "8/a", "9/a", "10/a", "11/a"}
	final := map[string]result{
		"1/a": {st: stClean}, "2/a": {st: stReported, alias: true}, "3/a": {st: stOpened, opened: true, repin: true},
		"4/a": {st: stMerged, resumed: true}, "5/a": {st: stRed}, "6/a": {st: stUnread}, "7/a": {st: stClaimed},
		"8/a": {st: stArchived}, "9/a": {st: stFailed}, "10/a": {st: stDeferred},
	}
	code := s.summary(repos, final)
	if code != 1 || strings.Contains(b.String(), "ACCOUNTING FAILED") {
		t.Fatalf("code=%d\n%s", code, b.String())
	}
	for _, want := range []string{"1 merged", "1 RED", "1 checks unreadable", "1 claimed by others", "1 archived and skipped", "1 not reached", "1 resumed from state", "1 to move from 1.27.2"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("summary lacks %q:\n%s", want, b.String())
		}
	}
}

func TestClaimIsTakenBeforeEditingAndReleasedAfter(t *testing.T) {
	w := newWorld(t)
	w.repo("o/a", pinned272, "")
	w.repo("o/held", pinned272, "")
	w.claimed["o/held"] = "merge+release"
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/a", "o/held"), "-apply", "-claim", "-pause", "0s")
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if !strings.Contains(out, "claimed by another session: REFUSED o/held") || !strings.Contains(out, "1 claimed by others") {
		t.Fatalf("refusal not reported:\n%s", out)
	}
	if len(w.created) != 1 || w.created[0] != "o/a" {
		t.Fatalf("created = %v; the claimed repository must not be edited", w.created)
	}
	if len(w.released) != 1 || w.released[0] != "o/a" {
		t.Fatalf("released = %v", w.released)
	}
	if !strings.Contains(strings.Join(w.tools, "\n"), `agentsync claim --note Go 1.27.3 pin (gopin) o/a`) {
		t.Fatalf("tools = %v", w.tools)
	}
}

func TestFollowMergesGreenAndListsRed(t *testing.T) {
	w := newWorld(t)
	w.repo("o/green", pinned272, "")
	w.repo("o/red", pinned272, "")
	w.repo("o/blind", pinned272, "")
	w.repo("o/refused", pinned272, "")
	// prwait/ghmerge answer per call, in order.
	codes := map[string][]int{"prwait": {0, 1, 2, 0}, "ghmerge": {0, 1}}
	w.toolCode = nil
	base := tool
	tool = func(name string, args ...string) (string, int) {
		out, c := base(name, args...)
		if q := codes[name]; len(q) > 0 {
			codes[name] = q[1:]
			return "line one\nthe last line", q[0]
		}
		return out, c
	}
	st := filepath.Join(t.TempDir(), "s.json")
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/green", "o/red", "o/blind", "o/refused"),
		"-apply", "-follow", "-pause", "0s", "-state", st)
	if code != 1 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	for _, want := range []string{"1 merged", "2 RED", "1 checks unreadable", "checks not green: the last line", "merge refused: the last line",
		"needs attention: o/blind (unread), o/red (red), o/refused (red)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	joined := strings.Join(w.tools, "\n")
	if !strings.Contains(joined, "prwait -timeout 30m0s o/green#7") || !strings.Contains(joined, "ghmerge o/green 7") {
		t.Fatalf("tools:\n%s", joined)
	}
	if strings.Contains(joined, "ghmerge o/red") {
		t.Fatalf("a red pull request was merged:\n%s", joined)
	}
}

func TestStateFileSkipsFinishedAndResumesFollowing(t *testing.T) {
	w := newWorld(t)
	w.repo("o/done", pinned272, "")
	w.repo("o/open", pinned272, "")
	w.repo("o/new", pinned272, "")
	st := filepath.Join(t.TempDir(), "sub", "s.json")
	seed := stateFile{From: "1.27.2", Version: "1.27.3", Repos: map[string]entry{
		"o/done": {Status: stMerged, PR: "https://github.com/o/done/pull/3"},
		"o/open": {Status: stRed, PR: "https://github.com/o/open/pull/4"},
	}}
	b, _ := json.Marshal(seed)
	_ = os.MkdirAll(filepath.Dir(st), 0o755)
	_ = os.WriteFile(st, b, 0o644)
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/done", "o/open", "o/new"),
		"-apply", "-follow", "-pause", "0s", "-state", st)
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out)
	}
	joined := strings.Join(w.ghCalls, "\n")
	if strings.Contains(joined, "o/done") {
		t.Fatalf("a finished repository was read again:\n%s", joined)
	}
	if !strings.Contains(out, "resumed o/done: merged") || !strings.Contains(out, "resume  o/open: following") {
		t.Fatalf("out:\n%s", out)
	}
	if len(w.created) != 1 || w.created[0] != "o/new" {
		t.Fatalf("created = %v; o/open already has its pull request", w.created)
	}
	// The file now says merged for all three, and was written whole.
	raw, err := os.ReadFile(st)
	if err != nil {
		t.Fatal(err)
	}
	var got stateFile
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"o/done", "o/open", "o/new"} {
		if got.Repos[r].Status != stMerged {
			t.Errorf("%s: %+v", r, got.Repos[r])
		}
	}
	if _, err := os.Stat(st + ".tmp"); err == nil {
		t.Error("temporary state file left behind")
	}
}

func TestStateRefusesAnotherCampaignAndGarbage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	_ = os.WriteFile(p, []byte(`{"from":"1.27.1","version":"1.27.2","repos":{"o/a":{"status":"merged"}}}`), 0o644)
	if _, err := loadState(p, "1.27.2", "1.27.3"); err == nil || !strings.Contains(err.Error(), "use another file") {
		t.Fatalf("err = %v", err)
	}
	_ = os.WriteFile(p, []byte(`{nope`), 0o644)
	if _, err := loadState(p, "1.27.2", "1.27.3"); err == nil {
		t.Fatal("garbage state accepted")
	}
	_ = os.WriteFile(p, []byte(`{"from":"a","version":"b"}`), 0o644)
	s, err := loadState(p, "a", "b")
	if err != nil || s.sf.Repos == nil {
		t.Fatalf("a state with no repos must still be usable: %v", err)
	}
	if _, err := loadState(dir, "a", "b"); err == nil {
		t.Fatal("a directory is not a state file")
	}
	var nilState *state
	if _, ok := nilState.get("x"); ok || nilState.set("x", entry{}) != nil {
		t.Fatal("nil state must remember nothing and fail nothing")
	}
	if got, err := loadState("", "a", "b"); got != nil || err != nil {
		t.Fatal("no path, no state")
	}
	// A state file whose directory cannot be created reports it.
	s2 := &state{path: filepath.Join(p, "x", "s.json"), sf: stateFile{Repos: map[string]entry{}}}
	if s2.set("o/a", entry{}) == nil {
		t.Fatal("set under a file succeeded")
	}
	// ... and the sweep says so rather than pretending.
	w := newWorld(t)
	w.repo("o/a", pinned272, "")
	var b strings.Builder
	sw := &sweeper{cfg: config{}, out: &b, st: s2}
	sw.record("o/a", result{st: stMerged})
	if !strings.Contains(b.String(), "state NOT saved") {
		t.Fatalf("out = %q", b.String())
	}
}

func TestExistingRenovatePullRequestIsFollowedNotDuplicated(t *testing.T) {
	w := newWorld(t)
	w.repo("o/a", pinned272, "")
	w.prs["o/a"] = `[{"url":"https://github.com/o/a/pull/9","title":"Update dependency go to v1.27.3","headRefName":"renovate/go-1.x"}]`
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/a"), "-apply", "-follow", "-pause", "0s")
	if code != 0 || len(w.created) != 0 || len(w.pushed) != 0 {
		t.Fatalf("code=%d created=%v pushed=%v\n%s", code, w.created, w.pushed, out)
	}
	if !strings.Contains(out, "existing pull request https://github.com/o/a/pull/9") || !strings.Contains(out, "1 merged") {
		t.Fatalf("out:\n%s", out)
	}
	if !strings.Contains(strings.Join(w.tools, "\n"), "ghmerge o/a 9") {
		t.Fatalf("tools = %v", w.tools)
	}
	// Our own earlier branch counts too; other titles do not.
	for _, tc := range []struct{ prs, want string }{
		{`[{"url":"u1","title":"x","headRefName":"go-1273-moved-from-1272"}]`, "u1"},
		{`[{"url":"u2","title":"Update dependency go to 1.27.3","headRefName":"r"}]`, "u2"},
		{`[{"url":"u3","title":"Update dependency golang to v1.27.3","headRefName":"r"}]`, ""},
	} {
		w.prs["o/a"] = tc.prs
		u, _, err := existingPR("o/a", "go-1273-moved-from-1272", "1.27.3")
		if err != nil || u != tc.want {
			t.Errorf("%s: %q %v", tc.prs, u, err)
		}
	}
	w.prs["o/a"] = "not json"
	if _, _, err := existingPR("o/a", "b", "1.27.3"); err == nil {
		t.Error("garbled pr list accepted")
	}
	w.quota["o/a"] = 100
	if _, _, err := existingPR("o/a", "b", "1.27.3"); err == nil {
		t.Error("quota error lost")
	}
}

func TestOpenFailuresAreReportedPerRepository(t *testing.T) {
	w := newWorld(t)
	w.repo("o/a", pinned272, "")
	w.repo("o/arch", pinned272, "")
	prev := git
	git = func(dir string, args ...string) (string, error) {
		if args[0] == "commit" {
			return "hook said no", errors.New("exit 1")
		}
		return prev(dir, args...)
	}
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/a"), "-apply", "-pause", "0s")
	if code != 1 || !strings.Contains(out, "pull request FAILED: commit") || !strings.Contains(out, "1 unreadable") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	git = prev
	// Archived: a skip, not a failure.
	base := ghRaw
	ghRaw = func(args ...string) ([]byte, error) {
		if args[0] == "api" && args[1] == "repos/o/arch" && strings.Contains(strings.Join(args, " "), "default_branch") {
			return []byte("main\ntrue\n"), nil
		}
		return base(args...)
	}
	code, out, _ = w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/arch"), "-apply", "-pause", "0s")
	if code != 0 || !strings.Contains(out, "skipped: o/arch is archived") || !strings.Contains(out, "1 archived and skipped") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	// A quota that outlasts the retries during the open defers the repository.
	ghRaw = func(args ...string) ([]byte, error) {
		if args[0] == "pr" && args[1] == "create" {
			return nil, errors.New("HTTP 403: API rate limit exceeded for user ID 42")
		}
		return base(args...)
	}
	code, out, _ = w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/a"), "-apply", "-pause", "0s", "--passes", "1")
	if code != 1 || !strings.Contains(out, "DEFER   o/a") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestInspectFailureIsAnErrorRowNotADroppedOne(t *testing.T) {
	w := newWorld(t)
	w.repo("o/a", pinned272, "")
	base := ghRaw
	ghRaw = func(args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "contents/.github/workflows/ci.yml") {
			return nil, errors.New("HTTP 500")
		}
		return base(args...)
	}
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/a"))
	if code != 1 || !strings.Contains(out, "ERROR   o/a: HTTP 500") || !strings.Contains(out, "1 unreadable") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestGuardProbesWithARealCallAndDefersOnQuota(t *testing.T) {
	w := newWorld(t)
	w.repo("o/a", pinned272, "")
	w.repo("o/gone", pinned272, "")
	delete(w.files, "o/gone") // a 404 on the probe is not a quota: inspect then reports it
	w.quota["o/a"] = 1000
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/a"), "--guard", "--passes", "1")
	if code != 1 || !strings.Contains(out, "DEFER   o/a") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if !strings.Contains(strings.Join(w.ghCalls, "\n"), "api repos/o/a --jq .id") {
		t.Fatalf("no probe call: %v", w.ghCalls)
	}
	code, out, _ = w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/gone"), "--guard")
	if code != 0 || strings.Contains(out, "DEFER") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

func TestGomodModeRaisesTheDirectiveReadThroughGoVersionFile(t *testing.T) {
	w := newWorld(t)
	wf := "      - uses: actions/setup-go@v7\n        with:\n          go-version-file: go.mod\n"
	w.repo("o/mod", wf, "module x\n\ngo 1.27.2\n\nrequire y v1.0.0 // go 1.27.2\n")
	w.files["o/mod"]["go.sum"] = "y v1.0.0 h1:x\n"
	w.repo("o/newer", wf, "module x\n\ngo 1.28.0\n")
	w.repo("o/versionfile", "          go-version-file: .go-version\n", "")
	w.repo("o/sub", "          go-version-file: 'tools/go.mod' # comment\n", "")
	w.files["o/sub"]["tools/go.mod"] = "module t\n\ngo 1.27.2\n"
	var tidied []string
	goTidy = func(dir string) (string, error) { tidied = append(tidied, filepath.Base(dir)); return "", nil }
	var gitLog []string
	prev := git
	git = func(dir string, args ...string) (string, error) {
		gitLog = append(gitLog, strings.Join(args, " "))
		return prev(dir, args...)
	}
	// without --gomod the repository is invisible, which is the old behaviour
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/mod"))
	if code != 0 || strings.Contains(out, "REPIN") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	code, out, _ = w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/mod", "o/newer", "o/versionfile", "o/sub"), "--gomod", "--apply", "-pause", "0s")
	if code != 0 || !strings.Contains(out, "REPIN   o/mod: go-version-file go.mod (go 1.27.2 -> 1.27.3)") ||
		!strings.Contains(out, "REPIN   o/sub: ") || strings.Contains(out, "REPIN   o/newer") || strings.Contains(out, "REPIN   o/versionfile") {
		t.Fatalf("code=%d\n%s", code, out)
	}
	if len(tidied) != 2 {
		t.Fatalf("tidied = %v", tidied)
	}
	joined := strings.Join(gitLog, "\n")
	// by NAME: go.mod and go.sum, never `commit -a` over whatever tidy touched
	if !strings.Contains(joined, "add -- go.mod go.sum") || !strings.Contains(joined, "add -- tools/go.mod") {
		t.Fatalf("git log:\n%s", joined)
	}
	body := prBody(finding{GoModFiles: []string{"go.mod"}}, "1.27.3", "1.27.2")
	if !strings.Contains(body, "go-version-file") || strings.Contains(body, "is **not** raised") {
		t.Fatalf("body:\n%s", body)
	}
	msg := commitMessage(finding{GoModFiles: []string{"go.mod"}}, "1.27.3", "1.27.2")
	if !strings.Contains(msg, "go-version-file") || strings.Contains(msg, "go.mod is not raised") {
		t.Fatalf("commit message:\n%s", msg)
	}
	// tidy failing aborts that repository with the reason, and pushes nothing.
	goTidy = func(string) (string, error) { return "no network", errors.New("exit 1") }
	w.pushed = nil
	code, out, _ = w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/mod"), "--gomod", "--apply", "-pause", "0s")
	if code != 1 || !strings.Contains(out, "go mod tidy in go.mod") || len(w.pushed) != 0 {
		t.Fatalf("code=%d pushed=%v\n%s", code, w.pushed, out)
	}
	// an unreadable go.mod is an error row
	delete(w.files["o/mod"], "go.mod")
	code, out, _ = w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/mod"), "--gomod")
	if code != 1 || !strings.Contains(out, "ERROR   o/mod") {
		t.Fatalf("code=%d\n%s", code, out)
	}
}

// -from must not raise go.mod, as its pull request body says. It did, for a
// repository whose directive happened to be older than the target.
func TestMoveDoesNotRaiseGoModLikeItsBodySays(t *testing.T) {
	w := newWorld(t)
	w.repo("o/a", pinned272, "module x\n\ngo 1.27.0\n")
	var wrote bool
	prev := push
	push = func(dir, branch string) (string, error) {
		b, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
		wrote = strings.Contains(string(b), "1.27.3")
		return prev(dir, branch)
	}
	code, out, _ := w.run("-from", "1.27.2", "-version", "1.27.3", "-list", w.list("o/a"), "-apply", "-pause", "0s")
	if code != 0 || wrote {
		t.Fatalf("code=%d go.mod raised=%v\n%s", code, wrote, out)
	}
}

func TestCLIShape(t *testing.T) {
	w := newWorld(t)
	for _, tc := range []struct {
		args []string
		code int
		in   string
	}{
		{[]string{"-h"}, 0, "Usage"},
		{[]string{"--bogus"}, 2, "unknown flag"},
		{[]string{"extra"}, 2, "unknown command"},
		{[]string{"-version", "1.27.3", "-from", "1.27.3"}, 2, "same version"},
		{[]string{"-version", "1.27.2", "-from", "1.27.3"}, 2, "going back is not a sweep"},
		{[]string{"-version", "nope"}, 2, "gopin:"},
		{[]string{"-version", "1.27.3", "-from", "nope"}, 2, "--from"},
		{[]string{"-version", "1.27.3", "-gomod"}, 2, "--gomod needs --from"},
		{[]string{"-version", "1.27.3", "-follow"}, 2, "need --apply"},
		{[]string{"-version", "1.27.3", "-list", "/nonexistent/list"}, 2, "no such file"},
		{[]string{"-version", "1.27.3", "-list", w.list("not-a-repo")}, 2, "is not org/repo"},
		{[]string{"-version", "1.27.3", "-state", t.TempDir(), "-list", w.list("o/a")}, 2, "is a directory"},
		{[]string{"-version", "1.27.3", "-list", w.list("o/a"), "--passes", "0"}, 0, ""},
	} {
		code, out, errs := w.run(tc.args...)
		if code != tc.code || !strings.Contains(out+errs, tc.in) {
			t.Errorf("%v: code=%d (want %d) out=%q err=%q", tc.args, code, tc.code, out, errs)
		}
	}
	// stdin is the default list
	var o, e strings.Builder
	if code := run([]string{"-version", "1.27.3"}, strings.NewReader("o/a\n"), &o, &e); code != 0 || !strings.Contains(o.String(), "1 read") {
		t.Fatalf("stdin list: %d %s %s", code, o.String(), e.String())
	}
	if got := legacyArgs([]string{"-claim", "-pause=1s", "-h", "--apply", "-x", "5"}); strings.Join(got, " ") != "--claim --pause=1s -h --apply -x 5" {
		t.Fatalf("legacyArgs = %v", got)
	}
	if got := legacyArgs(nil); got == nil {
		t.Fatal("legacyArgs(nil) must not be nil: cobra would read os.Args")
	}
}

func TestToolReportsExitStatusAndStartFailure(t *testing.T) {
	// `true` and `false` are not this program: nothing re-executes the test binary.
	if out, code := tool("true"); code != 0 || out != "" {
		t.Fatalf("true: %q %d", out, code)
	}
	if _, code := tool("false"); code != 1 {
		t.Fatalf("false: %d", code)
	}
	if _, code := tool("gopin-no-such-tool-anywhere"); code != -1 {
		t.Fatalf("missing: %d", code)
	}
}

func TestGoTidyRunsInTheDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := goTidy(dir); err == nil {
		t.Fatal("go mod tidy with no go.mod must fail")
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := goTidy(dir); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func TestSmallHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/o/r/pull/12\n": "12", "https://x/pull/": "", "garbage": "", "": "",
	} {
		if got := prNumber(in); got != want {
			t.Errorf("prNumber(%q) = %q; want %q", in, got, want)
		}
	}
	if lastLine("a\nb\n") != "b" || lastLine("only") != "only" || oneLine(" a \n b ") != "a b" {
		t.Fatal("line helpers")
	}
	s := &sweeper{cfg: config{follow: true}, out: &strings.Builder{}}
	if st, _ := s.follow("o/r", "no-number"); st != stUnread {
		t.Fatalf("status %q", st)
	}
	_ = fmt.Sprint((&exitErr{msg: "m"}).Error())
}
