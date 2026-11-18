package binnames

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// GitTree reads a repository at a ref WITHOUT checking it out.
//
// ⛔ A clone on this machine is usually sitting on somebody's branch, and a
// working-tree scan of it reports that branch, not what `go install ...@latest`
// would build. Reading origin's default branch from the object database answers
// the question that was asked, and touches nothing: no checkout, no index.
type GitTree struct {
	Dir string
	Ref string
}

func (g GitTree) git(args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", g.Dir}, args...)...)
	cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var e bytes.Buffer
	cmd.Stderr = &e
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(e.String()))
	}
	return out, nil
}

func (g GitTree) Files() ([]string, error) {
	out, err := g.git("ls-tree", "-r", "-z", "--name-only", g.Ref)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			files = append(files, string(f))
		}
	}
	return files, nil
}

func (g GitTree) Read(p string) ([]byte, error) { return g.git("show", g.Ref+":"+p) }

// Budget counts the REST calls a sweep makes and refuses past a cap.
//
// ⛔ The cap exists because sweeps starve Renovate: the hourly REST budget is
// per user, and a fleet sweep and the scheduled Renovate runs draw on the same
// 5000. This is a ceiling on THIS command, in addition to the shared guard in
// package fleet, which only stops at a reserve.
type Budget struct {
	mu   sync.Mutex
	Cap  int
	Used int
}

// ErrCap means the sweep reached its own ceiling.
var ErrCap = errors.New("REST call cap reached")

// Spend charges n calls, or refuses without charging.
func (b *Budget) Spend(n int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Used+n > b.Cap {
		return fmt.Errorf("%w (%d of %d used)", ErrCap, b.Used, b.Cap)
	}
	b.Used += n
	return nil
}

// Remote reads a repository from GitHub: ONE recursive git-trees API call for
// the file list, then raw file contents, which are not REST API calls and do
// not draw on the hourly budget.
type Remote struct {
	Repo   string // owner/name
	Branch string
	Budget *Budget
	// API runs `gh api` style requests; Raw fetches a URL. Tests replace both.
	API func(args ...string) ([]byte, error)
	Raw func(url string) ([]byte, error)
}

func (r *Remote) Files() ([]string, error) {
	if err := r.Budget.Spend(1); err != nil {
		return nil, err
	}
	b, err := r.API("api", "repos/"+r.Repo+"/git/trees/"+r.Branch+"?recursive=1")
	if err != nil {
		return nil, err
	}
	var tr struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(b, &tr); err != nil {
		return nil, fmt.Errorf("the tree of %s did not decode: %v", r.Repo, err)
	}
	if tr.Truncated {
		// A truncated tree is a PART of the repository: judging it would call
		// the unseen part clean.
		return nil, fmt.Errorf("the tree of %s is truncated by GitHub", r.Repo)
	}
	var files []string
	for _, e := range tr.Tree {
		if e.Type == "blob" {
			files = append(files, e.Path)
		}
	}
	return files, nil
}

func (r *Remote) Read(p string) ([]byte, error) {
	return r.Raw("https://raw.githubusercontent.com/" + r.Repo + "/" + r.Branch + "/" + p)
}

// RemoteCandidate is the directory filter a remote scan uses: the root, any
// directory holding a main.go, and anything under cmd/. A `package main` in a
// differently named file elsewhere is not seen, and the report says so.
func RemoteCandidate(dir string, files []string) bool {
	if dir == "" || dir == "cmd" || strings.HasPrefix(dir, "cmd/") || strings.Contains(dir, "/cmd/") {
		return true
	}
	for _, f := range files {
		if strings.HasSuffix(f, "/main.go") || f == "main.go" {
			return true
		}
	}
	return false
}

// HTTPRaw is the default Raw: an HTTP GET with an optional token, and a
// non-200 is an error, never an empty file.
func HTTPRaw(token string) func(string) ([]byte, error) {
	cl := &http.Client{Timeout: 30 * time.Second}
	return func(url string) ([]byte, error) {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := cl.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	}
}
