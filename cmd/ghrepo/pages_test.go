package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pagesServer is a repository o/r whose Pages site has the build type in bt
// ("" is no site at all); a write changes it, unless refuse is set (the API
// accepted the call and the state did not change).
type pagesServer struct {
	*httptest.Server
	bt      string
	refuse  bool
	methods []string
	bodies  []string
}

func newPagesServer(t *testing.T, bt string) *pagesServer {
	t.Helper()
	p := &pagesServer{bt: bt}
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
			_, _ = io.WriteString(w, `{"build_type":"`+p.bt+`","html_url":"https://o.github.io/"}`)
		case http.MethodPut, http.MethodPost:
			p.methods, p.bodies = append(p.methods, r.Method), append(p.bodies, string(b))
			if !p.refuse {
				p.bt = "workflow"
			}
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

func (p *pagesServer) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return exec(t, "", append([]string{"--base", p.URL, "--token-file", tokenFile(t, "SEKRIT")}, args...)...)
}

func TestPagesWorkflowChangesALegacySite(t *testing.T) {
	p := newPagesServer(t, "legacy")
	out, err := p.run(t, "o/r", "pages", "workflow")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(p.methods) != 1 || p.methods[0] != http.MethodPut || p.bodies[0] != `{"build_type":"workflow"}` {
		t.Errorf("writes %v %v", p.methods, p.bodies)
	}
	if !strings.Contains(out, `build_type="legacy"`) || !strings.Contains(out, `build_type="workflow" on o/r`) {
		t.Errorf("the state before and after is not shown:\n%s", out)
	}
}

func TestPagesWorkflowCreatesASite(t *testing.T) {
	p := newPagesServer(t, "")
	if out, err := p.run(t, "o/r", "pages", "workflow"); err != nil || len(p.methods) != 1 || p.methods[0] != http.MethodPost {
		t.Fatalf("%v %v\n%s", err, p.methods, out)
	}
}

func TestPagesWorkflowAlreadyDoneWritesNothing(t *testing.T) {
	p := newPagesServer(t, "workflow")
	out, err := p.run(t, "o/r", "pages", "workflow")
	if err != nil || len(p.methods) != 0 || !strings.Contains(out, "nothing to do") {
		t.Fatalf("%v %v\n%s", err, p.methods, out)
	}
}

// The API said 204 and the site did not change: that is not "done".
func TestPagesWorkflowReadsTheStateBack(t *testing.T) {
	p := newPagesServer(t, "legacy")
	p.refuse = true
	out, err := p.run(t, "o/r", "pages", "workflow")
	if err == nil || !strings.Contains(err.Error(), `build_type="legacy"`) {
		t.Fatalf("a write that changed nothing was reported as done: %v\n%s", err, out)
	}
}

func TestPagesDryRunAndRefusals(t *testing.T) {
	p := newPagesServer(t, "legacy")
	if out, err := p.run(t, "--dry-run", "o/r", "pages", "workflow"); err != nil || len(p.methods) != 0 || !strings.Contains(out, "would change") {
		t.Fatalf("%v %v\n%s", err, p.methods, out)
	}
	for _, arg := range []string{"legacy", "", "Workflow"} {
		args := []string{"o/r", "pages"}
		if arg != "" {
			args = append(args, arg)
		}
		if _, err := p.run(t, args...); err == nil {
			t.Errorf("pages %q accepted", arg)
		}
	}
	if len(p.methods) != 0 {
		t.Errorf("a refused value was written: %v", p.methods)
	}
}
