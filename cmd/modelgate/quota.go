package main

import (
	"fmt"
	"regexp"
)

// quotaRE finds a waiver reason that cites a limit, a quota, a 429, a reset or
// an unavailable model (English and French). Such a reason describes a PAST
// state: the agent that hit it may be answering again.
var quotaRE = regexp.MustCompile(`(?i)\b(limit\w*|quota\w*|429|resets?|unavailable|indisponible\w*)\b`)

// probeRE is the evidence a quota waiver must carry: the time of a probe and
// what it returned, "probe: 14:05 still 429 after SendMessage".
var probeRE = regexp.MustCompile(`(?i)\bprobe:[ \t]*\d{1,2}:\d{2}[ \t]+\S`)

// quotaRefusal returns the refusal for a waiver reason that cites a quota with
// no probe, or "" when the reason is not a quota claim or carries its probe.
//
// It trusts the text, not the transcript: the hook's transcript_path is the
// PARENT session's, and the 429 a stopped sub-agent hit is not in it, so there
// is nothing there to date. The probe line is the dated evidence instead.
func quotaRefusal(reason string) string {
	if !quotaRE.MatchString(reason) || probeRE.MatchString(reason) {
		return ""
	}
	return fmt.Sprintf("modelgate refuses this model-waiver: its reason cites a limit, quota, 429, reset or unavailable model (%q) and carries no probe. A 429 is a PAST state, and the quota may have changed since (2026-11-28: three agents stopped on \"weekly limit\", the work was relaunched 3h30 later without retrying, and the quota was back). Probe first: resume the stopped agent with SendMessage and read the notification, then add to the waiver line `probe: <HH:MM> <result>`.", reason)
}
