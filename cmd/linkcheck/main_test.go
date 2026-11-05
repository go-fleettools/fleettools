package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-fleettools/fleettools/internal/cli"
)

func TestExtractKeepsTheOldExclusions(t *testing.T) {
	text := "[a](https://github.com/o/r/blob/main/x.md). <https://ok.dev/p>, `https://trail.dev/q`.\n" +
		"https://img.shields.io/badge/x https://example.com/a http://localhost:8080/ http://127.0.0.1/ " +
		"http://web/ping https://x.invalid/ https://me.github.io/docs/page https://github.com/me/docs/blob/main/a " +
		"\"https://quoted.dev/z\" https://ok.dev/p"
	got := Extract([]byte(text), []string{"me.github.io/docs", "github.com/me/docs/"})
	want := []string{"https://github.com/o/r/blob/main/x.md", "https://ok.dev/p", "https://quoted.dev/z", "https://trail.dev/q"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEmptyHostIsSkippedAndCounted(t *testing.T) {
	// A scheme in backticks reaches the checker bare: the extractor keeps the
	// closing backtick and the trailing-punctuation trim then removes it.
	text := "`https://`, `http://` and `https://<host>/...`\n" +
		"http://`-Aussteller https://`、`http://`。 https://$HOST/x https://$HOST/x\n" +
		"real https://real.dev/ok\n"
	urls, sk := Scan([]byte(text), nil)
	if strings.Join(urls, " ") != "https://real.dev/ok" {
		t.Fatalf("checked %q", urls)
	}
	if len(sk) == 0 {
		t.Fatal("nothing reported as skipped")
	}
	for _, s := range sk {
		if s.Reason != reasonNoHost {
			t.Errorf("%q skipped as %q, want %q", s.URL, s.Reason, reasonNoHost)
		}
	}
	for _, want := range []string{"http://", "https://", "http://`-Aussteller", "https://$HOST/x"} {
		found := false
		for _, s := range sk {
			found = found || s.URL == want
		}
		if !found {
			t.Errorf("%q not among the skipped %v", want, sk)
		}
	}
}

func TestPlaceholderHostsAreSkippedAndARealHostIsNot(t *testing.T) {
	skippedHosts := []string{
		"https://idp.univ-example.example/idp", "https://a.b.example", "https://example.com/x",
		"https://www.example.org/", "https://sub.example.net:8443/p", "https://x.invalid/",
		"https://host.test/", "http://mirror.cluster.local:8080/univ-b-ssh", "http://localhost:3000/",
		"http://127.0.0.1:8080/", "http://[::1]:8080/", "https://EXAMPLE.COM./",
	}
	realHosts := []string{
		"https://notexample.com/", "https://example.com.evil.dev/", "https://real.dev/ok",
		"https://localhost.dev/", "https://contest.dev/", "https://github.com/o/r",
	}
	text := strings.Join(skippedHosts, " ") + " " + strings.Join(realHosts, " ")
	urls, sk := Scan([]byte(text), nil)
	if strings.Join(urls, " ") != strings.Join(sortedCopy(realHosts), " ") {
		t.Fatalf("checked %q, want %q", urls, realHosts)
	}
	if len(sk) != len(skippedHosts) {
		t.Fatalf("%d skipped, want %d: %v", len(sk), len(skippedHosts), sk)
	}
	for _, s := range sk {
		if s.Reason != reasonPlaceholder {
			t.Errorf("%q skipped as %q", s.URL, s.Reason)
		}
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestSkippedURLsArePrintedAndCounted(t *testing.T) {
	md := "https://github.com/o/fine https://idp.x.example/a `http://`\n" +
		"https://img.shields.io/b https://me.github.io/docs/p\n"
	code, out, errOut := drive(t, "http://unused", md, "--skip", "me.github.io/docs")
	// The one real URL is a github.com repo decided by the (here absent)
	// API; what matters is the report of the skipped ones.
	_ = code
	for _, want := range []string{
		"skip placeholder host", "https://idp.x.example/a",
		"skip no host", "skipped 4 distinct URLs: 1 no host, 1 placeholder host, 1 built-in, 1 --skip",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s%s", want, out, errOut)
		}
	}
	if strings.Contains(out, "me.github.io") {
		t.Errorf("a --skip match is listed:\n%s", out)
	}
}

func TestOnlySkippableURLsIsNotACleanResult(t *testing.T) {
	code, _, errOut := drive(t, "http://x", "only https://a.example/ and `http://`\n")
	if code != 2 || !strings.Contains(errOut, "no links found") {
		t.Fatalf("code %d %s", code, errOut)
	}
}

func TestGithubProbes(t *testing.T) {
	const api = "http://api"
	for _, tc := range []struct {
		url  string
		want []string // nil: not an API URL
	}{
		{"https://github.com/o/r", []string{api + "/repos/o/r"}},
		{"https://github.com/o/r#readme", []string{api + "/repos/o/r"}},
		{"https://github.com/o/r/blob/main/docs/a.md#x", []string{
			api + "/repos/o/r/contents/docs/a.md?ref=main",
			api + "/repos/o/r/contents/a.md?ref=main%2Fdocs",
			api + "/repos/o/r/commits/main%2Fdocs%2Fa.md"}},
		{"https://github.com/o/r/tree/v1.0.0", []string{api + "/repos/o/r/commits/v1.0.0"}},
		{"https://github.com/o/r/releases", []string{api + "/repos/o/r/releases?per_page=1"}},
		{"https://github.com/o/r/releases/tag/v1", []string{api + "/repos/o/r/releases/tags/v1"}},
		{"https://github.com/o/r/releases/latest", []string{api + "/repos/o/r/releases/latest"}},
		{"https://github.com/o/r/releases/download/v1/a.tgz", nil},
		{"https://github.com/o", nil},
		{"https://github.com/orgs/o", nil},
		{"https://example.org/o/r", nil},
	} {
		var got []string
		for _, p := range githubProbes(tc.url, api) {
			got = append(got, p.url)
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s: got %q, want %q", tc.url, got, tc.want)
		}
	}
}

func recorder() (*[]time.Duration, func(context.Context, time.Duration) error) {
	var mu sync.Mutex
	var waits []time.Duration
	return &waits, func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		waits = append(waits, d)
		return nil
	}
}

func TestRetryAfterAndBackoffAreHonoured(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
		case 2:
			w.WriteHeader(504)
		default:
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()
	waits, sl := recorder()
	c := &Checker{Client: srv.Client(), Attempts: 4, Backoff: 2 * time.Second, MaxBackoff: time.Minute, Sleep: sl}
	r := c.Check(context.Background(), srv.URL)
	if r.Class != OK || r.Attempts != 3 {
		t.Fatalf("got %+v", r)
	}
	w := *waits
	if len(w) != 2 || w[0] != 7*time.Second || w[1] < 4*time.Second || w[1] > 6*time.Second+time.Millisecond {
		t.Fatalf("waits %v: want Retry-After 7s, then 4s plus at most 50%% jitter", w)
	}
}

func TestRetryAfterIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	waits, sl := recorder()
	c := &Checker{Client: srv.Client(), Attempts: 2, Backoff: time.Second, MaxBackoff: 10 * time.Second, Sleep: sl}
	if r := c.Check(context.Background(), srv.URL); r.Class != Flaky || r.Code != 429 {
		t.Fatalf("got %+v", r)
	}
	if w := *waits; len(w) != 1 || w[0] != 10*time.Second {
		t.Fatalf("waits %v", w)
	}
}

func TestFinalTransientFailureIsAFailureAndNotADeadLink(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(504)
	}))
	defer srv.Close()
	_, sl := recorder()
	c := &Checker{Client: srv.Client(), Attempts: 3, Backoff: time.Millisecond, Sleep: sl}
	r := c.Check(context.Background(), srv.URL)
	if r.Class != Flaky || r.Code != 504 || r.Attempts != 3 || n.Load() != 3 {
		t.Fatalf("got %+v after %d requests", r, n.Load())
	}
	var buf bytes.Buffer
	dead, flaky := Report(&buf, []Result{r, {URL: "u", Class: Dead, Code: 404, Via: "html"}})
	if dead != 1 || flaky != 1 || !strings.Contains(buf.String(), "FLAKY 504") || !strings.Contains(buf.String(), "DEAD  404") {
		t.Fatalf("dead=%d flaky=%d\n%s", dead, flaky, buf.String())
	}
}

func TestDeadLinkIsNotRetried(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(404)
	}))
	defer srv.Close()
	c := &Checker{Client: srv.Client(), Attempts: 4, Backoff: time.Hour}
	if r := c.Check(context.Background(), srv.URL); r.Class != Dead || r.Attempts != 1 || n.Load() != 1 {
		t.Fatalf("got %+v after %d requests", r, n.Load())
	}
}

func TestRateLimitedForbiddenIsFlaky(t *testing.T) {
	if classify(403, http.Header{"X-Ratelimit-Remaining": {"0"}}) != Flaky || classify(403, http.Header{}) != Dead {
		t.Fatal("403 is flaky only when it is a rate limit")
	}
}

func TestRunDeadlineLeavesNothingUnchecked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &Checker{Client: http.DefaultClient, Attempts: 1}
	rs := c.Run(ctx, []string{"https://a.test/", "https://b.test/"}, 2)
	for _, r := range rs {
		if r.Class != Flaky || !strings.Contains(r.Detail, "not checked") {
			t.Fatalf("got %+v", r)
		}
	}
}

func TestConcurrencyIsBounded(t *testing.T) {
	var cur, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := cur.Add(1)
		for p := peak.Load(); v > p && !peak.CompareAndSwap(p, v); p = peak.Load() {
		}
		time.Sleep(10 * time.Millisecond)
		cur.Add(-1)
	}))
	defer srv.Close()
	var urls []string
	for i := 0; i < 20; i++ {
		urls = append(urls, srv.URL+"/"+string(rune('a'+i)))
	}
	c := &Checker{Client: srv.Client(), Attempts: 1}
	c.Run(context.Background(), urls, 3)
	if p := peak.Load(); p > 3 {
		t.Fatalf("peak %d > 3", p)
	}
}

func TestTokenGoesToTheAPIOnly(t *testing.T) {
	var apiAuth, htmlAuth atomic.Value
	apiAuth.Store("")
	htmlAuth.Store("none")
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		htmlAuth.Store(r.Header.Get("Authorization"))
	}))
	defer html.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiAuth.Store(r.Header.Get("Authorization"))
	}))
	defer api.Close()
	c := &Checker{Client: api.Client(), APIBase: api.URL, Token: "sekret", Attempts: 1}
	c.Check(context.Background(), "https://github.com/o/r")
	c.Check(context.Background(), html.URL)
	if apiAuth.Load() != "Bearer sekret" || htmlAuth.Load() != "" {
		t.Fatalf("api %q html %q", apiAuth.Load(), htmlAuth.Load())
	}
}

// drive runs the real command, reading a Markdown file that names github.com
// URLs, with the API replaced by a test server.
func drive(t *testing.T, api string, md string, extra ...string) (code int, out, errOut string) {
	t.Helper()
	dir := t.TempDir()
	page := filepath.Join(dir, "page.md")
	if err := os.WriteFile(page, []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"--api-url", api, "--attempts", "2", "--backoff", "1ms"}, extra...)
	args = append(args, page)
	var o, e bytes.Buffer
	code = cli.Drive(RootCmd(), args, &o, &e)
	return code, o.String(), e.String()
}

func TestTokenNeverAppearsInOutputAndExitCodesDistinguishDeadFromFlaky(t *testing.T) {
	const secret = "ghs_SECRETSECRETSECRET"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/repos/o/gone":
			w.WriteHeader(404)
		case "/repos/o/flaky":
			w.WriteHeader(504)
		}
	}))
	defer api.Close()
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := drive(t, api.URL, "https://github.com/o/fine\n", "--token-file", tok)
	if code != 0 {
		t.Fatalf("code %d\n%s%s", code, out, errOut)
	}
	code, out2, err2 := drive(t, api.URL, "https://github.com/o/fine https://github.com/o/flaky\n", "--token-file", tok)
	if code != 2 || !strings.Contains(out2, "FLAKY 504") || strings.Contains(out2, "  DEAD ") {
		t.Fatalf("flaky only: code %d\n%s%s", code, out2, err2)
	}
	code, out3, err3 := drive(t, api.URL, "https://github.com/o/gone https://github.com/o/flaky\n", "--token-file", tok)
	if code != 1 || !strings.Contains(out3, "DEAD  404") || !strings.Contains(out3, "FLAKY 504") {
		t.Fatalf("dead and flaky: code %d\n%s%s", code, out3, err3)
	}
	// A wrong token is not printed either: the 401 is a DEAD answer.
	badTok := filepath.Join(t.TempDir(), "bad")
	os.WriteFile(badTok, []byte("ghs_WRONGTOKEN"), 0o600)
	code, out4, err4 := drive(t, api.URL, "https://github.com/o/fine\n", "--token-file", badTok)
	if code != 1 {
		t.Fatalf("code %d\n%s%s", code, out4, err4)
	}
	for _, s := range []string{out, errOut, out2, err2, out3, err3, out4, err4} {
		if strings.Contains(s, secret) || strings.Contains(s, "WRONGTOKEN") {
			t.Fatalf("a token reached the output:\n%s", s)
		}
	}
}

func TestSetupFailuresAreNotCleanResults(t *testing.T) {
	var o, e bytes.Buffer
	if c := cli.Drive(RootCmd(), nil, &o, &e); c != 2 {
		t.Errorf("no path: %d", c)
	}
	if c := cli.Drive(RootCmd(), []string{"--token-file", filepath.Join(t.TempDir(), "none"), "."}, &o, &e); c != 2 {
		t.Errorf("missing token file: %d", c)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	os.WriteFile(empty, nil, 0o600)
	if c := cli.Drive(RootCmd(), []string{"--token-file", empty, "."}, &o, &e); c != 2 {
		t.Errorf("empty token file: %d", c)
	}
	if code, _, errOut := drive(t, "http://x", "no links here\n"); code != 2 || !strings.Contains(errOut, "no links found") {
		t.Errorf("no links: %d %s", code, errOut)
	}
}

func TestHelpIsZeroAndATypoIsTwo(t *testing.T) {
	var o, e bytes.Buffer
	if c := cli.Drive(RootCmd(), []string{"-h"}, &o, &e); c != 0 {
		t.Errorf("-h: %d", c)
	}
	if c := cli.Drive(RootCmd(), []string{"--no-such-flag"}, &o, &e); c != 2 {
		t.Errorf("typo: %d", c)
	}
}
