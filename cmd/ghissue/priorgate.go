package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/priorledger"
)

// The prior-art gate on --new.
//
// priorart (#193) answers "has this already been done?", but nothing tied its
// answer to the act of filing. Twice in two days an issue was opened in the
// SAME shell command as the priorart run meant to precede it, so the verdict
// (exit 1: found) arrived after the issue existed: go-gitsafe/gitsafe#99
// duplicated #90, which PR #96 was already implementing. A check in the same
// command as the act it guards is a log, not a gate.
//
// So --new reads priorart's ledger (internal/priorledger) and refuses unless a
// run covering the repository happened within priorWindow:
//
//   - verdict "none found": the issue is filed, and says so;
//   - verdict "found" or "a surface unread": --prior-art "<what was read and
//     why this is not a duplicate>" is required;
//   - no run at all: refused, naming the priorart command; --prior-art-waiver
//     "<reason>" files anyway.
//
// Every outcome is STAMPED into the published body, as the private-path waiver
// is: a waiver is a statement made in public, not a switch flicked in private.
// The gate cannot prove the list was READ; it makes the run come first, and
// makes "I read it, and here is why this is new" something written down.

// priorWindow is how recent a priorart run must be.
const priorWindow = 2 * time.Hour

// maxPriorText bounds --prior-art and --prior-art-waiver: a line, not a body.
const maxPriorText = 500

// priorNow is the clock; tests replace it.
var priorNow = time.Now

func oneLine(flag, s string) error {
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("%s is one line", flag)
	}
	if len(s) > maxPriorText {
		return fmt.Errorf("%s is over %d bytes: a line, not a body", flag, maxPriorText)
	}
	return nil
}

// priorArtStamp returns the line appended to a new issue's body, or the
// refusal.
func priorArtStamp(repo, checked, waiver string) (string, error) {
	checked, waiver = strings.TrimSpace(checked), strings.TrimSpace(waiver)
	if err := oneLine("--prior-art", checked); err != nil {
		return "", err
	}
	if err := oneLine("--prior-art-waiver", waiver); err != nil {
		return "", err
	}
	if waiver != "" {
		return fmt.Sprintf("Prior art NOT checked with priorart: %s", waiver), nil
	}
	now := priorNow()
	rec, err := priorledger.Latest(repo, priorWindow, now)
	if errors.Is(err, priorledger.ErrNone) {
		return "", fmt.Errorf("no priorart run covers %s in the last %s.\n"+
			"  Run it ALONE, read what it lists, then file:\n"+
			"      priorart --repo %s \"<subject>\"\n"+
			"  (a priorart run in the same command as --new cannot stop it: that is how\n"+
			"  go-gitsafe/gitsafe#99 duplicated #90.) To file without it, say why:\n"+
			"      --prior-art-waiver \"<reason>\"   (stamped into the issue)",
			repo, priorWindow, repo)
	}
	if err != nil {
		return "", err
	}
	when := rec.Time.Local().Format("2006-01-02 15:04 MST")
	switch rec.Verdict {
	case priorledger.None:
		s := fmt.Sprintf("Prior art: `priorart` %q at %s found none.", rec.Subject, when)
		if checked != "" {
			s += " " + checked
		}
		return s, nil
	default:
		what := "reported prior art"
		if rec.Verdict == priorledger.Gap {
			what = "could not read every surface"
		}
		if checked == "" {
			return "", fmt.Errorf("priorart %q at %s %s for %s.\n"+
				"  READ that list, then say what you read and why this issue is not a duplicate:\n"+
				"      --prior-art \"<e.g. #82 covers pak only; this is the lmp parser>\"   (stamped into the issue)",
				rec.Subject, when, what, repo)
		}
		return fmt.Sprintf("Prior art: `priorart` %q at %s %s; checked: %s", rec.Subject, when, what, checked), nil
	}
}
