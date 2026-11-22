package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ⛔ A FETCH THAT NEVER HAPPENED MUST NOT READ LIKE A PAGE THAT LACKS THE TEXT.
// Both print a line and exit 1, and the whole purpose of this command is to
// answer "is the deploy out?" -- so the one answer it must never give is a
// confident "the text is not there" when it never reached the server. The four
// error paths below were unheld by anything on 2026-11-22 (mutation sweep:
// main.go:296, :303, :310 survived), which means deleting any of them would
// have turned a transport failure into a verdict about the page's contents.
func TestATransportErrorIsReportedAsAFailureNotAsMissingText(t *testing.T) {
	srv := httptest.NewServer(page("whatever"))
	url := srv.URL
	srv.Close() // nothing is listening on that port any more

	old := sleep
	sleep = func(time.Duration) {}
	defer func() { sleep = old }()

	code, out, errOut := drive(t, "-retry", "1", url, "jpeg-quality")
	if code != 1 {
		t.Fatalf("code %d, want 1", code)
	}
	if !strings.HasPrefix(out, "FAIL ") {
		t.Fatalf("a transport error must be reported as FAIL, not as a verdict; out %q", out)
	}
	if strings.Contains(out, "false \"jpeg-quality\"") {
		t.Fatalf("it answered a question it could not ask; out %q", out)
	}
	// ⛔ BOTH SIDES OF why()'s FIRST GUARD: here the waiting line must carry the
	// transport error itself. The other side -- a reachable page that is simply
	// missing the text -- is asserted in case_test.go, where the same line must
	// carry a count instead.
	if strings.Contains(errOut, "string(s) missing") || !strings.Contains(errOut, "not right yet") {
		t.Fatalf("the waiting line must name the error, not count strings; errOut %q", errOut)
	}
}

// truncated answers a short body under a long Content-Length, then hangs up:
// the client's read of the body fails after the status line has been accepted.
// Nothing simpler reproduces it -- a handler returning an error still sends a
// well-formed response.
func truncated() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 4096\r\n\r\n<p>short</p>")
		buf.Flush()
	}
}

func TestATruncatedBodyIsAnErrorInTextMode(t *testing.T) {
	srv := httptest.NewServer(truncated())
	defer srv.Close()
	code, out, _ := drive(t, "-retry", "0", srv.URL, "jpeg-quality")
	if code != 1 || !strings.HasPrefix(out, "FAIL ") {
		t.Fatalf("code %d, out %q: a body that could not be read is not a page without the text", code, out)
	}
}

func TestATruncatedBodyIsAnErrorInRawMode(t *testing.T) {
	srv := httptest.NewServer(truncated())
	defer srv.Close()
	code, out, _ := drive(t, "--raw", "-retry", "0", srv.URL, "jpeg-quality")
	if code != 1 || !strings.HasPrefix(out, "FAIL ") {
		t.Fatalf("code %d, out %q: --raw must report the read error, not a verdict", code, out)
	}
}

// A truncated body that already contains the want is still a settled question:
// --raw stops at the first match, so the read error never happens. This is the
// other side of the same guard, and it is why scanFor returns on left == 0.
func TestRawAnswersFromWhatItAlreadyReadEvenIfTheRestOfTheBodyFails(t *testing.T) {
	srv := httptest.NewServer(truncated())
	defer srv.Close()
	code, out, _ := drive(t, "--raw", "-retry", "0", srv.URL, "short")
	if code != 0 || !strings.Contains(out, "true \"short\"") {
		t.Fatalf("code %d, out %q", code, out)
	}
}

// hostile fails any read: a stream that must not be touched.
type hostile struct{ t *testing.T }

func (h hostile) Read([]byte) (int, error) {
	h.t.Error("the body was read although every want was already settled")
	return 0, errors.New("must not be read")
}

// ⛔ scanFor's `left == 0` RETURN IS THE ONLY THING THAT KEEPS AN ALREADY-
// ANSWERED SCAN OFF THE NETWORK, and no test held it (it survived the sweep at
// main.go:365): an empty want is true by definition, so with nothing left to
// look for the body must not be fetched byte by byte at all. A reader that
// fails on contact is the only way to assert "did not read" -- counting bytes
// would pass on a stream that was read and ignored.
func TestScanForSettlesEmptyWantsWithoutReadingTheStream(t *testing.T) {
	found, err := scanFor(hostile{t}, []string{"", ""})
	if err != nil {
		t.Fatalf("err %v", err)
	}
	for i, f := range found {
		if !f {
			t.Fatalf("want %d: an empty string is contained in anything, got false", i)
		}
	}
}

// And the control for that one: with a real want in the list, the stream IS
// read -- otherwise the test above would pass on a scanFor that never reads
// anything at all.
func TestScanForDoesReadTheStreamWhenAWantIsRealAndEmptyOnesStayTrue(t *testing.T) {
	found, err := scanFor(io.NopCloser(strings.NewReader("alpha beta")), []string{"", "beta", "gamma"})
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if !found[0] || !found[1] || found[2] {
		t.Fatalf("found %v, want [true true false]", found)
	}
}
