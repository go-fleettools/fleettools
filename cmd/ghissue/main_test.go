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

// tokenFile writes a token with owner-only mode, which readToken insists on.
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

// server stands in for the API and records what reached it.
type server struct {
	*httptest.Server
	auth    []string
	methods []string
	paths   []string
	bodies  []string
	state   string
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{state: "open"}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues/7/comments", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"html_url":"https://github.com/o/r/issues/7#issuecomment-1"}`)
	})
	mux.HandleFunc("/repos/o/r/issues/7", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if r.Method == http.MethodPatch {
			var in map[string]string
			_ = json.Unmarshal([]byte(s.bodies[len(s.bodies)-1]), &in)
			s.state = in["state"]
		}
		_, _ = io.WriteString(w, `{"number":7,"state":"`+s.state+`","title":"a title","html_url":"https://github.com/o/r/issues/7"}`)
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

func exec(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := RootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestPostsTheCommentAndShowsTheIssueFirst(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, "o/r", "7", "--base", s.URL,
		"--token-file", tokenFile(t, "SEKRIT"), "--body-file", bodyFile(t, "hello"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "o/r#7 [open] a title") {
		t.Errorf("output does not show the issue first:\n%s", out)
	}
	if !strings.Contains(out, "issuecomment-1") {
		t.Errorf("output does not report where the comment landed:\n%s", out)
	}
	if got := s.bodies[len(s.bodies)-1]; got != `{"body":"hello"}` {
		t.Errorf("sent %q, want the file's bytes as the body field", got)
	}
}

// ⛔ The token goes in the header and NOWHERE else. A token in a path or a
// query is in every proxy log between here and GitHub.
func TestTokenTravelsOnlyInTheHeader(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, "o/r", "7", "--base", s.URL,
		"--token-file", tokenFile(t, "SEKRIT"), "--body-file", bodyFile(t, "hi")); err != nil {
		t.Fatal(err)
	}
	for i, a := range s.auth {
		if a != "Bearer SEKRIT" {
			t.Errorf("request %d: Authorization = %q", i, a)
		}
	}
	for i, p := range s.paths {
		if strings.Contains(p, "SEKRIT") {
			t.Errorf("request %d: the token is in the PATH %q", i, p)
		}
	}
	for i, b := range s.bodies {
		if strings.Contains(b, "SEKRIT") {
			t.Errorf("request %d: the token is in the BODY %q", i, b)
		}
	}
}

func TestRefusesATokenFileAnyoneCanRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(p, []byte("SEKRIT"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := exec(t, "o/r", "7", "--base", "https://example.invalid", "--token-file", p,
		"--body-file", bodyFile(t, "hi"))
	if err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Errorf("err = %v, want a refusal naming the mode", err)
	}
}

func TestCloseSendsTheStateAndReportsWhatCameBack(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, "o/r", "7", "--base", s.URL, "--token-file", tokenFile(t, "k"),
		"--body-file", bodyFile(t, "done"), "--close", "--reason", "completed")
	if err != nil {
		t.Fatal(err)
	}
	var sawPatch bool
	for i, m := range s.methods {
		if m == http.MethodPatch {
			sawPatch = true
			if !strings.Contains(s.bodies[i], `"state":"closed"`) ||
				!strings.Contains(s.bodies[i], `"state_reason":"completed"`) {
				t.Errorf("PATCH body = %q, want state and state_reason", s.bodies[i])
			}
		}
	}
	if !sawPatch {
		t.Fatal("no PATCH was sent")
	}
	// ⛔ The state printed is the one the API REPORTS, not the one asked for: a
	// PATCH that is accepted and ignored looks exactly like one that worked.
	if !strings.Contains(out, "state: closed") {
		t.Errorf("output does not report the state that came back:\n%s", out)
	}
}

// --reason without --close is refused rather than dropped: GitHub ignores
// state_reason unless the state changes, and a caller who typed a reason would
// otherwise believe it was recorded.
func TestReasonWithoutCloseIsRefused(t *testing.T) {
	_, err := exec(t, "o/r", "7", "--base", "https://example.invalid",
		"--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, "x"), "--reason", "completed")
	if err == nil || !strings.Contains(err.Error(), "--close") {
		t.Errorf("err = %v, want a refusal pointing at --close", err)
	}
}

func TestRefusesAnUnknownReason(t *testing.T) {
	_, err := exec(t, "o/r", "7", "--base", "https://example.invalid",
		"--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, "x"),
		"--close", "--reason", "because")
	if err == nil || !strings.Contains(err.Error(), "not_planned") {
		t.Errorf("err = %v, want a refusal listing the allowed reasons", err)
	}
}

func TestDryRunSendsNothingBeyondTheRead(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, "o/r", "7", "--base", s.URL, "--token-file", tokenFile(t, "k"),
		"--body-file", bodyFile(t, "would say this"), "--close", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range s.methods {
		if m != http.MethodGet {
			t.Errorf("a %s was sent during --dry-run", m)
		}
	}
	if !strings.Contains(out, "would say this") || !strings.Contains(out, "would close it") {
		t.Errorf("--dry-run does not show what it would do:\n%s", out)
	}
}

func TestRefusesAnEmptyBodyAndNothingToDo(t *testing.T) {
	if _, err := exec(t, "o/r", "7", "--base", "https://example.invalid",
		"--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, "   \n")); err == nil {
		t.Error("an all-whitespace body was accepted; it notifies everyone watching and says nothing")
	}
	if _, err := exec(t, "o/r", "7", "--base", "https://example.invalid",
		"--token-file", tokenFile(t, "k")); err == nil {
		t.Error("no body, no --close, no --reopen was accepted")
	}
}

func TestRefusesCloseAndReopenTogether(t *testing.T) {
	_, err := exec(t, "o/r", "7", "--base", "https://example.invalid",
		"--token-file", tokenFile(t, "k"), "--close", "--reopen")
	if err == nil || !strings.Contains(err.Error(), "opposites") {
		t.Errorf("err = %v, want a refusal", err)
	}
}

func TestRefusesABaseThatIsNotHTTPS(t *testing.T) {
	_, err := exec(t, "o/r", "7", "--base", "http://example.com",
		"--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, "x"))
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("err = %v, want a refusal: the token would go in clear", err)
	}
}

func TestSplitRepoAndNumber(t *testing.T) {
	if _, _, err := splitRepo("noslash"); err == nil {
		t.Error("accepted a repository with no owner")
	}
	if _, _, err := splitRepo("a/b/c"); err == nil {
		t.Error("accepted a three-part path")
	}
	o, r, err := splitRepo(" go-tex/engine ")
	if err != nil || o != "go-tex" || r != "engine" {
		t.Errorf("splitRepo = %q,%q,%v", o, r, err)
	}
	if _, err := exec(t, "o/r", "zero", "--token-file", tokenFile(t, "k")); err == nil {
		t.Error("accepted a non-numeric issue number")
	}
}

// repoServer stands in for the repository and issue-creation endpoints.
func repoServer(t *testing.T, hasIssues bool) *server {
	t.Helper()
	s := &server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		has := "false"
		if hasIssues {
			has = "true"
		}
		_, _ = io.WriteString(w, `{"full_name":"o/r","html_url":"https://github.com/o/r","has_issues":`+has+`}`)
	})
	mux.HandleFunc("/repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"number":42,"state":"open","title":"t","html_url":"https://github.com/o/r/issues/42"}`)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestNewOpensAnIssueAndReportsWhatGitHubGave(t *testing.T) {
	s := repoServer(t, true)
	out, err := exec(t, "o/r", "--new", "--title", "BitLocker backend", "--base", s.URL,
		"--token-file", tokenFile(t, "SEKRIT"), "--body-file", bodyFile(t, "why"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.methods) != 2 || s.methods[0] != http.MethodGet || s.methods[1] != http.MethodPost {
		t.Fatalf("requests = %v, want the repository read THEN one POST", s.methods)
	}
	var sent map[string]string
	if err := json.Unmarshal([]byte(s.bodies[1]), &sent); err != nil {
		t.Fatal(err)
	}
	// The file's bytes, then the prior-art stamp (priorgate.go).
	if sent["title"] != "BitLocker backend" || !strings.HasPrefix(sent["body"], "why\n\nPrior art: `priorart` \"the test subject\"") ||
		!strings.HasSuffix(sent["body"], "found none.\n") {
		t.Errorf("sent %v, want the title flag, the file's bytes and the prior-art stamp", sent)
	}
	if !strings.Contains(out, "o/r  https://github.com/o/r") {
		t.Errorf("output does not show the repository first:\n%s", out)
	}
	if !strings.Contains(out, "created: o/r#42 https://github.com/o/r/issues/42") {
		t.Errorf("output does not report the number GitHub gave:\n%s", out)
	}
	for i, a := range s.auth {
		if a != "Bearer SEKRIT" {
			t.Errorf("request %d: Authorization = %q", i, a)
		}
	}
}

func TestNewRefusesARepositoryWithIssuesOff(t *testing.T) {
	s := repoServer(t, false)
	_, err := exec(t, "o/r", "--new", "--title", "t", "--base", s.URL,
		"--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, "b"))
	if err == nil || !strings.Contains(err.Error(), "issues turned off") {
		t.Errorf("err = %v, want a refusal", err)
	}
	for _, m := range s.methods {
		if m != http.MethodGet {
			t.Errorf("a %s was sent to a repository with issues off", m)
		}
	}
}

func TestNewDryRunSendsOnlyTheRead(t *testing.T) {
	s := repoServer(t, true)
	out, err := exec(t, "o/r", "--new", "--title", "t", "--dry-run", "--base", s.URL,
		"--token-file", tokenFile(t, "k"), "--body-file", bodyFile(t, "would file this"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range s.methods {
		if m != http.MethodGet {
			t.Errorf("a %s was sent during --dry-run", m)
		}
	}
	if !strings.Contains(out, "would file this") {
		t.Errorf("--dry-run does not show the body:\n%s", out)
	}
}

// Every refusal below happens before a token is read or a request is sent: the
// token file given does not exist, so reaching it would fail differently.
func TestNewRefusesBeforeSending(t *testing.T) {
	long := strings.Repeat("é", maxTitle+1)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"number given", []string{"o/r", "7", "--new", "--title", "t", "--body-file", bodyFile(t, "b")}, "number is GitHub's"},
		{"no title", []string{"o/r", "--new", "--body-file", bodyFile(t, "b")}, "--title"},
		{"two-line title", []string{"o/r", "--new", "--title", "a\nb", "--body-file", bodyFile(t, "b")}, "one line"},
		{"long title", []string{"o/r", "--new", "--title", long, "--body-file", bodyFile(t, "b")}, "over 256"},
		{"no body", []string{"o/r", "--new", "--title", "t"}, "--body-file"},
		{"empty body", []string{"o/r", "--new", "--title", "t", "--body-file", bodyFile(t, " \n")}, "empty"},
		{"with close", []string{"o/r", "--new", "--title", "t", "--close", "--body-file", bodyFile(t, "b")}, "existing one"},
		{"title without new", []string{"o/r", "7", "--title", "t", "--body-file", bodyFile(t, "b")}, "applies to --new"},
		{"number forgotten", []string{"o/r", "--body-file", bodyFile(t, "b")}, "--new to open"},
		{"plain http", []string{"o/r", "--new", "--title", "t", "--base", "http://example.com", "--body-file", bodyFile(t, "b")}, "https"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append(c.args, "--token-file", filepath.Join(t.TempDir(), "absent"))
			_, err := exec(t, args...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}

// A failure carries the API's own words: a bare status sends the reader to the
// wrong cause, and GitHub says which one it is in the body.
func TestAFailureQuotesTheAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
	}))
	defer srv.Close()
	_, err := exec(t, "o/r", "7", "--base", srv.URL, "--token-file", tokenFile(t, "k"),
		"--body-file", bodyFile(t, "x"))
	if err == nil || !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("err = %v, want the API's own message", err)
	}
}
