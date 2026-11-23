package ghorg

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFilePathBounds(t *testing.T) {
	if ValidPath("") || ValidPath(strings.Repeat("a", 301)) || !ValidPath(strings.Repeat("a", 300)) || !ValidPath("a/b.js") {
		t.Fatal("ValidPath bounds moved")
	}
	for _, p := range []string{"a//b", "./a", "a/../b", "a/"} {
		if ValidPath(p) {
			t.Errorf("%q accepted", p)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, n := range []string{".", "..", "", "a b", "a/b"} {
		if ValidName(n) {
			t.Errorf("%q accepted", n)
		}
	}
	if !ValidName(".github") || !ValidName("go-x_y.z") {
		t.Fatal("a real name was refused")
	}
}

func TestEscapePath(t *testing.T) {
	if got := EscapePath("a b/c#d"); got != "a%20b/c%23d" {
		t.Fatalf("got %q", got)
	}
}

func TestTheReserveLatches(t *testing.T) {
	c := &Client{Reserve: 10, remaining: 5}
	if !c.Low() {
		t.Fatal("below the reserve is not low")
	}
	c.remaining = 5000 // a quota that comes back mid-run does not un-stop the run
	if !c.Low() {
		t.Fatal("the stop did not latch")
	}
	if !c.Stopped() {
		t.Fatal("Stopped does not report the latch")
	}
}

func TestAnUnreadQuotaIsNotLow(t *testing.T) {
	c := New("", "t", 10)
	if c.Low() || c.Remaining() != -1 {
		t.Fatal("a client that has read nothing claims a quota")
	}
}

func TestGetReadsTheQuotaAndSendsTheToken(t *testing.T) {
	var auth, accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, accept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		w.Header().Set("X-RateLimit-Remaining", "7")
		w.Write([]byte("hello"))
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", 10)
	st, b, err := c.Get(context.Background(), "/x", true)
	if err != nil || st != 200 || string(b) != "hello" {
		t.Fatalf("%d %q %v", st, b, err)
	}
	if auth != "Bearer tok" || !strings.Contains(accept, "raw") {
		t.Fatalf("headers %q %q", auth, accept)
	}
	if c.Remaining() != 7 || c.Calls() != 1 || !c.Low() {
		t.Fatalf("remaining %d calls %d", c.Remaining(), c.Calls())
	}
}

func TestOversizedBodyIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", MaxBody+2)))
	}))
	defer srv.Close()
	if _, _, err := New(srv.URL, "t", 0).Get(context.Background(), "/", true); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("err %v", err)
	}
}

func TestOrgsPagesUntilAShortPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := 100
		if r.URL.Query().Get("page") == "2" {
			n = 1
		}
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf(`{"login":"o%03d-%s"}`, i, r.URL.Query().Get("page"))
		}
		fmt.Fprintf(w, "[%s]", strings.Join(parts, ","))
	}))
	defer srv.Close()
	got, err := New(srv.URL, "t", 0).Orgs(context.Background())
	if err != nil || len(got) != 101 {
		t.Fatalf("%d orgs, err %v", len(got), err)
	}
}

func TestOrgsRefusals(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"401": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); w.Write([]byte(`[{"login":"x"}]`)) },
		"bad": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) },
	} {
		srv := httptest.NewServer(h)
		if _, err := New(srv.URL, "t", 0).Orgs(context.Background()); err == nil {
			t.Errorf("%s: no error", name)
		}
		srv.Close()
	}
}
