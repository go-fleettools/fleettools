package main

import (
	"errors"
	"testing"
)

func TestRefusalKindSeparatesCausesThatNeedDifferentAnswers(t *testing.T) {
	cases := []struct{ msg, want string }{
		{"GraphQL: refusing to allow an OAuth App to create or update workflow `.github/workflows/docs.yml` without `workflow` scope (mergePullRequest)", "workflow-scope"},
		{"GraphQL: Pull Request has merge conflicts (mergePullRequest)", "conflict"},
		{"GraphQL: Base branch was modified. Review and try the merge again.", "base-moved"},
		{"To have the pull request merged after all the requirements have been met, add the `--auto` flag.", "requirements-unmet"},
		{"something nobody has seen before", "other"},
	}
	for _, c := range cases {
		// A summary that counts these together is what let a conflict read as
		// a scope refusal twice in one day.
		if got := refusalKind(errors.New(c.msg)); got != c.want {
			t.Errorf("refusalKind(%.44q…) = %q, want %q", c.msg, got, c.want)
		}
	}
}

func TestTheTwoRateLimitsWantOppositeTreatment(t *testing.T) {
	// A sweep of 206 candidates makes thousands of calls. Treating the hourly
	// budget as a burst brake slept five minutes per call and reached nothing:
	// the budget resets at a fixed time, not after a backoff.
	for _, c := range []struct {
		msg  string
		want bool
	}{
		{"You have exceeded a secondary rate limit", true},
		{"was submitted too quickly", true},
		{"triggered an abuse detection mechanism", true},
		{"API rate limit exceeded for user ID 11405852.", false},
		{"gh: not logged in", false},
	} {
		if got := throttled(c.msg); got != c.want {
			t.Errorf("throttled(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

// Which organisations a run sweeps, which decides what it costs.
//
// The whole fleet is ~300 candidates and thousands of REST calls, against a
// budget Renovate is also drawing on. Naming organisations is what lets a run
// be scoped to where its result can be checked — and the absence of that was
// how eighteen pull requests across three organisations came to be merged by
// somebody typing `prmerge -h` and expecting a usage message.
func TestOrgsToSweepPrefersWhatWasNamed(t *testing.T) {
	refuse := func() ([]byte, error) { return nil, errors.New("the fleet must not be asked for") }

	got, err := orgsToSweep([]string{"openweft", "go-crdt"}, refuse)
	if err != nil {
		t.Fatal(err)
	}
	// Sorted, because the caller batches them into search queries and two runs
	// naming the same organisations should ask the same questions.
	if len(got) != 2 || got[0] != "go-crdt" || got[1] != "openweft" {
		t.Errorf("orgsToSweep named = %v, want [go-crdt openweft]", got)
	}

	// And it must not have asked: a scoped run that still enumerates the fleet
	// pays the cost it was scoped to avoid.
	if _, err := orgsToSweep([]string{"one"}, refuse); err != nil {
		t.Errorf("a named sweep consulted the fleet: %v", err)
	}
}

// The control: with nothing named it still sweeps everything, which is the
// behaviour every existing caller has.
func TestOrgsToSweepFallsBackToTheWholeFleet(t *testing.T) {
	got, err := orgsToSweep(nil, func() ([]byte, error) {
		return []byte("  openweft \n\ngo-crdt\n"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "go-crdt" || got[1] != "openweft" {
		t.Errorf("orgsToSweep unnamed = %v, want [go-crdt openweft] with blanks and spaces dropped", got)
	}

	if _, err := orgsToSweep(nil, func() ([]byte, error) {
		return nil, errors.New("no token")
	}); err == nil {
		t.Error("a fleet sweep whose enumeration failed reported no error, and would have swept nothing quietly")
	}
}
