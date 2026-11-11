package main

import (
	"strings"
	"testing"
)

const parkedBody = "We do not use FMA in the default kernel because it would break bit-parity.\n"

// The gate refuses a comment that parks a performance change on bit parity,
// before the token is read, and names the rule and the model issue.
func TestParityGateRefusesAParkedChange(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, "o/r", "7", "--base", s.URL, "--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, parkedBody))
	if err == nil {
		t.Fatalf("the comment was sent:\n%s", out)
	}
	if len(s.methods) != 0 {
		t.Errorf("something reached the API: %v", s.methods)
	}
	for _, want := range []string{"go-fft/fft#121", "porting step", parityFlag} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}
}

// The control: a mention is not a deferral, so the commonest sentence of a
// performance pull request goes through.
func TestParityGateLetsAMentionThrough(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, "o/r", "7", "--base", s.URL, "--token-file", tokenFile(t, "k"),
		"--body-file", bodyFile(t, "Output unchanged, bit-identical.\n")); err != nil {
		t.Fatalf("refused: %v", err)
	}
}

func TestParityWaiverIsStamped(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, "o/r", "7", "--base", s.URL, "--token-file", tokenFile(t, "k"),
		"--body-file", bodyFile(t, parkedBody), "--parity-waiver", "callers need identical bits this release"); err != nil {
		t.Fatalf("the waiver did not let it through: %v", err)
	}
	sent := s.bodies[len(s.bodies)-1]
	if !strings.Contains(sent, "bit-parity waiver") || !strings.Contains(sent, "callers need identical bits this release") {
		t.Errorf("the reason was not stamped: %s", sent)
	}
}

func TestParityMeasuredBoundNeedsNoWaiver(t *testing.T) {
	s := newServer(t)
	body := parkedBody + "Measured relative RMS error 3.1e-16 against double-double.\n"
	if _, err := exec(t, "o/r", "7", "--base", s.URL, "--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, body)); err != nil {
		t.Fatalf("refused despite a measured bound: %v", err)
	}
}

func TestParityNoWaiverCoversATitle(t *testing.T) {
	s := newServer(t)
	_, err := exec(t, "o/r", "7", "--edit", "--base", s.URL, "--token-file", tokenFile(t, "k"),
		"--title", "perf: SIMD kernel skipped because it breaks bit-parity", "--parity-waiver", "why")
	if err == nil || !strings.Contains(err.Error(), "no waiver covers a title") {
		t.Fatalf("got %v", err)
	}
	if len(s.methods) != 0 {
		t.Errorf("something reached the API: %v", s.methods)
	}
}
