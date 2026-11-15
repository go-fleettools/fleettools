package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

// start is the clock every test begins at, chosen so that a reset is readable
// in the messages.
var start = time.Date(2026, 11, 15, 22, 6, 0, 0, time.UTC)

// fake installs a clock whose sleeps advance it, and a gh seam that answers the
// given readings in order. It returns the sleeps that were asked for.
func fake(t *testing.T, readings ...string) (slept *[]time.Duration, reads *int) {
	t.Helper()
	clock := start
	var durations []time.Duration
	var n int

	oldNow, oldSleep, oldRun := now, sleep, runGH
	t.Cleanup(func() { now, sleep, runGH = oldNow, oldSleep, oldRun })

	now = func() time.Time { return clock }
	sleep = func(d time.Duration) {
		durations = append(durations, d)
		clock = clock.Add(d)
	}
	runGH = func(_ ...string) ([]byte, error) {
		if n >= len(readings) {
			t.Fatalf("the command read the budget %d time(s); only %d reading(s) were provided", n+1, len(readings))
		}
		r := readings[n]
		n++
		if strings.HasPrefix(r, "!") {
			return nil, fmt.Errorf("%s", strings.TrimPrefix(r, "!"))
		}
		return []byte(r), nil
	}
	return &durations, &n
}

// answer builds one rate_limit body. resets are seconds from start.
func answer(core, coreLimit int, coreReset int, graphql, graphqlLimit int, graphqlReset int) string {
	type res struct {
		Limit     int   `json:"limit"`
		Remaining int   `json:"remaining"`
		Reset     int64 `json:"reset"`
	}
	b, err := json.Marshal(map[string]any{"resources": map[string]res{
		"core":    {coreLimit, core, start.Add(time.Duration(coreReset) * time.Second).Unix()},
		"graphql": {graphqlLimit, graphql, start.Add(time.Duration(graphqlReset) * time.Second).Unix()},
	}})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func drive(args ...string) (code int, out, errOut string) {
	var o, e bytes.Buffer
	code = cli.Drive(RootCmd(), args, &o, &e)
	return code, o.String(), e.String()
}

// ⛔ The shortest possible lie: a wait nobody gave a condition would answer "the
// budget is fine" having never looked at one.
func TestAWaitWithNoConditionIsRefusedRatherThanPassed(t *testing.T) {
	slept, reads := fake(t)
	code, out, errOut := drive()
	if code != 2 {
		t.Errorf("a run naming no resource exited %d, want 2 (the question could not be put)", code)
	}
	if out != "" {
		t.Errorf("it printed a verdict on stdout: %q", out)
	}
	if !strings.Contains(errOut, "--rest") {
		t.Errorf("the refusal does not name the flag to pass: %q", errOut)
	}
	if *reads != 0 {
		t.Errorf("it read the budget %d time(s) for a condition it did not have", *reads)
	}
	if len(*slept) != 0 {
		t.Errorf("it slept %v with nothing to wait for", *slept)
	}
}

func TestItReturnsAtOnceWhenTheBudgetIsAlreadyThere(t *testing.T) {
	slept, reads := fake(t, answer(4978, 5000, 600, 4745, 5000, 1200))
	code, out, errOut := drive("--rest", "50")
	if code != 0 {
		t.Fatalf("exit %d with 4978 of 5000 left, want 0; stderr=%q", code, errOut)
	}
	if !strings.HasPrefix(out, "PASS core 4978/50 of 5000") {
		t.Errorf("stdout does not carry the reading it passed on: %q", out)
	}
	if len(*slept) != 0 {
		t.Errorf("it slept %v although the threshold was already met", *slept)
	}
	if *reads != 1 {
		t.Errorf("it read the budget %d time(s), want 1", *reads)
	}
}

// ⚠ budget.go measured rate_limit reading 5000/5000 while every call answered
// 403. A success that does not say so reads as "safe to call".
func TestEverySuccessNamesTheLimitItCannotSee(t *testing.T) {
	fake(t, answer(4978, 5000, 600, 4745, 5000, 1200))
	code, _, errOut := drive("--rest", "50")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(errOut, "secondary limit") {
		t.Errorf("the success does not mention the secondary limit: %q", errOut)
	}
}

// ⛔ The point of the whole command: a poll loop spends the budget it waits for,
// so the sleep must be the distance to the reset the API reported.
func TestItSleepsToTheReportedResetAndNotOnAnInterval(t *testing.T) {
	slept, reads := fake(t,
		answer(0, 5000, 540, 4745, 5000, 1200),     // short, reset 9m away
		answer(5000, 5000, 4140, 4745, 5000, 4800), // refilled
	)
	code, out, errOut := drive("--rest", "50")
	if code != 0 {
		t.Fatalf("exit %d, want 0 after the reset; stderr=%q", code, errOut)
	}
	if len(*slept) != 1 {
		t.Fatalf("slept %d time(s), want exactly 1: %v", len(*slept), *slept)
	}
	if want := 540*time.Second + resetMargin; (*slept)[0] != want {
		t.Errorf("slept %s, want %s (to the reset plus the margin)", (*slept)[0], want)
	}
	if *reads != 2 {
		t.Errorf("it read the budget %d time(s), want 2: a wait that does not read again has not measured", *reads)
	}
	if !strings.Contains(errOut, "core 0/50") || !strings.Contains(errOut, "sleeping") {
		t.Errorf("stderr does not say what it waited for: %q", errOut)
	}
	if !strings.Contains(out, "PASS core 5000/50") {
		t.Errorf("stdout does not carry the reading that ended the wait: %q", out)
	}
}

// ⛔ The two budgets are separate; one can be spent while the other is full.
func TestOnlyTheResourcesNamedAreWaitedFor(t *testing.T) {
	slept, _ := fake(t, answer(4978, 5000, 600, 0, 5000, 1200))
	code, out, _ := drive("--rest", "50")
	if code != 0 {
		t.Fatalf("exit %d: a spent GraphQL budget nobody named must not hold a REST wait", code)
	}
	if strings.Contains(out, "graphql") {
		t.Errorf("stdout reports a resource that was not named: %q", out)
	}
	if len(*slept) != 0 {
		t.Errorf("it slept %v for a resource nobody asked about", *slept)
	}
}

// ⛔ No reset can lift a budget above its own limit, and that answer is
// available now rather than in an hour.
func TestAThresholdNoLimitCouldMeetIsRefusedBeforeTheFirstSleep(t *testing.T) {
	slept, _ := fake(t, answer(10, 5000, 540, 4745, 5000, 1200))
	code, out, errOut := drive("--rest", "9000")
	if code != 2 {
		t.Errorf("exit %d for a threshold above the limit, want 2", code)
	}
	if len(*slept) != 0 {
		t.Errorf("it slept %v on a condition no reset can satisfy", *slept)
	}
	if !strings.Contains(errOut, "limit of 5000") {
		t.Errorf("the refusal does not name the limit it compared against: %q", errOut)
	}
	if out != "" {
		t.Errorf("it printed a verdict: %q", out)
	}
}

// ⛔ Three ways the reading can fail to be a reading, and none of them is a
// clean budget. The statuses are what a caller branches on.
func TestAnAnswerThatDidNotMeasureIsNeverAPass(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reading string
		says    string
	}{
		{"gh failed", "!gh: API rate limit exceeded for user ID 11405852 (HTTP 403)", "rate limit exceeded"},
		{"not JSON", "<html>403 Forbidden</html>", "read back as"},
		{"no resources at all", `{"resources":{}}`, "no resources"},
		{"the named resource is absent", `{"resources":{"search":{"limit":30,"remaining":30,"reset":1}}}`, `no "core" resource`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slept, _ := fake(t, tc.reading)
			code, out, errOut := drive("--rest", "50")
			if code != 2 {
				t.Errorf("exit %d, want 2: %s is not a measurement", code, tc.name)
			}
			if out != "" {
				t.Errorf("it printed a verdict on stdout: %q", out)
			}
			if !strings.Contains(errOut, tc.says) {
				t.Errorf("the reason does not say %q: %q", tc.says, errOut)
			}
			if len(*slept) != 0 {
				t.Errorf("it slept %v on an answer it could not read", *slept)
			}
		})
	}
}

// ⛔ A deadline that passed is a finding, not a success: the caller must not
// write to GitHub because ratewait ran out of patience.
func TestTheDeadlineEndsTheWaitWithAFindingNotASuccess(t *testing.T) {
	slept, _ := fake(t, answer(0, 5000, 3600, 4745, 5000, 3600))
	code, out, _ := drive("--rest", "50", "--timeout", "5m")
	if code != 1 {
		t.Fatalf("exit %d when the reset is an hour out and the deadline five minutes, want 1", code)
	}
	if len(*slept) != 0 {
		t.Errorf("it slept %v past its own deadline", *slept)
	}
	// ⛔ cli.Status never prints the message of a status 1, so a finding that
	// does not print itself exits 1 in silence. This asserts the LINE, not the
	// status: the status alone was green on a command that said nothing.
	if !strings.HasPrefix(out, "TIMEOUT core 0/50") {
		t.Errorf("stdout does not carry the timeout as an answer: %q", out)
	}
	if !strings.Contains(out, "--rest 50") {
		t.Errorf("the timeout does not say what it was waiting for: %q", out)
	}
}

// ⛔ With two resources short, the instant at which BOTH could have refilled is
// the later reset. Sleeping to the earlier one wakes to the same answer.
func TestTheLaterResetIsTheOneSleptTo(t *testing.T) {
	slept, _ := fake(t,
		answer(0, 5000, 300, 0, 5000, 900),
		answer(5000, 5000, 4000, 5000, 5000, 4500),
	)
	code, _, _ := drive("--rest", "50", "--graphql", "500")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if len(*slept) != 1 {
		t.Fatalf("slept %d time(s), want 1: %v", len(*slept), *slept)
	}
	if want := 900*time.Second + resetMargin; (*slept)[0] != want {
		t.Errorf("slept %s, want %s -- the LATER of the two resets", (*slept)[0], want)
	}
}

// ⛔ "At least N" differs from "more than N" by one call, and a gate whose
// boundary is unstated is a gate nobody can use. The pair is the witness.
func TestTheThresholdIsAtLeastAndTheBoundaryIsPinned(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining int
		wantCode  int
		wantSleep bool
	}{
		{"exactly the threshold passes", 50, 0, false},
		{"one below it waits", 49, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slept, _ := fake(t,
				answer(tc.remaining, 5000, 120, 4745, 5000, 600),
				answer(5000, 5000, 3800, 4745, 5000, 4200),
			)
			code, _, errOut := drive("--rest", "50")
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d; stderr=%q", code, tc.wantCode, errOut)
			}
			if got := len(*slept) > 0; got != tc.wantSleep {
				t.Errorf("slept=%v, want %v with %d of a 50 threshold", got, tc.wantSleep, tc.remaining)
			}
		})
	}
}

// ⛔ The help has always said "exit 1 ... or the reset stopped advancing", and
// until this test the code did not do it: a window that resets while the
// resource stays short left the loop with a distance of zero to sleep, which is
// the tight poll this command exists to avoid.
func TestAResetThatDidNotAdvanceIsAFindingAndNotAnotherSleep(t *testing.T) {
	slept, reads := fake(t,
		answer(0, 5000, 300, 4745, 5000, 900),
		answer(0, 5000, 300, 4745, 5000, 900), // the same reset, still short
	)
	code, out, _ := drive("--rest", "50")
	if code != 1 {
		t.Fatalf("exit %d when the reset did not advance, want 1", code)
	}
	if !strings.HasPrefix(out, "STALLED core 0/50") {
		t.Errorf("stdout does not carry the stall as an answer: %q", out)
	}
	if len(*slept) != 1 {
		t.Errorf("slept %d time(s), want exactly 1: a second sleep to the same reset is a poll", len(*slept))
	}
	if *reads != 2 {
		t.Errorf("read %d time(s), want 2", *reads)
	}
}

// ⛔ A reset instant already behind us -- a stale reading, or a clock that
// disagrees with GitHub's -- gives a negative distance, and sleep returns
// instantly. The floor is what stops one bad reading becoming a thousand calls.
func TestAResetAlreadyInThePastCannotBecomeATightPoll(t *testing.T) {
	slept, reads := fake(t,
		answer(0, 5000, -100, 4745, 5000, 900), // reset 100s BEHIND the clock
		answer(5000, 5000, 3500, 4745, 5000, 4000),
	)
	code, out, _ := drive("--rest", "50")
	if code != 0 {
		t.Fatalf("exit %d, want 0 once the second reading is above the threshold", code)
	}
	if len(*slept) != 1 {
		t.Fatalf("slept %d time(s), want 1: %v", len(*slept), *slept)
	}
	if (*slept)[0] < resetMargin {
		t.Errorf("slept %s on a reset in the past, want at least the %s floor -- anything less is a poll",
			(*slept)[0], resetMargin)
	}
	if *reads != 2 {
		t.Errorf("read %d time(s), want 2", *reads)
	}
	if !strings.Contains(out, "PASS") {
		t.Errorf("stdout: %q", out)
	}
}

// A timeout of zero or less is not a wait, and must not pass by being instant.
func TestATimeoutThatCannotWaitIsRefused(t *testing.T) {
	slept, reads := fake(t)
	code, out, _ := drive("--rest", "50", "--timeout", "0s")
	if code != 2 {
		t.Errorf("exit %d for --timeout 0s, want 2", code)
	}
	if *reads != 0 || len(*slept) != 0 {
		t.Errorf("it read %d time(s) and slept %v on a timeout it had refused", *reads, *slept)
	}
	if out != "" {
		t.Errorf("it printed a verdict: %q", out)
	}
}

// Help is a status of zero and not a reason to do the work, and it goes to
// stderr because stdout is this command's answer.
func TestHelpIsNotAWait(t *testing.T) {
	slept, reads := fake(t)
	code, out, errOut := drive("--help")
	if code != 0 {
		t.Errorf("exit %d for --help, want 0", code)
	}
	if out != "" {
		t.Errorf("help reached stdout, which is the verdict's stream: %q", out)
	}
	if !strings.Contains(errOut, "--rest") {
		t.Errorf("help does not describe the flags: %q", errOut)
	}
	if *reads != 0 || len(*slept) != 0 {
		t.Errorf("--help read %d time(s) and slept %v", *reads, *slept)
	}
}
