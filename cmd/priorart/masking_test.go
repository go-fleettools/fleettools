package main

import (
	"errors"
	"strings"
	"testing"
)

// ⛔⛔ A HIT ON ONE SURFACE MUST NOT MASK ANOTHER THAT WAS NEVER READ.
//
// `found > 0` was tested first, so a branch match returned "already covered" —
// exit 1 — on a run whose tracker had answered HTTP 422 and been read by
// nobody. The warning was printed above a verdict that said the opposite.
//
// The status is what a gate in front of work keys on: `exit 1 = already done,
// 2 = a surface unread` is what callers are told to branch on, so exit 1 reads
// as "found, stop" — from a run whose most valuable surface was never queried.
// Measured 2026-11-09 on two repositories, both with the warning printed and
// both exiting 1. On one of them the branches genuinely held the answer, so the
// masking cost nothing: that is luck, not design.
func TestAnUnreadSurfaceOutranksAHitElsewhere(t *testing.T) {
	stub(t, func(args []string) ([]byte, error) {
		if args[1] == "graphql" {
			// A branch matches: this is the surface that used to win.
			return []byte(`{"data":{"repository":{"refs":{"nodes":[` +
				`{"name":"running-head","target":{"message":"running head: fix"}}]}}}}`), nil
		}
		// And the tracker is unreadable.
		return nil, errors.New("gh: Validation Failed (HTTP 422)")
	})
	code, out, errOut := drive(t, "-repo", "go-tex/engine", "running head")
	if code != 2 {
		t.Fatalf("status %d, want 2: a branch hit masked a tracker nobody read\n%s", code, out)
	}
	// Both facts reach the reader, because the status is one number and they
	// need the other half. The verdict goes to stderr and the surface report to
	// stdout, so the claim is about what a person sees, not about one stream.
	both := out + errOut
	if !strings.Contains(both, "NOT READ") {
		t.Errorf("the unread surface is not named:\n%s", both)
	}
	if !strings.Contains(both, "could not be read") {
		t.Errorf("the verdict does not mention the gap:\n%s", both)
	}
	if !strings.Contains(both, "carry prior art") {
		t.Errorf("the verdict dropped the prior art that WAS found:\n%s", both)
	}
}

// And with every surface read, a hit still exits 1 — the masking fix must not
// turn "already covered" into "unanswerable" for the ordinary case.
func TestAHitWithEverySurfaceReadStillExitsOne(t *testing.T) {
	stub(t, func(args []string) ([]byte, error) {
		if args[1] == "graphql" {
			return []byte(`{"data":{"repository":{"refs":{"nodes":[` +
				`{"name":"running-head","target":{"message":"running head: fix"}}]}}}}`), nil
		}
		return []byte(emptySearch), nil
	})
	code, out, _ := drive(t, "-repo", "go-tex/engine", "running head")
	if code != 1 {
		t.Fatalf("status %d, want 1 (prior art found, everything read)\n%s", code, out)
	}
}
