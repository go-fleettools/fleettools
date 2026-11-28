package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

const tok = "ghp_SECRETTOKENVALUE"

type rec struct {
	mu      sync.Mutex
	methods []string
	auths   []string
}

func server(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *rec) {
	t.Helper()
	r := &rec{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		r.mu.Lock()
		r.methods = append(r.methods, q.Method)
		r.auths = append(r.auths, q.Header.Get("Authorization"))
		r.mu.Unlock()
		h(w, q)
	}))
	t.Cleanup(s.Close)
	return s, r
}

func tokFile(t *testing.T, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte(tok+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func do(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var o, e bytes.Buffer
	code := cli.Drive(RootCmd(), args, &o, &e)
	return code, o.String(), e.String()
}

func TestAnswerRead(t *testing.T) {
	s, r := server(t, func(w http.ResponseWriter, q *http.Request) { fmt.Fprint(w, `{"a":{"b":"x"}}`) })
	code, out, _ := do(t, "--base", s.URL, "--token-file", tokFile(t, 0o600), "orgs/o/x")
	if code != 0 || strings.TrimSpace(out) != `{"a":{"b":"x"}}` {
		t.Fatalf("code %d out %q", code, out)
	}
	if r.auths[0] != "Bearer "+tok {
		t.Fatalf("auth %q", r.auths[0])
	}
	code, out, _ = do(t, "--base", s.URL, "--token-file", tokFile(t, 0o600), "--field", "a.b", "orgs/o/x")
	if code != 0 || strings.TrimSpace(out) != "x" {
		t.Fatalf("field: %d %q", code, out)
	}
}

func TestOnlyGET(t *testing.T) {
	s, r := server(t, func(w http.ResponseWriter, q *http.Request) { fmt.Fprint(w, `[1]`) })
	f := tokFile(t, 0o600)
	do(t, "--base", s.URL, "--token-file", f, "x")
	do(t, "--base", s.URL, "--token-file", f, "--paginate", "x")
	for _, m := range r.methods {
		if m != "GET" {
			t.Fatalf("method %s sent", m)
		}
	}
	for _, flag := range []string{"--method", "-X", "--input", "--data", "-f"} {
		if code, _, _ := do(t, "--base", s.URL, "--token-file", f, flag, "POST", "x"); code == 0 {
			t.Fatalf("flag %s accepted", flag)
		}
	}
	if len(r.methods) != 2 {
		t.Fatalf("extra requests: %v", r.methods)
	}
	src, _ := os.ReadFile("main.go")
	for _, m := range []string{"MethodPost", "MethodPut", "MethodPatch", "MethodDelete", `"POST"`, `"PUT"`, `"PATCH"`, `"DELETE"`} {
		if strings.Contains(string(src), m) {
			t.Fatalf("main.go mentions %s", m)
		}
	}
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		name string
		h    func(w http.ResponseWriter, r *http.Request)
		code int
		want string
	}{
		{"404", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"message":"Not Found"}`)
		}, 3, "absent"},
		{"403 scope", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Accepted-OAuth-Scopes", "admin:org")
			w.Header().Set("X-OAuth-Scopes", "repo")
			w.WriteHeader(403)
			fmt.Fprint(w, `{"message":"Resource not accessible"}`)
		}, 4, "admin:org"},
		{"401", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }, 4, "UNREAD"},
		{"rate", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "1700000000")
			w.WriteHeader(403)
		}, 5, "2023-11-14T22:13:20Z"},
		{"429", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }, 5, "rate limited"},
		{"500", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }, 2, "HTTP 500"},
	}
	for _, c := range cases {
		s, _ := server(t, c.h)
		code, out, errs := do(t, "--base", s.URL, "--token-file", tokFile(t, 0o600), "x")
		if code != c.code || out != "" || !strings.Contains(errs, c.want) {
			t.Errorf("%s: code %d out %q err %q", c.name, code, out, errs)
		}
	}
}

func TestPagination(t *testing.T) {
	var s *httptest.Server
	s, _ = server(t, func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Query().Get("per_page") != "100" {
			t.Errorf("per_page missing: %s", q.URL)
		}
		switch q.URL.Query().Get("page") {
		case "":
			w.Header().Set("Link", fmt.Sprintf(`<%s/x?per_page=100&page=2>; rel="next", <%s/x?page=3>; rel="last"`, s.URL, s.URL))
			fmt.Fprint(w, `[1,2]`)
		case "2":
			fmt.Fprint(w, `[3]`)
		}
	})
	code, out, _ := do(t, "--base", s.URL, "--token-file", tokFile(t, 0o600), "--paginate", "x")
	if code != 0 || strings.TrimSpace(out) != `[1,2,3]` {
		t.Fatalf("%d %q", code, out)
	}
}

func TestPaginationObject(t *testing.T) {
	var s *httptest.Server
	s, _ = server(t, func(w http.ResponseWriter, q *http.Request) {
		if q.URL.Query().Get("page") == "" {
			w.Header().Set("Link", fmt.Sprintf(`<%s/x?per_page=100&page=2>; rel="next"`, s.URL))
			fmt.Fprint(w, `{"total_count":2,"items":[1]}`)
			return
		}
		fmt.Fprint(w, `{"total_count":2,"items":[2]}`)
	})
	_, out, _ := do(t, "--base", s.URL, "--token-file", tokFile(t, 0o600), "--paginate", "x")
	if strings.TrimSpace(out) != `{"items":[1,2],"total_count":2}` {
		t.Fatalf("%q", out)
	}
}

func TestNextLinkOnOtherHostRefused(t *testing.T) {
	s, _ := server(t, func(w http.ResponseWriter, q *http.Request) {
		w.Header().Set("Link", `<http://127.0.0.1:1/x>; rel="next"`)
		fmt.Fprint(w, `[1]`)
	})
	code, _, errs := do(t, "--base", s.URL, "--token-file", tokFile(t, 0o600), "--paginate", "x")
	if code != 2 || !strings.Contains(errs, "refusing") {
		t.Fatalf("%d %q", code, errs)
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	other, r2 := server(t, func(w http.ResponseWriter, q *http.Request) { fmt.Fprint(w, `{}`) })
	s, _ := server(t, func(w http.ResponseWriter, q *http.Request) {
		http.Redirect(w, q, other.URL+"/y", http.StatusMovedPermanently)
	})
	code, _, _ := do(t, "--base", s.URL, "--token-file", tokFile(t, 0o600), "x")
	if code == 0 || len(r2.methods) != 0 {
		t.Fatalf("code %d, redirect target hit %d times", code, len(r2.methods))
	}
}

func TestTokenNeverInOutput(t *testing.T) {
	s, _ := server(t, func(w http.ResponseWriter, q *http.Request) {
		w.WriteHeader(403)
		fmt.Fprintf(w, `{"message":"bad credentials %s"}`, tok)
	})
	_, out, errs := do(t, "--base", s.URL, "--token-file", tokFile(t, 0o600), "x")
	if strings.Contains(out+errs, tok) {
		t.Fatalf("token leaked: %q %q", out, errs)
	}
	s2, _ := server(t, func(w http.ResponseWriter, q *http.Request) { fmt.Fprintf(w, `{"echo":"%s"}`, tok) })
	_, out, errs = do(t, "--base", s2.URL, "--token-file", tokFile(t, 0o600), "x")
	if strings.Contains(out+errs, tok) {
		t.Fatalf("token leaked in body: %q %q", out, errs)
	}
}

func TestTokenFileOnly(t *testing.T) {
	s, r := server(t, func(w http.ResponseWriter, q *http.Request) { fmt.Fprint(w, `{}`) })
	const sentinel = "SENTINEL-FROM-ENV"
	for _, k := range ignoredEnv {
		t.Setenv(k, sentinel)
	}
	code, out, errs := do(t, "--base", s.URL, "--token-file", tokFile(t, 0o600), "x")
	if code != 0 {
		t.Fatalf("%d %q", code, errs)
	}
	for _, a := range r.auths {
		if strings.Contains(a, sentinel) || a != "Bearer "+tok {
			t.Fatalf("Authorization %q", a)
		}
	}
	for _, k := range ignoredEnv {
		if !strings.Contains(errs, k+" is set and is ignored") {
			t.Errorf("no warning naming %s: %q", k, errs)
		}
	}
	if strings.Contains(out+errs, sentinel) {
		t.Fatal("env value printed")
	}
}

func TestNoTokenFileMeansNoRequest(t *testing.T) {
	s, r := server(t, func(w http.ResponseWriter, q *http.Request) { fmt.Fprint(w, `{}`) })
	t.Setenv("GH_TOKEN", "SENTINEL")
	t.Setenv("GITHUB_TOKEN", "SENTINEL")
	missing := filepath.Join(t.TempDir(), "none")
	if code, _, _ := do(t, "--base", s.URL, "--token-file", missing, "x"); code != 2 {
		t.Fatalf("code %d", code)
	}
	if code, _, errs := do(t, "--base", s.URL, "--token-file", tokFile(t, 0o644), "x"); code != 2 || !strings.Contains(errs, "owner-only") {
		t.Fatalf("code %d %q", code, errs)
	}
	if len(r.methods) != 0 {
		t.Fatal("a request was sent without a valid token file")
	}
}

func TestPathAndBaseRefused(t *testing.T) {
	f := tokFile(t, 0o600)
	for _, p := range []string{"https://evil.example/x", "//evil.example/x", "a b", "", "x#y"} {
		if code, _, _ := do(t, "--token-file", f, p); code != 2 {
			t.Errorf("path %q: code %d", p, code)
		}
	}
	if code, _, _ := do(t, "--base", "http://example.com", "--token-file", f, "x"); code != 2 {
		t.Error("http base accepted")
	}
}

func TestFieldMissing(t *testing.T) {
	s, _ := server(t, func(w http.ResponseWriter, q *http.Request) { fmt.Fprint(w, `{"a":[{"b":1}]}`) })
	f := tokFile(t, 0o600)
	if code, out, _ := do(t, "--base", s.URL, "--token-file", f, "--field", "a.0.b", "x"); code != 0 || strings.TrimSpace(out) != "1" {
		t.Fatalf("%d %q", code, out)
	}
	if code, _, _ := do(t, "--base", s.URL, "--token-file", f, "--field", "a.9", "x"); code != 2 {
		t.Fatal("bad index accepted")
	}
}
