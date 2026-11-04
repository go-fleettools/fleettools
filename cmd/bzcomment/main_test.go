package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const secret = "S3CRET-api-key-0123456789"

// fixture starts an https test server running h, points the client at it and
// writes a 0600 key file and a comment file. Everything the command prints is
// returned, so the leak test can search all of it.
type fixture struct {
	base, keyFile, comment string
}

func newFixture(t *testing.T, h http.Handler) *fixture {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	old := newClient
	newClient = func() *http.Client {
		c := srv.Client()
		c.CheckRedirect = old().CheckRedirect
		c.Timeout = old().Timeout
		return c
	}
	t.Cleanup(func() { newClient = old })
	dir := t.TempDir()
	f := &fixture{base: srv.URL, keyFile: filepath.Join(dir, "key"), comment: filepath.Join(dir, "c.txt")}
	if err := os.WriteFile(f.keyFile, []byte("  "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.comment, []byte("Hello bug.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) run(extra ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	args := append([]string{"--base", f.base, "--key-file", f.keyFile}, extra...)
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// server answers like Bugzilla and records what it was sent.
type server struct {
	mu      sync.Mutex
	posts   int
	posted  string
	keyHdr  string
	urlSeen []string
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.urlSeen = append(s.urlSeen, r.URL.String())
	s.keyHdr = r.Header.Get("X-BUGZILLA-API-KEY")
	if s.keyHdr != secret {
		w.WriteHeader(401)
		// An error body that echoes the request, key included.
		io.WriteString(w, `{"error":true,"code":306,"message":"bad key `+s.keyHdr+` rejected"}`)
		return
	}
	switch {
	case r.URL.Path == "/rest/whoami":
		io.WriteString(w, `{"id":7,"name":"me@example.org","real_name":"Me"}`)
	case r.URL.Path == "/rest/bug/1870699" && r.Method == "GET":
		io.WriteString(w, `{"bugs":[{"id":1870699,"summary":"A bug","status":"NEW"}]}`)
	case r.URL.Path == "/rest/bug/404":
		w.WriteHeader(404)
		io.WriteString(w, `{"error":true,"code":101,"message":"Bug #404 does not exist."}`)
	case r.URL.Path == "/rest/bug/1870699/comment" && r.Method == "POST":
		b, _ := io.ReadAll(r.Body)
		var in struct{ Comment string }
		json.Unmarshal(b, &in)
		s.posts++
		s.posted = in.Comment
		io.WriteString(w, `{"id":555}`)
	case r.URL.Path == "/rest/bug/comment/555":
		io.WriteString(w, `{"comments":{"555":{"count":9}}}`)
	default:
		w.WriteHeader(500)
	}
}

func TestWhoami(t *testing.T) {
	f := newFixture(t, &server{})
	code, out, errOut := f.run("--whoami")
	if code != 0 || strings.TrimSpace(out) != "me@example.org" {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
}

func TestPostSuccess(t *testing.T) {
	s := &server{}
	f := newFixture(t, s)
	code, out, errOut := f.run(f.args("1870699")...)
	if code != 0 {
		t.Fatalf("code %d err %q", code, errOut)
	}
	for _, want := range []string{"bug 1870699 [NEW] A bug", "comment id 555", "/show_bug.cgi?id=1870699#c9"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %q", want, out)
		}
	}
	if s.posts != 1 || s.posted != "Hello bug.\n" {
		t.Errorf("posts %d, text %q", s.posts, s.posted)
	}
	for _, u := range s.urlSeen {
		if strings.Contains(u, secret) {
			t.Errorf("key in URL %q", u)
		}
	}
}

func (f *fixture) args(id string) []string { return []string{id, f.comment} }

func TestDryRunPostsNothing(t *testing.T) {
	s := &server{}
	f := newFixture(t, s)
	code, out, _ := f.run(append([]string{"--dry-run"}, f.args("1870699")...)...)
	if code != 0 || s.posts != 0 {
		t.Fatalf("code %d posts %d", code, s.posts)
	}
	if !strings.Contains(out, "A bug") || !strings.Contains(out, "Hello bug.") {
		t.Errorf("dry run output %q", out)
	}
}

func TestBadKey401(t *testing.T) {
	f := newFixture(t, &server{})
	os.WriteFile(f.keyFile, []byte("wrong-key-value\n"), 0o600)
	code, out, errOut := f.run(f.args("1870699")...)
	if code != 2 || !strings.Contains(errOut, "HTTP 401") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if strings.Contains(out+errOut, "wrong-key-value") {
		t.Errorf("the echoed key reached the output: %q %q", out, errOut)
	}
}

func TestBug404(t *testing.T) {
	s := &server{}
	f := newFixture(t, s)
	code, _, errOut := f.run(f.args("404")...)
	if code != 2 || !strings.Contains(errOut, "HTTP 404") || s.posts != 0 {
		t.Fatalf("code %d err %q posts %d", code, errOut, s.posts)
	}
}

func TestNonJSONBody(t *testing.T) {
	f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html>"+r.Header.Get("X-BUGZILLA-API-KEY")+"</html>")
	}))
	code, out, errOut := f.run("--whoami")
	if code != 2 || !strings.Contains(errOut, "not JSON") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if strings.Contains(out+errOut, secret) {
		t.Error("key leaked from a non-JSON body")
	}
}

func TestOversizeBody(t *testing.T) {
	f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"name":"`+strings.Repeat("a", maxResponse+10)+`"}`)
	}))
	code, _, errOut := f.run("--whoami")
	if code != 2 || !strings.Contains(errOut, "refused") {
		t.Fatalf("code %d err %q", code, errOut)
	}
}

func TestKeyFilePermissionsRefused(t *testing.T) {
	s := &server{}
	f := newFixture(t, s)
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604} {
		os.Chmod(f.keyFile, mode)
		code, out, errOut := f.run("--whoami")
		if code != 2 || !strings.Contains(errOut, "must not be readable") {
			t.Errorf("mode %o: code %d err %q", mode, code, errOut)
		}
		if strings.Contains(out+errOut, secret) {
			t.Errorf("mode %o: key leaked", mode)
		}
	}
	if len(s.urlSeen) != 0 {
		t.Error("a request was made with a key file that was refused")
	}
	// Not a regular file.
	code, _, errOut := f.run("--key-file", t.TempDir(), "--whoami")
	if code != 2 || !strings.Contains(errOut, "regular file") {
		t.Errorf("directory: code %d err %q", code, errOut)
	}
}

func TestHTTPSOnly(t *testing.T) {
	f := newFixture(t, &server{})
	f.base = strings.Replace(f.base, "https://", "http://", 1)
	code, _, errOut := f.run("--whoami")
	if code != 2 || !strings.Contains(errOut, "https") {
		t.Fatalf("code %d err %q", code, errOut)
	}
}

func TestBoundsAndOperands(t *testing.T) {
	f := newFixture(t, &server{})
	big := filepath.Join(t.TempDir(), "big")
	os.WriteFile(big, bytes.Repeat([]byte("a"), maxComment+1), 0o600)
	bad := filepath.Join(t.TempDir(), "bad")
	os.WriteFile(bad, []byte{0xff, 0xfe}, 0o600)
	for name, args := range map[string][]string{
		"zero id":      {"0", f.comment},
		"negative id":  {"-5", f.comment},
		"text id":      {"abc", f.comment},
		"oversize":     {"1870699", big},
		"invalid utf8": {"1870699", bad},
		"missing file": {"1870699", filepath.Join(t.TempDir(), "nope")},
		"one operand":  {"1870699"},
	} {
		if code, _, _ := f.run(append([]string{"--dry-run", "--"}, args...)...); code != 2 {
			t.Errorf("%s: code %d, want 2", name, code)
		}
	}
}

func TestNoRedirectFollowed(t *testing.T) {
	var hits int
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer other.Close()
	f := newFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	if code, _, _ := f.run("--whoami"); code != 2 || hits != 0 {
		t.Errorf("code %d, redirect target hit %d times", code, hits)
	}
}

// The key must appear in no output of any path: success, dry run, every error.
func TestKeyNeverInOutput(t *testing.T) {
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := r.Header.Get("X-BUGZILLA-API-KEY")
		w.WriteHeader(403)
		io.WriteString(w, `{"error":true,"code":102,"message":"request had X-BUGZILLA-API-KEY: `+k+`"}`)
	})
	cases := map[string]http.Handler{"ok": &server{}, "echo": echo}
	for name, h := range cases {
		f := newFixture(t, h)
		for _, args := range [][]string{
			{"--whoami"},
			{"--dry-run", "1870699", f.comment},
			{"1870699", f.comment},
			{"--help"},
		} {
			_, out, errOut := f.run(args...)
			if strings.Contains(out+errOut, secret) {
				t.Errorf("%s %v: key in output %q %q", name, args, out, errOut)
			}
		}
	}
	// And the file-permission error path, and the real default path to a key
	// that does not exist.
	f := newFixture(t, &server{})
	os.Chmod(f.keyFile, 0o644)
	_, out, errOut := f.run("--whoami")
	if strings.Contains(out+errOut, secret) {
		t.Error("key in the permission refusal")
	}
}

func TestHelpIsZeroAndTypoIsTwo(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-h"}, &out, &errOut); code != 0 || out.Len() != 0 {
		t.Errorf("-h: code %d, stdout %q", code, out.String())
	}
	errOut.Reset()
	if code := run([]string{"--nonesuch"}, &out, &errOut); code != 2 {
		t.Errorf("typo: code %d, want 2", code)
	}
}
