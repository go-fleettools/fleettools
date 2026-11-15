// Command ratewait blocks until the GitHub budget a caller names is at or above
// a threshold it states, and exits 0 only then.
//
// ⛔ WHY THIS EXISTS, and it is not convenience. The hourly budget is PER USER
// and roughly twenty-five sessions share it, so a write program meets a 403 it
// did nothing to cause. [fleet.Throttled] answers false for "API rate limit
// exceeded" on purpose -- "that budget will not refill inside any backoff this
// package would sleep" -- so the fleet's retry policy correctly gives up at
// once, and that leaves nobody able to WAIT. Measured 2026-11-15: the budget was
// spent at 13:52 and again at 22:06 CET; both times the only fleet-legal way to
// come back at the reset was a shell until-loop, which the shell-loop guard
// refuses, or a blind sleep, which is a guess about somebody else's traffic.
//
// ⛔ IT SLEEPS TO THE RESET THE API REPORTS, not on an interval. A poll loop
// spends the budget it is waiting for; the reset is a fact GitHub hands over
// with every reading, and rate_limit itself costs nothing.
//
// ⚠ IT IS BLIND TO THE SECONDARY LIMIT AND SAYS SO ON EVERY SUCCESS. That is
// not modesty, it is [internal/fleet/budget.go]'s measurement: on 2026-10-10 gh
// answered 403 on every call while rate_limit reported 5000/5000 remaining. A
// tool that printed "the budget is fine" and stopped there would read as "safe
// to call" in exactly the case where it is not.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/spf13/cobra"
)

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

var (
	now   = time.Now
	sleep = time.Sleep

	// ghPath is the program the seam runs, so that a test can point it at a
	// binary of known behaviour instead of at the real gh.
	ghPath = "gh"

	// runGH is the exec seam, replaced by the tests.
	//
	// ⛔ IT IS NOT [fleet.GH], AND THE REASON IS THE SAME ONE budget.go GIVES
	// FOR ITS OWN autoStart: GH accounts for every call against the budget
	// projection, and the rate_limit endpoint this calls is itself free of the
	// budget. Charging it would make the guard believe a call was spent that
	// was not -- the MEASUREMENT must not move what it measures.
	//
	// ⚠ THE COMMAND IS NOT FREE, and the sentence above is deliberately about
	// the READING rather than the run, because an earlier version of it said
	// "a tool whose job is to read the budget must not move it" and that was
	// false of the process. Review measured why: [fleet.WarnIfStale], which
	// every command here calls first and a repository-wide test requires, makes
	// one uncached `repos/.../compare/...` request per run. A wait therefore
	// costs exactly one accounted call, plus one free reading per round -- and
	// under an exhausted budget that staleness note is the thing that prints a
	// 403, as it did at 22:06 CET on 2026-11-15 above ghissue's own refusal.
	runGH = execGH
)

// resetMargin is added to the reset instant before waking.
//
// ⛔ A reset is a one-second boundary, and a reading taken in the same second
// can still answer with the spent counter. Waking one second early turns a
// single wait into a second one, which is the failure this margin costs two
// seconds to avoid.
const resetMargin = 2 * time.Second

// maxWait is the default deadline.
//
// ⛔ A CHOSEN number, and the choice is forced: the primary budget resets at
// most one hour away, so a wait that outlives this is not patience, it is a
// budget that is not refilling -- a fault to report rather than to sit through,
// and [maxRounds] is what reports it.
const maxWait = 65 * time.Minute

// A resource is one line of rate_limit's answer.
type resource struct {
	Limit     int   `json:"limit"`
	Remaining int   `json:"remaining"`
	Reset     int64 `json:"reset"`
}

// A want is a resource a caller named, with the threshold it asked for.
type want struct {
	flag      string // what the caller typed, for the message
	name      string // the key inside rate_limit's resources
	threshold int
}

// RootCmd is the command, driven by the tests through cli.Drive.
func RootCmd() *cobra.Command {
	c := cli.New("ratewait [--rest N] [--graphql N] [--timeout D]",
		"Block until the GitHub budget a caller names is at or above a threshold",
		`Reads GitHub's rate_limit, which costs nothing, and exits 0 once every
resource named on the command line has at least the number of calls asked for.
When one is short it sleeps to the reset instant the API reported for that
resource -- not on an interval, which would spend the budget being waited for --
and reads again.

--rest N is the primary hourly REST budget (rate_limit's "core"), --graphql N the
GraphQL one. The two are SEPARATE budgets: one can be spent while the other is
untouched, so a resource nobody named is never waited for.

"At least N", not "more than N": a threshold that differs by one from what it
reads is a gate nobody can use.

A wait is a condition, so a run that names no resource is refused rather than
passed, and so is a threshold no limit could ever satisfy.

⚠ rate_limit is blind to the SECONDARY limit, which has no readable counter: on
2026-10-10 gh answered 403 on every call while rate_limit read 5000/5000. Every
success says so, because "the budget is fine" would otherwise read as "safe to
call".

The number of readings a run may take is bounded, at three plus one per hour the
deadline allows: the window is an hour, so a longer run is a reset that keeps
moving rather than a budget that is refilling. Three earlier rules about the
reset's own movement were each defeated by a reset that moves with the clock.

A reset is only believed if it is an instant to wait for: at or after 2020 and
no more than 24 hours out. The largest int64, which JSON accepts, overflows into
a time before year 1 and loses every comparison it is in, which made the loop
poll on its floor without end -- seven calls in thirteen seconds under
--timeout 3s.

--timeout bounds the RUN, in elapsed time, and bounds each gh call with it: a
child that stalls or streams is killed rather than read to the end, and one call
may not take more than 30s even under a long deadline. ⚠ The single exception is
the staleness note below, which runs before the flags are parsed and which this
command cannot bound.

⚠ One more cost to be exact about: like every command here, this one calls
fleet.WarnIfStale first, which spends ONE request asking whether the binary is
current. The budget reading itself is free; the process is not.

exit 0 every named resource is at or above its threshold
exit 1 the deadline passed in elapsed time, or the readings allowed were spent
       with the resource still short
exit 2 the question could not be put: nothing named, an impossible threshold, a
       short resource with no limit, a reset that is not an instant, a seam that
       will not answer within 30s, an unreadable or empty answer`)

	rest := c.Flags().Int("rest", 0, "wait until the REST (core) budget has at least this many calls left")
	graphql := c.Flags().Int("graphql", 0, "wait until the GraphQL budget has at least this many calls left")
	timeout := c.Flags().Duration("timeout", maxWait, "give up after this long and report it")

	c.RunE = func(c *cobra.Command, _ []string) error {
		if cli.HelpRequested(c) {
			return nil
		}
		var wants []want
		if *rest > 0 {
			wants = append(wants, want{"--rest", "core", *rest})
		}
		if *graphql > 0 {
			wants = append(wants, want{"--graphql", "graphql", *graphql})
		}
		// ⛔ A WAIT WITH NO CONDITION MUST NOT EXIT 0. It would be the shortest
		// possible lie: a caller that mistyped a flag would read "the budget is
		// fine" from a command that never looked at one.
		if len(wants) == 0 {
			return cli.Unanswerable("nothing to wait for: name a budget with --rest N or --graphql N")
		}
		if *timeout <= 0 {
			return cli.Unanswerable("--timeout %s is not a wait", *timeout)
		}
		return waitFor(c, wants, now().Add(*timeout), maxRounds(*timeout))
	}
	return c
}

// maxRounds is how many readings a run may take, derived from its deadline.
//
// ⛔⛔ THE LOOP IS BOUNDED BY A COUNT, because THREE successive attempts to bound
// it by the reset's own movement were each defeated in review, and the third
// time made the reason plain: the reset is GitHub's to report, so no predicate
// over it is a bound this command controls.
//
//  1. "strictly later than the last"  -- defeated by a reset creeping +1s per
//     reading, which is strictly later every time.
//  2. "later by at least the margin"  -- defeated the same way once the sleep
//     itself advances the clock by more than the margin.
//  3. "strictly in the future"        -- defeated by a reset that keeps itself
//     one second AHEAD of the clock: 5 readings in 12s, about 1300 under the
//     default deadline. Measured, like the two before it.
//
// A count cannot be defeated by what the answer says. The derivation: the
// primary window is one hour -- a real rate_limit body's farthest reset among
// its fifteen resources is 59m59s -- so a run may legitimately wait out at most
// one window per hour its deadline allows, plus a first reading, plus one short
// re-read for a reset a second behind the clock, plus one to confirm the refill.
//
// ⛔ THE WINDOWS ARE ROUNDED UP, AND ROUNDING DOWN REFUSED A LEGITIMATE RUN.
// The sentence above says "per hour its deadline ALLOWS", and a 59-minute
// deadline plainly allows one window -- integer division paid zero. Measured by
// review on one non-hostile sequence, every reset a real instant: exit 1 under
// --timeout 59m and exit 0 under --timeout 60m, with the refused run's STALLED
// line naming a reset THREE SECONDS in the future it would not wait three
// seconds for. ⛔ And the suite had pinned the undercount as the specification
// -- a case asserting 3 for a 30-second deadline -- which is why a mutation
// raising the count was "caught": the test moved with the defect instead of
// against it. The deadline is the other bound and is the one that stops a short
// run; this count exists to stop a RECEDING reset, so it must not also refuse a
// patient one.
// ⛔⛔ AND THE MINIMUM IS NOT A COURTESY, IT IS THE OVERFLOW GUARD. I first kept
// it for a hypothetical future caller, reasoning that a non-positive deadline
// cannot arrive through the command because RunE refuses one. Review measured
// the real path: at the largest duration cobra accepts,
// --timeout 2562047h47m16.854775807s, the `+ time.Hour - 1` of the ceil
// OVERFLOWS int64 and the unguarded formula yields a limit of -2562043, so
// `round >= limit` is true on the first round -- an instant STALLED with no
// wait at all, from the flag that asks for the longest wait possible. Right
// conclusion, wrong reason, and the reason is what a reader would have trusted.
func maxRounds(timeout time.Duration) int {
	windows := int((timeout + time.Hour - 1) / time.Hour) // ceil
	if windows < 1 {
		windows = 1 // a non-positive result means it overflowed, or was not a wait
	}
	return 3 + windows
}

// waitFor is the whole command: read, answer or sleep to the reset, read again,
// at most limit times.
func waitFor(c *cobra.Command, wants []want, deadline time.Time, limit int) error {
	for round := 1; ; round++ {
		res, err := readBudget(deadline)
		if errors.Is(err, errRunDeadline) {
			fmt.Fprintf(cli.Out(c), "TIMEOUT waited for %s, the deadline passed at %s while gh was running\n",
				describeWants(wants), deadline.Local().Format("2006-01-02 15:04:05 MST"))
			return cli.Found("the deadline passed while gh was running")
		}
		if err != nil {
			return cli.Wrap(err)
		}
		short, latest, err := unsatisfied(res, wants)
		if err != nil {
			return err
		}
		if len(short) == 0 {
			fmt.Fprintf(cli.Out(c), "PASS %s\n", describe(res, wants))
			// ⚠ The caveat goes to stderr so that a caller reading stdout gets
			// the verdict alone, and a person gets the warning.
			fmt.Fprintln(cli.Err(c), "⚠ the primary budget only: the secondary limit has no counter, "+
				"and rate_limit has read 5000/5000 while every call answered 403")
			return nil
		}

		// ⛔ THE CALLER'S BOUND IS JUDGED FIRST, and the order is a decision
		// rather than an accident: when the time is spent AND the readings are
		// used up, both are true and only one can be printed. "Your deadline
		// passed" is the fact the caller acts on; the other is about GitHub and
		// would send them looking the wrong way.
		if now().After(deadline) {
			fmt.Fprintf(cli.Out(c), "TIMEOUT %s, waited for %s, the deadline passed at %s, next reset %s\n",
				strings.Join(short, ", "), describeWants(wants),
				deadline.Local().Format("2006-01-02 15:04:05 MST"), latest.Local().Format("15:04:05 MST"))
			return cli.Found("the deadline passed before %s", describeWants(wants))
		}

		wake := latest.Add(resetMargin)
		// ⛔ THE DEADLINE IS ELAPSED TIME, AND COMPARING IT ONLY TO wake WAS A
		// SENTENCE WITH NO CODE: the help promised "exit 1 the deadline passed
		// first" while nothing ever compared now() to it, so --timeout bounded
		// the next sleep rather than the run. Both tests are needed, and they
		// are SEPARATE because they are different facts -- one says the time is
		// spent, the other says the next reset is out of reach -- and a single
		// message for both printed "the deadline passed at 22:11" at 22:06.
		//
		// ⛔ A FINDING PRINTS ITSELF. cli.Status does not print the message of a
		// status 1 ("the command has already printed what it found"), so a
		// timeout that only returned the error would exit 1 in silence. Both
		// lines carry the reset instant, which is the one datum a caller needs
		// in order to come back.
		if wake.After(deadline) {
			fmt.Fprintf(cli.Out(c), "TIMEOUT %s, waited for %s, the next reset at %s is past the deadline at %s\n",
				strings.Join(short, ", "), describeWants(wants),
				latest.Local().Format("2006-01-02 15:04:05 MST"), deadline.Local().Format("15:04:05 MST"))
			return cli.Found("the next reset is past the deadline for %s", describeWants(wants))
		}

		// ⛔ THE COUNT IS THE BOUND. See [maxRounds] for why it is a count and
		// not a predicate over the reset: three such predicates were written
		// here and all three were defeated by a reset that moves.
		//
		// ⛔ IT IS JUDGED AFTER BOTH DEADLINE TESTS, which is the third ordering
		// decision in this loop and the same principle as the first two: when
		// the readings are spent AND the next reset lies past the deadline, both
		// are true and the caller acts on the deadline. Printing STALLED there
		// would describe GitHub's budget when the answer is about their own
		// patience.
		if round >= limit {
			fmt.Fprintf(cli.Out(c), "STALLED %s after %d reading(s), the last reset at %s did not refill it\n",
				strings.Join(short, ", "), round, latest.Local().Format("2006-01-02 15:04:05 MST"))
			return cli.Found("%d reading(s) and %s still short of %s",
				round, strings.Join(short, ", "), describeWants(wants))
		}

		left := wake.Sub(now())
		// ⛔ A reset already behind us gives a negative distance, and sleep
		// would return instantly. The floor keeps one stale reading from
		// becoming a spin inside a round; the round count above is what keeps
		// the rounds themselves from becoming an hour of polling.
		if left < resetMargin {
			left = resetMargin
		}
		// ⛔ THE DATE IS PRINTED, NOT ONLY THE CLOCK. A year-1 instant read as
		// "00:09:21" in the reviewer's log, which looks like a plausible reset
		// and is how the overflow above stayed invisible.
		fmt.Fprintf(cli.Err(c), "%s -- sleeping %s, to the reset at %s\n",
			strings.Join(short, ", "), left.Round(time.Second), latest.Local().Format("2006-01-02 15:04:05 MST"))
		sleep(left)
	}
}

// unsatisfied names every want the reading does not meet, and the latest reset
// among them: the one instant at which all of them could have refilled.
//
// ⛔ A RESOURCE THE ANSWER DOES NOT CARRY IS NOT A SATISFIED ONE. Treating a
// missing key as zero would wait forever on a typo; treating it as satisfied
// would pass on one. It is unanswerable, and named.
func unsatisfied(res map[string]resource, wants []want) (short []string, latest time.Time, err error) {
	for _, w := range wants {
		r, ok := res[w.name]
		if !ok {
			return nil, time.Time{}, cli.Unanswerable("rate_limit carries no %q resource, so %s %d cannot be judged",
				w.name, w.flag, w.threshold)
		}
		// ⛔ SATISFACTION IS JUDGED FIRST, AND THE ORDER IS THE FIX FOR A REAL
		// REFUSAL: with the limit consulted first, a body carrying
		// {"remaining":5000} and no "limit" was refused as "--rest 50 is above
		// core's limit of 0" -- a wait that 5000 remaining plainly met. What a
		// caller asked for is "at least N calls", and that question is answered
		// by `remaining` alone.
		if r.Remaining >= w.threshold {
			continue
		}
		// Only a SHORT resource needs its limit and its reset, which is why
		// neither is validated above: a satisfied wait must not be refused over
		// a field it never had to read.
		if r.Limit <= 0 {
			return nil, time.Time{}, cli.Unanswerable("rate_limit carried no limit for %s (remaining %d), so %s %d cannot be judged",
				w.name, r.Remaining, w.flag, w.threshold)
		}
		// ⛔ An unsatisfiable threshold is refused before the first sleep: the
		// budget can never exceed its own limit, so waiting an hour to say so
		// would be an hour spent on an answer available now.
		if w.threshold > r.Limit {
			return nil, time.Time{}, cli.Unanswerable("%s %d is above %s's limit of %d, so no reset can satisfy it",
				w.flag, w.threshold, w.name, r.Limit)
		}
		t, err := resetInstant(w.name, r.Reset)
		if err != nil {
			return nil, time.Time{}, err
		}
		short = append(short, fmt.Sprintf("%s %d/%d", w.name, r.Remaining, w.threshold))
		if t.After(latest) {
			latest = t
		}
	}
	return short, latest, nil
}

// resetFloor and resetHorizon bound what counts as a reset instant.
//
// The floor is written as a date rather than as 1577836800, because a magic
// number is a claim about a conversion nobody re-checks.
var (
	resetFloor   = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	resetHorizon = 24 * time.Hour
)

// resetInstant turns rate_limit's unix reset into an instant, and refuses a
// value that is not one.
//
// ⛔⛔ THIS IS THE GUARD UNDER THE WHOLE LOOP, and it was missing: review found
// that `"reset": 9223372036854775807` -- which JSON accepts -- overflows
// time.Unix into an instant BEFORE YEAR 1, which is before the zero Time. Such
// a value loses every comparison it is in: the deadline test, the stall test,
// and `t.After(latest)` itself, so `latest` stayed ZERO, the sleep fell to its
// floor, and the command polled every two seconds without end. Measured by the
// reviewer with a fake gh on PATH: seven calls in thirteen seconds under
// `--timeout 3s`. The bound is compared on the RAW int64 for that same reason
// -- a comparison made after the overflow is a comparison on nonsense.
func resetInstant(name string, unix int64) (time.Time, error) {
	if unix < resetFloor.Unix() || unix > now().Add(resetHorizon).Unix() {
		return time.Time{}, cli.Unanswerable(
			"rate_limit reported a reset of %d for %s, which is not an instant to wait for (expected between %s and %s from now)",
			unix, name, resetFloor.Format("2006-01-02"), resetHorizon)
	}
	return time.Unix(unix, 0), nil
}

// describe prints what the named resources held when the wait ended.
func describe(res map[string]resource, wants []want) string {
	var b strings.Builder
	for i, w := range wants {
		if i > 0 {
			b.WriteString(" ")
		}
		r := res[w.name]
		fmt.Fprintf(&b, "%s %d/%d of %d", w.name, r.Remaining, w.threshold, r.Limit)
	}
	return b.String()
}

// describeWants prints the conditions, for a run that ended without meeting
// them: a timeout must say what it was waiting for, not only that it waited.
func describeWants(wants []want) string {
	var parts []string
	for _, w := range wants {
		parts = append(parts, fmt.Sprintf("%s %d", w.flag, w.threshold))
	}
	return strings.Join(parts, " ")
}

// readBudget reads rate_limit and parses it in Go.
//
// ⛔ NOT THROUGH --jq: this needs three numbers per resource and has to tell an
// empty answer from a zero one. A --jq expression that selects nothing prints an
// empty line, which unmarshals into a zero and reads exactly like a spent
// budget.
func readBudget(deadline time.Time) (map[string]resource, error) {
	// ⛔⛔ THE CHILD IS BOUNDED, AND NOT BOUNDING IT LEFT --timeout A SENTENCE
	// WITH NO CODE FOR THE ONE FAILURE AN AGENT CANNOT RECOVER FROM. Review
	// measured a gh that sleeps 15s: `--timeout 1s` took 31 SECONDS, because
	// exec.Command has no deadline of its own and the capped writer reported
	// every write as a success, so a child that stalls or streams was read to
	// the end. The bound is the SOONER of the run's deadline and a per-call
	// cap: a single rate_limit answer that takes longer than the cap is a
	// broken seam, not a slow budget.
	until := now().Add(callCap)
	if deadline.Before(until) {
		until = deadline
	}
	ctx, cancel := context.WithTimeout(context.Background(), until.Sub(now()))
	defer cancel()

	out, err := runGH(ctx, "api", "rate_limit")
	if err != nil {
		// ⛔ WHICH BOUND EXPIRED CHANGES THE STATUS, so the caller can tell "I
		// ran out of time" from "this seam does not answer".
		//
		// ⛔ AND IT IS ASKED OF THE BOUND THAT WAS SET, not of the clock. An
		// earlier version re-read now() and compared it to the deadline, which
		// a mutation to `if true` survived: under --timeout 31s a 30s cap plus
		// the latency of killing a child lands after the deadline too, and the
		// run would be told it had run out of time when the seam had simply not
		// answered. `until` already holds the answer, decided above.
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			if until.Equal(deadline) {
				return nil, errRunDeadline
			}
			return nil, cli.Unanswerable("gh did not answer rate_limit within %s", callCap)
		}
		return nil, fmt.Errorf("reading the budget: %w", err)
	}
	var v struct {
		Resources map[string]resource `json:"resources"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &v); err != nil {
		return nil, fmt.Errorf("rate_limit read back as %q", clip(out))
	}
	// ⛔ An answer with no resources is an answer that did not measure. It must
	// not be read as "nothing is short".
	if len(v.Resources) == 0 {
		return nil, errors.New("rate_limit carried no resources at all")
	}
	return v.Resources, nil
}

// maxAnswer caps what this command will hold from gh.
//
// ⚠ rate_limit's answer is a few hundred bytes. An unbounded buffer here would
// let whatever answers on that seam -- a proxy, an error page, a wrong gh on
// PATH -- decide this process's memory, in a command whose whole job is to run
// while a machine is under pressure.
const maxAnswer = 1 << 20

// callCap bounds ONE gh call, under the run's own deadline.
//
// A rate_limit answer is one request and a few hundred bytes; thirty seconds is
// a seam that does not answer, not a budget that is slow.
const callCap = 30 * time.Second

// errRunDeadline says the run's own deadline expired while gh was running, so
// the answer is a timeout rather than an unreadable seam.
var errRunDeadline = errors.New("the run's deadline expired while gh was running")

// errTooBig stops the copy rather than only recording that it should have.
var errTooBig = errors.New("more than the answer this command will hold")

// capped is a writer that stops at maxAnswer and FAILS once it is reached.
//
// ⛔ AN EARLIER VERSION REPORTED EVERY WRITE AS FULLY WRITTEN, on the reasoning
// that a short write would break the child rather than bound this process.
// Review showed that is the wrong way round: a writer that never fails is
// exactly why a child that streams is read to the end, so the cap recorded a
// number and bounded nothing. Breaking the child IS the bound -- os/exec stops
// copying and reports the error, and the child gets a closed pipe.
type capped struct {
	buf  bytes.Buffer
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	room := maxAnswer - c.buf.Len()
	if room <= 0 {
		c.over = true
		return 0, errTooBig
	}
	if len(p) > room {
		c.over = true
		c.buf.Write(p[:room])
		return room, errTooBig
	}
	c.buf.Write(p)
	return len(p), nil
}

// execGH is the real seam. Stdin is closed: a command that inherits a terminal
// can block a wait forever on a prompt nobody sees. The context is what bounds
// a child that stalls or streams.
func execGH(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, ghPath, args...)
	var out, errOut capped
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &out, &errOut, nil
	err := cmd.Run()
	if out.over || errors.Is(err, errTooBig) {
		return nil, fmt.Errorf("gh answered more than %d bytes for rate_limit, which is not that answer", maxAnswer)
	}
	if err != nil {
		return nil, ghError(err, &errOut)
	}
	return out.buf.Bytes(), nil
}

// ghError turns a failed child into the reason a reader gets.
//
// ⛔ IT IS A FUNCTION OF ITS OWN BECAUSE IT WAS UNTESTABLE INSIDE execGH:
// review mutated both of its branches -- dropping the truncation note, and
// dropping the clip -- and BOTH SURVIVED, since reaching them needs a child
// that writes a megabyte to stderr and then fails, which no stock program
// does. The behaviour is the same; what changed is that it can now be put a
// question directly.
//
// ⚠ The cut is declared: an unannounced truncation reads as the whole message,
// and a megabyte of somebody else's HTML is not a reason.
func ghError(err error, errOut *capped) error {
	msg := strings.TrimSpace(errOut.buf.String())
	if msg == "" {
		return err
	}
	if errOut.over {
		return fmt.Errorf("%s [truncated at %d bytes]", clip([]byte(msg)), maxAnswer)
	}
	return errors.New(clip([]byte(msg)))
}

// clip shortens an answer for a message, counting RUNES: slicing bytes at 120
// can cut a rune in half and put a replacement character in a report.
func clip(b []byte) string {
	s := strings.TrimSpace(string(b))
	if r := []rune(s); len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return s
}
