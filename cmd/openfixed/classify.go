package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-gitsafe/gitsafe/issuegate"
)

// Class is the verdict on one open issue.
type Class string

const (
	// Closable: a merged pull request referenced the issue with a closing
	// keyword (or the timeline records the issue as "connected"), yet it is open.
	Closable Class = "CLOSABLE"
	// Stated: a merged pull request referenced the issue with `Refs` (or
	// `Ref`), which says in the fleet's merge rule that it stays open ON
	// PURPOSE. Nobody has to decide: somebody already did, in writing.
	//
	// ⛔ `See #N` IS NOT ONE, however often it is written. The gate's keywords
	// are clos(e|es|ed), fix(es|ed), resolv(e|es|ed) and ref(s) -- nothing
	// else -- and issuegate's own documentation names `see #12` as the case
	// the gate exists to refuse, because it reads as a decision and is not
	// one. go-fleettools/fleettools#204 asked for `Refs`/`See` to be read
	// alike; doing that would have turned the one phrasing the fleet refuses
	// into the one that silences the audit.
	//
	// ⛔ This is a separate word from Review because the fleet-wide merge rule
	// (go-gitsafe/gitsafe v0.36.0) made `Refs #N` MANDATORY where there is no
	// closing keyword: ghmerge refuses a merge that mentions an issue and says
	// nothing about it. Every correctly-classified reference therefore became
	// permanent REVIEW output, and the bucket that means "a person decides"
	// filled with what a person had already decided -- 37 of 64 open issues in
	// one organisation (go-fleettools/fleettools#204).
	Stated Class = "REFS"
	// Review: a merged pull request mentioned the issue with NO keyword at all,
	// or the issue was reopened after the merge. A human decides.
	Review Class = "REVIEW"
	// None: no merged pull request refers to the issue; not listed.
	None Class = ""
)

// PR is a merged pull request found on an issue's timeline.
type PR struct {
	Repo     string // owner/name
	Number   int
	MergedAt time.Time
	Keyword  bool // its body carries a closing keyword for this issue
	Stated   bool // its body carries `Refs`/`See` for this issue: it stays open
}

// Ref is owner/name#N.
func (p PR) Ref() string { return fmt.Sprintf("%s#%d", p.Repo, p.Number) }

// Date is the merge date, YYYY-MM-DD.
func (p PR) Date() string { return p.MergedAt.UTC().Format("2006-01-02") }

type event struct {
	Event     string `json:"event"`
	CreatedAt string `json:"created_at"`
	Source    *struct {
		Issue *struct {
			Number      int    `json:"number"`
			Body        string `json:"body"`
			PullRequest *struct {
				MergedAt string `json:"merged_at"`
			} `json:"pull_request"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		} `json:"issue"`
	} `json:"source"`
}

// parseTimeline reads the output of `gh api --paginate --slurp`: an array of
// pages, each an array of events. A bare array of events is accepted too.
func parseTimeline(raw []byte) ([]event, error) {
	var pages []json.RawMessage
	if err := json.Unmarshal(raw, &pages); err != nil {
		return nil, fmt.Errorf("timeline: %w", err)
	}
	var out []event
	for _, p := range pages {
		var evs []event
		if err := json.Unmarshal(p, &evs); err == nil {
			out = append(out, evs...)
			continue
		}
		var one event
		if err := json.Unmarshal(p, &one); err != nil {
			return nil, fmt.Errorf("timeline: %w", err)
		}
		out = append(out, one)
	}
	return out, nil
}

// refKind reports what body SAYS about issueRepo#n: issuegate.Closes (a closing
// keyword), issuegate.Refs (it stays open on purpose) or issuegate.Bare (the
// reference is there and says nothing). Per GitHub's documented syntax a bare #N
// means the pull request's own repository; another repository needs owner/name#N.
//
// A body that does not mention the issue at all also yields Bare, which is why
// the caller asks only about bodies the timeline already tied to this issue.
func refKind(body, prRepo, issueRepo string, n int) issuegate.Kind {
	// ⛔ The parser is the merge gate's own (go-gitsafe/gitsafe issuegate), so
	// the audit and the gate cannot disagree: the keyword must be on the SAME
	// line, right before the one reference it closes; fenced code, code spans,
	// HTML comments and URLs are not prose. A private regexp here once let
	// `\s+` cross newlines, and a heading "## Also filed, not fixed" bound to
	// a reference two lines below (go-fleettools/fleettools#205).
	//
	// ⛔ And the SAME reason forbids a second regexp for `Refs`: the gate
	// already decides what a `Refs` is when it allows the merge, so reading it
	// here with anything else would let an issue be audited under a rule it was
	// never merged under. issuegate.Parse keeps the strongest kind per
	// reference (Closes over Refs over Bare), so one call answers both.
	for _, r := range issuegate.Parse(body, prRepo) {
		if strings.EqualFold(r.Repo, issueRepo) && r.Number == n {
			return r.Kind
		}
	}
	return issuegate.Bare
}

// Classify reads one issue's timeline. Pull requests that are not merged are
// ignored. The PRs returned are the merged ones that referenced the issue.
func Classify(issueRepo string, n int, evs []event) (Class, []PR) {
	var prs []PR
	connected := false
	var reopened []time.Time
	for _, e := range evs {
		switch e.Event {
		case "connected":
			connected = true
		case "reopened":
			if t, err := time.Parse(time.RFC3339, e.CreatedAt); err == nil {
				reopened = append(reopened, t)
			}
		case "cross-referenced":
			if e.Source == nil || e.Source.Issue == nil || e.Source.Issue.PullRequest == nil {
				continue
			}
			is := e.Source.Issue
			merged, err := time.Parse(time.RFC3339, is.PullRequest.MergedAt)
			if is.PullRequest.MergedAt == "" || err != nil {
				continue
			}
			repo := is.Repository.FullName
			if strings.EqualFold(repo, issueRepo) && is.Number == n {
				continue
			}
			kind := refKind(is.Body, repo, issueRepo, n)
			prs = append(prs, PR{
				Repo: repo, Number: is.Number, MergedAt: merged,
				Keyword: kind == issuegate.Closes,
				Stated:  kind == issuegate.Refs,
			})
		}
	}
	if len(prs) == 0 {
		return None, nil
	}
	closing, stated := false, false
	var latest time.Time
	for _, p := range prs {
		if p.Keyword || connected {
			closing = true
		}
		if p.Stated {
			stated = true
		}
		if p.MergedAt.After(latest) {
			latest = p.MergedAt
		}
	}
	// Reopened after the last merge: a person looked at the merged fix and
	// decided the issue was not done. Closing it would overrule them, and so
	// would calling it a stated decision -- the reopening IS the later
	// decision, and it says the opposite of whatever the body said.
	for _, t := range reopened {
		if t.After(latest) {
			return Review, prs
		}
	}
	switch {
	case closing:
		return Closable, prs
	case stated:
		// ⛔ ONE `Refs` CLASSIFIES THE ISSUE, even beside a bare mention from
		// another pull request. The question this bucket answers is "has
		// anybody said what becomes of this issue", and one author writing
		// `Refs` answered it; a later bare mention adds a reference, not a
		// doubt. The row is still PRINTED, with its own word, because an author
		// can write `Refs` and be wrong -- what changes is that REVIEW goes
		// back to meaning "nobody has decided yet".
		return Stated, prs
	}
	return Review, prs
}
