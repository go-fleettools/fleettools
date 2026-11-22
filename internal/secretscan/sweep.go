package secretscan

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Git runs git in dir and returns its trimmed standard output. It is a
// parameter so that a test can answer for a clone that is not there; the real
// one never fetches, because staleness is what the LOCAL refs say.
type Git func(dir string, args ...string) (string, error)

// ExecGit is the Git that runs the git binary.
func ExecGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// Staleness says how old a clone's view of its remote is.
type Staleness struct {
	NeverFetched bool       `json:"never_fetched"`
	FetchedAt    *time.Time `json:"fetched_at,omitempty"` // .git/FETCH_HEAD mtime
	NoUpstream   bool       `json:"no_upstream"`          // detached, or a branch with no tracking ref
	Ahead        int        `json:"ahead"`
	Behind       int        `json:"behind"`
	Stale        bool       `json:"stale"` // never fetched, older than the limit, or behind
	GitError     string     `json:"git_error,omitempty"`
}

// Clone is one directory under the root.
type Clone struct {
	Dir        string    `json:"dir"`
	Org        string    `json:"org,omitempty"`
	Origin     string    `json:"origin,omitempty"` // canonical origin URL; see Key
	Read       bool      `json:"read"`
	SkipReason string    `json:"skip_reason,omitempty"`
	Repo       Repo      `json:"repo"`
	Staleness  Staleness `json:"staleness"`
}

// Denominator is what the sweep covered. Every count is of something the
// sweep did, so that "none found" can be told from "none looked at".
type Denominator struct {
	Seen             int `json:"clones_seen"`
	Read             int `json:"clones_read"`
	Skipped          int `json:"clones_skipped"`
	FilesRead        int `json:"files_read"`
	FilesUnread      int `json:"files_unread"`
	Ignored          int `json:"entries_ignored"` // loose files and dot-directories: not clone candidates
	GitUnreadable    int `json:"git_unreadable"`
	NeverFetched     int `json:"never_fetched"`
	Stale            int `json:"stale"`
	DaemonRepos      int `json:"daemon_repos"`
	DaemonsPlaintext int `json:"daemon_repos_with_plaintext"`
	UsesSecretsource int `json:"uses_secretsource"`
}

// Report is the whole sweep.
type Report struct {
	Generated   time.Time   `json:"generated"`
	Root        string      `json:"root"`
	Denominator Denominator `json:"denominator"`
	Clones      []Clone     `json:"clones"`
}

// Complete says whether everything seen was read: no clone skipped, no file
// unread, no git answer missing. A sweep that is not complete is NOT READ
// where it is incomplete, and must not be read as clean there.
func (r Report) Complete() bool {
	d := r.Denominator
	return d.Skipped == 0 && d.FilesUnread == 0 && d.GitUnreadable == 0
}

// Sweep reads every clone under root.
func Sweep(root string, git Git, now time.Time, staleAfter time.Duration) (Report, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Generated: now, Root: root}
	d := &rep.Denominator
	daemon, plain := map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			d.Ignored++
			continue
		}
		d.Seen++
		dir := filepath.Join(root, e.Name())
		c := Clone{Dir: e.Name()}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
			c.SkipReason = "not a git clone (no .git)"
		} else if repo, err := ScanRepo(dir, false); err != nil {
			c.SkipReason = err.Error()
		} else {
			c.Read, c.Repo = true, repo
		}
		if !c.Read {
			d.Skipped++
			rep.Clones = append(rep.Clones, c)
			continue
		}
		c.Origin = originOf(git, dir)
		c.Org = orgOf(c.Origin)
		c.Staleness = staleness(git, dir, now, staleAfter)
		d.Read++
		d.FilesRead += c.Repo.FilesRead
		d.FilesUnread += len(c.Repo.Unread)
		if c.Staleness.GitError != "" {
			d.GitUnreadable++
		}
		if c.Staleness.NeverFetched {
			d.NeverFetched++
		}
		if c.Staleness.Stale {
			d.Stale++
		}
		if c.Repo.UsesSecretsource {
			d.UsesSecretsource++
		}
		if c.Repo.Daemon {
			// per REPOSITORY, not per clone: the worktrees of one origin are
			// one daemon.
			daemon[c.Key()] = true
			if len(c.Repo.Plaintext()) > 0 {
				plain[c.Key()] = true
			}
		}
		rep.Clones = append(rep.Clones, c)
	}
	d.DaemonRepos, d.DaemonsPlaintext = len(daemon), len(plain)
	sort.Slice(rep.Clones, func(i, j int) bool { return rep.Clones[i].Dir < rep.Clones[j].Dir })
	return rep, nil
}

// originOf reads origin's URL in one canonical spelling, so that the
// worktrees and copies of one repository share it (`github.com/ORG/REPO`
// whether the clone was made over https or ssh). "" when there is none.
func originOf(git Git, dir string) string {
	url, err := git(dir, "config", "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	url = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(url), "/"), ".git")
	if _, rest, ok := strings.Cut(url, "://"); ok {
		url = rest
	}
	if _, rest, ok := strings.Cut(url, "@"); ok {
		url = rest
	}
	return strings.ToLower(strings.Replace(url, ":", "/", 1))
}

// Key is what makes two clones the same repository: the origin URL, or, with
// none, the clone's own name, so that clones without an origin are never
// merged into one.
func (c Clone) Key() string {
	if c.Origin != "" {
		return c.Origin
	}
	return "dir:" + c.Dir
}

// orgOf reads the owner from origin's URL (https://github.com/ORG/REPO or
// git@github.com:ORG/REPO): an organisation name may itself contain a hyphen,
// so the directory name cannot say where the org ends.
func orgOf(url string) string {
	i := strings.LastIndexAny(url, "/:")
	if i < 0 {
		return ""
	}
	url = url[:i]
	j := strings.LastIndexAny(url, "/:")
	return url[j+1:]
}

func staleness(git Git, dir string, now time.Time, staleAfter time.Duration) Staleness {
	var s Staleness
	p, err := git(dir, "rev-parse", "--git-path", "FETCH_HEAD")
	if err != nil {
		s.GitError = "rev-parse --git-path FETCH_HEAD: " + err.Error()
		return s
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if fi, err := os.Stat(p); err != nil {
		s.NeverFetched = true
	} else {
		t := fi.ModTime()
		s.FetchedAt = &t
	}
	out, err := git(dir, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		s.NoUpstream = true
	} else {
		a, b, _ := strings.Cut(out, "\t")
		s.Ahead, _ = strconv.Atoi(a)
		s.Behind, _ = strconv.Atoi(b)
	}
	s.Stale = s.NeverFetched || s.Behind > 0 || (s.FetchedAt != nil && now.Sub(*s.FetchedAt) > staleAfter)
	return s
}
