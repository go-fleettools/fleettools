package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
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

// ⛔ THE FALSE NEGATIVE THAT COST SIX RETRIES ON A CORRECT PAGE, 2026-11-01.
// The landing said "262 of this fleet's own"; Hugo printed "fleet’s" with a
// typographic apostrophe; this command reported the string MISSING and polled
// until it gave up. Every English possessive and contraction was affected, which
// is most sentences we write.
func TestTypographicPunctuationIsFoldedOnBothSides(t *testing.T) {
	// What Goldmark's typographer actually emits for 'x', "x", -- and ---.
	const doc = `<p>262 of this fleet&rsquo;s own &mdash; a &ldquo;campaign&rdquo; &ndash; see more&hellip;</p>`
	srv := httptest.NewServer(page(doc))
	defer srv.Close()

	code, out, _ := drive(t, srv.URL,
		"262 of this fleet's own", // the apostrophe, the case that bit
		`a "campaign"`,            // both quote pairs
		"own - a",                 // an em dash, written as a hyphen
		"see more...",             // an ellipsis, written as three dots
	)
	if code != 0 {
		t.Fatalf("code %d, out %q", code, out)
	}

	// The control, in the other direction: a want written WITH the typographic
	// characters must match a page that has them too, so the fold cannot be
	// passing by erasing one side only.
	if code, out, _ := drive(t, srv.URL, "262 of this fleet’s own"); code != 0 {
		t.Fatalf("the typographic spelling must match as well: code %d, out %q", code, out)
	}

	// And the negative control: folding punctuation must not make unrelated
	// words match, or the whole check is worthless.
	if code, _, _ := drive(t, srv.URL, "a stampede"); code != 1 {
		t.Fatalf("an absent string became present: code %d", code)
	}

	// --raw stays exact: it compares bytes, and the page's bytes are entities.
	if code, _, _ := drive(t, "--raw", srv.URL, "262 of this fleet's own"); code != 1 {
		t.Fatalf("--raw must not fold: code %d", code)
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

func TestAnOversizedPageIsRefusedByTheTextMatchAndSaysWhichModeToUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("a"), maxBody+10))
	}))
	defer srv.Close()
	code, out, _ := drive(t, srv.URL, "a")
	if code != 1 || !strings.HasPrefix(out, "FAIL ") {
		t.Fatalf("code %d, out %.80q", code, out)
	}
	// The refusal has to name the way out, or the next person reads it as "this
	// page cannot be checked" -- which is how it was read, for every wasm the
	// fleet publishes.
	if !strings.Contains(out, "--raw") {
		t.Fatalf("the refusal does not say which mode reads a file this size: %.200q", out)
	}
}

// ⛔ THE CHECK THAT COULD NOT CHECK AN APPLICATION, 2026-11-05. Every published
// `GOOS=js GOARCH=wasm` binary is past maxBody, so --raw -- the mode that says
// whether a deploy carried the new build -- refused exactly the files that are
// the application, and the release had to be verified by hand.
//
// The needle is planted ACROSS a window boundary, which is the case a streaming
// scan gets wrong when it forgets the carry; a needle in the middle of a window
// is found with or without it.
func TestRawReadsAPageLargerThanTheTextCapAndFindsANeedleAcrossAWindow(t *testing.T) {
	const needle = "v0.19.1-across-the-boundary"
	// Put the needle so that scanWindow falls inside it: the first window ends
	// mid-needle and the second begins mid-needle.
	at := scanWindow - len(needle)/2
	body := bytes.Repeat([]byte("a"), maxBody+scanWindow)
	copy(body[at:], needle)

	var served atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := w.Write(body)
		served.Add(int64(n))
	}))
	defer srv.Close()

	code, out, _ := drive(t, "--raw", srv.URL, needle)
	if code != 0 || !strings.Contains(out, "true \""+needle+"\"") {
		t.Fatalf("a %d-byte body hid a needle at offset %d: code %d, out %.200q",
			len(body), at, code, out)
	}

	// The negative control, on the same oversized body: a string that is NOT
	// there must come back ABSENT (exit 1), never REFUSED (exit 1 with FAIL) --
	// and it is the absent case that reads the whole body, so this is also the
	// reading that proves the cap no longer applies.
	code, out, _ = drive(t, "--raw", srv.URL, "v0.19.2")
	if code != 1 || strings.Contains(out, "FAIL") || !strings.Contains(out, "false ") {
		t.Fatalf("an absent string in a streamed body: code %d, out %.200q", code, out)
	}
	if n := served.Load(); n <= maxBody {
		t.Fatalf("only %d bytes were read past the %d-byte cap; the test is not exercising it",
			n, maxBody)
	}
}

// A needle longer than the window has to work too: the window grows to hold it.
func TestRawFindsANeedleLongerThanTheWindow(t *testing.T) {
	needle := strings.Repeat("abcdefgh", scanWindow/4) // 2x scanWindow
	body := append(bytes.Repeat([]byte("."), scanWindow+3), needle...)
	body = append(body, bytes.Repeat([]byte("."), scanWindow)...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()
	if code, out, _ := drive(t, "--raw", srv.URL, needle); code != 0 {
		t.Fatalf("code %d, out %.120q", code, out)
	}
}

func TestScanForIsBoundedByTheWindowNotTheBody(t *testing.T) {
	// 64 MiB of body, read through a reader that refuses to hand out more than
	// one window at a time: if scanFor buffered the body, this would allocate it.
	const total = 64 << 20
	before := heapInUse()
	found, err := scanFor(io.LimitReader(constantReader{'z'}, total), []string{"zz", "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if !found[0] || found[1] {
		t.Fatalf("found %v", found)
	}
	if grew := heapInUse() - before; grew > total/8 {
		t.Fatalf("the heap grew by %d bytes reading a %d-byte body", grew, total)
	}
}

// FuzzScanForAgreesWithContains holds the streaming scan to the answer the
// whole-body version gives. bytes.Contains over the complete body is the oracle:
// it is the behaviour --raw had before it streamed, and the one users read the
// output as.
//
// The window is shrunk to a few bytes, so every input of any length straddles
// boundaries -- at 64 KiB a fuzzer would spend the whole run inside one window
// and never test the carry at all.
func FuzzScanForAgreesWithContains(f *testing.F) {
	f.Add([]byte("abcdef"), "cd")
	f.Add([]byte("aaaaaaaaaaaa"), "aaa")
	f.Add([]byte(""), "")
	f.Add([]byte("x"), "xx")
	f.Add([]byte("\x00\xff\x00\xff"), "\xff\x00")

	f.Fuzz(func(t *testing.T, body []byte, want string) {
		for _, win := range []int{1, 2, 3, 8, 64} {
			old := scanWindow
			scanWindow = win
			got, err := scanFor(bytes.NewReader(body), []string{want, "unrelated-needle"})
			scanWindow = old
			if err != nil {
				t.Fatalf("window %d: %v", win, err)
			}
			if oracle := bytes.Contains(body, []byte(want)); got[0] != oracle {
				t.Fatalf("window %d: scanFor said %v, bytes.Contains says %v for want %q in %q",
					win, got[0], oracle, want, body)
			}
			if got[1] != bytes.Contains(body, []byte("unrelated-needle")) {
				t.Fatalf("window %d: the second want disagreed", win)
			}
		}
	})
}

type constantReader struct{ b byte }

func (c constantReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = c.b
	}
	return len(p), nil
}

func heapInUse() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapInuse)
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"not-a-url", "x"}, {"-retry", "-1", "http://x"}, {"-nope"}} {
		if code, _, _ := drive(t, args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}
