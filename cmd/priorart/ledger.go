package main

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/priorledger"
)

// record writes this run's verdict to priorart's ledger, which `ghissue
// --new` reads before it files: an issue opened in the same command as the
// priorart run meant to precede it cannot be stopped by that run, and twice it
// was not (go-gitsafe/gitsafe#99, a duplicate of #90). Only runs that named
// repositories (--repo) or an organisation (--org) are recorded: a --deps-only
// run says nothing about a tracker.
//
// The verdict is the exit status: 0 none found, 1 found, 2 a surface unread or
// the question could not be put. --report prints the findings and exits 0
// "even so", so it is recorded from what it printed, not from its status:
// such a run is recorded as a gap, never as "none found".
func record(warn io.Writer, subject string, repos []string, org string, report bool, runErr error) {
	if len(repos) == 0 && org == "" {
		return
	}
	verdict := priorledger.None
	if runErr != nil {
		verdict = priorledger.Gap
		var e *cli.Exit
		if errors.As(runErr, &e) && e.Code == 1 {
			verdict = priorledger.Found
		}
	}
	if report && verdict == priorledger.None {
		verdict = priorledger.Gap
	}
	err := priorledger.Append(priorledger.Record{Time: time.Now(), Repos: append([]string(nil), repos...),
		Org: org, Subject: subject, Verdict: verdict})
	if err != nil {
		fmt.Fprintf(warn, "priorart: could not record this run (ghissue --new will not see it): %v\n", err)
	}
}
