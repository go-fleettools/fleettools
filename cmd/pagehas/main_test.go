package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func runArgs(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	c := RootCmd()
	var out, errb bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errb)
	c.SetArgs(args)
	return execute(c), out.String(), errb.String()
}

func TestEveryTextPresentIsExitZero(t *testing.T) {
	s := serve(t, 200, "<p>Fuzzed since v0.13.8</p><p>no pixel limit</p>")
	code, out, _ := runArgs(t, s.URL, "Fuzzed since v0.13.8", "no pixel limit")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if strings.Count(out, "present") != 2 {
		t.Fatalf("each text must be reported:\n%s", out)
	}
}

func TestAMissingTextIsExitOneAndNamed(t *testing.T) {
	s := serve(t, 200, "<p>Fuzzed since v0.13.8</p>")
	code, out, _ := runArgs(t, s.URL, "Fuzzed since v0.13.8", "v0.13.9")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, `MISSING  "v0.13.9"`) {
		t.Fatalf("the missing text must be named:\n%s", out)
	}
}

// A page that could not be read must never be reported as missing the text:
// that is the go-simd 404 of 2026-10-18 read the wrong way round.
func TestA404IsUnreadNotMissing(t *testing.T) {
	s := serve(t, 404, "Fuzzed since v0.13.8") // the text is even in the body
	code, out, errb := runArgs(t, s.URL, "Fuzzed since v0.13.8")
	if code != 2 {
		t.Fatalf("exit %d, want 2\n%s%s", code, out, errb)
	}
	if strings.Contains(out, "present") || strings.Contains(out, "MISSING") {
		t.Fatalf("no verdict on text may be given for a page that answered 404:\n%s", out)
	}
}

func TestAnUnreachableHostIsUnread(t *testing.T) {
	s := serve(t, 200, "x")
	s.Close() // nothing listens any more
	if code, _, _ := runArgs(t, s.URL, "x"); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

// A body cut off at the limit is not searched in part.
func TestABodyOverTheLimitIsUnread(t *testing.T) {
	s := serve(t, 200, strings.Repeat("a", maxBody+10)+"needle")
	code, out, errb := runArgs(t, s.URL, "needle")
	if code != 2 || !strings.Contains(errb, "larger than") {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
}

func TestFreshAddsAQueryTheCacheHasNotSeen(t *testing.T) {
	var seen string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.RawQuery
		w.Write([]byte("text"))
	}))
	defer s.Close()
	code, out, _ := runArgs(t, "--fresh", s.URL+"/page/?keep=1", "text")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(seen, "pagehas=") || !strings.Contains(seen, "keep=1") {
		t.Fatalf("query sent: %q (must add pagehas= and keep the existing one)", seen)
	}
	if !strings.Contains(out, "from the origin") {
		t.Fatalf("the report must say the origin was asked:\n%s", out)
	}
	a, _ := withFreshQuery("https://x.test/", time.Unix(0, 1))
	b, _ := withFreshQuery("https://x.test/", time.Unix(0, 2))
	if a == b {
		t.Fatalf("two different instants gave the same query: %s", a)
	}
}

func TestUsageErrorsAreExitTwo(t *testing.T) {
	for _, args := range [][]string{
		{"https://x.test/"},                         // no text
		{"--timeout", "0s", "https://x.test/", "t"}, // non-positive timeout
		{"--fresh", "not a url", "t"},               // relative URL with --fresh
	} {
		if code, _, _ := runArgs(t, args...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
}
