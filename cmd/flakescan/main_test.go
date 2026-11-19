package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// The log as GitHub serves it: every line carries an RFC3339Nano timestamp,
// and the failure's own lines are indented under the --- FAIL line. These are
// the real numbers from go-crdt/collab run 39069166645, attempt 1.
const ghLog = `2026-10-17T21:03:14.0000000Z === RUN   TestWhatEverybodyHasSeenAdvances
2026-10-17T21:03:34.0100000Z --- FAIL: TestWhatEverybodyHasSeenAdvances (20.01s)
2026-10-17T21:03:34.0100000Z     stable_test.go:114: what everybody has certainly seen stalled at 142 of 160 operations
2026-10-17T21:03:34.0100000Z         site 5: acknowledged up to 14 of 20  <-- behind
2026-10-17T21:03:34.0100000Z         site 8: acknowledged up to 20 of 20
2026-10-17T21:03:34.0200000Z FAIL
2026-10-17T21:03:34.0200000Z FAIL	github.com/go-crdt/collab	72.094s
`

func TestFailureBlocksReadsAStampedLog(t *testing.T) {
	got := failureBlocks(ghLog, "TestWhatEverybodyHasSeenAdvances", 40)
	if len(got) != 1 {
		t.Fatalf("got %d block(s), want 1: %q", len(got), got)
	}
	block := got[0]
	if len(block) != 4 {
		t.Fatalf("got %d line(s), want 4 (the FAIL line and three under it): %q", len(block), block)
	}
	if !strings.HasPrefix(block[0], "--- FAIL: TestWhatEverybodyHasSeenAdvances") {
		t.Errorf("first line is %q", block[0])
	}
	if !strings.Contains(block[1], "142 of 160") {
		t.Errorf("second line is %q, want the stall message", block[1])
	}
	// ⛔ The timestamp must be gone: with it, every line is "more indented"
	// than the last and the whole log reads as one failure.
	for _, l := range block {
		if strings.Contains(l, "2026-10-17T21:03") {
			t.Errorf("line still carries its timestamp: %q", l)
		}
	}
	// FAIL and the package line are NOT part of the block: they are not
	// indented under it.
	for _, l := range block {
		if strings.HasPrefix(l, "FAIL") {
			t.Errorf("block swallowed the package verdict: %q", l)
		}
	}
}

func TestASubtestAnswersForItsParentAndNotTheReverse(t *testing.T) {
	log := `--- FAIL: TestTheBacklogBoundsHowManyMayEditAtOnce/a_participant_that_stops_reading_is_disconnected (15.00s)
    load_test.go:156: a participant that read nothing was never disconnected
`
	if n := len(failureBlocks(log, "TestTheBacklogBoundsHowManyMayEditAtOnce", 40)); n != 1 {
		t.Errorf("parent got %d block(s), want 1", n)
	}
	if n := len(failureBlocks(log, "TestTheBacklogBoundsHowManyMayEditAtOnce/a_participant_that_stops_reading_is_disconnected", 40)); n != 1 {
		t.Errorf("the subtest itself got %d block(s), want 1", n)
	}
	// The converse: asking for a subtest must not be answered by the parent.
	if n := len(failureBlocks("--- FAIL: TestA (1.00s)\n    a_test.go:1: no\n", "TestA/case", 40)); n != 0 {
		t.Errorf("a bare parent answered a request for its subtest: %d block(s)", n)
	}
}

// ⛔ A PREFIX IS NOT A NAME. TestStable and TestStableMeet are different tests,
// and a census that conflates them counts somebody else's failures.
func TestANameIsNotAPrefix(t *testing.T) {
	log := "--- FAIL: TestStableMeet (0.10s)\n    x_test.go:9: no\n"
	if n := len(failureBlocks(log, "TestStable", 40)); n != 0 {
		t.Errorf("TestStable matched TestStableMeet: %d block(s)", n)
	}
	if n := len(failureBlocks(log, "TestStableMeet", 40)); n != 1 {
		t.Errorf("TestStableMeet did not match itself: %d block(s)", n)
	}
}

func TestAnotherTestsFailureIsNotOurs(t *testing.T) {
	log := `--- FAIL: TestOther (1.00s)
    other_test.go:1: something else
    more of somebody else's output
--- FAIL: TestMine (2.00s)
    mine_test.go:2: mine
`
	got := failureBlocks(log, "TestMine", 40)
	if len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("got %v, want one block of two lines", got)
	}
	if !strings.Contains(got[0][1], "mine") {
		t.Errorf("block is %q", got[0])
	}
}

func TestTheQuotedLinesAreBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("--- FAIL: TestLoud (1.00s)\n")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "    line %d\n", i)
	}
	got := failureBlocks(b.String(), "TestLoud", 5)
	if len(got) != 1 {
		t.Fatalf("got %d block(s)", len(got))
	}
	if len(got[0]) != 5 {
		t.Errorf("got %d line(s), want the cap of 5", len(got[0]))
	}
}

// ⛔ continue-on-error leaves the JOB green while a step under it failed, which
// is the lane configured not to complain -- exactly the one worth reading.
func TestAFailedStepInsideAGreenJobIsRead(t *testing.T) {
	green := job{Conclusion: "success"}
	if needsLog(green) {
		t.Error("a wholly green job was read")
	}
	soft := job{Conclusion: "success"}
	soft.Steps = append(soft.Steps, struct {
		Name       string `json:"name"`
		Conclusion string `json:"conclusion"`
	}{Name: "Test", Conclusion: "failure"})
	if !needsLog(soft) {
		t.Error("a green job with a failed step was skipped")
	}
	if failedStep(soft) != "Test" {
		t.Errorf("failedStep = %q, want Test", failedStep(soft))
	}
	if !needsLog(job{Conclusion: "failure"}) {
		t.Error("a failed job was skipped")
	}
	if needsLog(job{Conclusion: "skipped"}) {
		t.Error("a skipped job was read")
	}
	// A job still running carries an empty conclusion, and an unfinished run
	// is the case #204 records being counted as a failure. It is not one.
	if needsLog(job{Conclusion: ""}) {
		t.Error("an unfinished job was read as a failure")
	}
}

func TestEveryAttemptIsRead(t *testing.T) {
	if got := attemptsOf(run{RunAttempt: 2}); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("got %v, want [1 2]", got)
	}
	if got := attemptsOf(run{RunAttempt: 0}); len(got) != 1 || got[0] != 1 {
		t.Errorf("a run with no attempt count got %v, want [1]", got)
	}
}

func TestSinceIsADateOrAnInstant(t *testing.T) {
	got, err := parseSince("2026-09-25")
	if err != nil {
		t.Fatalf("date: %v", err)
	}
	if want := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("got %s, want %s", got, want)
	}
	if _, err := parseSince("2026-09-25T10:00:00Z"); err != nil {
		t.Errorf("instant: %v", err)
	}
	if _, err := parseSince("last tuesday"); err == nil {
		t.Error("prose was accepted as a boundary")
	}
	if got, err := parseSince(""); err != nil || !got.IsZero() {
		t.Errorf("empty got %s, %v", got, err)
	}
}

// fake answers the paths the census asks for, and nothing else: a path it does
// not know is an error, so a test cannot pass because the code asked the wrong
// question.
type fake struct {
	pages map[string]string
	logs  map[int64]string
	fail  map[int64]error
	asked []string
}

func (f *fake) get(path string) ([]byte, error) {
	f.asked = append(f.asked, path)
	if body, ok := f.pages[path]; ok {
		return []byte(body), nil
	}
	return nil, fmt.Errorf("fake: no answer for %s", path)
}

func (f *fake) logText(_ string, jobID int64) (string, error) {
	if err, ok := f.fail[jobID]; ok {
		return "", err
	}
	if log, ok := f.logs[jobID]; ok {
		return log, nil
	}
	return "", fmt.Errorf("fake: no log for job %d", jobID)
}

// theGreenRun is run 39069166645 as GitHub reports it: attempt 2, conclusion
// SUCCESS, on a pull request -- and its first attempt holds the failure.
func theGreenRun() *fake {
	return &fake{
		pages: map[string]string{
			"repos/go-crdt/collab/actions/runs?page=1&per_page=100": `{"total_count":1,"workflow_runs":[
				{"id":39069166645,"run_attempt":2,"event":"pull_request","head_branch":"test/two-more-survivors-held",
				 "head_sha":"1983bfbb","conclusion":"success","status":"completed",
				 "created_at":"2026-10-17T21:01:23Z","name":"ci","path":".github/workflows/ci.yml"}]}`,
			"repos/go-crdt/collab/actions/runs/39069166645/attempts/1/jobs?page=1&per_page=100": `{"total_count":2,"jobs":[
				{"id":111,"name":"test (ubuntu-latest)","conclusion":"failure","started_at":"2026-10-17T21:01:25Z",
				 "steps":[{"name":"Coverage","conclusion":"failure"}]},
				{"id":112,"name":"arch amd64","conclusion":"success","started_at":"2026-10-17T21:01:25Z","steps":[]}]}`,
			"repos/go-crdt/collab/actions/runs/39069166645/attempts/2/jobs?page=1&per_page=100": `{"total_count":1,"jobs":[
				{"id":113,"name":"test (ubuntu-latest)","conclusion":"success","started_at":"2026-10-17T21:10:00Z","steps":[]}]}`,
		},
		logs: map[int64]string{111: ghLog},
		fail: map[int64]error{},
	}
}

// ⭐ THE WITNESS. A census over runs whose conclusion is "failure" reports zero
// here, because this run is green. Reading attempts finds it.
func TestAGreenRunsEarlierAttemptIsCounted(t *testing.T) {
	f := theGreenRun()
	runs, window, err := listRuns(f, "go-crdt/collab", "", "", "", 100)
	if err != nil {
		t.Fatalf("listRuns: %v", err)
	}
	if len(runs) != 1 || window != 1 {
		t.Fatalf("got %d run(s), window %d", len(runs), window)
	}
	if runs[0].Conclusion != "success" {
		t.Fatalf("the witness must be a GREEN run, got %q", runs[0].Conclusion)
	}
	cen := scan(f, "go-crdt/collab", "TestWhatEverybodyHasSeenAdvances", runs, window, 40)
	if len(cen.Occurrences) != 1 {
		t.Fatalf("got %d occurrence(s), want 1: %+v", len(cen.Occurrences), cen.Occurrences)
	}
	o := cen.Occurrences[0]
	if o.Lane != "test (ubuntu-latest)" {
		t.Errorf("lane = %q, want test (ubuntu-latest) -- the lane is the axis a platform hypothesis is about", o.Lane)
	}
	if o.Attempt != 1 || o.Attempts != 2 {
		t.Errorf("attempt %d/%d, want 1/2", o.Attempt, o.Attempts)
	}
	if o.Event != "pull_request" {
		t.Errorf("event = %q", o.Event)
	}
	if cen.Attempts != 2 {
		t.Errorf("read %d attempt(s), want both", cen.Attempts)
	}
	// The green sibling lane's log must not have been fetched: there was
	// nothing in it to read, and a census that reads every log of every run
	// starves the budget it shares.
	if cen.LogsRead != 1 {
		t.Errorf("read %d log(s), want 1", cen.LogsRead)
	}
	if len(cen.Unread) != 0 {
		t.Errorf("unread: %+v", cen.Unread)
	}
}

// ⛔ ONE FAILURE, NOT TWO. `go test` prints the parent's --- FAIL line and the
// subtest's indented under it, and both match a request for the parent. This is
// a counting tool, so counting that pair as two occurrences would be the one
// mistake it cannot afford.
func TestAFailingSubtestInsideItsParentCountsOnce(t *testing.T) {
	log := `2026-10-09T03:00:00.0000000Z --- FAIL: TestTheBacklogBounds (15.00s)
2026-10-09T03:00:00.0000000Z     --- FAIL: TestTheBacklogBounds/a_participant_that_stops_reading (15.00s)
2026-10-09T03:00:00.0000000Z         load_test.go:156: a participant that read nothing was never disconnected
2026-10-09T03:00:00.0000000Z FAIL
`
	got := failureBlocks(log, "TestTheBacklogBounds", 40)
	if len(got) != 1 {
		t.Fatalf("got %d block(s), want 1: %q", len(got), got)
	}
	// And the subtest's lines are kept, because they are what names the case.
	joined := strings.Join(got[0], "\n")
	for _, want := range []string{"a_participant_that_stops_reading", "load_test.go:156"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the block lost %q:\n%s", want, joined)
		}
	}
	// Two SEPARATE top-level failures of the same test in one log are two.
	two := "--- FAIL: TestA (1.00s)\n    a_test.go:1: first\n--- FAIL: TestA (2.00s)\n    a_test.go:2: second\n"
	if n := len(failureBlocks(two, "TestA", 40)); n != 2 {
		t.Errorf("two top-level failures counted as %d", n)
	}
}

// ⛔ A matrix makes a run's job list long, and one page is not every job.
func TestEveryPageOfJobsIsRead(t *testing.T) {
	f := &fake{
		pages: map[string]string{
			"repos/o/r/actions/runs/1/attempts/1/jobs?page=1&per_page=100": `{"total_count":2,"jobs":[
				{"id":1,"name":"lane one","conclusion":"success","steps":[]}]}`,
			"repos/o/r/actions/runs/1/attempts/1/jobs?page=2&per_page=100": `{"total_count":2,"jobs":[
				{"id":2,"name":"lane two","conclusion":"failure","steps":[]}]}`,
		},
		logs: map[int64]string{2: "--- FAIL: TestX (1.00s)\n    x_test.go:1: no\n"},
		fail: map[int64]error{},
	}
	cen := scan(f, "o/r", "TestX", []run{{ID: 1, RunAttempt: 1}}, 1, 40)
	if len(cen.Occurrences) != 1 {
		t.Fatalf("got %d occurrence(s), want 1 -- the failure is on page TWO", len(cen.Occurrences))
	}
	if cen.Occurrences[0].Lane != "lane two" {
		t.Errorf("lane = %q", cen.Occurrences[0].Lane)
	}
	if cen.JobsRead != 2 {
		t.Errorf("read %d job(s), want 2", cen.JobsRead)
	}
}

// ⛔ A SHORT READ IS NOT A SMALLER ANSWER. GitHub says how many jobs the attempt
// has; reading fewer must be reported, not quietly accepted.
func TestAShortJobListIsUnread(t *testing.T) {
	f := &fake{
		pages: map[string]string{
			"repos/o/r/actions/runs/1/attempts/1/jobs?page=1&per_page=100": `{"total_count":3,"jobs":[
				{"id":1,"name":"lane one","conclusion":"success","steps":[]}]}`,
			"repos/o/r/actions/runs/1/attempts/1/jobs?page=2&per_page=100": `{"total_count":3,"jobs":[]}`,
		},
		logs: map[int64]string{},
		fail: map[int64]error{},
	}
	cen := scan(f, "o/r", "TestX", []run{{ID: 1, RunAttempt: 1}}, 1, 40)
	if len(cen.Unread) != 1 {
		t.Fatalf("a short read produced %d unread entr(ies), want 1: %+v", len(cen.Unread), cen.Unread)
	}
	if !strings.Contains(cen.Unread[0].Why, "1 of 3") {
		t.Errorf("the reason does not name the shortfall: %q", cen.Unread[0].Why)
	}
}

// ⛔ THE BUSIEST LANE IS LAST ALPHABETICALLY HERE, DELIBERATELY. The first
// version of this test used "arch amd64" (2) against "test (ubuntu-latest)"
// (1), where the busiest lane is also the alphabetically first one -- so
// deleting the count comparison from the comparator left the test green, and
// mutsweep reported the survivor. A fixture whose two orderings agree cannot
// tell which one the code used.
// ⛔ --max-runs IS A BOUND, so it is asserted AT its edge: boundary mutation
// moved `len(out) < max` to `<=`, and nothing reddened, because no test ever
// gave a window more runs than the bound. One run past the bound is one extra
// request against a budget the whole fleet shares, and a denominator larger
// than the one the operator asked for.
func TestTheRunBoundIsExact(t *testing.T) {
	f := &fake{pages: map[string]string{
		"repos/o/r/actions/runs?page=1&per_page=100": `{"total_count":2,"workflow_runs":[
			{"id":1,"run_attempt":1,"conclusion":"failure"},
			{"id":2,"run_attempt":1,"conclusion":"failure"}]}`,
	}}
	runs, window, err := listRuns(f, "o/r", "", "", "", 1)
	if err != nil {
		t.Fatalf("listRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("--max-runs 1 read %d run(s)", len(runs))
	}
	// The window's REAL size is still reported, so a bounded read cannot be
	// mistaken for the whole history.
	if window != 2 {
		t.Errorf("window = %d, want the 2 GitHub reports", window)
	}
	if len(f.asked) != 1 {
		t.Errorf("asked %d page(s), want 1: %v", len(f.asked), f.asked)
	}

	// ⛔ AND THE BOUND HAS TO BE PUT THE QUESTION ON A FULL PAGE. The fixture
	// above cannot: its page is short, so the loop ends because the page ended
	// and not because the bound was reached -- the mutant `<=` gives the same
	// answer and survives. A window of 200 runs served 100 to a page is where
	// the two differ: the bound must stop the sweep although another page
	// exists.
	var runsJSON strings.Builder
	runsJSON.WriteString(`{"total_count":200,"workflow_runs":[`)
	for i := 1; i <= perPage; i++ {
		if i > 1 {
			runsJSON.WriteString(",")
		}
		fmt.Fprintf(&runsJSON, `{"id":%d,"run_attempt":1,"conclusion":"failure"}`, i)
	}
	runsJSON.WriteString("]}")
	full := &fake{pages: map[string]string{
		"repos/o/r/actions/runs?page=1&per_page=100": runsJSON.String(),
		// Page two exists, and asking for it is the defect.
		"repos/o/r/actions/runs?page=2&per_page=100": `{"total_count":200,"workflow_runs":[
			{"id":101,"run_attempt":1,"conclusion":"failure"}]}`,
	}}
	runs, _, err = listRuns(full, "o/r", "", "", "", 1)
	if err != nil {
		t.Fatalf("listRuns on a full page: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("--max-runs 1 over a 200-run window read %d run(s)", len(runs))
	}
	if len(full.asked) != 1 {
		t.Errorf("asked %d page(s) although the bound was reached on page one: %v", len(full.asked), full.asked)
	}
}

// ⛔ The oracle in BOTH directions: a bound that refuses its own smallest legal
// value is the over-refusal a must-refuse table can never find. Asked of `check`
// rather than of the command, because everything after the check is a GitHub
// read and a unit test must not make one.
func TestTheSmallestLegalBoundIsAccepted(t *testing.T) {
	if _, err := check("go-crdt/collab", "TestX", 1, ""); err != nil {
		t.Errorf("--max-runs 1 was refused: %v", err)
	}
	// And the window bound comes back as the instant GitHub is asked for.
	got, err := check("go-crdt/collab", "TestX", 100, "2026-09-25")
	if err != nil || got != "2026-09-25T00:00:00Z" {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := check("go-crdt/collab", "TestX", 100, ""); err != nil || got != "" {
		t.Errorf("no --since must send no bound, got %q, %v", got, err)
	}
}

func TestTruncateKeepsAStringThatExactlyFits(t *testing.T) {
	if got := truncate("12345", 5); got != "12345" {
		t.Errorf("got %q, want the string unchanged at exactly its length", got)
	}
	if got := truncate("123456", 5); got != "1234…" {
		t.Errorf("got %q, want four runes and the ellipsis", got)
	}
	// Counted in RUNES, so a multi-byte reason is not cut in half.
	if got := truncate("ééééé", 5); got != "ééééé" {
		t.Errorf("got %q, want five runes kept", got)
	}
}

func TestLaneTableCountsByLane(t *testing.T) {
	occs := []occurrence{{Lane: "zz last alphabetically"}, {Lane: "aa first alphabetically"}, {Lane: "zz last alphabetically"}}
	got := byLane(occs)
	if len(got) != 2 {
		t.Fatalf("got %d line(s): %v", len(got), got)
	}
	if !strings.Contains(got[0], "zz last alphabetically") || !strings.HasSuffix(strings.TrimSpace(got[0]), "2") {
		t.Errorf("first line is %q, want the BUSIEST lane (2) although it sorts last", got[0])
	}
	if !strings.Contains(got[1], "aa first alphabetically") {
		t.Errorf("second line is %q", got[1])
	}
	// And the alphabetical order still decides a TIE, which is what makes two
	// runs of the same census comparable.
	tie := byLane([]occurrence{{Lane: "zz"}, {Lane: "aa"}})
	if !strings.Contains(tie[0], "aa") {
		t.Errorf("a tie came out %q first, want the alphabetical order", tie[0])
	}
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var e *cli.Exit
	if !errors.As(err, &e) {
		t.Fatalf("error carries no status: %v", err)
	}
	return e.Code
}

func TestTheVerdictDistinguishesZeroFromUnread(t *testing.T) {
	c := RootCmd()
	var out, errOut bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errOut)

	// A finding is a 1, and it prints itself.
	f := theGreenRun()
	err := report(c, f, "go-crdt/collab", "TestWhatEverybodyHasSeenAdvances", "", "", "", 100, 40)
	if got := exitCode(t, err); got != 1 {
		t.Errorf("a found occurrence exited %d, want 1", got)
	}
	if !strings.Contains(out.String(), "test (ubuntu-latest)") {
		t.Errorf("the report does not name the lane:\n%s", out.String())
	}

	// A test that never failed, with everything readable, is a 0.
	out.Reset()
	errOut.Reset()
	err = report(c, theGreenRun(), "go-crdt/collab", "TestSomethingElseEntirely", "", "", "", 100, 40)
	if got := exitCode(t, err); got != 0 {
		t.Errorf("a clean history exited %d, want 0", got)
	}

	// ⛔ The same question with ONE log unreadable is not zero: it is 2.
	out.Reset()
	errOut.Reset()
	g := theGreenRun()
	g.fail[111] = errors.New("HTTP 410: Not Found (log expired)")
	err = report(c, g, "go-crdt/collab", "TestSomethingElseEntirely", "", "", "", 100, 40)
	if got := exitCode(t, err); got != 2 {
		t.Errorf("an unread log exited %d, want 2", got)
	}
	if !strings.Contains(errOut.String(), "410") {
		t.Errorf("the reason was not reported:\n%s", errOut.String())
	}
}

// An empty window is not a clean bill either: nothing was measured.
func TestAnEmptyWindowIsUnanswerable(t *testing.T) {
	c := RootCmd()
	var out, errOut bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errOut)
	f := &fake{pages: map[string]string{
		"repos/go-crdt/collab/actions/runs?page=1&per_page=100": `{"total_count":0,"workflow_runs":[]}`,
	}}
	err := report(c, f, "go-crdt/collab", "TestAnything", "", "", "", 100, 40)
	if got := exitCode(t, err); got != 2 {
		t.Errorf("an empty window exited %d, want 2", got)
	}
}

// ⛔ EXIT 2 IS NOT ENOUGH TO ASSERT HERE, and mutsweep proved it: deleting the
// `--repo` check, the `--test` check or the `--max-runs` check left all three
// cases still exiting 2 -- because without the flag the command goes on to ask
// GitHub, and a failure on the way to the measurement is also a 2. The status
// cannot tell "I refused your arguments" from "I could not reach the forge", so
// each case asserts the REASON, which only the guard can produce.
// offline points the command at a fake for the whole test, so that no
// assertion -- and no mutation of one -- can reach GitHub.
func offline(t *testing.T, f api) {
	t.Helper()
	prev := newAPI
	newAPI = func() api { return f }
	t.Cleanup(func() { newAPI = prev })
}

func TestRequiredFlagsAreRefusedNotGuessed(t *testing.T) {
	offline(t, &fake{})
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "--repo"},
		{[]string{"--repo", "go-crdt/collab"}, "--test"},
		{[]string{"--test", "TestX"}, "--repo"},
		{[]string{"--repo", "collab", "--test", "TestX"}, "--repo"},
		{[]string{"--repo", "go-crdt/collab", "--test", "TestX", "--max-runs", "0"}, "--max-runs"},
		{[]string{"--repo", "go-crdt/collab", "--test", "TestX", "--since", "yesterday"}, "--since"},
	} {
		var out, errOut bytes.Buffer
		got := cli.Drive(RootCmd(), c.args, &out, &errOut)
		if got != 2 {
			t.Errorf("%v exited %d, want 2 (%s)", c.args, got, strings.TrimSpace(errOut.String()))
			continue
		}
		if !strings.Contains(errOut.String(), c.want) {
			t.Errorf("%v exited 2 for the wrong reason: want a message naming %s, got %q",
				c.args, c.want, strings.TrimSpace(errOut.String()))
		}
		if out.Len() != 0 {
			t.Errorf("%v wrote to stdout before it had run anything: %q", c.args, out.String())
		}
	}
}

// ⛔ RunE MUST NOT DO THE WORK WHEN HELP WAS ASKED FOR, and this asks RunE
// directly because nothing else can: cobra answers --help itself and never
// reaches RunE, so driving the command leaves the guard unexercised and
// mutation shows it surviving. What this pins is the contract cli.HelpRequested
// states -- a command that reads "status 0, carry on" would print its help AND
// run -- not a path cobra takes today.
func TestHelpDoesNotAlsoDoTheWork(t *testing.T) {
	offline(t, &fake{})
	c := RootCmd()
	var out, errOut bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errOut)
	// cobra adds the help flag during Execute, so a command built and not run
	// has none yet: "no such flag -help" is what this said first.
	c.InitDefaultHelpFlag()
	if err := c.Flags().Set("help", "true"); err != nil {
		t.Fatalf("setting help: %v", err)
	}
	if err := c.RunE(c, nil); err != nil {
		t.Errorf("RunE with help requested returned %v, want nil and no work", err)
	}
	if out.Len() != 0 {
		t.Errorf("it wrote a report: %q", out.String())
	}
}

// stamped is the half that makes every other reading possible: without it every
// line of a downloaded log is "more indented" than the one before by the width
// of a timestamp.
func TestStampedStripsOnlyARealTimestamp(t *testing.T) {
	line, ok := stamped("2026-10-17T21:03:34.0100000Z     stable_test.go:114: stalled")
	if !ok || line != "    stable_test.go:114: stalled" {
		t.Errorf("got %q, %v", line, ok)
	}
	// A line with no space at all must come back whole rather than panic.
	if line, ok := stamped("nospace"); ok || line != "nospace" {
		t.Errorf("got %q, %v", line, ok)
	}
	// A first word that is not an instant is not a timestamp.
	if line, ok := stamped("FAIL github.com/go-crdt/collab 72.094s"); ok || !strings.HasPrefix(line, "FAIL") {
		t.Errorf("got %q, %v", line, ok)
	}
	if line, ok := stamped(""); ok || line != "" {
		t.Errorf("empty got %q, %v", line, ok)
	}
}
