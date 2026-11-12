package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tokenFile(t *testing.T, tok string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte(tok), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func bodyFile(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

type server struct {
	*httptest.Server
	state, title                 string
	draft, merged                bool
	ignore                       bool // accept PATCH and ignore it
	gqlErr                       bool
	auth, methods, paths, bodies []string
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{state: "open", title: "a title"}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if r.Method == http.MethodPatch && !s.ignore {
			var in map[string]string
			_ = json.Unmarshal([]byte(s.bodies[len(s.bodies)-1]), &in)
			if v, ok := in["state"]; ok {
				s.state = v
			}
			if v, ok := in["title"]; ok {
				s.title = v
			}
		}
		d := "false"
		if s.draft {
			d = "true"
		}
		m := "false"
		if s.merged {
			m = "true"
		}
		_, _ = io.WriteString(w, `{"number":7,"state":"`+s.state+`","draft":`+d+`,"merged":`+m+`,"title":"`+s.title+
			`","html_url":"https://github.com/o/r/pull/7","node_id":"PR_node","head":{"ref":"feat","sha":"abc123"},"base":{"ref":"main"}}`)
	})
	mux.HandleFunc("/repos/o/r/pulls/7/update-branch", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"message":"Updating pull request branch.","url":"u"}`)
	})
	mux.HandleFunc("/repos/o/r/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		_, _ = io.WriteString(w, `{"state":"APPROVED","html_url":"https://github.com/o/r/pull/7#pullrequestreview-1"}`)
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if s.gqlErr {
			_, _ = io.WriteString(w, `{"errors":[{"message":"Pull request is not a draft"}]}`)
			return
		}
		s.draft = false
		_, _ = io.WriteString(w, `{"data":{"markPullRequestReadyForReview":{"pullRequest":{"isDraft":false}}}}`)
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
	cmd.SetArgs(append(args, "--base", s.URL, "--token-file", tokenFile(t, "SEKRIT")))
	err := cmd.Execute()
	return out.String(), err
}

func last(s *server) string { return s.bodies[len(s.bodies)-1] }

func TestShowsThePullRequestFirst(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, s, "o/r", "7", "close")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "o/r#7 [open] a title\n  feat -> main  https://github.com/o/r/pull/7\n") {
		t.Errorf("pull request not shown first:\n%s", out)
	}
	if s.methods[0] != "GET" {
		t.Errorf("first request %s, want GET", s.methods[0])
	}
}

func TestEditTitleAndBody(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, s, "o/r", "7", "edit", "--title", "new", "--body-file", bodyFile(t, "the `body`"))
	if err != nil {
		t.Fatal(err)
	}
	if last(s) != "{\"body\":\"the `body`\",\"title\":\"new\"}" {
		t.Errorf("sent %q", last(s))
	}
	if !strings.Contains(out, "now: [open] new") {
		t.Errorf("no reported state:\n%s", out)
	}
}

func TestEditTitleOnlyOmitsBody(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, s, "o/r", "7", "edit", "--title", "x"); err != nil {
		t.Fatal(err)
	}
	if last(s) != `{"title":"x"}` {
		t.Errorf("sent %q", last(s))
	}
}

func TestEditIgnoredPatchIsAnError(t *testing.T) {
	s := newServer(t)
	s.ignore = true
	if _, err := exec(t, s, "o/r", "7", "edit", "--title", "new"); err == nil {
		t.Fatal("an ignored edit was reported as done")
	}
}

func TestCloseAndReopen(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, s, "o/r", "7", "close")
	if err != nil || last(s) != `{"state":"closed"}` || !strings.Contains(out, "now: [closed]") {
		t.Fatalf("close: %v %q\n%s", err, last(s), out)
	}
	out, err = exec(t, s, "o/r", "7", "reopen")
	if err != nil || last(s) != `{"state":"open"}` || !strings.Contains(out, "now: [open]") {
		t.Fatalf("reopen: %v %q\n%s", err, last(s), out)
	}
}

func TestCloseIgnoredIsAnError(t *testing.T) {
	s := newServer(t)
	s.ignore = true
	if _, err := exec(t, s, "o/r", "7", "close"); err == nil {
		t.Fatal("an ignored close was reported as done")
	}
}

func TestRefusesWrongStateBeforeWriting(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*server)
		args  []string
		want  string
	}{
		{"close closed", func(s *server) { s.state = "closed" }, []string{"close"}, "already closed"},
		{"reopen open", func(s *server) {}, []string{"reopen"}, "not closed"},
		{"reopen merged", func(s *server) { s.state = "closed"; s.merged = true }, []string{"reopen"}, "merged"},
		{"ready not draft", func(s *server) {}, []string{"ready"}, "not a draft"},
		{"update closed", func(s *server) { s.state = "closed" }, []string{"update-branch"}, "closed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newServer(t)
			c.setup(s)
			_, err := exec(t, s, append([]string{"o/r", "7"}, c.args...)...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			if s.writes() != 0 {
				t.Errorf("wrote despite refusal: %v", s.methods)
			}
		})
	}
}

func TestReady(t *testing.T) {
	s := newServer(t)
	s.draft = true
	out, err := exec(t, s, "o/r", "7", "ready")
	if err != nil {
		t.Fatal(err)
	}
	var in map[string]any
	_ = json.Unmarshal([]byte(s.bodies[1]), &in)
	if s.paths[1] != "/graphql" || !strings.Contains(s.bodies[1], `"id":"PR_node"`) ||
		!strings.Contains(in["query"].(string), "markPullRequestReadyForReview") {
		t.Errorf("bad mutation: %s %s", s.paths[1], s.bodies[1])
	}
	if !strings.Contains(out, "now: [open]") || strings.Contains(out[strings.Index(out, "now:"):], "draft") {
		t.Errorf("did not report the re-read state:\n%s", out)
	}
}

func TestReadyGraphQLErrorIsAnError(t *testing.T) {
	s := newServer(t)
	s.draft, s.gqlErr = true, true
	_, err := exec(t, s, "o/r", "7", "ready")
	if err == nil || !strings.Contains(err.Error(), "not a draft") {
		t.Fatalf("err %v, want the GraphQL message", err)
	}
}

func TestUpdateBranchPinsTheHead(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, s, "o/r", "7", "update-branch")
	if err != nil {
		t.Fatal(err)
	}
	if s.methods[1] != "PUT" || s.paths[1] != "/repos/o/r/pulls/7/update-branch" || last(s) != `{"expected_head_sha":"abc123"}` {
		t.Errorf("sent %v %v %q", s.methods, s.paths, last(s))
	}
	if !strings.Contains(out, "Updating pull request branch.") {
		t.Errorf("output:\n%s", out)
	}
}

func TestReview(t *testing.T) {
	for flag, event := range map[string]string{"--approve": "APPROVE", "--comment": "COMMENT", "--request-changes": "REQUEST_CHANGES"} {
		s := newServer(t)
		out, err := exec(t, s, "o/r", "7", "review", flag, "--body-file", bodyFile(t, "looks fine"))
		if err != nil {
			t.Fatal(err)
		}
		var in map[string]string
		_ = json.Unmarshal([]byte(last(s)), &in)
		if in["event"] != event || in["commit_id"] != "abc123" || in["body"] != "looks fine" {
			t.Errorf("%s sent %q", flag, last(s))
		}
		if !strings.Contains(out, "review: APPROVED") {
			t.Errorf("no reported state:\n%s", out)
		}
	}
}

func TestApproveNeedsNoBody(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, s, "o/r", "7", "review", "--approve"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(last(s), `"body"`) {
		t.Errorf("sent a body: %q", last(s))
	}
}

func TestDryRunSendsOnlyTheRead(t *testing.T) {
	for _, args := range [][]string{
		{"close"}, {"update-branch"}, {"edit", "--title", "x"},
		{"review", "--approve"},
	} {
		s := newServer(t)
		out, err := exec(t, s, append([]string{"o/r", "7"}, append(args, "--dry-run")...)...)
		if err != nil {
			t.Fatal(err)
		}
		if s.writes() != 0 || len(s.methods) != 1 {
			t.Errorf("%v: dry run sent %v", args, s.methods)
		}
		if !strings.Contains(out, "o/r#7") {
			t.Errorf("%v: no PR shown", args)
		}
	}
	s := newServer(t)
	s.draft = true
	if _, err := exec(t, s, "o/r", "7", "ready", "--dry-run"); err != nil || len(s.methods) != 1 {
		t.Errorf("ready dry run: %v %v", err, s.methods)
	}
}

func TestRefusalsSendNothing(t *testing.T) {
	long := strings.Repeat("x", 257)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown verb", []string{"o/r", "7", "merge"}, "verb"},
		{"bad number", []string{"o/r", "x", "close"}, "positive"},
		{"bad repo", []string{"orr", "7", "close"}, "owner/repo"},
		{"edit nothing", []string{"o/r", "7", "edit"}, "nothing to change"},
		{"multiline title", []string{"o/r", "7", "edit", "--title", "a\nb"}, "one line"},
		{"long title", []string{"o/r", "7", "edit", "--title", long}, "over 256"},
		{"empty title", []string{"o/r", "7", "edit", "--title", " "}, "empty"},
		{"title on close", []string{"o/r", "7", "close", "--title", "x"}, "applies to edit"},
		{"body on close", []string{"o/r", "7", "close", "--body-file", "f"}, "applies to edit and review"},
		{"approve on close", []string{"o/r", "7", "close", "--approve"}, "apply to review"},
		{"review no event", []string{"o/r", "7", "review"}, "exactly one"},
		{"review two events", []string{"o/r", "7", "review", "--approve", "--comment"}, "exactly one"},
		{"comment no body", []string{"o/r", "7", "review", "--comment"}, "need --body-file"},
		{"request-changes no body", []string{"o/r", "7", "review", "--request-changes"}, "need --body-file"},
		{"missing body file", []string{"o/r", "7", "edit", "--body-file", "/nonexistent/x"}, "body file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newServer(t)
			_, err := exec(t, s, c.args...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			if len(s.methods) != 0 {
				t.Errorf("sent %v", s.methods)
			}
		})
	}
}

func TestEmptyBodyFileRefused(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, s, "o/r", "7", "edit", "--body-file", bodyFile(t, "  \n")); err == nil || len(s.methods) != 0 {
		t.Fatalf("err %v methods %v", err, s.methods)
	}
}

func TestTokenOnlyInTheHeader(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, s, "o/r", "7", "close"); err != nil {
		t.Fatal(err)
	}
	for i, a := range s.auth {
		if a != "Bearer SEKRIT" {
			t.Errorf("request %d auth %q", i, a)
		}
	}
	for _, x := range append(s.paths, s.bodies...) {
		if strings.Contains(x, "SEKRIT") {
			t.Errorf("token leaked: %q", x)
		}
	}
}

func TestTokenFileMustBePrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tok")
	_ = os.WriteFile(p, []byte("x"), 0o644)
	if _, err := readToken(p); err == nil {
		t.Fatal("world-readable token accepted")
	}
}

func TestPlainHTTPRefusedExceptLoopback(t *testing.T) {
	cmd := RootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"o/r", "7", "close", "--base", "http://example.com", "--token-file", tokenFile(t, "x")})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "must be https") {
		t.Fatalf("err %v", err)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer srv.Close()
	cmd := RootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"o/r", "7", "close", "--base", srv.URL, "--token-file", tokenFile(t, "x")})
	if err := cmd.Execute(); err == nil || hit {
		t.Fatalf("err %v, redirect followed %v", err, hit)
	}
}

func TestAPIErrorIsQuoted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"Can not approve your own pull request"}`)
	}))
	defer srv.Close()
	cmd := RootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"o/r", "7", "close", "--base", srv.URL, "--token-file", tokenFile(t, "x")})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "Can not approve") {
		t.Fatalf("err %v", err)
	}
}

func TestParityGateOnBodiesAndTitles(t *testing.T) {
	parked := "We do not use FMA in the default kernel because it would break bit-parity.\n"
	s := newServer(t)
	_, err := exec(t, s, "o/r", "7", "edit", "--body-file", bodyFile(t, parked))
	if err == nil || !strings.Contains(err.Error(), "go-fft/fft#121") || s.writes() != 0 {
		t.Fatalf("a parked body was not refused: %v", err)
	}
	if _, err := exec(t, s, "o/r", "7", "edit", "--title", "perf: SIMD kernel skipped because it breaks bit-parity", "--parity-waiver", "why"); err == nil || !strings.Contains(err.Error(), "no waiver covers a title") {
		t.Fatalf("a parked title: %v", err)
	}
	if _, err := exec(t, s, "o/r", "7", "edit", "--body-file", bodyFile(t, "Output unchanged, bit-identical.\n")); err != nil {
		t.Fatalf("a mention was refused: %v", err)
	}
	if _, err := exec(t, s, "o/r", "7", "edit", "--body-file", bodyFile(t, parked), "--parity-waiver", "callers need identical bits"); err != nil {
		t.Fatalf("the waiver did not let it through: %v", err)
	}
	if !strings.Contains(last(s), "bit-parity waiver") || !strings.Contains(last(s), "callers need identical bits") {
		t.Errorf("the reason was not stamped: %s", last(s))
	}
}
