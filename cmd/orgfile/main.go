// Answers, for every organisation the account belongs to: does file F in
// repository R contain string S? (go-fleettools/fleettools#305)
//
// GitHub code search is BLIND to the files this was written for (a control
// query for a string known to be present returned nothing), and a search of
// pull requests only sees an organisation that has an open one. So the files are
// read through the REST contents endpoint, one call per repository tried.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
	"github.com/go-fleettools/fleettools/internal/fleet"
	"github.com/go-fleettools/fleettools/internal/ghtoken"
	"github.com/spf13/cobra"
)

const longHelp = `Reads one file from one of a few repositories in every organisation the account
belongs to and says whether it contains each --contains string.

  orgfile --file config.js --contains 'enabled: true' --contains 'dependencyDashboard: true'

Per organisation, one verdict line:

  match    the file was read and contains EVERY --contains string
  nomatch  the file was read and lacks at least one (the line says which)
  nofile   no repository in --repos has the file (404 on each)
  unread   any other answer, with its status: never read as nofile or nomatch

Repositories are tried in order and the first that has the file wins. Each
costs ONE contents call (raw media type), so a pass is about one call per
organisation. Matching and nomatch lines are printed with --list; match and
unread lines are always printed. Totals come last, per string too.

⛔ A refusal is never a verdict. A 403 or a 500 is unread; a reserve reached
before every organisation was read leaves the rest unread and the counts a
FLOOR, not a total. A run that read nothing never prints a clean result.

The token is read from --token-file (default ~/.github-token, owner-only) and
goes in the Authorization header only.

Exit status: 0 every organisation read, 1 some organisation unread or the run
stopped at --reserve, 2 usage error or the run could not start.`

type verdict string

const (
	vMatch   verdict = "match"
	vNoMatch verdict = "nomatch"
	vNoFile  verdict = "nofile"
	vUnread  verdict = "unread"
)

// failVerdict is what an answer other than 200 or 404 becomes. It is a
// variable only so that a test can turn it into nofile and watch the unread
// test fail.
var failVerdict = vUnread

// maxBody bounds a file read: a runner config is a few KB.
const maxBody = 4 << 20

// readToken is ghtoken.Read; tests replace it.
var readToken = ghtoken.Read

type options struct {
	repos     []string
	file      string
	contains  []string
	orgs      []string
	workers   int
	reserve   int
	list      bool
	tokenFile string
	base      string // API root; tests point it at an httptest server
}

type result struct {
	org   string
	v     verdict
	repo  string
	has   []bool // per --contains string, when the file was read
	why   string // for unread
	count int
}

// client counts nothing itself: it reads the reserve off every response.
type client struct {
	base, token string
	http        *http.Client
	reserve     int

	mu        sync.Mutex
	remaining int // -1 until a response has said
	stopped   bool
	calls     int
}

// low reports whether the reserve is reached, latching the stop.
func (c *client) low() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remaining >= 0 && c.remaining < c.reserve {
		c.stopped = true
	}
	return c.stopped
}

func (c *client) note(h http.Header) {
	n, err := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if err == nil && n >= 0 {
		c.remaining = n
	}
}

// get returns the status and body of one GET. raw asks for the file's bytes.
func (c *client) get(ctx context.Context, path string, raw bool) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return 0, nil, err
	}
	accept := "application/vnd.github+json"
	if raw {
		accept = "application/vnd.github.raw+json"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	c.note(resp.Header)
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(b) > maxBody {
		return resp.StatusCode, nil, fmt.Errorf("body over %d bytes", maxBody)
	}
	return resp.StatusCode, b, nil
}

// orgs lists every organisation of the account, page by page.
func (c *client) orgs(ctx context.Context) ([]string, error) {
	var out []string
	for page := 1; page <= 100; page++ {
		st, b, err := c.get(ctx, fmt.Sprintf("/user/orgs?per_page=100&page=%d", page), false)
		if err != nil {
			return nil, err
		}
		if st != http.StatusOK {
			return nil, fmt.Errorf("listing organisations: HTTP %d", st)
		}
		names, err := logins(b)
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

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func validPath(p string) bool {
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

func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// check reads the file in one organisation. A non-404 answer is unread and ends
// the search: the repository that failed might be the one that has the file.
func (c *client) check(ctx context.Context, org string, o options) result {
	r := result{org: org}
	for _, repo := range o.repos {
		if c.low() {
			r.v, r.why = vUnread, "not read: stopped at the reserve"
			return r
		}
		st, b, err := c.get(ctx, "/repos/"+org+"/"+repo+"/contents/"+escapePath(o.file), true)
		switch {
		case err != nil:
			r.v, r.repo, r.why = failVerdict, repo, repo+": "+err.Error()
			return r
		case st == http.StatusNotFound:
			continue
		case st != http.StatusOK:
			r.v, r.repo, r.why = failVerdict, repo, fmt.Sprintf("%s: HTTP %d", repo, st)
			return r
		}
		r.repo = repo
		r.has = make([]bool, len(o.contains))
		all := true
		for i, s := range o.contains {
			r.has[i] = strings.Contains(string(b), s)
			all = all && r.has[i]
		}
		r.v = vNoMatch
		if all {
			r.v = vMatch
		}
		return r
	}
	r.v = vNoFile
	return r
}

func main() {
	fleet.WarnIfStale(os.Stderr)
	cli.Run(RootCmd())
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var (
		o     options
		repos string
	)
	c := cli.New("orgfile [flags]",
		"In every organisation, does file F of repository R contain string S?", longHelp)
	c.Args = cobra.NoArgs
	f := c.Flags()
	f.StringVar(&repos, "repos", ".github,renovate-runner", "comma-separated repositories, tried in order; the first that has the file wins")
	f.StringVar(&o.file, "file", "", "path of the file in each repository (required)")
	f.StringArrayVar(&o.contains, "contains", nil, "literal string to look for; repeatable, reported per string (required)")
	orgs := f.String("orgs", "", "comma-separated organisations to check instead of every one")
	f.IntVar(&o.workers, "workers", 6, "concurrent readers")
	f.IntVar(&o.reserve, "reserve", 1000, "stop cleanly when REST requests remaining fall below this")
	f.BoolVar(&o.list, "list", false, "print every organisation, not only matches and unread")
	f.StringVar(&o.tokenFile, "token-file", "", "token file, owner-only (default ~/.github-token)")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		o.repos = split(repos)
		o.orgs = split(*orgs)
		o.base = "https://api.github.com"
		return cli.FromStatus(sweep(cmd.Context(), cli.Out(cmd), cli.Err(cmd), o))
	}
	return c
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sweepBase lets a test aim the command at a server: RootCmd sets the real
// host, and a non-empty override wins.
var sweepBase string

func sweep(ctx context.Context, stdout, stderr io.Writer, o options) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if sweepBase != "" {
		o.base = sweepBase
	}
	switch {
	case !validPath(o.file):
		fmt.Fprintln(stderr, "orgfile: --file is required and must be a relative path")
		return 2
	case len(o.contains) == 0:
		fmt.Fprintln(stderr, "orgfile: give at least one --contains string")
		return 2
	case len(o.repos) == 0:
		fmt.Fprintln(stderr, "orgfile: --repos names no repository")
		return 2
	case o.workers < 1:
		fmt.Fprintln(stderr, "orgfile: --workers must be at least 1")
		return 2
	}
	for _, s := range o.contains {
		if s == "" {
			fmt.Fprintln(stderr, "orgfile: an empty --contains string matches everything")
			return 2
		}
	}
	for _, n := range append(append([]string{}, o.repos...), o.orgs...) {
		if !nameRe.MatchString(n) || n == "." || n == ".." {
			fmt.Fprintf(stderr, "orgfile: %q is not a GitHub name\n", n)
			return 2
		}
	}
	tok, err := readToken(o.tokenFile)
	if err != nil {
		fmt.Fprintf(stderr, "orgfile: %v\n", err)
		return 2
	}
	c := &client{base: o.base, token: tok, http: &http.Client{Timeout: 30 * time.Second},
		reserve: o.reserve, remaining: -1}

	list := o.orgs
	if len(list) == 0 {
		if list, err = c.orgs(ctx); err != nil {
			fmt.Fprintf(stderr, "orgfile: could not list organisations: %v\n", err)
			return 2
		}
	} else {
		sort.Strings(list)
	}
	if len(list) == 0 {
		fmt.Fprintln(stderr, "orgfile: no organisation to read; refusing to print a clean result")
		return 2
	}

	res := make([]result, len(list))
	sem := make(chan struct{}, o.workers)
	var wg sync.WaitGroup
	for i, org := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res[i] = c.check(ctx, org, o)
		}()
	}
	wg.Wait()
	return report(stdout, stderr, res, o, c)
}

func report(stdout, stderr io.Writer, res []result, o options, c *client) int {
	tot := map[verdict]int{}
	per := make([]int, len(o.contains))
	notRead := 0
	for _, r := range res {
		tot[r.v]++
		if r.v == vUnread && strings.HasPrefix(r.why, "not read:") {
			notRead++
		}
		for i, h := range r.has {
			if h {
				per[i]++
			}
		}
		if r.v == vMatch || r.v == vUnread || o.list {
			fmt.Fprintln(stdout, line(r, o))
		}
	}
	read := tot[vMatch] + tot[vNoMatch] + tot[vNoFile]
	fmt.Fprintf(stdout, "totals: %d organisations, match %d, nomatch %d, nofile %d, unread %d; %d API calls\n",
		len(res), tot[vMatch], tot[vNoMatch], tot[vNoFile], tot[vUnread], c.calls)
	for i, s := range o.contains {
		fmt.Fprintf(stdout, "  contains %q: %d of %d files read\n", s, per[i], tot[vMatch]+tot[vNoMatch])
	}
	code := 0
	if c.stopped {
		fmt.Fprintf(stderr, "orgfile: stopped at the reserve (%d left, %d reserved): %d of %d organisations read, "+
			"%d not read; every count above is a FLOOR, not a total\n", c.remaining, c.reserve, read, len(res), notRead)
		code = 1
	}
	if tot[vUnread] > 0 {
		if !c.stopped {
			fmt.Fprintf(stderr, "orgfile: %d organisation(s) unread; counts exclude them\n", tot[vUnread])
		}
		code = 1
	}
	if read == 0 && code == 0 {
		code = 2 // unreachable by construction; a run that read nothing is never clean
	}
	return code
}

func line(r result, o options) string {
	s := fmt.Sprintf("%-8s %s", r.v, r.org)
	switch r.v {
	case vUnread:
		return s + "  " + r.why
	case vNoFile:
		return s
	}
	s += "/" + r.repo
	for i, str := range o.contains {
		mark := "no"
		if r.has[i] {
			mark = "yes"
		}
		s += fmt.Sprintf("  %q=%s", str, mark)
	}
	return s
}

// logins decodes the organisation names of one /user/orgs page.
func logins(b []byte) ([]string, error) {
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
