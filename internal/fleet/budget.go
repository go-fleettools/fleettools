package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
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
var (
	budgetMu       sync.Mutex
	budgetLeft     int  // what the primary budget had, less the calls made since
	budgetReserve  int  // the floor this package stops at, chosen
	budgetTracking bool // false until StartBudget succeeds
)

// ErrBudgetFloor means the budget was already below what the sweep asked for,
// so it never began. Beginning anyway is what leaves a sweep half done and a
// Renovate run dead.
var ErrBudgetFloor = errors.New("fleet: the hourly REST budget is below the floor this sweep asked for")

// ErrBudgetReserve means the sweep ran down to the reserve and stopped. Every
// sweep here already knows how to report INCOMPLETE, which is the honest
// answer and the reason stopping is better than failing.
var ErrBudgetReserve = errors.New("fleet: stopped at the budget reserve")

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
	budgetLeft, budgetReserve, budgetTracking = left, reserve, true
	return nil
}

// BudgetLeft is the projection, and whether anything is tracking it. A caller
// that prints a figure must also say when there is none: "0 left" and "not
// watched" are different answers.
func BudgetLeft() (left int, tracking bool) {
	budgetMu.Lock()
	defer budgetMu.Unlock()
	return budgetLeft, budgetTracking
}

// StopBudget forgets the projection. A test uses it; a long-lived process that
// starts a second sweep in a new hour would too.
func StopBudget() {
	budgetMu.Lock()
	defer budgetMu.Unlock()
	budgetLeft, budgetReserve, budgetTracking = 0, 0, false
}

// spend accounts for one call and reports whether the reserve has been reached.
// It is consulted BEFORE the call, so the reserve is a floor the sweep stops
// at rather than one it crosses.
func spend() error {
	budgetMu.Lock()
	defer budgetMu.Unlock()
	if !budgetTracking {
		return nil
	}
	if budgetLeft <= budgetReserve {
		return fmt.Errorf("%w: %d left, %d reserved for everything else",
			ErrBudgetReserve, budgetLeft, budgetReserve)
	}
	budgetLeft--
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
