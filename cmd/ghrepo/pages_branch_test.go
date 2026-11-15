package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// branchServer is a repository o/r whose Pages site is absent (bt "") or has
// the given build type and source; a POST creates a gh-pages site unless
// refuse is set (the API answered 201 and nothing changed).
type branchServer struct {
	*httptest.Server
	bt, branch string
	refuse     bool
	posts      []string
	writes     int
}

func newBranchServer(t *testing.T, bt, branch string) *branchServer {
	t.Helper()
	p := &branchServer{bt: bt, branch: branch}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"full_name":"o/r","html_url":"https://github.com/o/r","default_branch":"main"}`)
	})
	mux.HandleFunc("/repos/o/r/pages", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch r.Method {
		case http.MethodGet:
			if p.bt == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, `{"build_type":"`+p.bt+`","source":{"branch":"`+p.branch+`","path":"/"}}`)
		default:
			p.writes++
			p.posts = append(p.posts, r.Method+" "+string(b))
			if !p.refuse && r.Method == http.MethodPost {
				p.bt, p.branch = "legacy", "gh-pages"
			}
			w.WriteHeader(http.StatusCreated)
		}
	})
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

func (p *branchServer) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return exec(t, "", append([]string{"--base", p.URL, "--token-file", tokenFile(t, "SEKRIT")}, args...)...)
}

func TestPagesGhPagesCreatesASite(t *testing.T) {
	p := newBranchServer(t, "", "")
	out, err := p.run(t, "o/r", "pages", "gh-pages")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := `POST {"source":{"branch":"gh-pages","path":"/"}}`
	if len(p.posts) != 1 || p.posts[0] != want {
		t.Errorf("writes %v, want %s", p.posts, want)
	}
	if !strings.Contains(out, "serves gh-pages on o/r") {
		t.Errorf("no read-back line:\n%s", out)
	}
}

func TestPagesGhPagesAlreadyDoneWritesNothing(t *testing.T) {
	p := newBranchServer(t, "legacy", "gh-pages")
	out, err := p.run(t, "o/r", "pages", "gh-pages")
	if err != nil || p.writes != 0 || !strings.Contains(out, "nothing to do") {
		t.Fatalf("%v %d\n%s", err, p.writes, out)
	}
}

// An existing site with another source or build type is never changed.
func TestPagesGhPagesRefusesAnotherSite(t *testing.T) {
	for _, c := range []struct{ bt, branch string }{{"legacy", "main"}, {"workflow", ""}} {
		p := newBranchServer(t, c.bt, c.branch)
		if _, err := p.run(t, "o/r", "pages", "gh-pages"); err == nil || p.writes != 0 {
			t.Errorf("%+v: err=%v writes=%d", c, err, p.writes)
		}
	}
}

// The API said 201 and the site did not change: that is not "done".
func TestPagesGhPagesReadsTheStateBack(t *testing.T) {
	p := newBranchServer(t, "", "")
	p.refuse = true
	if out, err := p.run(t, "o/r", "pages", "gh-pages"); err == nil {
		t.Fatalf("a write that changed nothing was reported as done:\n%s", out)
	}
}

func TestPagesGhPagesDryRun(t *testing.T) {
	p := newBranchServer(t, "", "")
	out, err := p.run(t, "--dry-run", "o/r", "pages", "gh-pages")
	if err != nil || p.writes != 0 || !strings.Contains(out, "would create") {
		t.Fatalf("%v %d\n%s", err, p.writes, out)
	}
}
