// Package fleet is the one place this repository shells out to gh, and the one
// place that decides what is worth retrying.
//
// It exists because six commands each had their own. The four spellings were
// not equivalent, and the difference was not stylistic:
//
//	prscan tagscan redscan docscan   throttling only
//	prmerge quietscan                throttling AND transient network faults
//
// prmerge learned the second list the expensive way, and says so in the
// comment this package inherits: one pass lost 186 of 460 candidates to
// "no route to host" while the machine's link flapped, and a later DNS outage
// produced 109 more that the retry never fired on, because the matching was
// written against the errors that had been SEEN rather than the ones gh emits.
// Neither lesson ever reached the other four sweeps, so a network blip still
// turns any of them into a silent hole -- and a hole in a sweep reads as
// "nothing to report", which is the one answer a sweep must never give by
// accident.
//
// ⛔ The two rate limits want OPPOSITE treatment and this is the distinction
// worth keeping. The SECONDARY limit is a burst brake: it lifts in seconds and
// waiting is the whole remedy. The PRIMARY one is an hourly budget that resets
// at a fixed time, and no short backoff reaches it -- matching on "rate" alone
// made a spent budget sleep five minutes and fail anyway, per batch.
package fleet

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Attempts is how many times a retryable failure is tried before giving up.
const Attempts = 5

// Backoff is how long to wait before the next attempt. A test sets it to
// nothing.
var Backoff = func(attempt int) time.Duration {
	return time.Duration(20*(attempt+1)) * time.Second
}

// run is the exec seam. A test replaces it; nothing else should.
var run = func(args []string) (stdout []byte, stderr string, err error) {
	cmd := exec.Command("gh", args...)
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.Output()
	return out, errb.String(), err
}

// Guard, when set, is consulted before every call and can refuse it. quietscan
// uses this to keep a watcher read-only: a tool that only looks must not be one
// edit away from writing.
var Guard func(args []string) error

// Throttled reports whether a failure is GitHub braking a burst, which waiting
// cures.
//
// It is false for the primary hourly budget even though that message also says
// "rate limit": that budget will not refill inside any backoff this package
// would sleep, so retrying it spends minutes to fail anyway.
func Throttled(msg string) bool {
	if strings.Contains(msg, "API rate limit exceeded") {
		return false
	}
	low := strings.ToLower(msg)
	return strings.Contains(low, "secondary rate") ||
		strings.Contains(low, "rate limit") ||
		strings.Contains(low, "ratelimit-remaining: 0") ||
		strings.Contains(low, "abuse") ||
		strings.Contains(low, "too quickly")
}

// Transient reports whether a failure is the network rather than GitHub.
//
// The second half of this list is gh's own wording. Matching only the Go-level
// strings is what let a DNS outage produce 109 failures with the retry never
// firing: the errors that had been seen were not the errors the tool emits.
func Transient(msg string) bool {
	for _, s := range []string{
		"no route to host",
		"operation timed out",
		"connection reset",
		"i/o timeout",
		"TLS handshake timeout",
		"EOF",
		"error connecting to",
		"no such host",
		"check your internet connection",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// Retryable is the whole policy: wait for a burst brake or a network blip,
// give up at once on a spent hourly budget or a real refusal.
func Retryable(msg string) bool { return Transient(msg) || Throttled(msg) }

// GH runs gh with the shared retry policy and returns its stdout.
func GH(args ...string) ([]byte, error) {
	if Guard != nil {
		if err := Guard(args); err != nil {
			return nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		out, msg, err := run(args)
		if err == nil {
			return out, nil
		}
		if attempt < Attempts && Retryable(msg) {
			time.Sleep(Backoff(attempt))
			continue
		}
		return nil, fmt.Errorf("%s", strings.TrimSpace(msg))
	}
}

// Orgs lists every organisation the authenticated account belongs to.
func Orgs() ([]string, error) {
	b, err := GH("api", "user/orgs", "--paginate", "--jq", ".[].login")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

// Repo is the subset of a repository every sweep here needs.
type Repo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
	Fork          bool   `json:"fork"`
}

// Repos lists an organisation's repositories, skipping archived ones and
// forks: neither is ours to judge, and counting them makes every total
// disagree with every other total.
func Repos(org string) ([]Repo, error) {
	b, err := GH("api", "orgs/"+org+"/repos?per_page=100", "--paginate")
	if err != nil {
		return nil, err
	}
	// --paginate concatenates JSON arrays rather than merging them, so this
	// decodes a stream of arrays and not one array.
	dec := json.NewDecoder(strings.NewReader(string(b)))
	var out []Repo
	for {
		var page []Repo
		if dec.Decode(&page) != nil {
			break
		}
		for _, r := range page {
			if !r.Archived && !r.Fork {
				out = append(out, r)
			}
		}
	}
	return out, nil
}
