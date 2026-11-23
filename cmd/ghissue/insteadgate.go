package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// The instead-of-a-pull-request gate on posted text.
//
// Twice the same mistake: findings written up as an issue, or as a comment on
// someone else's tracker, when the work that settles them is a pull request
// (user, 2026-11-22: "les issues ne résolvent rien"; 2026-11-23: "encore une
// fois les PR m'intéressent plus que les issues"). A lesson that recurs becomes
// a gate, so text posted through ghissue (a comment, or --new) needs
// --instead-of-pr "<reason>": a REASON, never a boolean, so that writing it is
// the moment to notice there is a fix to open instead.
//
// Unlike the other waivers the reason is NOT added to the GitHub text: it is
// for the caller, printed on stderr beside the posted URL. close, reopen, edit
// and delete post no new text and are not covered.

// maxInsteadText bounds --instead-of-pr: a line, not a body.
const maxInsteadText = 500

// gateInsteadOfPR returns the trimmed reason, or the refusal.
func gateInsteadOfPR(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", errors.New("refused: findings go into a PULL REQUEST, not an issue or a comment: a regression test plus the fix,\n" +
			"  or a draft PR carrying a prototype. If this text really cannot be a pull request, say why:\n" +
			"      --instead-of-pr \"<why this is not a pull request>\"\n" +
			"  (the reason is printed for you with the posted URL; it is NOT added to the GitHub text.)")
	}
	if err := oneLine("--instead-of-pr", reason); err != nil {
		return "", err
	}
	return reason, nil
}

// reportInsteadOfPR prints the reason beside what was posted.
func reportInsteadOfPR(errOut io.Writer, url, reason string) {
	fmt.Fprintf(errOut, "posted: %s\n  instead of a pull request because: %s\n", url, reason)
}
