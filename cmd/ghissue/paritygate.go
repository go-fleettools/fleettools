package main

import "github.com/go-fleettools/fleettools/internal/paritytext"

// ⛔ THE BIT-PARITY GATE. Decision of 2026-11-11 (go-fft/fft#121): bit identity
// with a reference implementation is a PORTING STEP, never the contract; the
// contract for numeric code is "correct and as fast as possible", a stated error
// bound asserted against an extended-precision oracle, with bit-exact arithmetic
// as an option. A performance gap is never parked because "it would break
// bit-parity" — go-fft/fft#116 is the pattern, written as a decision request.
//
// It lives where privgate.go lives, in readBody and checkTitle, so that every
// path through this command — a comment, a new issue, an edit — passes it and a
// new subcommand cannot send text that did not. internal/paritytext is the one
// scanner, shared with `paritycheck text`, so the gate and the tool cannot
// diverge in what they refuse.

// parityFlag is quoted in the refusal so the author does not look it up.
const parityFlag = "--parity-waiver"

// waivers are the reasons the author gave for each gate; the zero value waives
// nothing.
type waivers struct {
	private string // --allow-private-path
	parity  string // --parity-waiver
}

func (o opts) waivers() waivers { return waivers{private: o.waiver, parity: o.parityWaiver} }

// gateParityBody refuses a body that defers a performance change because of bit
// identity unless it cites a measured bound; a waiver's reason is STAMPED into
// the returned text.
func gateParityBody(text, waiver string) (string, error) {
	return paritytext.Gate(text, waiver, parityFlag)
}

// gateParityTitle refuses such a title, and no waiver covers it: a title cannot
// carry the reason, and it is republished everywhere an edit never reaches.
func gateParityTitle(t string) error { return paritytext.GateTitle(t, parityFlag) }
