package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// ---------------------------------------------------------------------------
// Seams. Tests replace every one of these; none of them spawns a process or
// sleeps for real under test.

var (
	sleep = time.Sleep
	now   = time.Now
	logw  = io.Writer(os.Stderr)

	// ghRaw is the one place gh is invoked for the API.
	ghRaw = func(args ...string) ([]byte, error) { return fleet.GH(args...) }

	// tool runs agentsync, prwait and ghmerge; code is the exit status, -1 if the
	// program could not be started.
	tool = func(name string, args ...string) (out string, code int) {
		cmd := exec.Command(name, args...)
		b, err := cmd.CombinedOutput()
		out = strings.TrimSpace(string(b))
		var ee *exec.ExitError
		switch {
		case err == nil:
			return out, 0
		case errors.As(err, &ee):
			return out, ee.ExitCode()
		}
		return err.Error(), -1
	}
)

// quotaRetries is how many quota sleeps one API call may take (flag).
var quotaRetries = 12

// quotaWaitMax is the longest single sleep after a quota 403 (flag).
var quotaWaitMax = 5 * time.Minute

// ghJSON is a seam over gh so the inspection is testable. It never returns a
// quota 403 as an answer while it can still wait one out.
var ghJSON = func(args ...string) ([]byte, error) { return callWithQuota(ghRaw, args...) }

// isQuota says whether an error is GitHub refusing for BUDGET, which is not an
// answer about the repository: primary ("API rate limit exceeded for user ID")
// and secondary limits and abuse detection.
func isQuota(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "rate limit") || strings.Contains(l, "abuse detection")
}

// callWithQuota is gh with the one rule the 2026-10-10 campaign lacked.
//
// ⛔ A 403 "API rate limit exceeded" IS NOT A RESULT. That day the budget
// (5000/h, shared with Renovate and other sessions) ran out, and `gh api
// rate_limit` read 5000/5000 throughout -- it shows one token's bucket, not
// the account's -- while every real call answered 403. The repositories hit
// were reported "unreadable" and counted as done. So the only probe is a real
// call, and its quota refusal means "wait and ask again".
func callWithQuota(call func(...string) ([]byte, error), args ...string) ([]byte, error) {
	for try := 0; ; try++ {
		b, err := call(args...)
		if err == nil || !isQuota(err.Error()+string(b)) || try >= quotaRetries {
			return b, err
		}
		d := quotaDelay()
		fmt.Fprintf(logw, "gopin: GitHub quota exhausted; sleeping %s (retry %d/%d)\n", d.Round(time.Second), try+1, quotaRetries)
		sleep(d)
	}
}

// quotaDelay sleeps until the advertised reset, but never longer than
// quotaWaitMax and never less than a minute: the advertised reset is one
// token's bucket and can be wrong, so waking early only costs one more try.
func quotaDelay() time.Duration {
	d := quotaWaitMax
	if b, err := ghRaw("api", "rate_limit", "--jq", ".resources.core.reset"); err == nil {
		if sec, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
			if until := time.Unix(sec, 0).Sub(now()) + 2*time.Second; until < d {
				d = until
			}
		}
	}
	if d < time.Minute {
		d = time.Minute
	}
	return d
}

// ---------------------------------------------------------------------------
// State.

type entry struct {
	Status string `json:"status"`
	PR     string `json:"pr,omitempty"`
	Note   string `json:"note,omitempty"`
}

type stateFile struct {
	From    string           `json:"from"`
	Version string           `json:"version"`
	Repos   map[string]entry `json:"repos"`
}

// state is a nil-safe resumable record: a nil *state remembers nothing.
type state struct {
	path string
	sf   stateFile
}

func loadState(path, from, version string) (*state, error) {
	if path == "" {
		return nil, nil
	}
	s := &state{path: path, sf: stateFile{From: from, Version: version, Repos: map[string]entry{}}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var got stateFile
	if err := json.Unmarshal(b, &got); err != nil {
		return nil, fmt.Errorf("state %s: %w", path, err)
	}
	// ⛔ A state file belongs to ONE campaign. Rerunning 1.27.3 over a file that
	// finished "1.27.2 -> 1.27.3" would skip everything and call it done.
	if got.From != from || got.Version != version {
		return nil, fmt.Errorf("state %s is for %s -> %s, not %s -> %s; use another file", path, got.From, got.Version, from, version)
	}
	if got.Repos != nil {
		s.sf.Repos = got.Repos
	}
	return s, nil
}

func (s *state) get(repo string) (entry, bool) {
	if s == nil {
		return entry{}, false
	}
	e, ok := s.sf.Repos[repo]
	return e, ok
}

// set records an entry and writes the file whole and atomically, so a kill
// mid-write leaves the previous state and not half of one.
func (s *state) set(repo string, e entry) error {
	if s == nil {
		return nil
	}
	s.sf.Repos[repo] = e
	b, err := json.MarshalIndent(s.sf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// ---------------------------------------------------------------------------
// The sweep.

// Statuses. clean, merged and archived are FINISHED: a rerun skips them.
const (
	stClean    = "clean"    // nothing to change
	stReported = "reported" // needs a change; not applying
	stOpened   = "opened"   // pull request open, not followed
	stMerged   = "merged"
	stRed      = "red"      // checks red or merge refused
	stUnread   = "unread"   // checks could not be read
	stClaimed  = "claimed"  // another session holds it
	stArchived = "archived" // read-only
	stFailed   = "failed"
	stDeferred = "deferred" // the quota never answered: retry, never done
)

func finished(st string) bool { return st == stClean || st == stMerged || st == stArchived }

// followable: a pull request exists and its fate is not settled.
func followable(e entry) bool {
	return e.PR != "" && (e.Status == stOpened || e.Status == stRed || e.Status == stUnread)
}

type result struct {
	st      string
	pr      string
	note    string
	alias   bool // held the alias
	repin   bool // an explicit pin to move
	changed bool // needed a change (counts towards -limit)
	resumed bool
	opened  bool // this run opened a pull request
}

type sweeper struct {
	cfg    config
	target goVersion
	out    io.Writer
	st     *state
}

func (s *sweeper) printf(format string, a ...any) { fmt.Fprintf(s.out, format, a...) }

func (s *sweeper) sweep(repos []string) int {
	quotaWaitMax = s.cfg.quotaWait
	final := map[string]result{}
	changed := 0
	pending := repos
passes:
	for pass := 1; pass <= s.cfg.passes && len(pending) > 0; pass++ {
		if pass > 1 {
			s.printf("-- pass %d: %d repositories the quota had not answered --\n", pass, len(pending))
			sleep(s.cfg.quotaWait)
		}
		var again []string
		for _, repo := range pending {
			r := s.handle(repo)
			final[repo] = r
			if r.st == stDeferred {
				again = append(again, repo)
				continue
			}
			if r.changed && !r.resumed {
				changed++
			}
			if s.cfg.limit > 0 && changed >= s.cfg.limit {
				s.printf("stopping at -limit %d\n", s.cfg.limit)
				break passes
			}
		}
		pending = again
	}
	return s.summary(repos, final)
}

// handle brings ONE repository to a status. It never returns "done" for a
// repository nobody answered about.
func (s *sweeper) handle(repo string) result {
	if e, ok := s.st.get(repo); ok {
		if finished(e.Status) {
			s.printf("resumed %s: %s\n", repo, e.Status)
			return result{st: e.Status, pr: e.PR, resumed: true}
		}
		if s.cfg.follow && followable(e) {
			s.printf("resume  %s: following %s\n", repo, e.PR)
			return s.settle(repo, result{st: stOpened, pr: e.PR, resumed: true})
		}
	}
	if s.cfg.guard {
		// A real call. Its quota refusal was already waited out by ghJSON; one
		// that is still a refusal means the budget never came back.
		if _, err := ghJSON("api", "repos/"+repo, "--jq", ".id"); err != nil && isQuota(err.Error()) {
			return s.deferred(repo, err.Error())
		}
	}
	f := inspectWith(repo, s.cfg.version, s.cfg.from, s.cfg.gomod)
	switch {
	case isQuota(f.Err):
		return s.deferred(repo, f.Err)
	case f.Err != "":
		s.printf("ERROR   %s: %s\n", repo, f.Err)
		return s.record(repo, result{st: stFailed, note: f.Err})
	}
	r := result{}
	switch {
	case f.Aliases == 0 && f.moveCount() > 0:
		// An explicit pin on the version being moved away from. Reported
		// under its own word, because "ALIAS" would say the opposite of
		// what is true here: this repository CAN be reviewed, it is
		// simply on a Go that something is wrong with.
		r.repin, r.changed = true, true
		s.printf("REPIN   %s:", repo)
		if f.Moves > 0 {
			s.printf(" %d × %s -> %s in %s", f.Moves, s.cfg.from, s.cfg.version, strings.Join(f.MoveFiles, " "))
		}
		if len(f.GoModFiles) > 0 {
			s.printf(" go-version-file %s (go %s -> %s)", strings.Join(f.GoModFiles, " "), s.cfg.from, s.cfg.version)
		}
		s.printf("\n")
	case f.Aliases == 0:
		if len(f.Literals) > 0 {
			s.printf("pinned  %s (%s)\n", repo, strings.Join(f.Literals, " "))
		}
		return s.record(repo, result{st: stClean})
	default:
		r.alias, r.changed = true, true
		s.printf("ALIAS   %s: %d in %s", repo, f.Aliases, strings.Join(f.Files, " "))
		held, isHeld := heldBackBy(f.Literals, s.target)
		switch {
		case olderGoMod(f.GoMod, s.target) && isHeld:
			s.printf(" ; go.mod HELD at %s by the %s pin", f.GoMod, held)
		case olderGoMod(f.GoMod, s.target):
			s.printf(" ; go.mod %s -> %s", f.GoMod, s.cfg.version)
		}
		if len(f.Literals) > 0 {
			s.printf(" ; leaving %s alone", strings.Join(f.Literals, " "))
		}
		s.printf("\n")
	}
	if !s.cfg.apply {
		r.st = stReported
		return r
	}
	return s.apply(repo, f, r)
}

// apply claims, opens and (with --follow) settles one repository.
func (s *sweeper) apply(repo string, f finding, r result) result {
	if s.cfg.claim {
		out, code := tool("agentsync", "claim", "--note", "Go "+s.cfg.version+" pin (gopin)", repo)
		if code != 0 {
			s.printf("        skipped: claimed by another session: %s\n", oneLine(out))
			r.st, r.note = stClaimed, oneLine(out)
			return s.record(repo, r)
		}
		defer tool("agentsync", "release", repo)
	}
	// Do not duplicate: Renovate (or an earlier run of this tool) may already
	// have a pull request for exactly this move. Merge that one instead.
	if pr, title, err := existingPR(repo, pinBranch(f, s.cfg.version, s.cfg.from), s.cfg.version); err == nil && pr != "" {
		s.printf("        existing pull request %s (%s): not opening another\n", pr, title)
		r.st, r.pr = stOpened, pr
		return s.settle(repo, r)
	}
	pr, err := openPR(repo, f, s.cfg.version, s.cfg.from)
	switch {
	case errors.Is(err, errArchived):
		s.printf("        skipped: %v\n", err)
		r.st = stArchived
		return s.record(repo, r)
	case err != nil && isQuota(err.Error()):
		return s.deferredR(repo, r, err.Error())
	case err != nil:
		s.printf("        pull request FAILED: %v\n", err)
		r.st, r.note = stFailed, err.Error()
		return s.record(repo, r)
	}
	r.st, r.pr, r.opened = stOpened, pr, true
	sleep(s.cfg.pause)
	return s.settle(repo, r)
}

// settle follows an open pull request when asked and records the outcome.
func (s *sweeper) settle(repo string, r result) result {
	if !s.cfg.follow || r.pr == "" {
		return s.record(repo, r)
	}
	r.st, r.note = s.follow(repo, r.pr)
	if r.note != "" {
		s.printf("        %s: %s\n", r.st, r.note)
	}
	return s.record(repo, r)
}

// follow waits for the checks like prwait (an empty list is not a pass) and
// merges with ghmerge when green.
func (s *sweeper) follow(repo, pr string) (status, note string) {
	num := prNumber(pr)
	if num == "" {
		return stUnread, "no pull request number in " + pr
	}
	out, code := tool("prwait", "-timeout", s.cfg.ciTimeout.String(), repo+"#"+num)
	switch code {
	case 0:
	case 1:
		return stRed, "checks not green: " + lastLine(out)
	default:
		return stUnread, "checks unreadable: " + lastLine(out)
	}
	out, code = tool("ghmerge", repo, num)
	if code != 0 {
		return stRed, "merge refused: " + lastLine(out)
	}
	return stMerged, ""
}

func (s *sweeper) deferred(repo, why string) result {
	return s.deferredR(repo, result{}, why)
}

func (s *sweeper) deferredR(repo string, r result, why string) result {
	s.printf("DEFER   %s: quota never answered (%s); retried in a later pass, not counted done\n", repo, oneLine(why))
	r.st, r.note = stDeferred, why
	return r
}

// record persists a status and returns r.
func (s *sweeper) record(repo string, r result) result {
	if err := s.st.set(repo, entry{Status: r.st, PR: r.pr, Note: r.note}); err != nil {
		s.printf("        state NOT saved: %v\n", err)
	}
	return r
}

// summary prints the totals and reconciles them against the input.
//
// ⛔ INPUTS MUST EQUAL OUTPUTS. A missing field must never silently drop a row
// (the lesson of the jq pipelines): every listed repository ends in exactly one
// counted state, "not reached" included, and a sum that differs from the list
// is itself a failure.
func (s *sweeper) summary(repos []string, final map[string]result) int {
	var aliased, repinned, explicit, failed, opened, archived, merged, red, unread, claimed, deferred, resumed, reported, notReached, unknown int
	for _, repo := range repos {
		r, ok := final[repo]
		if !ok {
			notReached++
			continue
		}
		if r.alias {
			aliased++
		}
		if r.repin {
			repinned++
		}
		if r.resumed {
			resumed++
		}
		if r.opened {
			opened++
		}
		switch r.st {
		case stClean:
			explicit++
		case stReported:
			reported++
		case stOpened:
		case stMerged:
			merged++
		case stRed:
			red++
		case stUnread:
			unread++
		case stClaimed:
			claimed++
		case stArchived:
			archived++
		case stFailed:
			failed++
		case stDeferred:
			deferred++
		default:
			unknown++
		}
	}
	// opened-but-unfollowed is its own bucket so the sum can close.
	openOnly := 0
	for _, repo := range repos {
		if r, ok := final[repo]; ok && r.st == stOpened {
			openOnly++
		}
	}
	s.printf("\n%d read · %d hold the alias · %d already explicit · %d unreadable", len(repos), aliased, explicit, failed)
	if repinned > 0 {
		s.printf(" · %d to move from %s", repinned, s.cfg.from)
	}
	if s.cfg.apply {
		s.printf(" · %d pull request(s) opened", opened)
		if archived > 0 {
			s.printf(" · %d archived and skipped", archived)
		}
	}
	for _, c := range []struct {
		n    int
		what string
	}{{merged, "merged"}, {red, "RED"}, {unread, "checks unreadable"}, {claimed, "claimed by others"},
		{deferred, "DEFERRED by the quota"}, {resumed, "resumed from state"}, {notReached, "not reached"}} {
		if c.n > 0 {
			s.printf(" · %d %s", c.n, c.what)
		}
	}
	s.printf("\n")

	sum := explicit + reported + openOnly + merged + red + unread + claimed + archived + failed + deferred + notReached + unknown
	if sum != len(repos) || unknown > 0 {
		s.printf("ACCOUNTING FAILED: %d listed, %d accounted for (%d with an unknown status)\n", len(repos), sum, unknown)
		return 1
	}
	var bad []string
	for _, repo := range repos {
		if r, ok := final[repo]; ok {
			switch r.st {
			case stRed, stUnread, stFailed, stDeferred:
				bad = append(bad, fmt.Sprintf("%s (%s)", repo, r.st))
			}
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		s.printf("needs attention: %s\n", strings.Join(bad, ", "))
		return 1
	}
	return 0
}

// existingPR finds an open pull request already doing this job: our own branch
// from an earlier run, or Renovate's "Update dependency go to vX".
func existingPR(repo, branch, version string) (url, title string, err error) {
	b, err := ghJSON("pr", "list", "--repo", repo, "--state", "open", "--limit", "100",
		"--json", "url,title,headRefName")
	if err != nil {
		return "", "", err
	}
	var prs []struct {
		URL, Title, HeadRefName string
	}
	if err := json.Unmarshal(b, &prs); err != nil {
		return "", "", fmt.Errorf("%s: pr list: %w", repo, err)
	}
	for _, p := range prs {
		t := strings.ToLower(p.Title)
		if p.HeadRefName == branch || strings.Contains(t, "dependency go to v"+version) || strings.Contains(t, "dependency go to "+version) {
			return p.URL, p.Title, nil
		}
	}
	return "", "", nil
}

// prNumber is the trailing number of a pull request URL.
func prNumber(u string) string {
	u = strings.TrimSpace(u)
	i := strings.LastIndexByte(u, '/')
	n := u[i+1:]
	if _, err := strconv.Atoi(n); err != nil {
		return ""
	}
	return n
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// readList reads org/repo lines from a file or stdin.
func readList(path string, stdin io.Reader) ([]string, error) {
	var b []byte
	var err error
	if path == "" {
		b, err = io.ReadAll(stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	return readListFrom(string(b))
}
