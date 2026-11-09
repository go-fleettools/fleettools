package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tokenFile(t *testing.T, tok string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte(tok), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

type server struct {
	*httptest.Server
	auth, methods, paths, bodies []string
	status, conclusion           string
	dispatchCode                 int
	wfState                      string
	cancelCode                   int
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{status: "completed", conclusion: "failure", dispatchCode: 200, wfState: "active", cancelCode: 202}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/actions/workflows/ci.yml", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		_, _ = io.WriteString(w, `{"id":55,"name":"CI","path":".github/workflows/ci.yml","state":"`+s.wfState+`","html_url":"https://github.com/o/r/actions/workflows/ci.yml"}`)
	})
	mux.HandleFunc("/repos/o/r/actions/workflows/ci.yml/dispatches", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(s.dispatchCode)
		if s.dispatchCode == 200 {
			_, _ = io.WriteString(w, `{"workflow_run_id":99,"run_url":"u","html_url":"https://github.com/o/r/actions/runs/99"}`)
		}
	})
	mux.HandleFunc("/repos/o/r/actions/runs/7", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		_, _ = io.WriteString(w, `{"id":7,"name":"CI","head_branch":"main","head_sha":"abc123","status":"`+s.status+`","conclusion":"`+s.conclusion+`","html_url":"https://github.com/o/r/actions/runs/7"}`)
	})
	mux.HandleFunc("/repos/o/r/actions/runs/7/rerun", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		s.status, s.conclusion = "queued", ""
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/repos/o/r/actions/runs/7/rerun-failed-jobs", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		s.status, s.conclusion = "in_progress", ""
		w.WriteHeader(201)
	})
	mux.HandleFunc("/repos/o/r/actions/runs/7/cancel", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if s.cancelCode == 409 {
			w.WriteHeader(409)
			_, _ = io.WriteString(w, `{"message":"Cannot cancel a workflow run that is completed."}`)
			return
		}
		s.status = "completed"
		s.conclusion = "cancelled"
		w.WriteHeader(202)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *server) record(r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	s.auth = append(s.auth, r.Header.Get("Authorization"))
	s.methods = append(s.methods, r.Method)
	s.paths = append(s.paths, r.URL.Path)
	s.bodies = append(s.bodies, string(b))
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

func exec(t *testing.T, s *server, args ...string) (string, error) {
	t.Helper()
	cmd := RootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append(args, "--base", s.URL, "--token-file", tokenFile(t, "SEKRIT", 0o600)))
	err := cmd.Execute()
	return out.String(), err
}

func TestDispatchShowsWorkflowThenReportsRun(t *testing.T) {
	s := newServer(t)
	in := filepath.Join(t.TempDir(), "in.json")
	_ = os.WriteFile(in, []byte(`{"a":"file","n":3,"b":true}`), 0o644)
	out, err := exec(t, s, "o/r", "dispatch", "ci.yml", "--ref", "main", "--input", "a=flag", "--inputs-file", in)
	if err != nil {
		t.Fatal(err, out)
	}
	if !strings.Contains(out, `"CI"`) || !strings.Contains(out, "dispatched: run 99") {
		t.Errorf("output:\n%s", out)
	}
	last := s.bodies[len(s.bodies)-1]
	for _, want := range []string{`"ref":"main"`, `"a":"flag"`, `"n":"3"`, `"b":"true"`} {
		if !strings.Contains(last, want) {
			t.Errorf("body %s lacks %s", last, want)
		}
	}
	if s.methods[0] != "GET" || s.methods[len(s.methods)-1] != "POST" {
		t.Errorf("order %v: the read must come first", s.methods)
	}
	for i, a := range s.auth {
		if a != "Bearer SEKRIT" || strings.Contains(s.paths[i], "SEKRIT") {
			t.Errorf("request %d leaks or lacks the token", i)
		}
	}
}

func TestDispatch204SaysNoRunID(t *testing.T) {
	s := newServer(t)
	s.dispatchCode = 204
	out, err := exec(t, s, "o/r", "dispatch", "ci.yml", "--ref", "main")
	if err != nil || !strings.Contains(out, "no run id") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestDispatchDryRunSendsOnlyTheRead(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, s, "o/r", "dispatch", "ci.yml", "--ref", "main", "--dry-run")
	if err != nil || !strings.Contains(out, "nothing sent") || s.writes() != 0 {
		t.Fatalf("err=%v writes=%d out=%s", err, s.writes(), out)
	}
}

func TestDispatchRefusesDisabledWorkflow(t *testing.T) {
	s := newServer(t)
	s.wfState = "disabled_manually"
	_, err := exec(t, s, "o/r", "dispatch", "ci.yml", "--ref", "main")
	if err == nil || s.writes() != 0 {
		t.Fatalf("err=%v writes=%d", err, s.writes())
	}
}

func TestRerunDryRunAndReal(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, s, "o/r", "rerun", "7", "--dry-run")
	if err != nil || !strings.Contains(out, "abc123") || !strings.Contains(out, "completed/failure") || s.writes() != 0 {
		t.Fatalf("dry: err=%v writes=%d out=%s", err, s.writes(), out)
	}
	out, err = exec(t, s, "o/r", "rerun", "7")
	if err != nil || !strings.HasSuffix(s.paths[len(s.paths)-2], "/rerun") || !strings.Contains(out, "now queued") {
		t.Fatalf("err=%v paths=%v out=%s", err, s.paths, out)
	}
}

func TestRerunFailedUsesItsEndpoint(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, s, "o/r", "rerun", "7", "--failed")
	if err != nil || !strings.Contains(strings.Join(s.paths, " "), "/rerun-failed-jobs") || !strings.Contains(out, "now in_progress") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestRerunRefusals(t *testing.T) {
	s := newServer(t)
	s.status, s.conclusion = "in_progress", ""
	if _, err := exec(t, s, "o/r", "rerun", "7"); err == nil || s.writes() != 0 {
		t.Errorf("a running run was re-run: %v", err)
	}
	s.status, s.conclusion = "completed", "success"
	if _, err := exec(t, s, "o/r", "rerun", "7", "--failed"); err == nil || s.writes() != 0 {
		t.Errorf("--failed on a green run was sent: %v", err)
	}
}

func TestCancelDryRunAndReal(t *testing.T) {
	s := newServer(t)
	s.status, s.conclusion = "in_progress", ""
	out, err := exec(t, s, "o/r", "cancel", "7", "--dry-run")
	if err != nil || s.writes() != 0 || !strings.Contains(out, "would cancel") {
		t.Fatalf("dry: err=%v out=%s", err, out)
	}
	out, err = exec(t, s, "o/r", "cancel", "7")
	if err != nil || !strings.Contains(out, "now completed/cancelled") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func TestCancelRefusesCompletedLocally(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, s, "o/r", "cancel", "7"); err == nil || s.writes() != 0 {
		t.Fatalf("err=%v writes=%d", err, s.writes())
	}
}

func TestAPIMessageIsQuoted(t *testing.T) {
	s := newServer(t)
	s.status, s.conclusion = "in_progress", ""
	s.cancelCode = 409
	_, err := exec(t, s, "o/r", "cancel", "7")
	if err == nil || !strings.Contains(err.Error(), "Cannot cancel a workflow run") {
		t.Fatalf("err=%v", err)
	}
}

func TestLocalRefusalsSendNothing(t *testing.T) {
	s := newServer(t)
	cases := map[string][]string{
		"unknown verb":       {"o/r", "delete", "7"},
		"bad repo":           {"o", "rerun", "7"},
		"bad run id":         {"o/r", "rerun", "x"},
		"zero run id":        {"o/r", "cancel", "0"},
		"dispatch no ref":    {"o/r", "dispatch", "ci.yml"},
		"workflow path":      {"o/r", "dispatch", ".github/workflows/ci.yml", "--ref", "main"},
		"bad input":          {"o/r", "dispatch", "ci.yml", "--ref", "main", "--input", "novalue"},
		"failed on dispatch": {"o/r", "dispatch", "ci.yml", "--ref", "main", "--failed"},
		"failed on cancel":   {"o/r", "cancel", "7", "--failed"},
		"ref on rerun":       {"o/r", "rerun", "7", "--ref", "main"},
		"input on cancel":    {"o/r", "cancel", "7", "--input", "a=b"},
	}
	for name, args := range cases {
		if out, err := exec(t, s, args...); err == nil {
			t.Errorf("%s: accepted:\n%s", name, out)
		}
	}
	if len(s.methods) != 0 {
		t.Errorf("local refusals reached the server: %v", s.methods)
	}
}

func TestTooManyInputs(t *testing.T) {
	s := newServer(t)
	args := []string{"o/r", "dispatch", "ci.yml", "--ref", "main"}
	for i := 0; i < 26; i++ {
		args = append(args, "--input", "k"+strings.Repeat("x", i)+"=v")
	}
	if _, err := exec(t, s, args...); err == nil || len(s.methods) != 0 {
		t.Fatalf("err=%v", err)
	}
}

func TestInputsFileRefusals(t *testing.T) {
	s := newServer(t)
	for name, content := range map[string]string{"array": `[1]`, "nested": `{"a":{"b":1}}`, "junk": `x`} {
		p := filepath.Join(t.TempDir(), "in.json")
		_ = os.WriteFile(p, []byte(content), 0o644)
		if _, err := exec(t, s, "o/r", "dispatch", "ci.yml", "--ref", "main", "--inputs-file", p); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestTokenFileModeAndBase(t *testing.T) {
	s := newServer(t)
	cmd := RootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"o/r", "rerun", "7", "--base", s.URL, "--token-file", tokenFile(t, "x", 0o644)})
	if err := cmd.Execute(); err == nil {
		t.Error("world-readable token accepted")
	}
	cmd = RootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"o/r", "rerun", "7", "--base", "http://example.com", "--token-file", tokenFile(t, "x", 0o600)})
	if err := cmd.Execute(); err == nil {
		t.Error("plain http accepted")
	}
	if len(s.methods) != 0 {
		t.Error("request sent")
	}
}

func TestRedirectIsRefused(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer other.Close()
	red := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer red.Close()
	s := &server{Server: red}
	if _, err := exec(t, s, "o/r", "rerun", "7"); err == nil || hit {
		t.Fatalf("err=%v followed=%v", err, hit)
	}
}
