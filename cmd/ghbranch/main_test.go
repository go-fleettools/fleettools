package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeBranch struct {
	sha       string
	protected bool
	prs       []pull
	cmp       map[string]string // "tip...sha" -> status
}

type server struct {
	*httptest.Server
	branches map[string]*fakeBranch
	cmpBase  map[string]string // compare against the default branch: "main...sha" -> status
	deleted  []string
	auth     []string
	methods  []string
	stay     bool // a deleted branch still answers 200
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{branches: map[string]*fakeBranch{}, cmpBase: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.methods = append(s.methods, r.Method)
		p := r.URL.EscapedPath()
		switch {
		case p == "/repos/o/r":
			_ = json.NewEncoder(w).Encode(map[string]any{"full_name": "o/r", "default_branch": "main"})
		case strings.HasPrefix(p, "/repos/o/r/branches/"):
			name := strings.TrimPrefix(p, "/repos/o/r/branches/")
			b, ok := s.branches[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"protected": b.protected, "commit": map[string]any{"sha": b.sha}})
		case p == "/repos/o/r/pulls":
			head := strings.TrimPrefix(r.URL.Query().Get("head"), "o:")
			b, ok := s.branches[head]
			if !ok {
				_, _ = w.Write([]byte("[]"))
				return
			}
			out := b.prs
			if out == nil {
				out = []pull{}
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(p, "/repos/o/r/compare/"):
			spec := strings.TrimPrefix(p, "/repos/o/r/compare/")
			tip, sha, _ := strings.Cut(spec, "...")
			status := "diverged"
			if tip == "main" {
				if v, ok := s.cmpBase[spec]; ok {
					status = v
				}
			} else {
				for _, b := range s.branches {
					if v, ok := b.cmp[spec]; ok {
						status = v
					}
				}
			}
			_ = sha
			_ = json.NewEncoder(w).Encode(map[string]any{"status": status})
		case strings.HasPrefix(p, "/repos/o/r/git/refs/heads/") && r.Method == http.MethodDelete:
			name := strings.TrimPrefix(p, "/repos/o/r/git/refs/heads/")
			s.deleted = append(s.deleted, name)
			if !s.stay {
				delete(s.branches, name)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *server) writes() int {
	n := 0
	for _, m := range s.methods {
		if m != http.MethodGet {
			n++
		}
	}
	return n
}

func tokenFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte("SEKRIT"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (s *server) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := RootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--base", s.URL, "--token-file", tokenFile(t)}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func merged(n int, ref, sha string) pull {
	p := pull{Number: n, State: "closed", MergedAt: "2026-11-28T00:00:00Z"}
	p.Head.Ref, p.Head.SHA = ref, sha
	return p
}

func TestDeletesALandedBranchAndReadsItBack(t *testing.T) {
	s := newServer(t)
	s.branches["fix/x"] = &fakeBranch{sha: "aaa", prs: []pull{merged(5, "fix/x", "aaa")}}
	out, err := s.run(t, "o/r", "delete", "fix/x")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(s.deleted) != 1 || s.deleted[0] != "fix/x" {
		t.Fatalf("deleted %v", s.deleted)
	}
	if !strings.Contains(out, "deleted fix/x") {
		t.Fatalf("output: %s", out)
	}
	for _, a := range s.auth {
		if a != "Bearer SEKRIT" {
			t.Fatalf("auth %q", a)
		}
	}
}

func TestRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(s *server)
		args  []string
		want  string
	}{
		{"default", func(s *server) { s.branches["main"] = &fakeBranch{sha: "m"} }, []string{"main"}, "the default branch"},
		{"protected", func(s *server) { s.branches["rel"] = &fakeBranch{sha: "a", protected: true} }, []string{"rel"}, "protected"},
		{"open pr", func(s *server) {
			p := merged(7, "wip", "a")
			p.State, p.MergedAt = "open", ""
			s.branches["wip"] = &fakeBranch{sha: "a", prs: []pull{p}}
		}, []string{"wip"}, "open pull request #7"},
		{"unlanded", func(s *server) { s.branches["new"] = &fakeBranch{sha: "n"} }, []string{"new"}, "unlanded"},
		{"unlanded: pr head older than branch head", func(s *server) {
			s.branches["more"] = &fakeBranch{sha: "b2", prs: []pull{merged(3, "more", "b1")}, cmp: map[string]string{"b1...b2": "ahead"}}
		}, []string{"more"}, "unlanded"},
		{"missing", func(s *server) {}, []string{"ghost"}, "no such branch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newServer(t)
			s.branches["keep"] = &fakeBranch{sha: "k", prs: []pull{merged(1, "keep", "k")}}
			tc.setup(s)
			out, err := s.run(t, append([]string{"o/r", "delete", "keep"}, tc.args...)...)
			if err == nil || !strings.Contains(out+err.Error(), tc.want) {
				t.Fatalf("want refusal %q, got err=%v out=%s", tc.want, err, out)
			}
			if s.writes() != 0 {
				t.Fatalf("a refusal must delete nothing, got %v", s.deleted)
			}
		})
	}
}

func TestForceUnlandedWaivesOnlyThatCheck(t *testing.T) {
	s := newServer(t)
	s.branches["new"] = &fakeBranch{sha: "n"}
	if out, err := s.run(t, "o/r", "delete", "new", "--force-unlanded"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	s = newServer(t)
	s.branches["main"] = &fakeBranch{sha: "m"}
	s.branches["rel"] = &fakeBranch{sha: "a", protected: true}
	for _, b := range []string{"main", "rel"} {
		if _, err := s.run(t, "o/r", "delete", b, "--force-unlanded"); err == nil {
			t.Fatalf("%s must stay refused under --force-unlanded", b)
		}
	}
	if s.writes() != 0 {
		t.Fatal("wrote")
	}
}

func TestReachableFromDefaultCountsAsLanded(t *testing.T) {
	s := newServer(t)
	s.branches["old"] = &fakeBranch{sha: "o1"}
	s.cmpBase["main...o1"] = "behind"
	if out, err := s.run(t, "o/r", "delete", "old"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestClosedUnmergedPRHoldingTheHeadCounts(t *testing.T) {
	s := newServer(t)
	p := merged(9, "ab/x", "h")
	p.MergedAt = ""
	s.branches["ab/x"] = &fakeBranch{sha: "h2", prs: []pull{p}, cmp: map[string]string{"h...h2": "behind"}}
	if out, err := s.run(t, "o/r", "delete", "ab/x"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestOneRefusalDeletesNothing(t *testing.T) {
	s := newServer(t)
	s.branches["good"] = &fakeBranch{sha: "g", prs: []pull{merged(1, "good", "g")}}
	s.branches["bad"] = &fakeBranch{sha: "b"}
	if _, err := s.run(t, "o/r", "delete", "good", "bad"); err == nil {
		t.Fatal("want refusal")
	}
	if s.writes() != 0 {
		t.Fatalf("deleted %v", s.deleted)
	}
}

func TestDryRunSendsOnlyReads(t *testing.T) {
	s := newServer(t)
	s.branches["good"] = &fakeBranch{sha: "g", prs: []pull{merged(1, "good", "g")}}
	out, err := s.run(t, "o/r", "delete", "good", "--dry-run")
	if err != nil || s.writes() != 0 || !strings.Contains(out, "dry run") {
		t.Fatalf("err=%v writes=%d out=%s", err, s.writes(), out)
	}
}

func TestStillAnsweringAfterDeleteIsAFailure(t *testing.T) {
	s := newServer(t)
	s.stay = true
	s.branches["good"] = &fakeBranch{sha: "g", prs: []pull{merged(1, "good", "g")}}
	if _, err := s.run(t, "o/r", "delete", "good"); err == nil || !strings.Contains(err.Error(), "still answers") {
		t.Fatalf("err=%v", err)
	}
}

func TestSlashedBranchIsEscapedPerSegment(t *testing.T) {
	s := newServer(t)
	s.branches["a/b c"] = nil // never matched: the name is refused locally
	if _, err := s.run(t, "o/r", "delete", "a/b c"); err == nil {
		t.Fatal("a space must be refused")
	}
	if got := escapeRef("ab/x y#z"); got != "ab/x%20y%23z" {
		t.Fatalf("escapeRef: %s", got)
	}
}

func TestLocalRefusals(t *testing.T) {
	s := newServer(t)
	for _, args := range [][]string{
		{"o/r", "remove", "x"},
		{"o", "delete", "x"},
		{"o/r", "delete", "-x"},
		{"o/r", "delete", "a..b"},
		{"o/r", "delete", "x.lock"},
		{"o/r", "delete", "x", "x"},
		{"o/r", "delete", "a\nb"},
		{"o/r", "delete", ""},
	} {
		if _, err := s.run(t, args...); err == nil {
			t.Fatalf("%q must be refused", args)
		}
	}
	if len(s.methods) != 0 {
		t.Fatalf("local refusals must send nothing, sent %v", s.methods)
	}
}

func TestBaseMustBeHTTPS(t *testing.T) {
	cmd := RootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--base", "http://example.com", "--token-file", tokenFile(t), "o/r", "delete", "x"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("err=%v", err)
	}
}

func TestRedirectIsReportedNotFollowed(t *testing.T) {
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer srv.Close()
	cmd := RootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--base", srv.URL, "--token-file", tokenFile(t), "o/r", "delete", "x"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not followed") || hits != 0 {
		t.Fatalf("err=%v hits=%d", err, hits)
	}
}
