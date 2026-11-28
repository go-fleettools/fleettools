package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every API read and write can fail, and each failure must stop the run with
// the API's words and delete nothing it was not sure of.
func TestAnAPIFailureStopsTheRun(t *testing.T) {
	cases := []struct {
		name     string
		branch   *fakeBranch
		override map[string]reply
		want     string
		deleted  bool // the failure comes after the delete was sent
	}{
		{"repository 500", nil, map[string]reply{"/repos/o/r$": {500, "boom"}}, "500", false},
		{"repository without a name", nil, map[string]reply{"/repos/o/r$": {200, "{}"}}, "no name or default branch", false},
		{"repository not json", nil, map[string]reply{"/repos/o/r$": {200, "<html>"}}, "invalid character", false},
		{"branch 500", nil, map[string]reply{"/repos/o/r/branches/": {500, "boom"}}, "500", false},
		{"branch not json", nil, map[string]reply{"/repos/o/r/branches/": {200, "<html>"}}, "invalid character", false},
		{"branch without a head commit", nil, map[string]reply{"/repos/o/r/branches/": {200, `{"protected":false,"commit":{}}`}}, "no head commit", false},
		{"pulls 500", nil, map[string]reply{"/repos/o/r/pulls": {500, "boom"}}, "500", false},
		{"compare against a pull request head 500", &fakeBranch{sha: "h2", prs: []pull{merged(3, "good", "h")}},
			map[string]reply{"/repos/o/r/compare/h...h2": {500, "boom"}}, "500", false},
		{"compare against the default branch 500", &fakeBranch{sha: "n"},
			map[string]reply{"/repos/o/r/compare/": {500, "boom"}}, "500", false},
		{"delete 500", nil, map[string]reply{"DELETE /repos/o/r/git/refs/": {500, "boom"}}, "500", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newServer(t)
			s.branches["good"] = &fakeBranch{sha: "g", prs: []pull{merged(1, "good", "g")}}
			if tc.branch != nil {
				s.branches["good"] = tc.branch
			}
			s.override = tc.override
			out, err := s.run(t, "o/r", "delete", "good")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v out=%s, want %q", err, out, tc.want)
			}
			if !tc.deleted && len(s.deleted) != 0 {
				t.Fatalf("deleted %v after a failed check", s.deleted)
			}
		})
	}
}

func TestAReadBackFailureIsReported(t *testing.T) {
	s := newServer(t)
	s.stay = true
	s.afterDelete = 500
	s.branches["good"] = &fakeBranch{sha: "g", prs: []pull{merged(1, "good", "g")}}
	if _, err := s.run(t, "o/r", "delete", "good"); err == nil || !strings.Contains(err.Error(), "read back good") {
		t.Fatalf("err=%v", err)
	}
}

func TestAtMostFiftyBranchesPerRun(t *testing.T) {
	s := newServer(t)
	args := []string{"o/r", "delete"}
	for i := 0; i < maxBranches+1; i++ {
		args = append(args, fmt.Sprintf("b%d", i))
	}
	if _, err := s.run(t, args...); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("err=%v", err)
	}
	if len(s.methods) != 0 {
		t.Fatalf("sent %v", s.methods)
	}
}

func TestLocalRefusalsSayWhy(t *testing.T) {
	s := newServer(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"o", "delete", "x"}, "want owner/repo"},
		{[]string{"o/r", "remove", "x"}, "only delete"},
		{[]string{"o/r", "delete", "x", "x"}, "twice"},
	} {
		_, err := s.run(t, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err=%v, want %q", tc.args, err, tc.want)
		}
	}
	if len(s.methods) != 0 {
		t.Fatalf("sent %v", s.methods)
	}
}

func TestATokenThatCannotBeReadStopsBeforeAnyRequest(t *testing.T) {
	s := newServer(t)
	cmd := RootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--base", s.URL, "--token-file", filepath.Join(t.TempDir(), "absent"), "o/r", "delete", "x"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want an error")
	}
	if len(s.methods) != 0 {
		t.Fatalf("sent %v", s.methods)
	}
}

func exec(t *testing.T, base string, args ...string) error {
	t.Helper()
	cmd := RootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--base", base, "--token-file", tokenFile(t)}, args...))
	return cmd.Execute()
}

func TestTransportFailures(t *testing.T) {
	// A request that cannot even be built.
	if err := exec(t, "https://[bad", "o/r", "delete", "x"); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("an unparsable base must fail to parse: %v", err)
	}
	// A connection that is refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	_ = ln.Close()
	if err := exec(t, base, "o/r", "delete", "x"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("a refused connection must say so: %v", err)
	}
	// A body that stops short of its Content-Length.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("abc"))
		if h, ok := w.(http.Hijacker); ok {
			conn, _, _ := h.Hijack()
			_ = conn.Close()
		}
	}))
	defer srv.Close()
	if err := exec(t, srv.URL, "o/r", "delete", "x"); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Errorf("a truncated body must say so: %v", err)
	}
}

func TestTheTokenFileIsRequiredToBeReadable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte("SEKRIT"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	s := newServer(t)
	cmd := RootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--base", s.URL, "--token-file", p, "o/r", "delete", "x"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("a world-readable token file must be refused")
	}
	if len(s.methods) != 0 {
		t.Fatalf("sent %v", s.methods)
	}
}
