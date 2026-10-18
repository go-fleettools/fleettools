package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// drive runs the command and returns its exit code and both outputs.
func drive(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func page(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }
}

func TestTextPresent(t *testing.T) {
	srv := httptest.NewServer(page(`<html><body><p>jpeg-quality is here</p></body></html>`))
	defer srv.Close()
	code, out, _ := drive(t, srv.URL, "jpeg-quality")
	if code != 0 || !strings.Contains(out, "true \"jpeg-quality\"") || !strings.Contains(out, "200 ") {
		t.Fatalf("code %d, out %q", code, out)
	}
}

func TestTextAbsentIsExit1(t *testing.T) {
	srv := httptest.NewServer(page(`<p>nothing</p>`))
	defer srv.Close()
	code, out, _ := drive(t, srv.URL, "jpeg-quality")
	if code != 1 || !strings.Contains(out, "false \"jpeg-quality\"") {
		t.Fatalf("code %d, out %q", code, out)
	}
}

func TestEntitySplitTextMatchesInTextModeButNotRaw(t *testing.T) {
	const doc = `<p>use <code>--</code><span>output</span> &amp; more&nbsp;text<script>var hidden = "secret"</script></p>`
	srv := httptest.NewServer(page(doc))
	defer srv.Close()
	if code, out, _ := drive(t, srv.URL, "--output & more text"); code != 0 {
		t.Fatalf("text mode missed it: %q", out)
	}
	if code, _, _ := drive(t, "-raw", srv.URL, "--output"); code != 1 {
		t.Fatal("raw mode found text that the tags split")
	}
	if code, _, _ := drive(t, srv.URL, "secret"); code != 1 {
		t.Fatal("script content counted as page text")
	}
	if code, _, _ := drive(t, "-raw", srv.URL, "&amp;"); code != 0 {
		t.Fatal("raw mode did not see the raw entity")
	}
}

func TestBlockBoundariesDoNotJoinWords(t *testing.T) {
	srv := httptest.NewServer(page(`<li>alpha</li><li>beta</li>`))
	defer srv.Close()
	if code, _, _ := drive(t, srv.URL, "alphabeta"); code != 1 {
		t.Fatal("two list items were joined into one word")
	}
	if code, _, _ := drive(t, srv.URL, "alpha beta"); code != 0 {
		t.Fatal("two list items were not separated")
	}
}

func TestNon200IsAFailureEvenWithTheTextPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "jpeg-quality")
	}))
	defer srv.Close()
	code, out, _ := drive(t, srv.URL, "jpeg-quality")
	if code != 1 || !strings.HasPrefix(out, "404 ") {
		t.Fatalf("code %d, out %q", code, out)
	}
}

func TestARedirectIsFollowedAndReported(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/new", page(`<p>moved text</p>`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	code, out, _ := drive(t, srv.URL+"/old", "moved text")
	if code != 0 || !strings.Contains(out, "200 "+srv.URL+"/old -> "+srv.URL+"/new\n") {
		t.Fatalf("code %d, out %q", code, out)
	}
}

func TestCacheBusterIsAddedByDefaultAndKeepsTheQuery(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		fmt.Fprint(w, "x")
	}))
	defer srv.Close()
	drive(t, srv.URL+"/?a=1", "x")
	if !strings.Contains(got, "a=1") || !strings.Contains(got, "nocache=") {
		t.Fatalf("query %q", got)
	}
	drive(t, "-no-cache-bust", srv.URL+"/?a=1", "x")
	if got != "a=1" {
		t.Fatalf("with -no-cache-bust the query was %q", got)
	}
}

func TestTheBusterDoesNotLeakIntoTheReportedURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new?"+r.URL.RawQuery, http.StatusFound)
	})
	mux.HandleFunc("/new", page("x"))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	_, out, _ := drive(t, srv.URL+"/old", "x")
	if strings.Contains(out, "nocache") {
		t.Fatalf("buster shown: %q", out)
	}
}

func TestRetryUntilTheDeployIsVisible(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			fmt.Fprint(w, "old build")
			return
		}
		fmt.Fprint(w, "new build jpeg-quality")
	}))
	defer srv.Close()
	var slept []time.Duration
	old := sleep
	sleep = func(d time.Duration) { slept = append(slept, d) }
	defer func() { sleep = old }()

	code, out, _ := drive(t, "-retry", "5", "-wait", "7s", srv.URL, "jpeg-quality")
	if code != 0 || hits.Load() != 3 || len(slept) != 2 || slept[0] != 7*time.Second {
		t.Fatalf("code %d hits %d slept %v out %q", code, hits.Load(), slept, out)
	}
}

func TestRetryIsBounded(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "old build")
	}))
	defer srv.Close()
	old := sleep
	sleep = func(time.Duration) {}
	defer func() { sleep = old }()
	code, _, _ := drive(t, "-retry", "2", srv.URL, "jpeg-quality")
	if code != 1 || hits.Load() != 3 {
		t.Fatalf("code %d hits %d: want 1 and 3 (the first try plus two retries)", code, hits.Load())
	}
}

func TestGroupsAreIndependent(t *testing.T) {
	a := httptest.NewServer(page("alpha"))
	defer a.Close()
	b := httptest.NewServer(page("beta"))
	defer b.Close()
	code, out, _ := drive(t, a.URL, "alpha", "--", b.URL, "alpha")
	if code != 1 || strings.Count(out, "true") != 1 || strings.Count(out, "false") != 1 {
		t.Fatalf("code %d, out %q", code, out)
	}
}

func TestAnOversizedPageIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("a"), maxBody+10))
	}))
	defer srv.Close()
	code, out, _ := drive(t, srv.URL, "a")
	if code != 1 || !strings.HasPrefix(out, "FAIL ") {
		t.Fatalf("code %d, out %.80q", code, out)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"not-a-url", "x"}, {"-retry", "-1", "http://x"}, {"-nope"}} {
		if code, _, _ := drive(t, args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}
