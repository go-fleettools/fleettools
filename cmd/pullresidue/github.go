package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxTokFile bounds the token file: a token is tens of bytes.
const maxTokFile = 4096

// readToken reads the token from a file that only its owner may read (default
// ~/.github-token), as ghissue does. The mode check is the point: a token in a
// world-readable file is already spent.
func readToken(path string) (string, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".github-token")
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("token file %s: not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file %s: mode %04o, want owner-only (0600)", path, st.Mode().Perm())
	}
	if st.Size() > maxTokFile {
		return "", fmt.Errorf("token file %s: over %d bytes", path, maxTokFile)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	return tok, nil
}

// served is what GitHub says about a blob: still served, or gone.
type served int

const (
	servedYes served = iota
	servedGone
)

func (s served) String() string {
	if s == servedYes {
		return "served"
	}
	return "gone"
}

// prober asks GitHub whether it still serves a blob, with a HEAD request so
// that a 14 MB blob is not downloaded to learn that it exists. The token goes
// in the Authorization header only.
type prober struct {
	base   string // https://api.github.com, or a test server
	token  string
	client *http.Client
}

func (p prober) blob(ctx context.Context, repo, id string) (served, error) {
	if !validRepo(repo) {
		return 0, fmt.Errorf("repository %q: want owner/name", repo)
	}
	if !isSHA(id) {
		return 0, fmt.Errorf("blob %q: want a hex object id", id)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, p.base+"/repos/"+repo+"/git/blobs/"+id, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return servedYes, nil
	case http.StatusNotFound:
		return servedGone, nil
	default:
		return 0, fmt.Errorf("GitHub answered %s for blob %s of %s", resp.Status, id, repo)
	}
}

func newProber(tokenFile string) (prober, error) {
	tok, err := readToken(tokenFile)
	if err != nil {
		return prober{}, err
	}
	return prober{base: "https://api.github.com", token: tok, client: &http.Client{Timeout: 30 * time.Second}}, nil
}

// validRepo accepts owner/name of GitHub's characters, nothing that could
// change the request path.
func validRepo(s string) bool {
	owner, name, ok := strings.Cut(s, "/")
	if !ok || owner == "" || name == "" || len(s) > 200 {
		return false
	}
	for _, part := range []string{owner, name} {
		if part == "." || part == ".." {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return false
			}
		}
	}
	return true
}

// isSHA accepts a SHA-1 or SHA-256 object id in lower-case hex.
func isSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// supportURL is where a Support request is filed; there is no API for it.
const supportURL = "https://support.github.com/contact"

// letter is the Support request for the blobs of repo that GitHub still serves.
func letter(repo string, rs []residue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Subject: Please garbage-collect unreachable blobs in %s\n\n", repo)
	b.WriteString("Hello,\n\n")
	fmt.Fprintf(&b, "Pull requests in %s contained files committed by mistake that later\n", repo)
	b.WriteString("commits removed. No branch and no tag contains them. They are reachable\n")
	b.WriteString("only through the read-only refs/pull/* refs, which I cannot delete myself,\n")
	b.WriteString("so the API still serves them:\n\n")
	for _, r := range rs {
		path := "(no path)"
		if len(r.Paths) > 0 {
			path = r.Paths[0]
		}
		prs := make([]string, len(r.PRs))
		for i, n := range r.PRs {
			prs[i] = fmt.Sprintf("#%d", n)
		}
		fmt.Fprintf(&b, "- %s, %s, pull request %s", path, human(r.Size), strings.Join(prs, ", "))
		if r.Added != "" {
			fmt.Fprintf(&b, ", added by commit %s", r.Added)
		}
		fmt.Fprintf(&b, "\n  GET /repos/%s/git/blobs/%s\n", repo, r.ID)
	}
	b.WriteString("\nThey hold no secret. We keep build outputs out of our repositories'\n")
	b.WriteString("history, so I would like these blobs collected and the pull requests'\n")
	b.WriteString("heads dereferenced, whichever you prefer.\n\nThank you.\n")
	return b.String()
}
