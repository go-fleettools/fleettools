package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// fixedClock pins now and the stdin seam for one test.
func fixedClock(t *testing.T, payload string) {
	t.Helper()
	savedNow, savedIn := now, stdin
	now = func() time.Time {
		return time.Date(2026, 11, 14, 18, 52, 5, 0, time.FixedZone("CET", 3600))
	}
	stdin = strings.NewReader(payload)
	t.Cleanup(func() { now, stdin = savedNow, savedIn })
}

type clockOut struct {
	H struct {
		Event   string `json:"hookEventName"`
		Context string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func runClockArgs(t *testing.T, payload string, args ...string) clockOut {
	t.Helper()
	fixedClock(t, payload)
	var out, errOut bytes.Buffer
	if !dispatch(args, &out, &errOut, func(io.Writer) {}) {
		t.Fatal("dispatch did not take --clock")
	}
	var got clockOut
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %q: %v", out.String(), err)
	}
	return got
}

const wantContext = "Current date/time: 2026-11-14 18:52:05 CET (Saturday)"

func TestClockPrintsTheTimeForEachEvent(t *testing.T) {
	for _, ev := range []string{"UserPromptSubmit", "SessionStart"} {
		got := runClockArgs(t, `{"hook_event_name":"`+ev+`"}`, "--clock")
		if got.H.Event != ev {
			t.Errorf("event = %q, want %q", got.H.Event, ev)
		}
		if got.H.Context != wantContext {
			t.Errorf("context = %q, want %q", got.H.Context, wantContext)
		}
	}
}

func TestClockToleratesEmptyAndGarbagePayloads(t *testing.T) {
	for _, payload := range []string{"", "not json", "{", `{"hook_event_name":42}`, `{"hook_event_name":"a\"b"}`, `{"hook_event_name":""}`} {
		got := runClockArgs(t, payload, "--clock")
		if got.H.Event != "UserPromptSubmit" || got.H.Context != wantContext {
			t.Errorf("payload %q: got %+v", payload, got)
		}
	}
}

// ⛔ A bad flag must not turn the clock into a refusal or into silence.
func TestClockWithABadFlagStillPrintsValidJSON(t *testing.T) {
	for _, args := range [][]string{
		{"--clock", "--tolerance=zzz"},
		{"--clock", "--nonsense"},
		{"--clock=yes", "--max-blocks=abc"},
		{"-clock", "--bogus"},
	} {
		got := runClockArgs(t, `{"hook_event_name":"SessionStart"}`, args...)
		if got.H.Event != "SessionStart" || got.H.Context != wantContext {
			t.Errorf("%q: got %+v", args, got)
		}
	}
}

func TestClockFalseIsNotClockMode(t *testing.T) {
	if clockMode([]string{"--clock=false"}) || clockMode([]string{"--hook"}) {
		t.Error("only an unspecified-or-true --clock is clock mode")
	}
}

func TestClockNeverCallsTheStalenessWarning(t *testing.T) {
	fixedClock(t, "")
	calls := 0
	dispatch([]string{"--clock"}, &bytes.Buffer{}, &bytes.Buffer{}, func(io.Writer) { calls++ })
	if calls != 0 {
		t.Errorf("warn called %d times; a hook must not touch the network", calls)
	}
}
