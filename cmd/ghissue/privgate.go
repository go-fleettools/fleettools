package main

import (
	"fmt"
	"strings"

	"github.com/go-fleettools/fleettools/internal/privpath"
)

// ⛔⛔ THE DOOR. On 2026-11-09 three issues were filed from this fleet whose
// bodies each carried `/Users/<account>/Documents/<project>/<file>.tex` — a home
// directory, an account name, a directory layout and the name of an unpublished
// document. Nobody noticed until the user asked "did you leak my name", and
// their instruction was not "be careful": it was "I want a tool AND a door to
// stop this".
//
// It is here, in the program that sends, and not in a checklist, because the
// lesson that has to be remembered at the moment of sending is not a control.
// It is in readBody and checkTitle — the two functions every path through this
// command passes — so that no future subcommand can send text that did not go
// through it. The signatures carry the waiver for the same reason: a body cannot
// be read without the compiler asking what to do about a disclosure.
//
// ⛔ A refusal costs one flag. A false negative cannot be recalled: editing a
// body afterwards does not remove it, because the forge keeps the original
// under "edited" for anyone who opens the history.

// waiverFlag is the name of the flag that overrides the gate, quoted in its own
// refusal so that the reader does not have to look it up.
const waiverFlag = "--allow-private-path"

// gateBody refuses a body that names a person's own file system.
//
// ⛔ The waiver takes a REASON and has no default. A boolean waiver is one
// keystroke and gets passed by reflex; a reason has to be written, and writing
// it is the moment to notice there is nothing to write. And it STAMPS: the
// reason is appended to the published text, where the recipient reads it too, so
// a waiver is a statement made in public and not a switch flicked in private.
func gateBody(text, waiver string) (string, error) {
	found := scanOutgoing(text)
	if len(found) == 0 {
		return text, nil
	}
	if waiver == "" {
		return "", refusal("body", found)
	}
	return text + "\n\n" + stamp(waiver) + "\n", nil
}

// gateTitle refuses a title that names a person's own file system, and NO
// waiver covers it.
//
// ⛔ The asymmetry is deliberate. A title is one line — it cannot carry the
// stamp that makes a waiver public — and it is the most-read text a forge has:
// it appears in every listing, every search result, every notification mail and
// every cross-reference, none of which an edit reaches. So a disclosure in a
// title is rewritten, not excused.
func gateTitle(t, waiver string) error {
	found := scanOutgoing(t)
	if len(found) == 0 {
		return nil
	}
	if waiver != "" {
		return fmt.Errorf("the title names a person's own file system, and %s does not cover a title: it cannot carry the waiver's reason, and it is republished in every listing, notification and cross-reference that an edit never reaches. Rewrite the title.\n  %s",
			waiverFlag, found[0])
	}
	return refusal("title", found)
}

// scanOutgoing is the one call both gates make, so that they cannot diverge in
// what they consider a disclosure. The account of whoever is running the command
// is hunted whether or not a path in the text revealed it: the leak that caused
// all this had the path in one section and the bare account in a session label
// further down, and a body holding only the label is exactly as disclosed.
func scanOutgoing(text string) []privpath.Finding {
	return privpath.ScanKnown(text, privpath.SelfNames())
}

// refusal is what the author reads.
//
// ⛔ It does NOT reprint what it found. This message goes where the body was
// going, and that is sometimes a public CI log; a gate against publishing a path
// must not publish it in the course of refusing it. The line and column locate
// it in a file the author already has, and `privscan --show FILE` prints the
// match on the author's own terminal.
func refusal(what string, found []privpath.Finding) error {
	var b strings.Builder
	fmt.Fprintf(&b, "the %s names a person's own file system in %d place(s), and sending it cannot be undone — the forge keeps the original under \"edited\" even after a rewrite.\n", what, len(found))
	for i, f := range found {
		if i == 8 {
			fmt.Fprintf(&b, "  … and %d more\n", len(found)-i)
			break
		}
		fmt.Fprintf(&b, "  %s\n", f)
	}
	fmt.Fprintf(&b, "Fix it with `privscan --fix <file>`, look with `privscan --show <file>`, or, if the path IS the subject, pass %s \"<why>\" — the reason is appended to the published text.", waiverFlag)
	return fmt.Errorf("%s", b.String())
}

// stamp is what a waiver publishes alongside the text it excused.
func stamp(reason string) string {
	return "⚠ Published with a reference to a private file system, by express waiver: " +
		strings.TrimSpace(reason)
}
