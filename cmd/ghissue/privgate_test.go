package main

import (
	"strings"
	"testing"
)

const privateBody = "## The measurement\n\n" +
	"`/Users/alicia/Documents/fairshare-b300/fairshare-b300.tex`, 36 pages.\n"

// ⛔⛔ THE DOOR. Three issues were filed on 2026-11-09 with a body of exactly
// this shape, and the user's instruction afterwards was not "be careful" but
// "I want a tool and a door to stop this". This is the door: the send is
// refused, before the token is read and before anything leaves the machine.
func TestGateRefusesABodyNamingAPrivateFileSystem(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, "o/r", "7", "--base", s.URL,
		"--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, privateBody))
	if err == nil {
		t.Fatalf("the comment was sent:\n%s", out)
	}
	if len(s.methods) != 0 {
		t.Errorf("something reached the API before the refusal: %v %v", s.methods, s.paths)
	}
	msg := err.Error()
	// ⛔ The refusal does not reprint what it found: it is printed where the
	// body was going, and that is sometimes a public CI log.
	for _, secret := range []string{"alicia", "fairshare-b300", "/Users/"} {
		if strings.Contains(msg, secret) {
			t.Errorf("the refusal published %q: %s", secret, msg)
		}
	}
	for _, want := range []string{"line 3", "privscan --fix", waiverFlag, "edited"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q: %s", want, msg)
		}
	}
}

// The control for the test above: the same path with a GENERIC account is a
// sentence a report may legitimately need, and must go through. Without this,
// the gate could be refusing everything and the test above would still pass.
func TestGateLetsALegitimatePathThrough(t *testing.T) {
	s := newServer(t)
	body := "`/Users/runner/.pkgx` is the runner's home directory, and the loader looks there.\n"
	if _, err := exec(t, "o/r", "7", "--base", s.URL,
		"--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, body)); err != nil {
		t.Fatalf("a legitimate sentence was refused: %v", err)
	}
	if len(s.methods) == 0 {
		t.Error("nothing was sent")
	}
}

// ⛔ The waiver takes a REASON and STAMPS it into what is published, so that a
// waiver is a statement made in public rather than a switch flicked in private.
func TestWaiverSendsAndStampsItsReason(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, "o/r", "7", "--base", s.URL,
		"--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, privateBody),
		"--allow-private-path", "the path IS the defect: the loader mangles a space in it")
	if err != nil {
		t.Fatalf("the waiver did not let it through: %v\n%s", err, out)
	}
	if len(s.bodies) == 0 {
		t.Fatal("nothing was sent")
	}
	sent := s.bodies[len(s.bodies)-1]
	if !strings.Contains(sent, "express waiver") ||
		!strings.Contains(sent, "the loader mangles a space in it") {
		t.Errorf("the published text does not carry the waiver's reason: %s", sent)
	}
	// And the text itself still went, unaltered: a waiver is permission to
	// publish, not a silent redaction of what the author wrote.
	if !strings.Contains(sent, "36 pages") {
		t.Errorf("the waiver changed the body: %s", sent)
	}
}

// ⛔ An EMPTY waiver is no waiver. A flag whose zero value passes the gate is a
// gate with a default, which is the shape this fleet has recorded as failing:
// the obligatory flag has no default, and the refusal comes before the spend.
func TestEmptyWaiverIsNoWaiver(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, "o/r", "7", "--base", s.URL,
		"--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, privateBody),
		"--allow-private-path", ""); err == nil {
		t.Error("an empty reason passed the gate")
	}
	if len(s.methods) != 0 {
		t.Errorf("something was sent: %v", s.paths)
	}
}

// ⛔ A TITLE is refused even with the waiver. It is one line, so it cannot carry
// the stamp that makes a waiver public, and it is republished in every listing,
// notification and cross-reference that an edit never reaches.
func TestNoWaiverCoversATitle(t *testing.T) {
	for _, extra := range [][]string{nil, {"--allow-private-path", "the path is the subject"}} {
		args := append([]string{"o/r", "--new", "--base", "https://example.invalid",
			"--token-file", tokenFile(t, "k"),
			"--title", "crash reading /Users/alicia/thesis/main.tex",
			"--body-file", bodyFile(t, "a clean body")}, extra...)
		_, err := exec(t, args...)
		if err == nil {
			t.Fatalf("a title naming a private path was accepted with %v", extra)
		}
		if !strings.Contains(err.Error(), "title") {
			t.Errorf("the refusal does not name the title: %v", err)
		}
		if strings.Contains(err.Error(), "alicia") {
			t.Errorf("the refusal published the path: %v", err)
		}
	}
}

// --dry-run is not a way around the gate: it prints what WOULD be sent, and
// printing it is a disclosure to whatever reads that output.
func TestDryRunIsStillGated(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, "o/r", "7", "--base", s.URL, "--dry-run",
		"--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, privateBody))
	if err == nil {
		t.Fatalf("--dry-run printed it:\n%s", out)
	}
	if strings.Contains(out, "alicia") {
		t.Errorf("--dry-run printed the path before refusing:\n%s", out)
	}
}

// The stamp is appended, not substituted, and reads as a sentence.
func TestStampReads(t *testing.T) {
	got := stamp("  because the path is the defect  ")
	if !strings.HasPrefix(got, "⚠ ") || !strings.HasSuffix(got, "because the path is the defect") {
		t.Errorf("stamp = %q", got)
	}
}
