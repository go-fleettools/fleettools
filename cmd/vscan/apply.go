package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// A tag is permanent. The Go module proxy caches what it resolves and keeps
// serving it after the tag is deleted, so there is no undo worth the name:
// a wrong tag is a wrong answer given to every consumer, for ever.
//
// Everything in this file is built around that one fact.
//
//   - It re-reads the remote head and REFUSES when it no longer matches the
//     commit the verdict was measured on. A branch that moved between the scan
//     and the write is a different tree, and the suggestion described the old
//     one.
//   - It creates the ref and then READS IT BACK, because a 201 says the request
//     was accepted, not that the tag resolves.
//   - It stops at the first FAILURE rather than carrying on. A sweep that
//     half-succeeds across 231 repositories leaves nobody able to say which
//     half.
//   - It never creates a tag that already exists, whatever it points at.
//
// ⛔ A SKIP IS NOT A FAILURE, and conflating them cost a whole dry run. The
// first version stopped on anything that was not a tag, so one orphaned local
// clone -- go-compressions/matchlen, whose repository has been deleted, git
// says "Repository not found" -- ended the pass after 34 of 231. A branch that
// moved, a tag that already exists and a repository that is gone are all
// reasons to leave that one alone and carry on. Only a write that went wrong
// stops the run.
//
// ⛔ AND A DRY RUN STOPS FOR NOTHING AT ALL. It writes nothing, so there is no
// half-finished state to protect; its whole purpose is to show you every
// problem before you start, and one that reports the first is worth little.

// applyPlan is what -apply names: the categories a person decided on.
type applyPlan struct {
	safe      bool // patch, every platform agreed, no changes section at all
	noAPI     bool // a minor whose only cause is requirementsChanged()
	additions bool // "## compatible changes"
	frozen    bool // base tag on an orphaned history
}

func parseApply(s string) (applyPlan, error) {
	var p applyPlan
	if strings.TrimSpace(s) == "" {
		return p, nil
	}
	for _, w := range strings.Split(s, ",") {
		switch strings.TrimSpace(w) {
		case "safe":
			p.safe = true
		case "no-api-change":
			p.noAPI = true
		case "additions":
			p.additions = true
		case "frozen":
			p.frozen = true
		default:
			return p, fmt.Errorf("unknown category %q: want safe, no-api-change, additions or frozen", w)
		}
	}
	return p, nil
}

func (p applyPlan) any() bool { return p.safe || p.noAPI || p.additions || p.frozen }

// wants reports whether this verdict falls in a category the plan named.
//
// ⛔ An INCOMPATIBLE verdict is in no category. There is no flag for it, and
// that absence is deliberate: a removal or a changed signature is a decision
// about consumers, and no derivation makes it mechanical.
func (p applyPlan) wants(v verdict) bool {
	switch {
	case len(v.unread) > 0 || v.disagree || v.breaks > 0:
		return false
	// ⛔ A PRERELEASE BASE IS NEVER PROMOTED HERE. openweft/weft-ha-irods sits
	// at v0.4.0-rc9 and gorelease suggests v0.4.0: arithmetically a tiny step,
	// in fact the decision to call a release candidate finished. Nothing in an
	// API diff knows whether it is. Four openweft modules carry -rc tags.
	case splitSemver(v.base) == nil:
		return false
	case v.frozen:
		return p.frozen
	case v.safe():
		return p.safe
	case v.grew > 0:
		return p.additions
	default:
		return p.noAPI
	}
}

// tagOutcome is one line of the record this leaves behind.
type tagOutcome struct {
	repo, tag, sha, note string
	ok                   bool // the tag now exists and points where it should
	fatal                bool // stop: this says nothing good about what follows
}

// applyTags creates one lightweight tag ref per verdict, in order, stopping at
// the first failure.
//
// Lightweight, not annotated: `go get` resolves a tag ref, most of the fleet's
// existing tags are lightweight, and an annotated tag would need an author
// identity this tool has no business inventing.
func applyTags(stdout io.Writer, vs []verdict, p applyPlan, pause time.Duration, dryRun bool) (done, skipped []tagOutcome, failed *tagOutcome) {
	for _, v := range vs {
		if !p.wants(v) {
			continue
		}
		o := applyOne(v, dryRun)
		fmt.Fprintf(stdout, "  %-46s %-10s %s\n", o.repo, o.tag, o.note)
		if o.fatal && !dryRun {
			return done, skipped, &o
		}
		switch {
		case o.ok:
			done = append(done, o)
			if !dryRun && pause > 0 {
				time.Sleep(pause)
			}
		default:
			skipped = append(skipped, o)
		}
	}
	return done, skipped, nil
}

func applyOne(v verdict, dryRun bool) tagOutcome {
	o := tagOutcome{repo: v.repo, tag: v.next, sha: v.sha}

	if v.sha == "" {
		// Nothing was measured here, so there is nothing to write and nothing
		// to be alarmed about further down the list.
		o.note = "SKIPPED: the scan recorded no commit for this verdict"
		return o
	}
	// The branch must still be where it was when the API was read.
	head, err := remoteHead(v.repo, v.branch)
	if err != nil {
		// ⛔ A 404 here is a local clone of a repository that is no longer
		// there -- deleted, renamed or out of this token's reach. It says
		// nothing about the next repository, so it must not end the run.
		if isGone(err) {
			// ⛔ Do not guess WHICH thing is missing. The first version of this
			// line said "a stale local clone?" for every 404, and
			// go-richdoc/markdown -- which exists -- got that message because
			// the BRANCH was gone, not the repository. Ask, and say what the
			// answer was.
			o.note = "SKIPPED: " + describeGone(v.repo, v.branch)
			return o
		}
		o.fatal = true
		o.note = "FAILED: could not re-read " + v.branch + ": " + err.Error()
		return o
	}
	if head != v.sha {
		o.note = fmt.Sprintf("SKIPPED: %s moved since the scan (%s -> %s); the suggestion described the old tree",
			v.branch, short(v.sha), short(head))
		return o
	}
	if exists, err := tagExists(v.repo, v.next); err != nil {
		o.fatal = true
		o.note = "FAILED: could not ask whether " + v.next + " exists: " + err.Error()
		return o
	} else if exists {
		o.note = "SKIPPED: " + v.next + " already exists"
		return o
	}
	if dryRun {
		o.ok, o.note = true, "would tag "+short(v.sha)
		return o
	}
	if err := createTag(v.repo, v.next, v.sha); err != nil {
		// ⛔ An ARCHIVED repository refuses every write, and GitHub says so
		// with 404 rather than 403 -- the same status as "no such thing". One
		// archived repository, go-composites/nonnil, ended a run after 22 of
		// 227 tags. Read-only by design is a reason to leave it alone, not to
		// doubt the next one.
		if isGone(err) && archived(v.repo) {
			o.note = "SKIPPED: " + v.repo + " is ARCHIVED -- GitHub refuses every write to it, with 404"
			return o
		}
		o.fatal = true
		o.note = "FAILED: " + err.Error()
		return o
	}
	// ⛔ A 201 says the request was accepted. Read it back.
	got, err := tagTarget(v.repo, v.next)
	if err != nil {
		o.fatal = true
		o.note = "FAILED: created, but could not read it back: " + err.Error()
		return o
	}
	if got != v.sha {
		o.fatal = true
		o.note = fmt.Sprintf("FAILED: created, but it points at %s and not %s", short(got), short(v.sha))
		return o
	}
	o.ok, o.note = true, "tagged "+short(v.sha)
	return o
}

// isGone recognises the one error that means "there is no such repository
// here", as opposed to one that means "I could not ask".
func isGone(err error) bool {
	m := err.Error()
	return strings.Contains(m, "Not Found") || strings.Contains(m, "HTTP 404")
}

// describeGone asks which of the two things a 404 was about. One extra call,
// on a path that is already not going to write anything.
func describeGone(repo, branch string) string {
	if _, err := repoExists(repo); err != nil {
		if isGone(err) {
			return repo + " no longer exists (404) -- a stale local clone"
		}
		return repo + ": branch " + branch + " reads 404, and the repository could not be asked about either (" + err.Error() + ")"
	}
	return repo + " exists, but its branch " + branch + " does not (404) -- this checkout's origin/HEAD is stale, so the version was derived from the WRONG branch"
}

// archived reports whether a write was refused because the repository is
// read-only. A repository this cannot ask about is NOT reported as archived:
// the caller then treats the failure as a failure, which is the safe way round.
var archived = func(repo string) bool {
	b, err := fleet.GH("api", "repos/"+repo, "--jq", ".archived")
	return err == nil && strings.TrimSpace(string(b)) == "true"
}

var repoExists = func(repo string) (string, error) {
	b, err := fleet.GH("api", "repos/"+repo, "--jq", ".default_branch")
	return strings.TrimSpace(string(b)), err
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

var remoteHead = func(repo, branch string) (string, error) {
	b, err := fleet.GH("api", "repos/"+repo+"/git/ref/heads/"+branch, "--jq", ".object.sha")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

var tagExists = func(repo, tag string) (bool, error) {
	_, err := fleet.GH("api", "repos/"+repo+"/git/ref/tags/"+tag, "--jq", ".ref")
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "Not Found") || strings.Contains(err.Error(), "404") {
		return false, nil
	}
	return false, err
}

// tagTarget resolves a tag ref to the commit it names, following an annotated
// tag object if there is one.
var tagTarget = func(repo, tag string) (string, error) {
	b, err := fleet.GH("api", "repos/"+repo+"/git/ref/tags/"+tag)
	if err != nil {
		return "", err
	}
	var ref struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	if err := json.Unmarshal(b, &ref); err != nil {
		return "", err
	}
	if ref.Object.Type != "tag" {
		return ref.Object.SHA, nil
	}
	b, err = fleet.GH("api", "repos/"+repo+"/git/tags/"+ref.Object.SHA, "--jq", ".object.sha")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

var createTag = func(repo, tag, sha string) error {
	_, err := fleet.GH("api", "--method", "POST", "repos/"+repo+"/git/refs",
		"-f", "ref=refs/tags/"+tag, "-f", "sha="+sha)
	return err
}
