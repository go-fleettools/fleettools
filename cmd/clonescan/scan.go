package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-fleettools/fleettools/internal/fleet"
)

// A repo is one main repository and everything that hangs off it.
type repo struct {
	path      string // the main worktree, or the git directory of a bare one
	commonDir string // $GIT_COMMON_DIR, absolute: the identity of the repository
	notes     []string
	reasons   []string // why it is KEEP; none means SAFE
	worktrees []*worktree
	heads     map[string]string // refs/heads/<name> -> commit
	bundle    string
	bundleErr error
}

// safe is SAFE: nothing to keep in the repository nor in any worktree.
func (r *repo) safe() bool {
	if len(r.reasons) > 0 {
		return false
	}
	for _, wt := range r.worktrees {
		if len(wt.reasons) > 0 {
			return false
		}
	}
	return true
}

// A worktree is a linked worktree of a repo (git-worktree(1)).
type worktree struct {
	path    string
	what    string // "branch x", "detached at 1234abc", "prunable: …"
	notes   []string
	reasons []string
}

type scanner struct {
	recent time.Duration
	now    time.Time
}

// git runs git read-only and returns its standard output.
//
// GIT_OPTIONAL_LOCKS=0 (git(1)) stops `git status` from refreshing the index,
// the one write a reading command otherwise makes; core.fsmonitor=false stops
// a repository's own configuration from starting a program.
var git = func(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-c", "core.fsmonitor=false", "-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = nil
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// A pullRequest is the part of `gh pr list --json` read here.
type pullRequest struct {
	Number     int    `json:"number"`
	HeadRefOid string `json:"headRefOid"`
}

// mergedPRs is the seam a test replaces: the merged pull requests of a GitHub
// repository whose head branch is branch.
var mergedPRs = func(slug, branch string) ([]pullRequest, error) {
	b, err := fleet.GH("pr", "list", "-R", slug, "--head", branch, "--state", "merged",
		"--json", "number,headRefOid", "--limit", "30")
	if err != nil {
		return nil, err
	}
	var prs []pullRequest
	if err := json.Unmarshal(b, &prs); err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	return prs, nil
}

// scan finds the repositories under dirs and judges each once, however many
// of its worktrees were found.
func (s scanner) scan(dirs []string, depth int) ([]*repo, error) {
	var found []string
	for _, d := range dirs {
		abs, err := filepath.Abs(d)
		if err != nil {
			return nil, err
		}
		// git reports real paths (/private/var on macOS, not /var); a path
		// spelled through a symlink would not match its own worktree record.
		if abs, err = filepath.EvalSymlinks(abs); err != nil {
			return nil, err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", abs)
		}
		if err := find(abs, depth, &found); err != nil {
			return nil, err
		}
	}
	var out []*repo
	byCommon := map[string]*repo{}
	for _, p := range found {
		common, err := git(p, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			// ⛔ The disaster this command exists for leaves exactly this: a
			// worktree whose main repository was deleted first. It is not
			// skipped: unreadable is not deletable.
			out = append(out, &repo{path: p, reasons: []string{"git cannot read it (" + firstLine(err.Error()) + "); a worktree whose main repository is gone looks like this"}})
			continue
		}
		common = filepath.Clean(strings.TrimSpace(common))
		if r := byCommon[common]; r != nil {
			continue
		}
		r := s.judge(p, common)
		byCommon[common] = r
		out = append(out, r)
	}
	return out, nil
}

// find appends every directory under dir, down to depth levels, that holds a
// .git (directory or file). It does not descend into one: what is below a
// work tree belongs to it, and its worktrees are listed by git.
func find(dir string, depth int, found *[]string) error {
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
		*found = append(*found, dir)
		return nil
	}
	if depth == 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() { // a symlink is not followed: it could loop, or lead out
			continue
		}
		if err := find(filepath.Join(dir, e.Name()), depth-1, found); err != nil {
			return err
		}
	}
	return nil
}

// A record is one worktree in `git worktree list --porcelain -z`.
type record struct {
	path, head, branch       string
	bare, detached           bool
	locked, prunable         bool
	lockReason, pruneReasons string
}

// parseWorktrees reads the porcelain format of git-worktree(1): one attribute
// per NUL-terminated line, label and value separated by a space, a record
// ended by an empty line.
func parseWorktrees(s string) []record {
	var out []record
	var cur *record
	for _, line := range strings.Split(s, "\x00") {
		if line == "" {
			cur = nil
			continue
		}
		label, value, _ := strings.Cut(line, " ")
		if label == "worktree" {
			out = append(out, record{path: value})
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil {
			continue
		}
		switch label {
		case "HEAD":
			cur.head = value
		case "branch":
			cur.branch = strings.TrimPrefix(value, "refs/heads/")
		case "bare":
			cur.bare = true
		case "detached":
			cur.detached = true
		case "locked":
			cur.locked, cur.lockReason = true, value
		case "prunable":
			cur.prunable, cur.pruneReasons = true, value
		}
	}
	return out
}

// judge reads one repository, found at p, whose common directory is common.
func (s scanner) judge(p, common string) *repo {
	r := &repo{path: p, commonDir: common, heads: map[string]string{}}
	list, err := git(p, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		r.reasons = append(r.reasons, "git worktree list failed: "+firstLine(err.Error()))
		return r
	}
	recs := parseWorktrees(list)
	if len(recs) == 0 {
		r.reasons = append(r.reasons, "git worktree list listed nothing")
		return r
	}
	main := recs[0]
	if main.bare {
		r.path = common
		r.notes = append(r.notes, "bare repository; only its linked worktrees have files")
	} else {
		r.path = filepath.Clean(main.path)
		if r.path != filepath.Clean(p) {
			r.notes = append(r.notes, "found through its worktree "+p)
		}
		why, notes := s.judgeTree(main)
		r.reasons = append(r.reasons, why...)
		r.notes = append(r.notes, notes...)
	}
	linked := 0
	for _, rec := range recs[1:] {
		wt := &worktree{path: rec.path, what: describe(rec)}
		r.worktrees = append(r.worktrees, wt)
		if rec.prunable {
			wt.what = "prunable: " + rec.pruneReasons
			wt.notes = append(wt.notes, "its directory is gone; `git worktree prune` would drop the metadata")
			continue
		}
		linked++
		wt.reasons, wt.notes = s.judgeTree(rec)
		if rec.locked {
			wt.reasons = append(wt.reasons, "locked"+cmp(": "+rec.lockReason, ": ", ""))
		}
	}
	if linked > 0 {
		// ⛔ The worktrees' metadata lives in this repository's
		// .git/worktrees: deleting it first orphans them.
		r.reasons = append(r.reasons, fmt.Sprintf("%d linked worktree(s) hang off it; remove them first with `git worktree remove`", linked))
	}
	if n := s.stashes(p); n != "" {
		r.reasons = append(r.reasons, n)
	}
	r.judgeBranches(p)
	return r
}

func cmp(s, empty, otherwise string) string {
	if s == empty {
		return otherwise
	}
	return s
}

func describe(rec record) string {
	switch {
	case rec.branch != "":
		return "branch " + rec.branch
	case len(rec.head) >= 7:
		return "detached at " + rec.head[:7]
	}
	return "no HEAD"
}

// judgeTree reads one worktree's files and HEAD.
func (s scanner) judgeTree(rec record) (reasons, notes []string) {
	st, err := git(rec.path, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return []string{"git status failed: " + firstLine(err.Error())}, nil
	}
	if n := countStatus(st); n > 0 {
		reasons = append(reasons, fmt.Sprintf("%d uncommitted path(s)", n))
	}
	if rec.detached && rec.head != "" {
		n, err := git(rec.path, "rev-list", "--count", rec.head, "--not", "--branches", "--remotes")
		switch {
		case err != nil:
			reasons = append(reasons, "detached HEAD could not be read: "+firstLine(err.Error()))
		case strings.TrimSpace(n) != "0":
			reasons = append(reasons, fmt.Sprintf("detached HEAD has %s commit(s) on no branch and no remote", strings.TrimSpace(n)))
		}
	}
	log, err := git(rec.path, "rev-parse", "--path-format=absolute", "--git-path", "logs/HEAD")
	if err != nil {
		return append(reasons, "HEAD reflog could not be located"), notes
	}
	fi, err := os.Stat(strings.TrimSpace(log))
	if err != nil {
		return reasons, append(notes, "no HEAD reflog: the age of the last HEAD movement is unknown")
	}
	age := s.now.Sub(fi.ModTime())
	if s.recent > 0 && age < s.recent {
		reasons = append(reasons, fmt.Sprintf("HEAD moved %s ago, within --recent %s: something may be working in it", roundAge(age), s.recent))
	} else {
		notes = append(notes, fmt.Sprintf("HEAD moved %s ago", roundAge(age)))
	}
	return reasons, notes
}

func roundAge(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Hour:
		return d.Round(time.Second).String()
	case d < 48*time.Hour:
		return d.Round(time.Minute).String()
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// countStatus counts the entries of `git status --porcelain=v1 -z`: "XY path"
// each, and a rename or copy carries its original path as one more field.
func countStatus(s string) int {
	fields := strings.Split(s, "\x00")
	n := 0
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 3 {
			continue
		}
		n++
		if f[0] == 'R' || f[0] == 'C' {
			i++
		}
	}
	return n
}

// stashes says how many stashes there are, or "" for none. refs/stash is
// shared by every worktree of a repository.
func (s scanner) stashes(p string) string {
	out, err := git(p, "stash", "list", "--format=%gd")
	if err != nil {
		return "stash list failed: " + firstLine(err.Error())
	}
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d stash(es)", n)
}

// judgeBranches holds every local branch to the rule: safe when its tip is
// reachable from a remote-tracking ref, or is the head of a merged pull
// request at the same commit.
func (r *repo) judgeBranches(p string) {
	out, err := git(p, "for-each-ref", "--format=%(refname)%00%(objectname)", "refs/heads")
	if err != nil {
		r.reasons = append(r.reasons, "branches could not be listed: "+firstLine(err.Error()))
		return
	}
	var names []string
	for _, l := range strings.Split(out, "\n") {
		ref, oid, ok := strings.Cut(l, "\x00")
		if !ok {
			continue
		}
		r.heads[ref] = oid
		names = append(names, ref)
	}
	slices.Sort(names)
	slug := ""
	if u, err := git(p, "config", "--get", "remote.origin.url"); err == nil {
		slug = githubSlug(strings.TrimSpace(u))
	}
	for _, ref := range names {
		oid, branch := r.heads[ref], strings.TrimPrefix(ref, "refs/heads/")
		n, err := git(p, "rev-list", "--count", oid, "--not", "--remotes")
		if err != nil {
			r.reasons = append(r.reasons, "branch "+branch+": "+firstLine(err.Error()))
			continue
		}
		count := strings.TrimSpace(n)
		if count == "0" {
			r.notes = append(r.notes, "branch "+branch+": on a remote-tracking ref (as of the last fetch)")
			continue
		}
		if slug == "" {
			r.reasons = append(r.reasons, fmt.Sprintf("branch %s: %s commit(s) on no remote, and no GitHub origin to ask about merged pull requests", branch, count))
			continue
		}
		prs, err := mergedPRs(slug, branch)
		if err != nil {
			r.reasons = append(r.reasons, fmt.Sprintf("branch %s: %s commit(s) on no remote, and GitHub could not be asked: %s", branch, count, firstLine(err.Error())))
			continue
		}
		r.judgeAgainstPRs(p, branch, oid, count, prs)
	}
}

func (r *repo) judgeAgainstPRs(p, branch, oid, count string, prs []pullRequest) {
	for _, pr := range prs {
		if pr.HeadRefOid == oid {
			r.notes = append(r.notes, fmt.Sprintf("branch %s: the head of merged PR #%d at the same tip", branch, pr.Number))
			return
		}
	}
	for _, pr := range prs {
		if pr.HeadRefOid == "" {
			continue
		}
		if _, err := git(p, "merge-base", "--is-ancestor", pr.HeadRefOid, oid); err != nil {
			continue
		}
		after, err := git(p, "rev-list", "--count", pr.HeadRefOid+".."+oid)
		if err != nil {
			continue
		}
		r.reasons = append(r.reasons, fmt.Sprintf("branch %s: %s commit(s) after merged PR #%d", branch, strings.TrimSpace(after), pr.Number))
		return
	}
	r.reasons = append(r.reasons, fmt.Sprintf("branch %s: %s commit(s) on no remote and no merged pull request at this tip", branch, count))
}

var slugRE = regexp.MustCompile(`^(?:https://github\.com/|ssh://git@github\.com/|git@github\.com:)([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// githubSlug is owner/repo for a github.com remote URL, or "".
func githubSlug(u string) string {
	m := slugRE.FindStringSubmatch(u)
	if m == nil {
		return ""
	}
	return m[1]
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// writeBundle writes every local branch of r into one bundle under dir, and
// returns its path once `git bundle verify` accepted it and its heads equal
// the repository's branches.
func writeBundle(r *repo, dir string) (string, error) {
	if r.commonDir == "" {
		return "", errors.New("not a readable repository")
	}
	if len(r.heads) == 0 {
		return "", errors.New("no local branch to bundle")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(r.commonDir))
	name := safeName(filepath.Base(r.path)) + "-" + hex.EncodeToString(sum[:4]) + ".bundle"
	final := filepath.Join(dir, name)
	tmp := final + ".tmp"
	defer os.Remove(tmp)
	if _, err := git(r.path, "bundle", "create", "--quiet", tmp, "--branches"); err != nil {
		return "", err
	}
	if _, err := git(r.path, "bundle", "verify", "--quiet", tmp); err != nil {
		return "", err
	}
	heads, err := git(r.path, "bundle", "list-heads", tmp)
	if err != nil {
		return "", err
	}
	got := map[string]string{}
	for _, l := range strings.Split(heads, "\n") {
		oid, ref, ok := strings.Cut(strings.TrimSpace(l), " ")
		if ok {
			got[ref] = oid
		}
	}
	if err := sameHeads(got, r.heads); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	return final, nil
}

// sameHeads says how a bundle's heads differ from the repository's branches.
func sameHeads(bundle, repo map[string]string) error {
	var diffs []string
	for ref, oid := range repo {
		if bundle[ref] != oid {
			diffs = append(diffs, fmt.Sprintf("%s is %s in the repository, %q in the bundle", ref, oid, bundle[ref]))
		}
	}
	for ref := range bundle {
		if _, ok := repo[ref]; !ok {
			diffs = append(diffs, ref+" is in the bundle only")
		}
	}
	if len(diffs) > 0 {
		slices.Sort(diffs)
		return errors.New("heads differ: " + strings.Join(diffs, "; "))
	}
	return nil
}

func safeName(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			b[i] = '_'
		}
	}
	if len(b) == 0 || b[0] == '.' {
		return "repo" + strconv.Itoa(len(b)) + string(b)
	}
	return string(b)
}
