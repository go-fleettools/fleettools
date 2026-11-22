package fleet

import (
	"errors"
	"strings"
	"testing"
)

// perResource answers a rate_limit read from a per-resource table, keyed by the
// jq path it was asked for, and records the paths read.
func perResource(core, graphql string, paths *[]string) func([]string) ([]byte, string, error) {
	return func(args []string) ([]byte, string, error) {
		if len(args) > 1 && args[1] == "rate_limit" {
			jq := args[len(args)-1]
			if paths != nil {
				*paths = append(*paths, jq)
			}
			switch jq {
			case ".resources.core.remaining":
				return []byte(core + "\n"), "", nil
			case ".resources.graphql.remaining":
				return []byte(graphql + "\n"), "", nil
			}
			return nil, "unexpected jq " + jq, errors.New("unexpected jq")
		}
		return []byte("ok"), "", nil
	}
}

func TestResourceOfChargesOnlyGraphQLToGraphQL(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"api", "graphql", "-f", "query=x"}, resGraphQL},
		{[]string{"api", "graphql"}, resGraphQL},
		{[]string{"api", "orgs/x/repos"}, resCore},
		{[]string{"api"}, resCore},
		{[]string{"graphql", "api"}, resCore},
		{[]string{"pr", "view", "graphql"}, resCore},
		{nil, resCore},
	} {
		if got := resourceOf(c.args); got != c.want {
			t.Errorf("resourceOf(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

// TestCoreAtZeroDoesNotBlockGraphQL is the measured defect: REST spent, GraphQL
// untouched, and a GraphQL-only tool refused with "0 left".
func TestCoreAtZeroDoesNotBlockGraphQL(t *testing.T) {
	var paths []string
	withRun(t, perResource("0", "5000", &paths))
	if _, err := GH("api", "graphql", "-f", "query=x"); err != nil {
		t.Fatalf("graphql refused while only core is spent: %v", err)
	}
	if len(paths) != 1 || paths[0] != ".resources.graphql.remaining" {
		t.Errorf("rate_limit read %v, want only the graphql path", paths)
	}
	// The core budget is still guarded on its own first call.
	_, err := GH("api", "user/orgs")
	if !errors.Is(err, ErrBudgetReserve) {
		t.Fatalf("core call at 0: err = %v, want ErrBudgetReserve", err)
	}
	if !strings.Contains(err.Error(), "stopped at the budget reserve: 0 left") {
		t.Errorf("core wording changed: %v", err)
	}
	if len(paths) != 2 || paths[1] != ".resources.core.remaining" {
		t.Errorf("rate_limit read %v, want the core path second", paths)
	}
}

func TestGraphQLAtReserveBlocksGraphQLButNotCore(t *testing.T) {
	withRun(t, perResource("5000", "1000", nil))
	_, err := GH("api", "graphql", "-f", "query=x")
	if !errors.Is(err, ErrBudgetReserve) {
		t.Fatalf("err = %v, want ErrBudgetReserve", err)
	}
	if !strings.Contains(err.Error(), "stopped at the graphql budget reserve: 1000 left, 1000 reserved") {
		t.Errorf("the refusal does not name the resource: %v", err)
	}
	if _, err := GH("api", "user/orgs"); err != nil {
		t.Fatalf("core refused while only graphql is at the reserve: %v", err)
	}
	if left, tracking := BudgetLeft(); !tracking || left != 4999 {
		t.Errorf("core left=%d tracking=%v, want 4999 and true", left, tracking)
	}
}

func TestGraphQLCallsAreCountedOnTheirOwn(t *testing.T) {
	withRun(t, perResource("5000", "1002", nil))
	for i := 0; i < 2; i++ {
		if _, err := GH("api", "graphql"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if _, err := GH("api", "graphql"); !errors.Is(err, ErrBudgetReserve) {
		t.Fatalf("third call: err = %v, want ErrBudgetReserve", err)
	}
	// None of that was charged to REST.
	if _, err := GH("api", "user/orgs"); err != nil {
		t.Fatal(err)
	}
	if left, _ := BudgetLeft(); left != 4999 {
		t.Errorf("core left = %d, want 4999 (one core call)", left)
	}
}

func TestStopBudgetResetsBothResources(t *testing.T) {
	withRun(t, perResource("5000", "5000", nil))
	if _, err := GH("api", "graphql"); err != nil {
		t.Fatal(err)
	}
	if _, err := GH("api", "user/orgs"); err != nil {
		t.Fatal(err)
	}
	StopBudget()
	for r, p := range budgets {
		if p.tracking || p.tried || p.left != 0 || p.reserve != 0 {
			t.Errorf("%s projection survived StopBudget: %+v", r, *p)
		}
	}
}

func TestAnUnreadableGraphQLBudgetFailsOpenAndNamesTheResource(t *testing.T) {
	withRun(t, func(args []string) ([]byte, string, error) {
		if len(args) > 1 && args[1] == "rate_limit" {
			return nil, "boom", errors.New("boom")
		}
		return []byte("ok"), "", nil
	})
	var w strings.Builder
	old := BudgetWarn
	BudgetWarn = &w
	t.Cleanup(func() { BudgetWarn = old })
	if _, err := GH("api", "graphql"); err != nil {
		t.Fatalf("an unreadable budget must fail open: %v", err)
	}
	if !strings.Contains(w.String(), "hourly graphql budget could not be read") ||
		!strings.Contains(w.String(), "UNGUARDED") {
		t.Errorf("warning = %q", w.String())
	}
}
