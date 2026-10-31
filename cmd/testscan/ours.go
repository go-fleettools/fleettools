package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/go-fleettools/fleettools/internal/checkout"
)

// sift drops the findings that are not ours and says which and why.
//
// The judgement lives in internal/checkout because judgescan needs the same
// one, and two copies of a rule is one copy being wrong — which is how this
// tool learnt to count commits behind while judgescan did not.
func sift(found []finding) (kept []finding, dropped []string) {
	repos := make([]string, 0, len(found))
	for _, f := range found {
		repos = append(repos, f.repo)
	}
	owners, have := checkout.Owners()
	keep, dropped := checkout.Ours(repos, owners, have, checkout.Ask)
	for _, f := range found {
		if keep[f.repo] {
			kept = append(kept, f)
		}
	}
	return kept, dropped
}

// reportDropped prints what was left out and why. A silent filter is one
// nobody can check.
//
// It takes its writer rather than reaching for os.Stderr, so that a test can
// read the filter's own account of itself -- which is the only way to tell a
// filter that dropped nothing from one that dropped everything and said so
// into a stream nobody captured.
func reportDropped(w io.Writer, dropped []string) {
	if len(dropped) == 0 {
		return
	}
	sort.Strings(dropped)
	fmt.Fprintf(w, "\n%d checkout(s) left out — not ours to fix:\n", len(dropped))
	for _, d := range dropped {
		fmt.Fprintf(w, "  %s\n", d)
	}
}
