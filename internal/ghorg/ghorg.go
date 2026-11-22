// Package ghorg reads files and organisation lists through the GitHub REST API
// with a reserve that latches: the one client behind `orgfile` and `orgedit`.
//
// GitHub code search is blind to the files these commands target, so a file is
// read through the contents endpoint, one call each, and the remaining quota is
// read off every response.
package ghorg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxBody bounds a file read: a runner config is a few KB.
const MaxBody = 4 << 20

// Client counts nothing itself: it reads the reserve off every response.
type Client struct {
	Base, Token string
	HTTP        *http.Client
	Reserve     int

	mu        sync.Mutex
	remaining int // -1 until a response has said
	stopped   bool
	calls     int
}

// New builds a client for base (an API root) with a bearer token.
func New(base, token string, reserve int) *Client {
	return &Client{Base: base, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second},
		Reserve: reserve, remaining: -1}
}

// Low reports whether the reserve is reached, latching the stop.
func (c *Client) Low() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remaining >= 0 && c.remaining < c.Reserve {
		c.stopped = true
	}
	return c.stopped
}

// Stopped reports whether the reserve was ever reached.
func (c *Client) Stopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

// Remaining is the last quota a response reported, or -1.
func (c *Client) Remaining() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.remaining
}

// Calls is the number of responses received.
func (c *Client) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *Client) note(h http.Header) {
	n, err := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if err == nil && n >= 0 {
		c.remaining = n
	}
}

// Get returns the status and body of one GET. raw asks for the file's bytes.
func (c *Client) Get(ctx context.Context, path string, raw bool) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return 0, nil, err
	}
	accept := "application/vnd.github+json"
	if raw {
		accept = "application/vnd.github.raw+json"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	c.note(resp.Header)
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(b) > MaxBody {
		return resp.StatusCode, nil, fmt.Errorf("body over %d bytes", MaxBody)
	}
	return resp.StatusCode, b, nil
}

// Orgs lists every organisation of the account, page by page, sorted.
func (c *Client) Orgs(ctx context.Context) ([]string, error) {
	var out []string
	for page := 1; page <= 100; page++ {
		st, b, err := c.Get(ctx, fmt.Sprintf("/user/orgs?per_page=100&page=%d", page), false)
		if err != nil {
			return nil, err
		}
		if st != http.StatusOK {
			return nil, fmt.Errorf("listing organisations: HTTP %d", st)
		}
		names, err := Logins(b)
		if err != nil {
			return nil, err
		}
		out = append(out, names...)
		if len(names) < 100 {
			sort.Strings(out)
			return out, nil
		}
	}
	return nil, fmt.Errorf("listing organisations: over 100 pages")
}

// NameRe matches a GitHub owner or repository name.
var NameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ValidName reports whether n is a usable GitHub name.
func ValidName(n string) bool { return NameRe.MatchString(n) && n != "." && n != ".." }

// ValidPath reports whether p is a bounded relative path with no empty, "." or
// ".." segment.
func ValidPath(p string) bool {
	if p == "" || len(p) > 300 {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// EscapePath escapes each segment of a path for a URL.
func EscapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// Logins decodes the organisation names of one /user/orgs page.
func Logins(b []byte) ([]string, error) {
	var page []struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(b, &page); err != nil {
		return nil, fmt.Errorf("listing organisations: %w", err)
	}
	out := make([]string, 0, len(page))
	for _, p := range page {
		if p.Login != "" {
			out = append(out, p.Login)
		}
	}
	return out, nil
}
