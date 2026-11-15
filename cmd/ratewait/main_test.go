package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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
	runGH = func(_ context.Context, _ ...string) ([]byte, error) {
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

// fakeGen is fake() for a seam whose answer depends on the clock, which is the
// only way to express a reset that MOVES. Its absence is why three successive
// bounds over the reset's movement were each written, mutated, and believed.
func fakeGen(t *testing.T, gen func(at time.Time) string) (slept *[]time.Duration, reads *int) {
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
		if len(durations) > 50 {
			t.Fatalf("the loop slept %d times: it is not bounded at all", len(durations))
		}
	}
	runGH = func(_ context.Context, _ ...string) ([]byte, error) {
		n++
		return []byte(gen(clock)), nil
	}
	return &durations, &n
}

// ⛔⛔ THE BOUND IS A COUNT OF READINGS, and this table is the witness three
// earlier rules did not have. Each of them was a predicate over the reset --
// "strictly later", "later by a margin", "strictly in the future" -- and each
// was defeated by a reset that MOVES, the last one measured at 5 readings in
// 12s, about 1300 under the default deadline. A count cannot be defeated by
// what the answer says.
func TestTheLoopIsBoundedByACountOfReadings(t *testing.T) {
	limit := maxRounds(maxWait)
	if limit != 5 {
		t.Fatalf("maxRounds(%s)=%d, and this table is written for 5", maxWait, limit)
	}
	for _, tc := range []struct {
		name string
		gen  func(at time.Time) string
	}{
		{"a reset that never moves", func(time.Time) string {
			return core(0, 5000, start.Add(300*time.Second).Unix())
		}},
		{"a reset creeping behind the clock", func(at time.Time) string {
			return core(0, 5000, at.Add(-time.Second).Unix())
		}},
		{"a reset that keeps itself ahead of the clock", func(at time.Time) string {
			return core(0, 5000, at.Add(time.Second).Unix())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slept, reads := fakeGen(t, tc.gen)
			code, out, _ := drive("--rest", "50")
			if code != 1 {
				t.Fatalf("exit %d, want 1", code)
			}
			if !strings.HasPrefix(out, "STALLED core 0/50") {
				t.Errorf("stdout does not carry the stall as an answer: %q", out)
			}
			if *reads != limit {
				t.Errorf("read %d time(s), want exactly the %d allowed", *reads, limit)
			}
			if len(*slept) != limit-1 {
				t.Errorf("slept %d time(s), want %d: %v", len(*slept), limit-1, *slept)
			}
		})
	}
}

// The allowance grows with the deadline, because a longer wait may legitimately
// cover more than one window.
//
// ⛔ THE WINDOWS ARE ROUNDED UP, and the 59-minute row is the one that matters:
// an earlier version of this table asserted 3 for a 30-second deadline, which
// PINNED THE UNDERCOUNT AS THE SPECIFICATION -- a mutation raising the count was
// "caught" by a test that had moved with the defect rather than against it. A
// 59-minute deadline allows a window; integer division paid none.
func TestTheAllowanceFollowsTheDeadline(t *testing.T) {
	for _, tc := range []struct {
		timeout time.Duration
		want    int
	}{
		// ⛔ A non-positive deadline cannot reach this through the command --
		// RunE refuses it first -- but a mutation of the minimum-window guard
		// SURVIVED, and the honest reading of that was the third one: not dead
		// code, a corpus with no witness. maxRounds is a package function its
		// own tests already call directly, so it must stay total: deleting the
		// guard would hand a future caller a bound of 3 instead of 4.
		{0, 4},
		{-5 * time.Second, 4},
		{30 * time.Second, 4},
		{59 * time.Minute, 4},
		{60 * time.Minute, 4},
		{65 * time.Minute, 5},
		{3 * time.Hour, 6},
	} {
		if got := maxRounds(tc.timeout); got != tc.want {
			t.Errorf("maxRounds(%s)=%d, want %d", tc.timeout, got, tc.want)
		}
	}
}

// ⛔⛔ THE COUNT MUST NOT REFUSE A PATIENT RUN, and under the floored arithmetic
// it did: this sequence is the derivation's own story -- a first reading a
// second behind the clock, a window waited out, a refill that lands one reading
// later -- and it exited 1 under --timeout 59m while passing under 60m. The
// STALLED line it printed named a reset three seconds in the future.
func TestAPatientRunOfFourReadingsIsNotRefused(t *testing.T) {
	slept, reads := fake(t,
		core(0, 5000, start.Add(-time.Second).Unix()),    // skew: a second behind
		core(0, 5000, start.Add(600*time.Second).Unix()), // a real window
		core(0, 5000, start.Add(605*time.Second).Unix()), // still short, just ahead
		answer(5000, 5000, 4200, 4745, 5000, 4800),       // refilled
	)
	code, out, errOut := drive("--rest", "50", "--timeout", "59m")
	if code != 0 {
		t.Fatalf("exit %d for a run that refills on its fourth reading under a 59m deadline, want 0.\nstdout=%q\nstderr=%q",
			code, out, errOut)
	}
	if *reads != 4 {
		t.Errorf("read %d time(s), want 4", *reads)
	}
	if len(*slept) != 3 {
		t.Errorf("slept %d time(s), want 3: %v", len(*slept), *slept)
	}
	if !strings.Contains(out, "PASS core 5000/50") {
		t.Errorf("stdout: %q", out)
	}
}

// ⛔ The third ordering decision in the loop: with the readings spent AND the
// next reset past the deadline, the caller acts on their own deadline.
func TestWhenBothTheReadingsAndTheTimeAreGoneTheDeadlineIsTheAnswer(t *testing.T) {
	// Four readings under a 30s deadline (limit 4), the last one short with a
	// reset an hour out: the count and the reset are both exhausted at once.
	slept, _ := fake(t,
		core(0, 5000, start.Add(3*time.Second).Unix()),
		core(0, 5000, start.Add(8*time.Second).Unix()),
		core(0, 5000, start.Add(13*time.Second).Unix()),
		core(0, 5000, start.Add(3600*time.Second).Unix()),
	)
	code, out, _ := drive("--rest", "50", "--timeout", "30s")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "TIMEOUT") || strings.Contains(out, "STALLED") {
		t.Errorf("the answer should be the deadline, not the stall: %q", out)
	}
	if len(*slept) != 3 {
		t.Errorf("slept %d time(s), want 3: %v", len(*slept), *slept)
	}
}

// ⛔ WHICH BOUND WAS SET, NOT WHAT THE CLOCK SAYS. Review mutated the old
// discriminator to `if true` and the suite survived: with --timeout 31s a 30s
// cap expiring plus the latency of killing a child also lands past the
// deadline, and the run would be told its time was up when the seam had simply
// not answered.
func TestWhichBoundWasSetDecidesTheStatus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout string
		want    int
		says    string
	}{
		{"the run's deadline was the bound", "1s", 1, "while gh was running"},
		{"the per-call cap was the bound", "30m", 2, "did not answer rate_limit within 30s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldRun := runGH
			t.Cleanup(func() { runGH = oldRun })
			runGH = func(context.Context, ...string) ([]byte, error) {
				return nil, context.DeadlineExceeded
			}
			code, out, errOut := drive("--rest", "50", "--timeout", tc.timeout)
			if code != tc.want {
				t.Fatalf("exit %d, want %d; stdout=%q stderr=%q", code, tc.want, out, errOut)
			}
			if !strings.Contains(out+errOut, tc.says) {
				t.Errorf("nothing says %q: stdout=%q stderr=%q", tc.says, out, errOut)
			}
		})
	}
}

// ⛔ BOTH BRANCHES SURVIVED A MUTATION until this existed, because reaching
// them needs a child that writes a megabyte to stderr and then fails, which no
// stock program does. The behaviour did not change; it became answerable.
func TestGhErrorDeclaresATruncationAndClipsIt(t *testing.T) {
	t.Run("no stderr leaves the exec error standing", func(t *testing.T) {
		base := errors.New("exit status 1")
		if got := ghError(base, &capped{}); got != base {
			t.Errorf("got %v, want the exec error itself", got)
		}
	})
	t.Run("a long stderr is clipped", func(t *testing.T) {
		var c capped
		c.Write([]byte(strings.Repeat("x", 500)))
		got := ghError(errors.New("exit status 1"), &c).Error()
		if r := []rune(got); len(r) != 121 {
			t.Errorf("the reason is %d runes, want 121 (120 plus the ellipsis)", len(r))
		}
		if strings.Contains(got, "truncated") {
			t.Error("a message under the cap must not claim truncation")
		}
	})
	t.Run("a truncated stderr says so", func(t *testing.T) {
		c := capped{over: true}
		c.Write([]byte("403 Forbidden, and a megabyte of somebody else's HTML"))
		got := ghError(errors.New("exit status 1"), &c).Error()
		if !strings.Contains(got, "[truncated at") {
			t.Errorf("an unannounced cut reads as the whole message: %q", got)
		}
		if !strings.Contains(got, "403 Forbidden") {
			t.Errorf("the reason itself was lost: %q", got)
		}
	})
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

// core builds one body with an exact reset, for the readings answer() cannot
// express: a reset that is not an instant, or a resource with no limit at all.
func core(remaining, limit int, reset int64) string {
	return fmt.Sprintf(`{"resources":{"core":{"limit":%d,"remaining":%d,"reset":%d}}}`, limit, remaining, reset)
}

// ⛔⛔ THE BLOCKER REVIEW FOUND. An int64 JSON accepts overflows time.Unix into
// an instant before year 1 -- before the zero Time -- so it loses the deadline
// test, the stall test and the comparison that picks the latest reset, and the
// loop polls on the floor without end. Seven calls in thirteen seconds under
// --timeout 3s, measured with a fake gh on PATH.
func TestAResetThatIsNotAnInstantIsRefusedRatherThanSleptOn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reset int64
	}{
		{"the largest int64", 9223372036854775807},
		{"zero", 0},
		{"before the unix epoch", -62135596801},
		{"two days out, past the horizon", start.Add(48 * time.Hour).Unix()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slept, _ := fake(t, core(0, 5000, tc.reset))
			code, out, errOut := drive("--rest", "50")
			if code != 2 {
				t.Errorf("exit %d for a reset of %d, want 2", code, tc.reset)
			}
			if len(*slept) != 0 {
				t.Errorf("it slept %v on a value that is not an instant", *slept)
			}
			if out != "" {
				t.Errorf("it printed a verdict: %q", out)
			}
			if !strings.Contains(errOut, "not an instant to wait for") {
				t.Errorf("the refusal does not say what was wrong: %q", errOut)
			}
		})
	}
}

// ⛔ --timeout bounds the RUN, not merely the next sleep. The deadline used to
// be compared only to the wake instant, so a run could spend its whole budget
// of time on earlier rounds and still start another wait.
func TestTheDeadlineIsElapsedTimeAndNotOnlyTheNextSleep(t *testing.T) {
	slept, reads := fake(t,
		core(0, 5000, start.Add(-100*time.Second).Unix()), // stale reset, sleeps the floor
		core(0, 5000, start.Add(-50*time.Second).Unix()),  // advanced, so not a stall
	)
	code, out, _ := drive("--rest", "50", "--timeout", "1s")
	if code != 1 {
		t.Fatalf("exit %d after the deadline had passed in elapsed time, want 1", code)
	}
	if len(*slept) != 1 {
		t.Errorf("slept %d time(s), want 1: the second round must not start a wait past the deadline", len(*slept))
	}
	if *reads != 2 {
		t.Errorf("read %d time(s), want 2", *reads)
	}
	if !strings.HasPrefix(out, "TIMEOUT core 0/50") {
		t.Errorf("stdout does not carry the timeout: %q", out)
	}
}

// ⛔ A mutation of `>` to `>=` survived the first suite: a threshold EQUAL to
// the limit is a legitimate wait -- "leave me the whole budget" -- and
// refusing it would contradict the README's own claim that the boundary is
// pinned.
func TestAThresholdEqualToTheLimitIsAWaitAndNotARefusal(t *testing.T) {
	slept, _ := fake(t,
		answer(10, 5000, 300, 4745, 5000, 900),
		answer(5000, 5000, 3900, 4745, 5000, 4500),
	)
	code, out, errOut := drive("--rest", "5000")
	if code != 0 {
		t.Fatalf("exit %d for a threshold equal to the limit, want 0 after the reset; stderr=%q", code, errOut)
	}
	if len(*slept) != 1 {
		t.Errorf("slept %d time(s), want 1", len(*slept))
	}
	if !strings.Contains(out, "PASS core 5000/5000 of 5000") {
		t.Errorf("stdout: %q", out)
	}
}

// ⛔ A mutation of the floor to `left < 0` survived the first suite: only its
// SIGN was pinned, not its size. A reset one second behind the clock leaves a
// positive distance smaller than the margin, which is the case that tells them
// apart.
func TestTheFloorIsTheMarginItselfAndNotMerelyAPositiveNumber(t *testing.T) {
	slept, _ := fake(t,
		core(0, 5000, start.Add(-1*time.Second).Unix()),
		answer(5000, 5000, 3900, 4745, 5000, 4500),
	)
	code, _, _ := drive("--rest", "50")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if len(*slept) != 1 {
		t.Fatalf("slept %d time(s), want 1: %v", len(*slept), *slept)
	}
	if (*slept)[0] != resetMargin {
		t.Errorf("slept %s on a reset 1s behind the clock, want exactly the %s floor", (*slept)[0], resetMargin)
	}
}

// ⛔ THE ORDER OF TWO CHECKS, AND THE PAIR IS THE WITNESS. With the limit
// consulted before satisfaction, a body carrying no "limit" was refused as
// "above core's limit of 0" although its remaining plainly met the threshold.
func TestALimitIsOnlyNeededWhenTheResourceIsShort(t *testing.T) {
	t.Run("satisfied without a limit passes", func(t *testing.T) {
		slept, _ := fake(t, `{"resources":{"core":{"remaining":5000}}}`)
		code, out, errOut := drive("--rest", "50")
		if code != 0 {
			t.Errorf("exit %d although 5000 remaining meets a threshold of 50; stderr=%q", code, errOut)
		}
		if !strings.Contains(out, "PASS core 5000/50") {
			t.Errorf("stdout: %q", out)
		}
		if len(*slept) != 0 {
			t.Errorf("slept %v", *slept)
		}
	})
	t.Run("short without a limit is unanswerable", func(t *testing.T) {
		slept, _ := fake(t, `{"resources":{"core":{"remaining":10}}}`)
		code, out, errOut := drive("--rest", "50")
		if code != 2 {
			t.Errorf("exit %d, want 2: a short resource with no limit cannot be judged", code)
		}
		if !strings.Contains(errOut, "no limit for core") {
			t.Errorf("the refusal does not name what was missing: %q", errOut)
		}
		if out != "" || len(*slept) != 0 {
			t.Errorf("stdout=%q, slept=%v", out, *slept)
		}
	})
}

// ⛔ TWO DIFFERENT FACTS, AND ONE MESSAGE FOR BOTH WAS FALSE: the single line
// printed "the deadline passed at 22:11" at 22:06, having slept not once. A
// mutation of that sentence survived, which is how review found it.
func TestTheTwoTimeoutsSayWhichBoundExpired(t *testing.T) {
	t.Run("the next reset is out of reach", func(t *testing.T) {
		slept, _ := fake(t, answer(0, 5000, 3600, 4745, 5000, 3600))
		code, out, _ := drive("--rest", "50", "--timeout", "5m")
		if code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		if !strings.Contains(out, "the next reset at") || !strings.Contains(out, "is past the deadline at") {
			t.Errorf("the line does not say that the RESET is out of reach: %q", out)
		}
		if len(*slept) != 0 {
			t.Errorf("slept %v before its deadline", *slept)
		}
	})
	t.Run("the time is spent", func(t *testing.T) {
		slept, _ := fake(t,
			core(0, 5000, start.Add(-100*time.Second).Unix()),
			core(0, 5000, start.Add(900*time.Second).Unix()),
		)
		code, out, _ := drive("--rest", "50", "--timeout", "1s")
		if code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		if !strings.Contains(out, "the deadline passed at") || !strings.Contains(out, "next reset") {
			t.Errorf("the line does not say that the TIME is spent, with the reset to come back to: %q", out)
		}
		if len(*slept) != 1 {
			t.Errorf("slept %d time(s), want 1", len(*slept))
		}
	})
}

// ⛔ EACH CALL IS BOUNDED BY THE SOONER OF THE RUN'S DEADLINE AND THE PER-CALL
// CAP. Without this witness the min() between them is untested, since the fake
// seam elsewhere ignores the context it is handed.
func TestEachCallIsBoundedByTheSoonerOfTheTwo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout string
		want    time.Duration
	}{
		{"the run's deadline is sooner", "1s", time.Second},
		{"the per-call cap is sooner", "30m", callCap},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got time.Duration
			oldRun := runGH
			t.Cleanup(func() { runGH = oldRun })
			runGH = func(ctx context.Context, _ ...string) ([]byte, error) {
				dl, ok := ctx.Deadline()
				if !ok {
					t.Error("the call was handed a context with no deadline at all")
					return nil, fmt.Errorf("no deadline")
				}
				got = time.Until(dl)
				return []byte(answer(5000, 5000, 600, 4745, 5000, 1200)), nil
			}
			if code, _, errOut := drive("--rest", "50", "--timeout", tc.timeout); code != 0 {
				t.Fatalf("exit %d; stderr=%q", code, errOut)
			}
			if d := got - tc.want; d > 500*time.Millisecond || d < -500*time.Millisecond {
				t.Errorf("the call's deadline was %s away, want about %s", got, tc.want)
			}
		})
	}
}

// ⛔ THE EXEC SEAM WAS ENTIRELY UNTESTED -- `grep -c "execGH\|capped\|maxAnswer"`
// over this file answered 0, and a mutation of maxAnswer to 1 survived. These
// run real programs rather than a fake, because what is being asserted is what
// os/exec does with a child that fails, streams or stalls.
func TestTheExecSeamIsBoundedAndCapped(t *testing.T) {
	look := func(t *testing.T, name string) string {
		t.Helper()
		p, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s is not on PATH here: %v", name, err)
		}
		return p
	}
	set := func(t *testing.T, p string) {
		t.Helper()
		old := ghPath
		ghPath = p
		t.Cleanup(func() { ghPath = old })
	}

	t.Run("stdout comes back", func(t *testing.T) {
		set(t, look(t, "echo"))
		out, err := execGH(context.Background(), "hello")
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if strings.TrimSpace(string(out)) != "hello" {
			t.Errorf("out=%q", out)
		}
	})

	t.Run("a failing child is an error and not an empty answer", func(t *testing.T) {
		set(t, look(t, "false"))
		out, err := execGH(context.Background(), "api", "rate_limit")
		if err == nil {
			t.Fatalf("a child that exits 1 returned out=%q and no error", out)
		}
	})

	t.Run("a child that streams is cut off rather than read to the end", func(t *testing.T) {
		set(t, look(t, "yes"))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := execGH(ctx, "x"); done <- err }()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "more than") {
				t.Errorf("err=%v, want the over-the-cap refusal", err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("execGH read an endless stream to the end: the cap bounded nothing")
		}
	})

	t.Run("a child that stalls is killed by the context", func(t *testing.T) {
		set(t, look(t, "sleep"))
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		began := time.Now()
		if _, err := execGH(ctx, "30"); err == nil {
			t.Fatal("a 30s child under a 300ms context returned no error")
		}
		if took := time.Since(began); took > 10*time.Second {
			t.Errorf("took %s: the context did not bound the child", took)
		}
	})
}

// ⛔ A writer that never fails is why a streaming child is read to the end: the
// cap recorded a number and bounded nothing.
func TestCappedFailsAtTheLimitRatherThanOnlyRecordingIt(t *testing.T) {
	var c capped
	n, err := c.Write(bytes.Repeat([]byte("a"), maxAnswer-1))
	if err != nil || n != maxAnswer-1 {
		t.Fatalf("under the cap: n=%d err=%v", n, err)
	}
	n, err = c.Write([]byte("bb"))
	if err == nil {
		t.Errorf("a write across the cap returned no error, so os/exec would keep copying")
	}
	if n != 1 {
		t.Errorf("n=%d, want the 1 byte that fitted", n)
	}
	if !c.over {
		t.Error("over=false after crossing the cap")
	}
	if c.buf.Len() != maxAnswer {
		t.Errorf("buffered %d bytes, want exactly the %d-byte cap", c.buf.Len(), maxAnswer)
	}
	if n, err := c.Write([]byte("c")); n != 0 || err == nil {
		t.Errorf("a write beyond the full buffer: n=%d err=%v", n, err)
	}
}

func TestClipCountsRunesAndNotBytes(t *testing.T) {
	// 200 runes of three bytes each: a byte slice would cut one in half.
	s := strings.Repeat("é", 200)
	got := clip([]byte(s))
	if r := []rune(got); len(r) != 121 { // 120 plus the ellipsis
		t.Errorf("clip returned %d runes, want 121", len(r))
	}
	if strings.Contains(got, "�") {
		t.Error("clip cut a rune in half")
	}
	if short := clip([]byte(" ok ")); short != "ok" {
		t.Errorf("clip(%q)=%q", " ok ", short)
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
