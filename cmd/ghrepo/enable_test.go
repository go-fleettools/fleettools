package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type enableServer struct {
	*httptest.Server
	issues       bool
	noIssuesKey  bool
	refuseIssues bool
	actions      bool
	refuseAction bool
	states       map[int]string // workflow id -> state
	lie          int            // total_count to report, 0 = honest
	writes       []string
	stuck        bool           // an enabled workflow stays disabled_fork
	fail         map[string]int // "METHOD path" -> hit number answered with a 500
	empty        map[string]bool
	hits         map[string]int
}

func newEnableServer(t *testing.T) *enableServer {
	t.Helper()
	e := &enableServer{states: map[int]string{1: "disabled_fork", 2: "disabled_manually", 3: "active"}, hits: map[string]int{}}
	mux := http.NewServeMux()
	repo := func(w http.ResponseWriter) {
		k := fmt.Sprintf(`,"has_issues":%t`, e.issues)
		if e.noIssuesKey {
			k = ""
		}
		_, _ = io.WriteString(w, `{"full_name":"o/r","html_url":"https://github.com/o/r","default_branch":"main"`+k+`}`)
	}
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			b, _ := io.ReadAll(r.Body)
			var m map[string]bool
			_ = json.Unmarshal(b, &m)
			e.writes = append(e.writes, "PATCH has_issues="+fmt.Sprint(m["has_issues"]))
			if !e.refuseIssues {
				e.issues = m["has_issues"]
			}
		}
		repo(w)
	})
	mux.HandleFunc("/repos/o/r/actions/permissions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			e.writes = append(e.writes, "PUT permissions "+string(b))
			if !e.refuseAction {
				e.actions = true
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = fmt.Fprintf(w, `{"enabled":%t}`, e.actions)
	})
	mux.HandleFunc("/repos/o/r/actions/workflows", func(w http.ResponseWriter, r *http.Request) {
		total := len(e.states)
		if e.lie != 0 {
			total = e.lie
		}
		var ws []string
		for id := 1; id <= len(e.states) && r.URL.Query().Get("page") == "1"; id++ {
			ws = append(ws, fmt.Sprintf(`{"id":%d,"name":"w%d","state":%q}`, id, id, e.states[id]))
		}
		_, _ = fmt.Fprintf(w, `{"total_count":%d,"workflows":[%s]}`, total, strings.Join(ws, ","))
	})
	mux.HandleFunc("/repos/o/r/actions/workflows/", func(w http.ResponseWriter, r *http.Request) {
		var id int
		if _, err := fmt.Sscanf(r.URL.Path, "/repos/o/r/actions/workflows/%d/enable", &id); err != nil || r.Method != http.MethodPut {
			http.NotFound(w, r)
			return
		}
		e.writes = append(e.writes, fmt.Sprintf("PUT workflow %d", id))
		if !e.stuck {
			e.states[id] = "active"
		}
		w.WriteHeader(http.StatusNoContent)
	})
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		e.hits[key]++
		if e.fail[key] == e.hits[key] {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if e.empty[key] {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(e.Close)
	return e
}

// TestEnableTransportAndShapeFailures makes each request fail (on its nth hit)
// or answer with an empty object, and asserts a refusal every time, so that
// each error branch of the verbs is held by an assertion.
func TestEnableTransportAndShapeFailures(t *testing.T) {
	const perm, wfs = "/repos/o/r/actions/permissions", "/repos/o/r/actions/workflows"
	cases := []struct {
		name  string
		args  []string
		fail  map[string]int
		empty map[string]bool
	}{
		{"patch fails", []string{"issues", "enable"}, map[string]int{"PATCH /repos/o/r": 1}, nil},
		{"permissions GET fails", []string{"actions", "status"}, map[string]int{"GET " + perm: 1}, nil},
		{"permissions has no enabled", []string{"actions", "status"}, nil, map[string]bool{"GET " + perm: true}},
		{"workflows GET fails", []string{"actions", "status"}, map[string]int{"GET " + wfs: 1}, nil},
		{"workflows has no total", []string{"actions", "status"}, nil, map[string]bool{"GET " + wfs: true}},
		{"permissions PUT fails", []string{"actions", "enable"}, map[string]int{"PUT " + perm: 1}, nil},
		{"workflow PUT fails", []string{"actions", "enable"}, map[string]int{"PUT " + wfs + "/1/enable": 1}, nil},
		{"permissions read-back fails", []string{"actions", "enable"}, map[string]int{"GET " + perm: 2}, nil},
		{"workflows read-back fails", []string{"actions", "enable"}, map[string]int{"GET " + wfs: 2}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnableServer(t)
			e.fail, e.empty = c.fail, c.empty
			out, err := e.run(t, append([]string{"o/r"}, c.args...)...)
			if err == nil {
				t.Fatalf("accepted:\n%s", out)
			}
			// the refusal must be the one for this failure, not a later one
			want := "nothing proves"
			if c.fail != nil {
				want = "500"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("refused for another reason, want %q: %v", want, err)
			}
		})
	}
}

func TestActionsRefusesAWorkflowThatStaysDisabledFork(t *testing.T) {
	e := newEnableServer(t)
	e.stuck = true
	if out, err := e.run(t, "o/r", "actions", "enable"); err == nil || !strings.Contains(err.Error(), "still disabled_fork") {
		t.Fatalf("a workflow still disabled_fork was reported as enabled: %v\n%s", err, out)
	}
}

func (e *enableServer) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return exec(t, "", append([]string{"--base", e.URL, "--token-file", tokenFile(t, "SEKRIT")}, args...)...)
}

func TestIssuesEnableDisableStatus(t *testing.T) {
	e := newEnableServer(t)
	out, err := e.run(t, "o/r", "issues", "enable")
	if err != nil || len(e.writes) != 1 || e.writes[0] != "PATCH has_issues=true" || !strings.Contains(out, "has_issues=true on o/r") {
		t.Fatalf("%v %v\n%s", err, e.writes, out)
	}
	if out, err = e.run(t, "o/r", "issues", "enable"); err != nil || !strings.Contains(out, "nothing to do") || len(e.writes) != 1 {
		t.Fatalf("%v %v\n%s", err, e.writes, out)
	}
	if out, err = e.run(t, "o/r", "issues", "status"); err != nil || !strings.Contains(out, "has_issues=true") || len(e.writes) != 1 {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err = e.run(t, "o/r", "issues", "disable"); err != nil || e.writes[1] != "PATCH has_issues=false" {
		t.Fatalf("%v %v\n%s", err, e.writes, out)
	}
}

func TestIssuesDryRunRefusalsAndReadBack(t *testing.T) {
	e := newEnableServer(t)
	if out, err := e.run(t, "--dry-run", "o/r", "issues", "enable"); err != nil || !strings.Contains(out, "would enable issues") || len(e.writes) != 0 {
		t.Fatalf("%v %v\n%s", err, e.writes, out)
	}
	for _, arg := range []string{"on", "", "Enable"} {
		args := []string{"o/r", "issues"}
		if arg != "" {
			args = append(args, arg)
		}
		if _, err := e.run(t, args...); err == nil {
			t.Errorf("issues %q accepted", arg)
		}
	}
	e.refuseIssues = true
	if out, err := e.run(t, "o/r", "issues", "enable"); err == nil || !strings.Contains(err.Error(), "has_issues=true") {
		t.Fatalf("a write that changed nothing was reported as done: %v\n%s", err, out)
	}
	e2 := newEnableServer(t)
	e2.noIssuesKey = true
	if _, err := e2.run(t, "o/r", "issues", "status"); err == nil || !strings.Contains(err.Error(), "has_issues") {
		t.Errorf("a reply with no has_issues was read as a state: %v", err)
	}
}

func TestActionsEnableTurnsOnAndEnablesForkWorkflowsOnly(t *testing.T) {
	e := newEnableServer(t)
	out, err := e.run(t, "o/r", "actions", "enable")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := []string{`PUT permissions {"enabled":true}`, "PUT workflow 1"}
	if strings.Join(e.writes, "|") != strings.Join(want, "|") {
		t.Errorf("writes %v, want %v", e.writes, want)
	}
	if e.states[2] != "disabled_manually" {
		t.Errorf("a hand-disabled workflow was switched on: %v", e.states)
	}
	if !strings.Contains(out, "enabled=false") || !strings.Contains(out, "enabled=true on o/r") || !strings.Contains(out, "disabled_manually") {
		t.Errorf("state before and after not shown:\n%s", out)
	}
}

func TestActionsAlreadyOnWritesOnlyWhatIsNeeded(t *testing.T) {
	e := newEnableServer(t)
	e.actions = true
	e.states[1] = "active"
	if out, err := e.run(t, "o/r", "actions", "enable"); err != nil || len(e.writes) != 0 {
		t.Fatalf("%v %v\n%s", err, e.writes, out)
	}
}

func TestActionsStatusDryRunAndRefusals(t *testing.T) {
	e := newEnableServer(t)
	if out, err := e.run(t, "o/r", "actions", "status"); err != nil || !strings.Contains(out, "workflow 1") || len(e.writes) != 0 {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := e.run(t, "--dry-run", "o/r", "actions", "enable")
	if err != nil || !strings.Contains(out, "would enable Actions") || !strings.Contains(out, "would enable workflow 1") || len(e.writes) != 0 {
		t.Fatalf("%v %v\n%s", err, e.writes, out)
	}
	for _, arg := range []string{"disable", "on", ""} {
		args := []string{"o/r", "actions"}
		if arg != "" {
			args = append(args, arg)
		}
		if _, err := e.run(t, args...); err == nil {
			t.Errorf("actions %q accepted", arg)
		}
	}
}

func TestActionsReadsTheStateBackAndRefusesAnIncompleteList(t *testing.T) {
	e := newEnableServer(t)
	e.refuseAction = true
	if out, err := e.run(t, "o/r", "actions", "enable"); err == nil || !strings.Contains(err.Error(), "enabled=false") {
		t.Fatalf("a write that changed nothing was reported as done: %v\n%s", err, out)
	}
	e = newEnableServer(t)
	e.lie = 5
	if _, err := e.run(t, "o/r", "actions", "status"); err == nil || !strings.Contains(err.Error(), "3 workflows listed of 5") {
		t.Errorf("a short list was accepted: %v", err)
	}
	if len(e.writes) != 0 {
		t.Errorf("wrote on a list it could not trust: %v", e.writes)
	}
}
