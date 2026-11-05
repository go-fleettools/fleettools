package main

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Class says what a check found. DEAD and FLAKY are both failures, and are
// printed apart on purpose: a dead link is the page's defect, a flaky host is
// the network's, and a lane that cannot tell them apart gets re-run until it
// goes green and then ignored.
type Class int

const (
	OK    Class = iota
	Dead        // the host answered, and the answer is "not here" (404, 410, 4xx)
	Flaky       // the host did not answer usefully: 429, 5xx, timeout, rate limit, deadline
)

func (c Class) String() string {
	switch c {
	case OK:
		return "ok"
	case Dead:
		return "DEAD"
	}
	return "FLAKY"
}

// Result is what checking one URL found.
type Result struct {
	URL      string
	Class    Class
	Code     int    // last HTTP status, 0 when none arrived
	Via      string // "api" or "html"
	Detail   string // why, for a failure
	Attempts int
}

// Checker fetches URLs with retries. Token, when set, is sent to APIBase and
// to nothing else.
type Checker struct {
	Client     *http.Client
	APIBase    string // https://api.github.com
	Token      string
	Attempts   int           // per probe, at least 1
	Backoff    time.Duration // first wait; doubles
	MaxBackoff time.Duration // ceiling for any wait, Retry-After included
	Sleep      func(ctx context.Context, d time.Duration) error
}

const userAgent = "linkcheck (github.com/go-fleettools/fleettools)"

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// classify maps one response to a class. 403 is Flaky only when it is a rate
// limit (GitHub says so with x-ratelimit-remaining: 0 or Retry-After).
func classify(code int, h http.Header) Class {
	switch {
	case code >= 200 && code < 400:
		return OK
	case code == 429 || code == 408 || code >= 500:
		return Flaky
	case code == 403 && (h.Get("Retry-After") != "" || h.Get("X-Ratelimit-Remaining") == "0"):
		return Flaky
	}
	return Dead
}

// retryAfter reads Retry-After (seconds or an HTTP date), or, for a GitHub
// rate limit, x-ratelimit-reset. Zero when the response names no wait.
func retryAfter(h http.Header, now time.Time) time.Duration {
	if v := h.Get("Retry-After"); v != "" {
		if s, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && s >= 0 {
			return time.Duration(s) * time.Second
		}
		if t, err := http.ParseTime(v); err == nil {
			if d := t.Sub(now); d > 0 {
				return d
			}
		}
		return 0
	}
	if h.Get("X-Ratelimit-Remaining") == "0" {
		if s, err := strconv.ParseInt(h.Get("X-Ratelimit-Reset"), 10, 64); err == nil {
			if d := time.Unix(s, 0).Sub(now); d > 0 {
				return d
			}
		}
	}
	return 0
}

// probe fetches one URL, retrying transient failures. api=true means the
// request goes to the GitHub REST API: it carries the token and the API
// headers.
func (c *Checker) probe(ctx context.Context, target string, api bool, accept string) (cls Class, code int, detail string, attempts int) {
	n := max(c.Attempts, 1)
	for i := 0; i < n; i++ {
		attempts = i + 1
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return Dead, 0, "bad URL", attempts
		}
		req.Header.Set("User-Agent", userAgent)
		if api {
			req.Header.Set("Accept", accept)
			req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
			if c.Token != "" {
				req.Header.Set("Authorization", "Bearer "+c.Token)
			}
		}
		var wait time.Duration
		resp, err := c.Client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return Flaky, 0, "run deadline reached", attempts
			}
			cls, code, detail = Flaky, 0, "no answer: "+shortErr(err)
		} else {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			cls, code, detail = classify(resp.StatusCode, resp.Header), resp.StatusCode, "HTTP "+strconv.Itoa(resp.StatusCode)
			if cls == OK {
				return OK, code, "", attempts
			}
			wait = retryAfter(resp.Header, time.Now())
		}
		if cls != Flaky || i == n-1 {
			return cls, code, detail, attempts
		}
		exp := c.Backoff << i
		exp += time.Duration(rand.Int64N(int64(exp)/2 + 1)) // jitter: a stampede retries together otherwise
		wait = max(wait, exp)
		if c.MaxBackoff > 0 {
			wait = min(wait, c.MaxBackoff)
		}
		slp := c.Sleep
		if slp == nil {
			slp = sleep
		}
		if err := slp(ctx, wait); err != nil {
			return Flaky, code, "run deadline reached", attempts
		}
	}
	return
}

// shortErr drops the URL from a transport error (it is printed next to it) and
// can never carry a header.
func shortErr(err error) string {
	if ue, ok := err.(*url.Error); ok {
		err = ue.Err
	}
	return err.Error()
}

// Check decides one URL.
func (c *Checker) Check(ctx context.Context, raw string) Result {
	r := Result{URL: raw, Via: "html"}
	if probes := githubProbes(raw, c.APIBase); len(probes) > 0 {
		r.Via = "api"
		// Dead only when every probe is dead: a ref containing a slash has
		// several ways to be split into ref and path.
		var worst Result
		for i, p := range probes {
			cls, code, detail, att := c.probe(ctx, p.url, true, p.accept)
			r.Attempts += att
			if cls == OK {
				r.Class, r.Code, r.Detail = OK, code, ""
				return r
			}
			if i == 0 || cls == Flaky {
				worst = Result{Class: cls, Code: code, Detail: detail}
			}
		}
		r.Class, r.Code, r.Detail = worst.Class, worst.Code, worst.Detail
		return r
	}
	r.Class, r.Code, r.Detail, r.Attempts = c.probe(ctx, raw, false, "")
	return r
}

// Run checks urls with at most concurrency requests in flight. Results come
// back in the order of urls. A URL not reached before ctx ends is Flaky
// ("not checked"), never skipped.
func (c *Checker) Run(ctx context.Context, urls []string, concurrency int) []Result {
	out := make([]Result, len(urls))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < max(concurrency, 1); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					out[i] = Result{URL: urls[i], Class: Flaky, Detail: "not checked: run deadline reached"}
					continue
				}
				out[i] = c.Check(ctx, urls[i])
			}
		}()
	}
	for i := range urls {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return out
}

type apiProbe struct{ url, accept string }

const (
	acceptJSON = "application/vnd.github+json"
	acceptSHA  = "application/vnd.github.sha"
)

// githubProbes turns a github.com URL that names a repository, or something in
// one, into the REST API requests that decide whether it exists: the HTML
// page answers 504 and 429 to a runner under load, the API has a budget and a
// status. Nil means "not one of those": fetch the URL itself.
func githubProbes(raw, api string) []apiProbe {
	u, err := url.Parse(raw)
	if err != nil || (u.Host != "github.com" && u.Host != "www.github.com") {
		return nil
	}
	var segs []string
	for _, s := range strings.Split(strings.Trim(u.EscapedPath(), "/"), "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	if len(segs) < 2 || githubReserved[segs[0]] {
		return nil
	}
	repo := api + "/repos/" + segs[0] + "/" + strings.TrimSuffix(segs[1], ".git")
	rest := segs[2:]
	switch {
	case len(rest) == 0:
		return []apiProbe{{repo, acceptJSON}}
	case (rest[0] == "blob" || rest[0] == "tree") && len(rest) >= 2:
		refPath := rest[1:]
		var ps []apiProbe
		for k := 1; k <= len(refPath); k++ {
			ref, _ := url.PathUnescape(strings.Join(refPath[:k], "/"))
			if k == len(refPath) {
				ps = append(ps, apiProbe{repo + "/commits/" + url.PathEscape(ref), acceptSHA})
				continue
			}
			ps = append(ps, apiProbe{repo + "/contents/" + strings.Join(refPath[k:], "/") + "?ref=" + url.QueryEscape(ref), acceptJSON})
		}
		return ps
	case rest[0] == "releases" && len(rest) == 1:
		return []apiProbe{{repo + "/releases?per_page=1", acceptJSON}}
	case rest[0] == "releases" && len(rest) == 2 && rest[1] == "latest":
		return []apiProbe{{repo + "/releases/latest", acceptJSON}}
	case rest[0] == "releases" && len(rest) == 3 && rest[1] == "tag":
		return []apiProbe{{repo + "/releases/tags/" + rest[2], acceptJSON}}
	}
	return nil
}

var githubReserved = map[string]bool{
	"orgs": true, "topics": true, "sponsors": true, "marketplace": true, "features": true,
	"about": true, "pricing": true, "settings": true, "apps": true, "users": true,
	"collections": true, "enterprise": true, "login": true, "search": true, "explore": true,
}

// Report prints one line per URL (failures after the successes would hide
// them; the order is the URL order) and the summary, and returns how many are
// dead and how many flaky.
func Report(w io.Writer, rs []Result) (dead, flaky int) {
	for _, r := range rs {
		switch r.Class {
		case OK:
			fmt.Fprintf(w, "  ok    %d  %s  (%s)\n", r.Code, r.URL, r.Via)
		case Dead:
			dead++
			fmt.Fprintf(w, "  DEAD  %d  %s  (%s)\n", r.Code, r.URL, r.Via)
		default:
			flaky++
			code := "---"
			if r.Code != 0 {
				code = strconv.Itoa(r.Code)
			}
			fmt.Fprintf(w, "  FLAKY %s  %s  (%s, %d attempts: %s)\n", code, r.URL, r.Via, r.Attempts, r.Detail)
		}
	}
	return dead, flaky
}
