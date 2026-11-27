package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// guarded runs f and turns a panic into a test failure that names the guard.
func guarded(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s: panicked instead of returning an error: %v", what, r)
		}
	}()
	f()
}

func wantErr(t *testing.T, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want an error containing %v, got none", parts)
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("error %q lacks %q", err, p)
		}
	}
}

func TestValidationMessages(t *testing.T) {
	f := newFake(t)
	for _, org := range []string{"", " ", "o x", "o?x", "o#x", "o/x", "o\nx"} {
		guarded(t, "org "+org, func() {
			_, err := exec(t, f, org, "list")
			wantErr(t, err, "want a plain name")
		})
	}
	for _, r := range []string{"a b", "a?b", "a#b", "a/b"} {
		guarded(t, "repo "+r, func() {
			_, err := exec(t, f, "o", "ensure", "g", "--repos", "r1,"+r)
			wantErr(t, err, "want a plain name in o")
		})
	}
	guarded(t, "empty repo", func() {
		_, err := exec(t, f, "o", "ensure", "g", "--repos", "r1,")
		wantErr(t, err, "want a plain name in o")
	})
	if len(f.reqs) != 0 {
		t.Fatalf("validation failures must send nothing, sent %v", f.reqs)
	}
}

func TestErrorsAreReportedNotIgnored(t *testing.T) {
	f := newFake(t)
	old := readToken
	defer func() { readToken = old }()
	run := func(args ...string) error {
		cmd := RootCmd()
		cmd.SetArgs(append(args, "--base", f.URL))
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		var err error
		guarded(t, strings.Join(args, " "), func() { err = cmd.Execute() })
		return err
	}
	readToken = func(string) (string, error) { return "", fmt.Errorf("no token here") }
	wantErr(t, run("o", "list"), "no token here")
	if len(f.reqs) != 0 {
		t.Fatalf("no token: nothing may be sent, sent %v", f.reqs)
	}
	readToken = func(string) (string, error) { return "wrong", nil }
	wantErr(t, run("o", "list"), "401")
	wantErr(t, run("o", "ensure", "g", "--repos", "r1"), "401")
	for _, r := range f.reqs {
		if !strings.HasPrefix(r, "GET /orgs/") {
			t.Fatalf("a failed listing must stop the run, sent %v", f.reqs)
		}
	}
	readToken = func(string) (string, error) { return "tok", nil }
	wantErr(t, run("o", "ensure", "g", "--repos", "missing"), "404", "/repos/o/missing")
}

func TestCreateFailureReported(t *testing.T) {
	calls := 0
	srv := capped(&calls, 30, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/"):
			_, _ = io.WriteString(w, `{"id":3}`)
		case r.Method == "POST":
			w.WriteHeader(422)
			_, _ = io.WriteString(w, "refused by org\nmore")
		default:
			_, _ = io.WriteString(w, `{"total_count":0,"runner_groups":[]}`)
		}
	})
	defer srv.Close()
	old := readToken
	readToken = func(string) (string, error) { return "t", nil }
	defer func() { readToken = old }()
	cmd := RootCmd()
	cmd.SetArgs([]string{"o", "ensure", "g", "--repos", "r", "--base", srv.URL})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	var err error
	guarded(t, "create", func() { err = cmd.Execute() })
	wantErr(t, err, "422", "refused by org")
}

func TestListAndEnsureAreDistinct(t *testing.T) {
	f := newFake(t)
	var out string
	var err error
	guarded(t, "list", func() { out, err = exec(t, f, "o", "list") })
	if err != nil || strings.Count(out, "\n") != 1 || strings.Contains(out, "ready") {
		t.Fatalf("%q %v", out, err)
	}
	for _, r := range f.reqs {
		if r != "GET /orgs/o/actions/runner-groups" {
			t.Fatalf("list sent %v", f.reqs)
		}
	}
	f2 := newFake(t)
	guarded(t, "ensure", func() { out, err = exec(t, f2, "o", "ensure", "g", "--repos", "r1") })
	if err != nil || strings.Contains(out, "\t") {
		t.Fatalf("ensure printed a listing: %q %v", out, err)
	}
}

// capped serves h for the first n calls, then a 500, so a loop that
// fails to stop ends in an error instead of hanging.
func capped(calls *int, n int, h http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		if *calls > n {
			w.WriteHeader(500)
			return
		}
		h(w, r)
	}))
}

func TestPaginationStops(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		pages int
		want  int
	}{
		{"total reached", `{"total_count":1,"runner_groups":[{"id":1,"name":"a"}]}`, 1, 1},
		{"empty page", `{"total_count":9,"runner_groups":[]}`, 1, 0},
		{"zero total", `{"total_count":0,"runner_groups":[{"id":1,"name":"a"}]}`, 1, 1},
	}
	for _, c := range cases {
		calls := 0
		srv := capped(&calls, 4, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, c.body) })
		g, err := listGroups(newClient(timeout), srv.URL, "t", "o")
		srv.Close()
		if err != nil || len(g) != c.want || calls != c.pages {
			t.Errorf("%s: %v %v calls=%d", c.name, g, err, calls)
		}
	}
	calls := 0
	srv := capped(&calls, 6, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("page")
		fmt.Fprintf(w, `{"total_count":3,"runner_groups":[{"id":%s,"name":"x"}]}`, p)
	})
	defer srv.Close()
	g, err := listGroups(newClient(timeout), srv.URL, "t", "o")
	if err != nil || len(g) != 3 || calls != 3 {
		t.Fatalf("%v %v calls=%d", g, err, calls)
	}
}

func TestDoErrorsNamed(t *testing.T) {
	guarded(t, "bad url", func() {
		_, err := do(newClient(timeout), "GET", "http://[bad", "t", nil, 200)
		wantErr(t, err, "bad")
	})
	guarded(t, "refused", func() {
		_, err := do(newClient(timeout), "GET", "http://127.0.0.1:1/", "t", nil, 200)
		wantErr(t, err, "127.0.0.1:1")
	})
	// A body cut short of its Content-Length is a read error, not an empty reply.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "short")
	}))
	defer srv.Close()
	guarded(t, "truncated", func() {
		b, err := do(newClient(timeout), "GET", srv.URL, "t", nil, 200)
		wantErr(t, err, "EOF")
		if b != nil {
			t.Fatalf("returned %q with an error", b)
		}
	})
}
