package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"fleet/quiet"
)

// The one call this tool must never make. `actions/workflows/{id}/dispatches`
// is one path segment away from the runs listing it reads all day, and a
// dispatch opens real pull requests across an organisation.
func TestReadOnlyRefusesWrites(t *testing.T) {
	for _, args := range [][]string{
		{"api", "-X", "POST", "repos/o/.github/actions/workflows/1/dispatches"},
		{"api", "--method", "PUT", "repos/o/.github"},
		{"api", "repos/o/.github/actions/workflows/1/dispatches"},
		{"api", "repos/o/.github/actions/workflows/1/dispatch"},
		{"api", "repos/o/.github", "-f", "name=x"},
		{"api", "repos/o/.github", "-F", "name=x"},
		{"api", "repos/o/.github", "--field", "name=x"},
		{"api", "repos/o/.github", "--raw-field", "name=x"},
		{"workflow", "run", "renovate.yml", "--repo", "o/.github"},
	} {
		if err := readOnly(args); err == nil {
			t.Errorf("readOnly(%v): want refusal, got none", args)
		}
	}
}

func TestReadOnlyAllowsReads(t *testing.T) {
	for _, args := range [][]string{
		{"api", "user/orgs", "--paginate", "--jq", ".[].login"},
		{"api", "repos/o/.github"},
		{"api", "repos/o/.github/actions/workflows?per_page=100"},
		{"api", "repos/o/.github/actions/workflows/1/runs?event=schedule&per_page=1"},
		{"api", "-H", "Accept: application/vnd.github.raw", "repos/o/.github/contents/x.yml"},
		{"api", "-X", "GET", "search/issues"},
	} {
		if err := readOnly(args); err != nil {
			t.Errorf("readOnly(%v): %v", args, err)
		}
	}
}

func TestIsNotFound(t *testing.T) {
	if !isNotFound(errString("gh: Not Found (HTTP 404)")) {
		t.Error("404 should read as not found")
	}
	if isNotFound(nil) || isNotFound(errString("HTTP 500")) {
		t.Error("only 404 is not-found")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestParseTime(t *testing.T) {
	if got := parseTime("2026-08-30T10:31:48.000+02:00"); got.Format(time.RFC3339) != "2026-08-30T08:31:48Z" {
		t.Errorf("offset timestamp = %v", got)
	}
	if got := parseTime("2026-08-31T10:33:41Z"); got.Format(time.RFC3339) != "2026-08-31T10:33:41Z" {
		t.Errorf("Z timestamp = %v", got)
	}
	if !parseTime("").IsZero() || !parseTime("nonsense").IsZero() {
		t.Error("an unreadable timestamp must be zero, not now")
	}
}

func TestLoadFixture(t *testing.T) {
	rs, now, err := loadFixture("testdata/broken.json")
	if err != nil {
		t.Fatal(err)
	}
	if now.Format(time.RFC3339) != "2026-08-31T14:00:00Z" {
		t.Fatalf("now = %v", now)
	}
	if len(rs) == 0 {
		t.Fatal("no runners")
	}
	if _, _, err := loadFixture("testdata/does-not-exist.json"); err == nil {
		t.Error("missing file: want error")
	}
	if _, _, err := loadFixture("testdata/bad-cron.json"); err == nil || !strings.Contains(err.Error(), "cron") {
		t.Errorf("bad cron: %v", err)
	}
	if _, _, err := loadFixture("testdata/bad-now.json"); err == nil {
		t.Error("bad now: want error")
	}
}

func TestHM(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0h00m"},
		{90 * time.Minute, "1h30m"},
		{7*time.Hour + 42*time.Minute, "7h42m"},
		{-90 * time.Minute, "-1h30m"},
	} {
		if got := hm(tc.d); got != tc.want {
			t.Errorf("hm(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestStamp(t *testing.T) {
	if got := stamp(time.Time{}); got != "never" {
		t.Errorf("zero time = %q, want never", got)
	}
	if got := stamp(parseTime("2026-08-31T10:33:41Z")); got != "2026-08-31 10:33Z" {
		t.Errorf("stamp = %q", got)
	}
}

// read() is where a wrong field name would make every runner in the fleet look
// healthy, silently. So it is exercised against a stub `gh` on PATH, answering
// with the exact JSON shapes the live API returns -- including the two that
// look like success and are not: an empty workflow_runs list, and a repository
// that has no runner in it.
func withFakeGH(t *testing.T, script string) *string {
	t.Helper()
	dir := t.TempDir()
	log := dir + "/calls.log"
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\n" + script + "\n"
	if err := os.WriteFile(dir+"/gh", []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return &log
}

func callLog(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

const stubRepo = `{"archived":false,"pushed_at":"2026-08-31T10:33:41Z"}`
const stubWorkflows = `{"total_count":1,"workflows":[{"id":345781787,"name":"Renovate","path":".github/workflows/renovate.yml","state":"active","created_at":"2026-08-30T10:31:48.000+02:00"}]}`
const stubFile = "name: Renovate\non:\n  schedule:\n    - cron: '0 4 * * *'\n  workflow_dispatch:\n"
const stubRuns = `{"total_count":1,"workflow_runs":[{"created_at":"2026-08-31T10:32:36Z","event":"schedule","conclusion":"success"}]}`

func happyStub(runs string) string {
	return `case "$*" in
  *"/actions/workflows/345781787/runs"*) cat <<'J'
` + runs + `
J
    ;;
  *"/contents/"*) cat <<'F'
` + stubFile + `
F
    ;;
  *"/actions/workflows?"*) cat <<'W'
` + stubWorkflows + `
W
    ;;
  *) cat <<'R'
` + stubRepo + `
R
    ;;
esac`
}

func TestReadHappyPath(t *testing.T) {
	log := withFakeGH(t, happyStub(stubRuns))
	r := read("go-widgets")
	if r.ReadError != "" {
		t.Fatalf("ReadError = %q", r.ReadError)
	}
	if !r.RepoExists || r.Archived || !r.WorkflowFound || r.WorkflowState != "active" {
		t.Fatalf("runner = %+v", r)
	}
	// The +02:00 offset the workflows endpoint returns must land as UTC.
	if got := r.Born.Format(time.RFC3339); got != "2026-08-30T08:31:48Z" {
		t.Errorf("Born = %s", got)
	}
	if r.BornSource != "workflow.created_at" {
		t.Errorf("BornSource = %q -- the file's mtime must never be the clock", r.BornSource)
	}
	if r.Schedule.String() != "0 4 * * *" {
		t.Errorf("Schedule = %q", r.Schedule.String())
	}
	if got := r.LastRun.Format(time.RFC3339); got != "2026-08-31T10:32:36Z" {
		t.Errorf("LastRun = %s", got)
	}
	if got := r.PushedAt.Format(time.RFC3339); got != "2026-08-31T10:33:41Z" {
		t.Errorf("PushedAt = %s", got)
	}
	// The runs listing must be filtered to schedule. A manual dispatch is not
	// evidence the schedule still fires -- and on this fleet, twelve runners
	// that had never run on their own carried successful dispatch runs from
	// rollout day, which is exactly how the failure hides.
	var sawRuns bool
	for _, c := range callLog(t, *log) {
		if strings.Contains(c, "/runs") {
			sawRuns = true
			if !strings.Contains(c, "event=schedule") {
				t.Errorf("runs listing not filtered to schedule: %s", c)
			}
		}
		if strings.Contains(c, "dispatch") {
			t.Fatalf("quietscan must never dispatch: %s", c)
		}
	}
	if !sawRuns {
		t.Error("no runs listing was made")
	}
}

// An empty workflow_runs list is what a runner that has NEVER run returns. It
// deserializes without error and must not be read as a run.
func TestReadNeverRan(t *testing.T) {
	withFakeGH(t, happyStub(`{"total_count":0,"workflow_runs":[]}`))
	r := read("go-macos")
	if r.ReadError != "" || !r.WorkflowFound {
		t.Fatalf("runner = %+v", r)
	}
	if !r.LastRun.IsZero() {
		t.Errorf("LastRun = %v, want zero", r.LastRun)
	}
	if v := quiet.Classify(r, time.Now().UTC(), quiet.DefaultSlack, quiet.DefaultPushWarn).Verdict; v != quiet.NeverFired {
		t.Errorf("verdict = %s, want never_fired", v)
	}
}

func TestReadNoRunnerRepo(t *testing.T) {
	log := withFakeGH(t, `echo "gh: Not Found (HTTP 404)" >&2; exit 1`)
	r := read("go-docutils")
	if r.RepoExists || r.ReadError != "" {
		t.Fatalf("runner = %+v", r)
	}
	if n := len(callLog(t, *log)); n != 1 {
		t.Errorf("calls = %d, want 1: a missing repo ends the read", n)
	}
}

func TestReadArchivedCostsOneCall(t *testing.T) {
	log := withFakeGH(t, `echo '{"archived":true,"pushed_at":"2026-08-30T10:48:52Z"}'`)
	r := read("go-iconoir")
	if !r.RepoExists || !r.Archived {
		t.Fatalf("runner = %+v", r)
	}
	// Nothing below archived can be fixed by a pull request, so nothing below
	// it is worth an API call.
	if n := len(callLog(t, *log)); n != 1 {
		t.Errorf("calls = %d, want 1", n)
	}
}

func TestReadRepoWithNoRunner(t *testing.T) {
	withFakeGH(t, `case "$*" in
  *"/actions/workflows?"*) echo '{"total_count":0,"workflows":[]}' ;;
  *) echo '`+stubRepo+`' ;;
esac`)
	r := read("openweft")
	if !r.RepoExists || r.WorkflowFound {
		t.Fatalf("runner = %+v", r)
	}
	if v := quiet.Classify(r, time.Now().UTC(), quiet.DefaultSlack, quiet.DefaultPushWarn).Verdict; v != quiet.NoRunner {
		t.Errorf("verdict = %s, want no_runner", v)
	}
}

func TestReadDisabledStopsEarly(t *testing.T) {
	log := withFakeGH(t, `case "$*" in
  *"/actions/workflows?"*) echo '{"workflows":[{"id":1,"name":"Renovate","path":".github/workflows/renovate.yml","state":"disabled_inactivity","created_at":"2026-05-30T10:31:48.000+02:00"}]}' ;;
  *) echo '`+stubRepo+`' ;;
esac`)
	r := read("someorg")
	if r.WorkflowState != "disabled_inactivity" {
		t.Fatalf("state = %q", r.WorkflowState)
	}
	if n := len(callLog(t, *log)); n != 2 {
		t.Errorf("calls = %d, want 2", n)
	}
}

// A channel that cannot answer must never read as "fine".
func TestReadErrorsAreNotSilence(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		{"repo unparseable", `echo 'not json'`},
		{"workflows unparseable", `case "$*" in *"/actions/workflows?"*) echo 'not json' ;; *) echo '` + stubRepo + `' ;; esac`},
		{"runs unparseable", `case "$*" in
  *"/runs"*) echo 'not json' ;;
  *"/contents/"*) printf '` + "on:\\n  schedule:\\n    - cron: 0 4 * * *\\n" + `' ;;
  *"/actions/workflows?"*) echo '` + stubWorkflows + `' ;;
  *) echo '` + stubRepo + `' ;;
esac`},
		{"workflow file unreadable", `case "$*" in
  *"/contents/"*) echo 'gh: HTTP 500' >&2; exit 1 ;;
  *"/actions/workflows?"*) echo '` + stubWorkflows + `' ;;
  *) echo '` + stubRepo + `' ;;
esac`},
		{"unparseable cron", `case "$*" in
  *"/contents/"*) printf '` + "on:\\n  schedule:\\n    - cron: nonsense\\n" + `' ;;
  *"/actions/workflows?"*) echo '` + stubWorkflows + `' ;;
  *) echo '` + stubRepo + `' ;;
esac`},
		{"runs refused", `case "$*" in
  *"/runs"*) echo 'gh: HTTP 500' >&2; exit 1 ;;
  *"/contents/"*) printf '` + "on:\\n  schedule:\\n    - cron: 0 4 * * *\\n" + `' ;;
  *"/actions/workflows?"*) echo '` + stubWorkflows + `' ;;
  *) echo '` + stubRepo + `' ;;
esac`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withFakeGH(t, tc.script)
			r := read("someorg")
			if r.ReadError == "" {
				t.Fatalf("want a read error, got %+v", r)
			}
			if v := quiet.Classify(r, time.Now().UTC(), quiet.DefaultSlack, quiet.DefaultPushWarn).Verdict; v != quiet.Unreadable {
				t.Errorf("verdict = %s, want unreadable", v)
			}
		})
	}
	// A workflows listing that 404s is a missing runner, not an outage.
	withFakeGH(t, `case "$*" in *"/actions/workflows?"*) echo 'gh: Not Found (HTTP 404)' >&2; exit 1 ;; *) echo '`+stubRepo+`' ;; esac`)
	if r := read("someorg"); r.ReadError != "" || r.WorkflowFound {
		t.Errorf("404 on workflows = %+v", r)
	}
}

func TestOrgs(t *testing.T) {
	withFakeGH(t, `printf 'go-widgets\n\n  go-macos  \nlibfw\n'`)
	got, err := orgs()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"go-macos", "go-widgets", "libfw"}
	if len(got) != len(want) {
		t.Fatalf("orgs = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orgs = %v, want %v", got, want)
		}
	}
	withFakeGH(t, `echo 'gh: HTTP 401' >&2; exit 1`)
	if _, err := orgs(); err == nil {
		t.Error("want an error when the org listing fails")
	}
}

func TestDetailNeverPanics(t *testing.T) {
	for _, v := range []quiet.Verdict{
		quiet.Healthy, quiet.NotYetDue, quiet.NeverFired, quiet.Overdue,
		quiet.DisabledInactivity, quiet.DisabledManually, quiet.Archived,
		quiet.NoSchedule, quiet.NoRunner, quiet.NoRunnerRepo, quiet.Unreadable,
	} {
		switch v {
		case quiet.NoRunnerRepo, quiet.NoRunner, quiet.Unreadable:
			continue // nothing to say beyond the verdict itself
		}
		if s := detail(quiet.Result{Verdict: v}); s == "" {
			t.Errorf("%s: empty detail", v)
		}
	}
	c, _ := quiet.ParseCron("0 0 30 2 *")
	r := quiet.Result{Verdict: quiet.NoSchedule}
	r.Schedule = quiet.Schedule{c}
	if s := detail(r); !strings.Contains(s, "no reachable occurrence") {
		t.Errorf("unreachable cron detail = %q", s)
	}
	r = quiet.Result{Verdict: quiet.NoRunner}
	r.RepoExists = true
	if s := detail(r); !strings.Contains(s, "last push") {
		t.Errorf("no_runner detail = %q", s)
	}
}
