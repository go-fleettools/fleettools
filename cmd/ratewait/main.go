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

	// runGH is the exec seam, replaced by the tests.
	//
	// ⛔ IT IS NOT [fleet.GH], AND THE REASON IS THE SAME ONE budget.go GIVES
	// FOR ITS OWN autoStart: GH accounts for every call against the budget
	// projection, and this command's one call is rate_limit, which costs
	// nothing. Charging it would make the guard believe a call was spent that
	// was not -- the MEASUREMENT must not move what it measures.
	//
	// ⚠ THE COMMAND IS NOT FREE, AND AN EARLIER VERSION OF THIS COMMENT
	// OVERCLAIMED THAT IT WAS. Review measured it: [fleet.WarnIfStale], which
	// every command here calls first and a repository-wide test requires, makes
	// one uncached `repos/.../compare/...` request per run. So a wait costs one
	// call plus one free reading per round, and under an exhausted budget the
	// staleness note is the thing that prints a 403 -- as it did at 22:06 CET
	// on 2026-11-15, above ghissue's own refusal. The claim is about the
	// budget READING, not about the process.
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
// reset instant that stopped advancing -- a fault to report rather than to sit
// through.
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

A reset is only believed if it is an instant to wait for: at or after 2020 and
no more than 24 hours out. The largest int64, which JSON accepts, overflows into
a time before year 1 and loses every comparison it is in, which made the loop
poll on its floor without end -- seven calls in thirteen seconds under
--timeout 3s.

--timeout bounds the RUN, in elapsed time, not merely the next sleep.

⚠ One more cost to be exact about: like every command here, this one calls
fleet.WarnIfStale first, which spends ONE request asking whether the binary is
current. The budget reading itself is free; the process is not.

exit 0 every named resource is at or above its threshold
exit 1 the deadline passed in elapsed time, or the reset stopped advancing
exit 2 the question could not be put: nothing named, an impossible threshold, a
       short resource with no limit, a reset that is not an instant, an
       unreadable or empty answer`)

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
		return waitFor(c, wants, now().Add(*timeout))
	}
	return c
}

// waitFor is the whole command: read, answer or sleep to the reset, read again.
//
// ⛔ EVERY ROUND MUST SLEEP TO A STRICTLY LATER RESET THAN THE LAST ONE, and
// that is what bounds this loop. Two readings can leave it with nothing to wait
// for: a reset instant already in the past (a stale reading, or a clock that
// disagrees with GitHub's), and a window that reset while the resource stayed
// short. In both, the distance to the reset is zero or negative, sleep returns
// at once, and the loop becomes the tight poll this command exists to avoid --
// spending the budget it is waiting for, a thousand calls inside one deadline.
// A reset that did not advance is therefore a FINDING, which is what this
// command's own help has always said ("the reset stopped advancing") and what
// its code did not do until a review of that sentence.
func waitFor(c *cobra.Command, wants []want, deadline time.Time) error {
	var sleptTo time.Time // the reset this loop has already waited past
	for {
		res, err := readBudget()
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

		// ⛔ ROUND 1 CANNOT STALL WITHOUT A GUARD OF ITS OWN ONLY BECAUSE
		// resetInstant REFUSES A ZERO OR ABSURD INSTANT: latest is therefore
		// always at or after 2020, which is after the zero Time that sleptTo
		// starts at. The earlier `!sleptTo.IsZero() &&` clause was removed
		// because a mutation deleting it survived every test -- it could only
		// ever matter for the zero latest that is now impossible, and a
		// condition that cannot fire reads as a guard without being one.
		if !latest.After(sleptTo) {
			fmt.Fprintf(cli.Out(c), "STALLED %s, the reset at %s did not advance after the wait\n",
				strings.Join(short, ", "), latest.Local().Format("2006-01-02 15:04:05 MST"))
			return cli.Found("the reset did not advance: %s still short of %s",
				strings.Join(short, ", "), describeWants(wants))
		}

		wake := latest.Add(resetMargin)
		// ⛔ THE DEADLINE IS ELAPSED TIME, AND COMPARING IT ONLY TO wake WAS A
		// SENTENCE WITH NO CODE: the help promised "exit 1 the deadline passed
		// first" while nothing ever compared now() to it, so --timeout bounded
		// the next sleep rather than the run. Both tests are needed -- wake
		// catches a reset beyond the deadline before sleeping into it, now()
		// catches a run that has already spent its time on earlier rounds.
		if now().After(deadline) || wake.After(deadline) {
			// ⛔ A FINDING PRINTS ITSELF. cli.Status does not print the message
			// of a status 1 -- "the command has already printed what it found"
			// -- so a timeout that only returned the error would exit 1 in
			// silence, which is the one shape a caller cannot act on. It goes
			// on stdout, next to PASS, because both are this command's answer.
			fmt.Fprintf(cli.Out(c), "TIMEOUT %s, waited for %s, the deadline passed at %s\n",
				strings.Join(short, ", "), describeWants(wants), deadline.Local().Format("2006-01-02 15:04:05 MST"))
			return cli.Found("the deadline passed before %s", describeWants(wants))
		}
		left := wake.Sub(now())
		// ⛔ A reset already behind us gives a negative distance, and sleep
		// would return instantly. The floor is what keeps one stale reading
		// from becoming a poll; the stall check above is what keeps it from
		// becoming an hour of them.
		if left < resetMargin {
			left = resetMargin
		}
		// ⛔ THE DATE IS PRINTED, NOT ONLY THE CLOCK. A year-1 instant read as
		// "00:09:21" in the reviewer's log, which looks like a plausible reset
		// and is how the overflow above stayed invisible.
		fmt.Fprintf(cli.Err(c), "%s -- sleeping %s, to the reset at %s\n",
			strings.Join(short, ", "), left.Round(time.Second), latest.Local().Format("2006-01-02 15:04:05 MST"))
		sleep(left)
		sleptTo = latest
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
func readBudget() (map[string]resource, error) {
	out, err := runGH("api", "rate_limit")
	if err != nil {
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

// capped is a writer that stops at maxAnswer and remembers that it did.
//
// It reports every write as fully written: a short write would break the child
// process rather than bound this one.
type capped struct {
	buf  bytes.Buffer
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := maxAnswer - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.over = true
			c.buf.Write(p[:room])
			return len(p), nil
		}
		c.buf.Write(p)
		return len(p), nil
	}
	if len(p) > 0 {
		c.over = true
	}
	return len(p), nil
}

// execGH is the real seam. Stdin is closed: a command that inherits a terminal
// can block a wait forever on a prompt nobody sees.
func execGH(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	var out, errOut capped
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &out, &errOut, nil
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errOut.buf.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	if out.over {
		return nil, fmt.Errorf("gh answered more than %d bytes for rate_limit, which is not that answer", maxAnswer)
	}
	return out.buf.Bytes(), nil
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
