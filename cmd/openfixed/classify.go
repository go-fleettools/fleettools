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
	// Review: a merged pull request mentioned the issue without a closing
	// keyword, or the issue was reopened after the merge. A human decides.
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

// closingRef reports whether body holds a closing keyword followed by a
// reference to issueRepo#n. Per GitHub's documented syntax a bare #N means the
// pull request's own repository; another repository needs owner/name#N.
func closingRef(body, prRepo, issueRepo string, n int) bool {
	// ⛔ The parser is the merge gate's own (go-gitsafe/gitsafe issuegate), so
	// the audit and the gate cannot disagree: the keyword must be on the SAME
	// line, right before the one reference it closes; fenced code, code spans,
	// HTML comments and URLs are not prose. A private regexp here once let
	// `\s+` cross newlines, and a heading "## Also filed, not fixed" bound to
	// a reference two lines below (go-fleettools/fleettools#205).
	for _, r := range issuegate.Parse(body, prRepo) {
		if r.Kind == issuegate.Closes && strings.EqualFold(r.Repo, issueRepo) && r.Number == n {
			return true
		}
	}
	return false
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
			prs = append(prs, PR{
				Repo: repo, Number: is.Number, MergedAt: merged,
				Keyword: closingRef(is.Body, repo, issueRepo, n),
			})
		}
	}
	if len(prs) == 0 {
		return None, nil
	}
	closing := false
	var latest time.Time
	for _, p := range prs {
		if p.Keyword || connected {
			closing = true
		}
		if p.MergedAt.After(latest) {
			latest = p.MergedAt
		}
	}
	if !closing {
		return Review, prs
	}
	// Reopened after the last merge: a person looked at the merged fix and
	// decided the issue was not done. Closing it would overrule them.
	for _, t := range reopened {
		if t.After(latest) {
			return Review, prs
		}
	}
	return Closable, prs
}
