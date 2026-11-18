package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type pvrServer struct {
	*httptest.Server
	enabled bool
	refuse  bool
	deny    int // status for every private-vulnerability-reporting call, 0 = none
	methods []string
}

func newPVRServer(t *testing.T, enabled bool) *pvrServer {
	t.Helper()
	p := &pvrServer{enabled: enabled}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"full_name":"o/r","html_url":"https://github.com/o/r","default_branch":"main"}`)
	})
	mux.HandleFunc("/repos/o/r/private-vulnerability-reporting", func(w http.ResponseWriter, r *http.Request) {
		if p.deny != 0 {
			w.WriteHeader(p.deny)
			_, _ = io.WriteString(w, `{"message":"Must have admin rights to Repository."}`)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if p.enabled {
				_, _ = io.WriteString(w, `{"enabled":true}`)
			} else {
				_, _ = io.WriteString(w, `{"enabled":false}`)
			}
		case http.MethodPut, http.MethodDelete:
			p.methods = append(p.methods, r.Method)
			if !p.refuse {
				p.enabled = r.Method == http.MethodPut
			}
			w.WriteHeader(http.StatusNoContent)
		}
	})
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

func (p *pvrServer) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return exec(t, "", append([]string{"--base", p.URL, "--token-file", tokenFile(t, "SEKRIT")}, args...)...)
}

func TestPrivateReportingEnableAndDisable(t *testing.T) {
	p := newPVRServer(t, false)
	out, err := p.run(t, "o/r", "private-reporting", "enable")
	if err != nil || len(p.methods) != 1 || p.methods[0] != http.MethodPut {
		t.Fatalf("%v %v\n%s", err, p.methods, out)
	}
	if !strings.Contains(out, "o/r  https://github.com/o/r") || !strings.Contains(out, "enabled=false") || !strings.Contains(out, "enabled=true on o/r") {
		t.Errorf("repository, state before and after not shown:\n%s", out)
	}
	out, err = p.run(t, "o/r", "private-reporting", "disable")
	if err != nil || len(p.methods) != 2 || p.methods[1] != http.MethodDelete || !strings.Contains(out, "enabled=false on o/r") {
		t.Fatalf("%v %v\n%s", err, p.methods, out)
	}
}

func TestPrivateReportingStatusAndNoop(t *testing.T) {
	p := newPVRServer(t, true)
	if out, err := p.run(t, "o/r", "private-reporting", "status"); err != nil || !strings.Contains(out, "enabled=true") {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := p.run(t, "o/r", "private-reporting", "enable"); err != nil || !strings.Contains(out, "nothing to do") {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(p.methods) != 0 {
		t.Errorf("wrote: %v", p.methods)
	}
}

func TestPrivateReportingDryRunAndRefusals(t *testing.T) {
	p := newPVRServer(t, false)
	if out, err := p.run(t, "--dry-run", "o/r", "private-reporting", "enable"); err != nil || !strings.Contains(out, "would enable") {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, arg := range []string{"on", "", "Enable"} {
		args := []string{"o/r", "private-reporting"}
		if arg != "" {
			args = append(args, arg)
		}
		if _, err := p.run(t, args...); err == nil {
			t.Errorf("private-reporting %q accepted", arg)
		}
	}
	if len(p.methods) != 0 {
		t.Errorf("wrote: %v", p.methods)
	}
}

func TestPrivateReportingReadsTheStateBack(t *testing.T) {
	p := newPVRServer(t, false)
	p.refuse = true
	if out, err := p.run(t, "o/r", "private-reporting", "enable"); err == nil || !strings.Contains(err.Error(), "enabled=false") {
		t.Fatalf("a write that changed nothing was reported as done: %v\n%s", err, out)
	}
}

// No admin rights: the API's answer is reported plainly, never as success.
func TestPrivateReportingDenied(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound} {
		p := newPVRServer(t, false)
		p.deny = code
		out, err := p.run(t, "o/r", "private-reporting", "enable")
		if err == nil || !strings.Contains(err.Error(), http.StatusText(code)) || !strings.Contains(err.Error(), "admin rights") {
			t.Errorf("%d: %v\n%s", code, err, out)
		}
		if len(p.methods) != 0 {
			t.Errorf("%d: wrote %v", code, p.methods)
		}
	}
}
