package fleet

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// withRun replaces the exec seam and restores it, and forgets any projection,
// so one test cannot carry its budget into the next.
func withRun(t *testing.T, f func(args []string) ([]byte, string, error)) {
	t.Helper()
	old := run
	run = f
	StopBudget()
	t.Cleanup(func() { run = old; StopBudget() })
}

// answers replies to a rate_limit read with left, and to anything else with ok.
func answers(left string, calls *int) func([]string) ([]byte, string, error) {
	return func(args []string) ([]byte, string, error) {
		if calls != nil {
			*calls++
		}
		if len(args) > 1 && args[1] == "rate_limit" {
			return []byte(left + "\n"), "", nil
		}
		return []byte("ok"), "", nil
	}
}

func TestABudgetBelowTheFloorRefusesToBegin(t *testing.T) {
	withRun(t, answers("800", nil))
	err := StartBudget(2500, DefaultReserve)
	if !errors.Is(err, ErrBudgetFloor) {
		t.Fatalf("err = %v, want ErrBudgetFloor", err)
	}
	if !strings.Contains(err.Error(), "800") || !strings.Contains(err.Error(), "2500") {
		t.Errorf("the error does not say both numbers: %v", err)
	}
	// ⛔ Nothing is tracked after a refusal: a sweep that ignored the error
	// must not then run under a projection it never established.
	if _, tracking := BudgetLeft(); tracking {
		t.Error("the budget is tracked after a refusal")
	}
}

func TestABudgetAboveTheFloorBegins(t *testing.T) {
	withRun(t, answers("5000", nil))
	if err := StartBudget(2500, 1000); err != nil {
		t.Fatalf("StartBudget: %v", err)
	}
	left, tracking := BudgetLeft()
	if !tracking || left != 5000 {
		t.Errorf("left=%d tracking=%v, want 5000 and true", left, tracking)
	}
}

// TestTheSweepStopsAtTheReserveRatherThanAtZero.
//
// ⛔ Stopping is the point, not failing: every sweep here already knows how to
// report INCOMPLETE, and an honest partial answer beats a complete one that
// killed somebody else's scheduled run.
func TestTheSweepStopsAtTheReserveRatherThanAtZero(t *testing.T) {
	withRun(t, answers("1005", nil))
	if err := StartBudget(0, 1000); err != nil {
		t.Fatal(err)
	}
	// Five calls fit between 1005 and the reserve of 1000.
	for i := 0; i < 5; i++ {
		if _, err := GH("api", "repos/x/y"); err != nil {
			t.Fatalf("call %d was refused early: %v", i, err)
		}
	}
	_, err := GH("api", "repos/x/y")
	if !errors.Is(err, ErrBudgetReserve) {
		t.Fatalf("err = %v, want ErrBudgetReserve", err)
	}
	if !strings.Contains(err.Error(), "1000") {
		t.Errorf("the error does not name the reserve: %v", err)
	}
	left, _ := BudgetLeft()
	if left != 1000 {
		t.Errorf("left=%d, want the reserve 1000 -- it stopped AT the floor, not past it", left)
	}
}

// TestARetryIsOneUnitOfBudget.
//
// ⛔ Charging each attempt would stop a sweep early on its own patience: a
// throttled call retried five times is the same unit of work asking again.
func TestARetryIsOneUnitOfBudget(t *testing.T) {
	Backoff = func(int) time.Duration { return 0 }
	attempts := 0
	withRun(t, func(args []string) ([]byte, string, error) {
		if len(args) > 1 && args[1] == "rate_limit" {
			return []byte("1010\n"), "", nil
		}
		attempts++
		if attempts < 3 {
			return nil, "secondary rate limit", errors.New("exit 1")
		}
		return []byte("ok"), "", nil
	})
	if err := StartBudget(0, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := GH("api", "repos/x/y"); err != nil {
		t.Fatalf("GH: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("%d attempts, want 3", attempts)
	}
	left, _ := BudgetLeft()
	if left != 1009 {
		t.Errorf("left=%d, want 1009 -- three attempts charged %d", left, 1010-left)
	}
}

// TestWithoutStartBudgetNothingIsCharged: a command that never asked for the
// guard keeps working exactly as before, so adding this cannot break the
// sweeps that do not use it yet.
func TestWithoutStartBudgetNothingIsCharged(t *testing.T) {
	calls := 0
	withRun(t, answers("5000", &calls))
	for i := 0; i < 20; i++ {
		if _, err := GH("api", "repos/x/y"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if left, tracking := BudgetLeft(); tracking || left != 0 {
		t.Errorf("left=%d tracking=%v, want 0 and false", left, tracking)
	}
	if calls != 20 {
		t.Errorf("%d calls reached the seam, want 20", calls)
	}
}

// TestAnUnreadableBudgetIsNotAFreePass.
//
// ⛔ A guard that cannot read its own counter must refuse, not proceed: the one
// thing worse than no guard is one that reports a clean start it never
// measured.
func TestAnUnreadableBudgetIsNotAFreePass(t *testing.T) {
	for _, c := range []struct {
		name string
		f    func([]string) ([]byte, string, error)
	}{
		{"gh fails", func([]string) ([]byte, string, error) {
			return nil, "could not resolve host", errors.New("exit 1")
		}},
		{"an answer that is not a number", func([]string) ([]byte, string, error) {
			return []byte("{\"message\":\"Not Found\"}\n"), "", nil
		}},
		{"an empty answer", func([]string) ([]byte, string, error) {
			return []byte("\n"), "", nil
		}},
	} {
		withRun(t, c.f)
		Backoff = func(int) time.Duration { return 0 }
		if err := StartBudget(2500, 1000); err == nil {
			t.Errorf("%s: StartBudget accepted it", c.name)
		}
		if _, tracking := BudgetLeft(); tracking {
			t.Errorf("%s: tracking after a failed read", c.name)
		}
	}
}

// TestTheReserveDefaultsRatherThanDisabling: a caller passing zero means "use
// the house figure", not "no reserve" -- the second reading would turn a
// mistyped flag into the behaviour this package exists to prevent.
func TestTheReserveDefaultsRatherThanDisabling(t *testing.T) {
	withRun(t, answers("1005", nil))
	if err := StartBudget(0, 0); err != nil {
		t.Fatal(err)
	}
	// With the default reserve of 1000, five calls fit and the sixth stops.
	for i := 0; i < 5; i++ {
		if _, err := GH("api", "x"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if _, err := GH("api", "x"); !errors.Is(err, ErrBudgetReserve) {
		t.Errorf("err = %v, want ErrBudgetReserve -- zero disabled the reserve", err)
	}
}
