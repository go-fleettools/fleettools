package main

import (
	"strings"
	"testing"
)

func quotaCall(reason string) payload {
	var p payload
	p.ToolName = "Agent"
	p.ToolInput.Description = "Implement the thing"
	p.ToolInput.Model = "opus"
	p.ToolInput.Prompt = "do it\nmodel-waiver: " + reason + "\n"
	return p
}

func TestQuotaWaiverNeedsAProbe(t *testing.T) {
	refused := []string{
		"Sonnet is unavailable (weekly limit, resets Dec 3)",
		"HTTP 429 from the API",
		"rate limit reached",
		"rate-limit",
		"quota exhausted",
		"limite hebdomadaire atteinte",
		"modèle indisponible",
		"quota épuisé",
		"resets Dec 3",
		"limit hit, probe: 14:05", // a time with no result is no probe
		"limit hit, probe 14:05 429 again",
	}
	for _, r := range refused {
		v := decide(quotaCall(r), noon)
		if !v.deny {
			t.Errorf("%q: allowed, want refused", r)
			continue
		}
		for _, want := range []string{"SendMessage", "probe: <HH:MM> <result>", "PAST state"} {
			if !strings.Contains(v.reason, want) {
				t.Errorf("%q: refusal lacks %q: %s", r, want, v.reason)
			}
		}
	}
}

func TestQuotaWaiverWithProbeAllows(t *testing.T) {
	for _, r := range []string{
		"Sonnet is unavailable (weekly limit) probe: 14:05 SendMessage resumed agent, still 429",
		"quota atteint; probe: 9:07 toujours 429",
	} {
		v := decide(quotaCall(r), noon)
		if v.deny || !strings.Contains(v.reason, "model waived") {
			t.Errorf("%q: %+v, want an allowance", r, v)
		}
	}
}

func TestOtherWaiversUntouchedByQuotaGate(t *testing.T) {
	for _, r := range []string{
		"opus is not enough: cross-module design",
		"the review needs the deepest reasoning",
	} {
		if v := decide(quotaCall(r), noon); v.deny {
			t.Errorf("%q refused: %s", r, v.reason)
		}
	}
}
