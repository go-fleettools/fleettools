// Package checkout holds the two questions every working-tree scanner has to
// ask before it believes what it read.
//
// ⛔ THEY WERE ASKED IN TWO PLACES AND DRIFTED. testscan and judgescan each had
// their own staleness(); on 2026-09-27 testscan's learnt to count commits and
// judgescan's did not, so the same clone was stale for one tool and fresh for
// the other. judgescan never had the ownership question at all and was still
// reporting forks. Two answers to one question is one answer being wrong.
package checkout

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Staleness reports how far a checkout's HEAD is behind its remote default
// branch, and how long since it last fetched.
//
// ⛔ THE DISTANCE IS THE SIGNAL, NOT THE AGE. `git fetch` refreshes FETCH_HEAD
// and the remote refs and does NOT touch the working tree, so a clone fetched a
// minute ago can be twenty commits behind — and on 2026-09-27 ten of twelve
// findings were exactly that. An earlier version returned a hard-coded 0 here
// while its own doc promised the distance.
//
// ok is false when this cannot be measured. Unknown is reported as unknown:
// it must not read as up to date.
func Staleness(dir string) (behind int, fetchAge time.Duration, ok bool) {
	st, err := os.Stat(filepath.Join(dir, ".git", "FETCH_HEAD"))
	if err != nil {
		return 0, 0, false
	}
	if ref, ok := defaultRemoteRef(dir); ok {
		if out, ok := git(dir, "rev-list", "--count", "HEAD.."+ref); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				behind = n
			}
		}
	}
	return behind, time.Since(st.ModTime()), true
}

// FetchHorizon is how long a checkout's remote refs are treated as current
// when nobody asked the remote.
//
// ⛔ IT IS A FALLBACK, NOT THE ANSWER, and the case that produced this file
// proves why: go-compressions/compress had fetched 36 MINUTES earlier and was
// already one commit behind, because the commit it was missing was merged in
// between. Any horizon long enough to be quiet is long enough to miss that.
// There is no interval that knows whether a remote moved; the only thing that
// knows is a fetch. So Refresh below does one, for the handful of checkouts a
// scanner is about to name, and this constant only covers a fetch that could
// not be done.
const FetchHorizon = time.Hour

// Refresh fetches one checkout's remote refs and re-measures the distance.
//
// A scanner reports a handful of repositories out of nine hundred, so this
// runs over the findings and not over the fleet: the cost is a few fetches,
// and what it buys is a distance that was MEASURED rather than remembered.
// It never touches the working tree -- git fetch moves refs and nothing else.
func Refresh(dir string) (behind int, ok bool) {
	if _, ok := git(dir, "fetch", "--quiet", "origin"); !ok {
		return 0, false
	}
	ref, ok := defaultRemoteRef(dir)
	if !ok {
		return 0, false
	}
	out, ok := git(dir, "rev-list", "--count", "HEAD.."+ref)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, false
	}
	return n, true
}

// Verdict is what one checkout's distance actually tells you.
type Verdict int

const (
	Current Verdict = iota // behind 0, against a remote ref fetched recently
	Behind                 // the remote default has commits this tree does not
	Unknown                // behind 0, but nobody has asked the remote lately
)

// Judge turns Staleness's three numbers into one answer.
//
// ⛔ A DISTANCE OF ZERO IS NOT FRESHNESS. `git rev-list HEAD..origin/main`
// compares against the LOCAL copy of origin/main, and that copy is only as
// current as the last fetch. A clone nobody has fetched from since its own
// commits were merged upstream reports zero, which reads exactly like being
// up to date.
//
// MEASURED, go-compressions/compress on 2026-09-27, 36 minutes after its last
// fetch and minutes after its fix was merged:
//
//	before git fetch: 0 behind
//	after  git fetch: 1 behind
//
// Note what that witness also says about THIS function: 36 minutes is inside
// any sane horizon, so Judge alone would have called it Current and been
// wrong. Judge is what is left when Refresh could not run.
//
// judgescan printed the repository as a finding again, on a tree whose fix was
// already on main, and said nothing about the tree being old -- because behind
// was 0. The fetch age WAS being computed, and then never read: the two fields
// were assigned into the finding and nothing looked at them.
//
// An answer nobody obtained is not a negative answer.
func Judge(behind int, fetchAge time.Duration, ok bool) Verdict {
	switch {
	case behind > 0:
		return Behind
	case !ok || fetchAge > FetchHorizon:
		return Unknown
	}
	return Current
}

// Age is one finding's checkout, as Staleness measured it.
type Age struct {
	Behind     int
	FetchAge   time.Duration
	FetchKnown bool
}

// StalenessWarning is the sentence a scanner prints about the evidence under
// its findings, or "" when there is nothing to say.
//
// It lives here because judgescan and testscan had the SAME twelve lines,
// separately, and that is the drift this package exists to stop: the distance
// fix landed in one of them on 2026-09-27 and not the other, and the comment
// left behind in the second said so.
func StalenessWarning(ages []Age) string {
	behind, unknown, furthest := 0, 0, 0
	for _, a := range ages {
		switch Judge(a.Behind, a.FetchAge, a.FetchKnown) {
		case Behind:
			behind++
			if a.Behind > furthest {
				furthest = a.Behind
			}
		case Unknown:
			unknown++
		}
	}
	var b strings.Builder
	if behind > 0 {
		fmt.Fprintf(&b,
			"\n⚠ %d of the %d findings above come from a checkout BEHIND its remote (furthest: %d commits).\n"+
				"  Pull those clones and RE-RUN this: refreshing without re-deriving makes a stale finding\n"+
				"  look confirmed.\n", behind, len(ages), furthest)
	}
	if unknown > 0 {
		fmt.Fprintf(&b,
			"\n⚠ %d of the %d findings above sit on a checkout that has not FETCHED for over %v,\n"+
				"  so its distance of 0 means nobody asked, not that it is current. `git fetch` those\n"+
				"  and re-run.\n", unknown, len(ages), FetchHorizon)
	}
	return b.String()
}

func git(dir string, args ...string) (string, bool) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func defaultRemoteRef(dir string) (string, bool) {
	if s, ok := git(dir, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); ok {
		if r := strings.TrimSpace(s); r != "" {
			return r, true
		}
	}
	for _, r := range []string{"origin/main", "origin/master"} {
		if _, ok := git(dir, "rev-parse", "--verify", "-q", r); ok {
			return r, true
		}
	}
	return "", false
}

// Ownership is what GitHub says about one repository.
type Ownership struct {
	Fork    bool
	Missing bool // GitHub does not have it
	Asked   bool // the question was answered at all
}

// Ask puts the question to GitHub.
//
// ⛔ A 404 IS AN ANSWER; EVERY OTHER FAILURE IS NOT. gh exits 1 for a rate
// limit and for a missing token as readily as for a missing repository, so
// keying on the exit status would delete real findings during exactly the
// minutes when the API is refusing — which is when a sweep is most likely to be
// running. The message has to say so.
func Ask(repo string) Ownership {
	cmd := exec.Command("gh", "api", "repos/"+repo, "--jq", `{f: .fork}`)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		if msg := errb.String(); strings.Contains(msg, "Not Found") || strings.Contains(msg, "HTTP 404") {
			return Ownership{Missing: true, Asked: true}
		}
		return Ownership{}
	}
	var got struct {
		F bool `json:"f"`
	}
	if json.Unmarshal(out, &got) != nil {
		return Ownership{}
	}
	return Ownership{Fork: got.F, Asked: true}
}

// WhyNotOurs names the reason a repository does not belong on a finding list,
// or "" — including when the question was never answered, which must read as
// "keep it".
func WhyNotOurs(o Ownership) string {
	switch {
	case !o.Asked:
		return ""
	case o.Missing:
		return "not on GitHub"
	case o.Fork:
		return "a fork"
	}
	return ""
}

// Owners is every account whose repositories are ours: the organisations this
// token belongs to, and the user themself.
//
// ⛔ The user is in the set deliberately. Leaving them out drops every personal
// repository, and on this machine several real modules live there; the noisy
// ones under that account are forks, which the fork test catches on its own.
func Owners() (map[string]bool, bool) {
	set := map[string]bool{}
	if out, err := exec.Command("gh", "api", "user/orgs", "--paginate", "--jq", ".[].login").Output(); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				set[l] = true
			}
		}
	}
	if out, err := exec.Command("gh", "api", "user", "--jq", ".login").Output(); err == nil {
		if l := strings.TrimSpace(string(out)); l != "" {
			set[l] = true
		}
	}
	return set, len(set) > 0
}

// Ours reports which of these repositories a finding list should keep, and why
// the others go.
//
// It asks GitHub once per candidate, never per repository scanned: on
// 2026-09-27 that was nineteen calls against nine hundred and twenty-five
// checkouts. ask is a parameter so callers can test without a network — the
// first test written against the real one got a real 404 for its made-up
// repository and failed for a reason that had nothing to do with the logic.
func Ours(repos []string, owners map[string]bool, haveOwners bool, ask func(string) Ownership) (keep map[string]bool, dropped []string) {
	keep = map[string]bool{}
	for _, r := range repos {
		reason := ""
		if haveOwners && !owners[OwnerOf(r)] {
			reason = "not one of our accounts"
		} else if w := WhyNotOurs(ask(r)); w != "" {
			reason = w
		}
		if reason == "" {
			keep[r] = true
			continue
		}
		dropped = append(dropped, fmt.Sprintf("%s (%s)", r, reason))
	}
	return keep, dropped
}

// OwnerOf is the account half of owner/repo.
func OwnerOf(repo string) string {
	o, _, _ := strings.Cut(repo, "/")
	return o
}
