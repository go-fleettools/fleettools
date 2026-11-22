package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// The primary hourly REST budget, and the one place a sweep is stopped before
// it spends somebody else's share of it.
//
// ⛔ WHY THIS EXISTS: four times now a fleet sweep has exhausted the hourly
// budget and made the fleet's Renovate runners fail. The budget is PER USER,
// not per token, so a sweep and a scheduled Renovate run draw on the same
// 5000. The symptom is not obvious from the other end -- Renovate reports a
// spent budget as
//
//	FATAL: Initialization error  "errorMessage": "Authentication failure"
//
// which sends the reader to check a token that has nothing wrong with it. The
// note said "the control must be in the TOOL, not in a note" after the third
// time, and the fourth happened anyway, with the note read afterwards.
//
// ⛔ THIS DOES NOT PROTECT AGAINST THE SECONDARY LIMIT, and saying so is half
// the point. `rate_limit` is BLIND to it: measured 2026-10-10, gh answered 403
// on every call while rate_limit reported 5000/5000 remaining. A guard that
// only watches the primary budget and does not say so reads as "safe to
// sweep". The secondary limit has no readable counter; the only control for it
// is a real call, and the only remedy is time.
//
// ⛔ ONE PROJECTION PER RESOURCE. GitHub's `core` (REST) and `graphql` budgets
// are separate per-user counters: measured 2026-11-22, `renovatedup`, which only
// speaks `gh api graphql`, was refused with "0 left" because REST was spent
// while GraphQL was untouched. A call is charged to the resource it spends
// (see [resourceOf]); GraphQL stays guarded, since Renovate spends it too.
const (
	resCore    = "core"
	resGraphQL = "graphql"
)

// projection is what one resource had, less the calls made since.
type projection struct {
	left     int  // what the budget had, less the calls made since
	reserve  int  // the floor this package stops at, chosen
	tracking bool // false until a start succeeds
	tried    bool // an automatic start has been attempted (budgetStartMu)
}

var (
	budgetMu sync.Mutex // guards left, reserve, tracking of every projection
	budgets  = map[string]*projection{resCore: {}, resGraphQL: {}}
)

// resourceOf names the budget a gh call is charged to: `gh api graphql ...`
// spends the GraphQL one, everything else the REST `core` one.
func resourceOf(args []string) string {
	if len(args) > 1 && args[0] == "api" && args[1] == "graphql" {
		return resGraphQL
	}
	return resCore
}

// reserveError is ErrBudgetReserve naming the resource that ran down.
type reserveError struct {
	resource      string
	left, reserve int
}

func (e *reserveError) Error() string {
	head := ErrBudgetReserve.Error()
	if e.resource != resCore {
		head = "fleet: stopped at the " + e.resource + " budget reserve"
	}
	return fmt.Sprintf("%s: %d left, %d reserved for everything else", head, e.left, e.reserve)
}

func (e *reserveError) Is(target error) bool { return target == ErrBudgetReserve }

// ErrBudgetFloor means the budget was already below what the sweep asked for,
// so it never began. Beginning anyway is what leaves a sweep half done and a
// Renovate run dead.
var ErrBudgetFloor = errors.New("fleet: the hourly REST budget is below the floor this sweep asked for")

// ErrBudgetReserve means the sweep ran down to the reserve and stopped. Every
// sweep here already knows how to report INCOMPLETE, which is the honest
// answer and the reason stopping is better than failing.
var ErrBudgetReserve = errors.New("fleet: stopped at the budget reserve")

// ⛔⛔ AND THE GUARD WAS INERT IN FOURTEEN OF THE FIFTEEN SWEEPS IT WAS WRITTEN
// FOR. Measured 2026-11-02: eighteen files under cmd/ call [GH], and exactly
// ONE -- redscan -- ever called [StartBudget]. Everywhere else budgetTracking
// stayed false, spend returned nil on every call, and the floor and the reserve
// protected nothing at all.
//
// That was a deliberate transition, and its own test said so: "a command that
// never asked for the guard keeps working exactly as before, so adding this
// cannot break the sweeps that do not use it YET". The migration never
// happened, and on 2026-11-01 a full quietscan pass -- one of the fourteen --
// made 1592 calls, drove the shared budget down and came back with 82 of 363
// organisations UNREADABLE. Fifth occurrence of the incident this file exists
// to prevent, in a sweep the file could not see.
//
// ⭐ SO THE GUARD IS NOW THE DEFAULT RATHER THAN A THING TO REMEMBER. A sweep
// that never calls StartBudget gets a projection on its first call anyway. That
// is the same lesson as the note which said "the control must be in the TOOL,
// not in a note": an opt-in control protects the callers who already knew.
var AutoBudget = true

// BudgetWarn is where an unguarded sweep is announced. A test redirects it.
var BudgetWarn io.Writer = os.Stderr

var budgetStartMu sync.Mutex // guards the tried flag of every projection

// autoStart gives a projection to a caller that never asked for one.
//
// ⚠ IT FAILS OPEN, AND SAYS SO. [StartBudget] refuses when it cannot read the
// budget, because a caller that ASKED for a guard must not be told it has one;
// here nobody asked, so refusing would turn every fleet tool into a broken one
// the moment rate_limit is unreadable. It therefore leaves the sweep exactly as
// it was before this change -- unguarded -- and writes one line saying so,
// because the failure mode this whole file is about is a sweep nobody knew was
// unprotected.
func autoStart(resource string) {
	budgetStartMu.Lock()
	defer budgetStartMu.Unlock()
	p := budgets[resource]
	if p.tried {
		return
	}
	p.tried = true

	// ⛔ READ THROUGH THE EXEC SEAM, NOT THROUGH GH: GH accounts for its calls
	// with spend, which is what called us, and a projection that charged itself
	// for being created would recurse.
	out, _, err := run([]string{"api", "rate_limit", "--jq", ".resources." + resource + ".remaining"})
	var left int
	if err == nil {
		err = json.Unmarshal(trimSpaceBytes(out), &left)
	}
	if err != nil {
		if BudgetWarn != nil {
			fmt.Fprintf(BudgetWarn, "fleet: the hourly %s budget could not be read (%v); "+
				"this sweep is UNGUARDED and may spend the share the fleet's Renovate runners need\n", resource, err)
		}
		return
	}

	budgetMu.Lock()
	defer budgetMu.Unlock()
	p.left, p.reserve, p.tracking = left, DefaultReserve, true
}

// DefaultReserve is how much of the hourly budget a sweep leaves behind.
//
// ⛔ It is a CHOSEN number, not a measured one, and the difference matters:
// what is measured is the cost of a sweep (docscan ~1395 calls, redscan ~2000)
// and that the fleet's runners are spread across the day. What is NOT measured
// is what one Renovate run costs, so this cannot be derived. Refining it means
// instrumenting a Renovate run; until then the figure is a judgement and says
// so here rather than looking like arithmetic.
const DefaultReserve = 1000

// StartBudget reads the hourly budget once and refuses to begin below floor.
//
// ⛔ It reads rate_limit ONCE and then counts this package's own calls, rather
// than re-reading per call: a guard that polls its own budget doubles the cost
// of the thing it is protecting. The count is therefore a PROJECTION, exact in
// what this process spends and blind to what anything else spends in the same
// hour -- which is the direction that matters, because it under-estimates what
// is left rather than over.
func StartBudget(floor, reserve int) error {
	if reserve <= 0 {
		reserve = DefaultReserve
	}
	// ⛔ FORGET ANY EARLIER PROJECTION FIRST. StartBudget reads the budget
	// through GH, which is charged against the projection -- so a spent one
	// from a previous sweep refuses the very call that would replace it, and
	// the refusal arrives wearing the words of the reserve rather than of the
	// floor. Found by a witness that asked for "below the floor" and was handed
	// "stopped at the reserve: 7 left, 7 reserved".
	StopBudget()
	// Claim the automatic start before making a call, or the read below goes
	// through spend, finds nothing tracking, and starts a SECOND projection
	// from its own extra request.
	budgetStartMu.Lock()
	budgets[resCore].tried = true
	budgetStartMu.Unlock()

	b, err := GH("api", "rate_limit", "--jq", ".resources.core.remaining")
	if err != nil {
		return fmt.Errorf("fleet: reading the hourly budget: %w", err)
	}
	var left int
	if err := json.Unmarshal(trimSpaceBytes(b), &left); err != nil {
		return fmt.Errorf("fleet: the hourly budget read back as %q", b)
	}
	if left < floor {
		return fmt.Errorf("%w: %d left, %d asked for", ErrBudgetFloor, left, floor)
	}

	budgetMu.Lock()
	defer budgetMu.Unlock()
	p := budgets[resCore]
	p.left, p.reserve, p.tracking = left, reserve, true
	return nil
}

// BudgetLeft is the projection, and whether anything is tracking it. A caller
// that prints a figure must also say when there is none: "0 left" and "not
// watched" are different answers.
func BudgetLeft() (left int, tracking bool) {
	budgetMu.Lock()
	defer budgetMu.Unlock()
	p := budgets[resCore]
	return p.left, p.tracking
}

// StopBudget forgets every projection, REST and GraphQL. A test uses it; a
// long-lived process that starts a second sweep in a new hour would too.
func StopBudget() {
	budgetStartMu.Lock()
	for _, p := range budgets {
		p.tried = false
	}
	budgetStartMu.Unlock()

	budgetMu.Lock()
	defer budgetMu.Unlock()
	for _, p := range budgets {
		p.left, p.reserve, p.tracking = 0, 0, false
	}
}

// spend accounts for one call against the budget it spends and reports whether
// that budget's reserve has been reached. It is consulted BEFORE the call, so
// the reserve is a floor the sweep stops at rather than one it crosses.
func spend(args []string) error {
	resource := resourceOf(args)
	p := budgets[resource]
	budgetMu.Lock()
	tracking := p.tracking
	budgetMu.Unlock()
	if !tracking && AutoBudget {
		autoStart(resource)
	}

	budgetMu.Lock()
	defer budgetMu.Unlock()
	if !p.tracking {
		return nil
	}
	if p.left <= p.reserve {
		return &reserveError{resource: resource, left: p.left, reserve: p.reserve}
	}
	p.left--
	return nil
}

// trimSpaceBytes is strings.TrimSpace without converting, so a numeric answer
// with a trailing newline unmarshals.
func trimSpaceBytes(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && isSpaceByte(b[i]) {
		i++
	}
	for j > i && isSpaceByte(b[j-1]) {
		j--
	}
	return b[i:j]
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
