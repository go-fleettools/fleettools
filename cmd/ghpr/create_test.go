package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cserver is a GitHub that knows one repository, o/r, and records every request.
type cserver struct {
	*httptest.Server
	repo, compare, pulls, created string // reply bodies; "" means the default
	createStatus                  int
	methods, paths, queries, auth []string
	bodies                        []string
}

func newCreateServer(t *testing.T) *cserver {
	t.Helper()
	s := &cserver{createStatus: http.StatusCreated}
	pick := func(v, def string) string {
		if v != "" {
			return v
		}
		return def
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.methods = append(s.methods, r.Method)
		s.paths = append(s.paths, r.URL.Path)
		s.queries = append(s.queries, r.URL.RawQuery)
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.bodies = append(s.bodies, string(b))
		switch {
		case r.URL.Path == "/repos/o/r":
			_, _ = io.WriteString(w, pick(s.repo, `{"full_name":"o/r","html_url":"https://github.com/o/r","default_branch":"main"}`))
		case strings.HasPrefix(r.URL.Path, "/repos/o/r/compare/"):
			_, _ = io.WriteString(w, pick(s.compare, `{"ahead_by":2,"status":"ahead"}`))
		case r.URL.Path == "/repos/o/r/pulls" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, pick(s.pulls, `[]`))
		case r.URL.Path == "/repos/o/r/pulls" && r.Method == http.MethodPost:
			w.WriteHeader(s.createStatus)
			var in struct {
				Draft bool `json:"draft"`
			}
			_ = json.Unmarshal(b, &in)
			d := "false"
			if in.Draft {
				d = "true"
			}
			_, _ = io.WriteString(w, pick(s.created, `{"number":42,"draft":`+d+`,"html_url":"https://github.com/o/r/pull/42"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *cserver) writes() int {
	n := 0
	for _, m := range s.methods {
		if m != http.MethodGet {
			n++
		}
	}
	return n
}

func (s *cserver) create(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := RootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"o/r", "create"}, append(args, "--base", s.URL, "--token-file", tokenFile(t, "SEKRIT"))...))
	err := cmd.Execute()
	return out.String(), err
}

func good(t *testing.T) []string {
	t.Helper()
	return []string{"--head", "feat", "--title", "add a thing", "--body-file", bodyFile(t, "Adds a thing.\n\nCloses #3\n")}
}

func TestCreateOpensThePullRequest(t *testing.T) {
	s := newCreateServer(t)
	out, err := s.create(t, good(t)...)
	if err != nil {
		t.Fatal(err, out)
	}
	if !strings.HasPrefix(out, "o/r  https://github.com/o/r\nhead: feat -> main\n2 commit(s) ahead of main\n") {
		t.Errorf("the repository and head are not shown first:\n%s", out)
	}
	if !strings.Contains(out, "created: o/r#42 https://github.com/o/r/pull/42") {
		t.Errorf("the API's number and URL are not printed:\n%s", out)
	}
	if s.writes() != 1 || s.methods[len(s.methods)-1] != http.MethodPost {
		t.Fatalf("want exactly one POST, last: %v", s.methods)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(s.bodies[len(s.bodies)-1]), &sent)
	if sent["head"] != "feat" || sent["base"] != "main" || sent["title"] != "add a thing" || sent["draft"] != false ||
		!strings.Contains(sent["body"].(string), "Closes #3") {
		t.Errorf("sent %v", sent)
	}
	for _, a := range s.auth {
		if a != "Bearer SEKRIT" {
			t.Errorf("Authorization %q", a)
		}
	}
	for i, p := range s.paths {
		if strings.Contains(p, "SEKRIT") || strings.Contains(s.queries[i], "SEKRIT") {
			t.Errorf("the token reached a URL: %s?%s", p, s.queries[i])
		}
	}
}

func TestCreateBaseBranchDraftAndSlashedHead(t *testing.T) {
	s := newCreateServer(t)
	args := append(good(t), "--base-branch", "release/1", "--draft")
	args[1] = "feat/x"
	out, err := s.create(t, args...)
	if err != nil {
		t.Fatal(err, out)
	}
	if !strings.Contains(out, "head: feat/x -> release/1") {
		t.Errorf("%s", out)
	}
	if !strings.Contains(strings.Join(s.paths, " "), "/repos/o/r/compare/release/1...feat/x") {
		t.Errorf("compare path: %v", s.paths)
	}
	if !strings.Contains(s.queries[2], "head=o%3Afeat%2Fx") {
		t.Errorf("the open-pull lookup is not qualified with the owner: %v", s.queries)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(s.bodies[len(s.bodies)-1]), &sent)
	if sent["base"] != "release/1" || sent["draft"] != true {
		t.Errorf("sent %v", sent)
	}
}

func TestCreateForkHeadIsNotRequalified(t *testing.T) {
	s := newCreateServer(t)
	args := good(t)
	args[1] = "someone:feat"
	if _, err := s.create(t, args...); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.queries[2], "head=someone%3Afeat") {
		t.Errorf("%v", s.queries)
	}
}

func TestCreateRefusesAHeadWithNothingAhead(t *testing.T) {
	s := newCreateServer(t)
	s.compare = `{"ahead_by":0,"status":"identical"}`
	_, err := s.create(t, good(t)...)
	if err == nil || !strings.Contains(err.Error(), "no commits ahead of main") || s.writes() != 0 {
		t.Fatalf("err %v, writes %d", err, s.writes())
	}
}

func TestCreateRefusesAHeadThatAlreadyHasAPullRequest(t *testing.T) {
	s := newCreateServer(t)
	s.pulls = `[{"number":9,"html_url":"https://github.com/o/r/pull/9"}]`
	_, err := s.create(t, good(t)...)
	if err == nil || !strings.Contains(err.Error(), "o/r#9") || s.writes() != 0 {
		t.Fatalf("err %v, writes %d", err, s.writes())
	}
}

func TestCreateDryRunSendsOnlyReads(t *testing.T) {
	for _, draft := range []bool{false, true} {
		s := newCreateServer(t)
		args := append(good(t), "--dry-run")
		if draft {
			args = append(args, "--draft")
		}
		out, err := s.create(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		if s.writes() != 0 {
			t.Errorf("--dry-run wrote: %v", s.methods)
		}
		if !strings.Contains(out, "NOT sent") || !strings.Contains(out, "title: add a thing") || !strings.Contains(out, "Adds a thing.") {
			t.Errorf("%s", out)
		}
		if draft != strings.Contains(out, "(draft)") {
			t.Errorf("draft %v: %s", draft, out)
		}
	}
}

func TestCreateRefusalsSendNothing(t *testing.T) {
	body := bodyFile(t, "Closes #3\n")
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no head":           {[]string{"--title", "t", "--body-file", body}, "needs --head"},
		"head with space":   {[]string{"--head", "a b", "--title", "t", "--body-file", body}, "want branch or owner:branch"},
		"head leading ':'":  {[]string{"--head", ":b", "--title", "t", "--body-file", body}, "want branch or owner:branch"},
		"head trailing ':'": {[]string{"--head", "o:", "--title", "t", "--body-file", body}, "want branch or owner:branch"},
		"bad base branch":   {[]string{"--head", "h", "--base-branch", "a b", "--title", "t", "--body-file", body}, "not a branch name"},
		"no title":          {[]string{"--head", "h", "--body-file", body}, "needs --title"},
		"empty title":       {[]string{"--head", "h", "--title", " ", "--body-file", body}, "empty"},
		"two-line title":    {[]string{"--head", "h", "--title", "a\nb", "--body-file", body}, "one line"},
		"long title":        {[]string{"--head", "h", "--title", strings.Repeat("x", 257), "--body-file", body}, "over 256"},
		"no body":           {[]string{"--head", "h", "--title", "t"}, "needs --body-file"},
		"missing body":      {[]string{"--head", "h", "--title", "t", "--body-file", "/nonexistent/b.md"}, "body file"},
		"review flag":       {[]string{"--head", "h", "--title", "t", "--body-file", body, "--approve"}, "apply to review"},
		"bare reference":    {[]string{"--head", "h", "--title", "t", "--body-file", bodyFile(t, "see #5 and Closes #6, #7")}, "o/r#5, o/r#7"},
		"title parity":      {[]string{"--head", "h", "--title", "perf: SIMD kernel skipped because it breaks bit-parity", "--body-file", body}, "no waiver covers a title"},
		"body private": {[]string{"--head", "h", "--title", "t", "--body-file",
			bodyFile(t, "see `/Users/alicia/Documents/thesis/main.tex`\n")}, "own file system"},
		"title private": {[]string{"--head", "h", "--title", "crash in /Users/alicia/Documents/thesis/main.tex", "--body-file", body}, "own file system"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newCreateServer(t)
			out, err := s.create(t, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			if len(s.methods) != 0 {
				t.Errorf("something reached the API before the refusal: %v", s.paths)
			}
			if strings.Contains(err.Error()+out, "alicia") {
				t.Errorf("the refusal published the path")
			}
		})
	}
}

func TestCreateDispositionsAcceptedForms(t *testing.T) {
	for _, body := range []string{
		"no issue mentioned",
		"Refs go-x/y#1, #2 and #3\n",
		"Closes #3\n",
		"`Closes #9` is only code\n",
	} {
		s := newCreateServer(t)
		args := []string{"--head", "h", "--title", "t", "--body-file", bodyFile(t, body)}
		if _, err := s.create(t, args...); err != nil && strings.Contains(err.Error(), "without saying") {
			t.Errorf("%q refused: %v", body, err)
		}
	}
}

func TestCreatePrivatePathWaiverIsStamped(t *testing.T) {
	s := newCreateServer(t)
	body := bodyFile(t, "the loader mangles `/Users/alicia/Documents/thesis/main.tex`\n")
	if _, err := s.create(t, "--head", "h", "--title", "t", "--body-file", body,
		"--allow-private-path", "the path is the defect"); err != nil {
		t.Fatal(err)
	}
	if sent := s.bodies[len(s.bodies)-1]; !strings.Contains(sent, "express waiver") || !strings.Contains(sent, "the path is the defect") {
		t.Errorf("not stamped: %s", sent)
	}
}

func TestCreateParityWaiverIsStamped(t *testing.T) {
	s := newCreateServer(t)
	parked := bodyFile(t, "We do not use FMA in the default kernel because it would break bit-parity.\n")
	if _, err := s.create(t, "--head", "h", "--title", "t", "--body-file", parked); err == nil || s.writes() != 0 {
		t.Fatalf("a parked body was not refused: %v", err)
	}
	if _, err := s.create(t, "--head", "h", "--title", "t", "--body-file", parked, "--parity-waiver", "callers need identical bits"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.bodies[len(s.bodies)-1], "callers need identical bits") {
		t.Errorf("not stamped")
	}
}

func TestCreateAPIShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		set  func(*cserver)
		want string
	}{
		"repo 404 path":        {func(s *cserver) { s.repo = "not json" }, "repository:"},
		"repo without name":    {func(s *cserver) { s.repo = `{}` }, "no full_name"},
		"archived":             {func(s *cserver) { s.repo = `{"full_name":"o/r","archived":true,"default_branch":"main"}` }, "is archived"},
		"no default branch":    {func(s *cserver) { s.repo = `{"full_name":"o/r"}` }, "no default branch"},
		"compare not json":     {func(s *cserver) { s.compare = "nope" }, "compare:"},
		"compare no ahead_by":  {func(s *cserver) { s.compare = `{}` }, "compare:"},
		"pulls not json":       {func(s *cserver) { s.pulls = "nope" }, "open pull requests:"},
		"post refused":         {func(s *cserver) { s.createStatus = 422; s.created = `{"message":"Validation Failed"}` }, "Validation Failed"},
		"reply without number": {func(s *cserver) { s.created = `{}` }, "did not say which"},
		"reply without url":    {func(s *cserver) { s.created = `{"number":3}` }, "did not say which"},
		"reply not json":       {func(s *cserver) { s.created = `nope` }, "did not say which"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newCreateServer(t)
			tc.set(s)
			_, err := s.create(t, good(t)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCreateDraftNotHonouredIsAnError(t *testing.T) {
	s := newCreateServer(t)
	s.created = `{"number":3,"draft":false,"html_url":"https://github.com/o/r/pull/3"}`
	out, err := s.create(t, append(good(t), "--draft")...)
	if err == nil || !strings.Contains(err.Error(), "not a draft") || !strings.Contains(out, "created: o/r#3") {
		t.Fatalf("err %v\n%s", err, out)
	}
}

func TestCreateNetworkErrorsAtEachRead(t *testing.T) {
	// Closing the server turns every read into a transport error; one test per
	// stage needs the earlier stages to answer, so each stage is knocked out in turn.
	for _, stage := range []string{"/repos/o/r", "/compare/", "/pulls"} {
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.Contains(r.URL.Path, "/compare/") && stage != "/compare/":
				_, _ = io.WriteString(w, `{"ahead_by":1}`)
			case strings.HasSuffix(r.URL.Path, "/pulls") && stage != "/pulls":
				_, _ = io.WriteString(w, `[]`)
			case r.URL.Path == "/repos/o/r" && stage != "/repos/o/r":
				_, _ = io.WriteString(w, `{"full_name":"o/r","default_branch":"main"}`)
			default:
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, "boom\nsecond line")
			}
		}))
		cmd := RootCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(append([]string{"o/r", "create", "--base", srv.URL, "--token-file", tokenFile(t, "x")}, good(t)...))
		err := cmd.Execute()
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "boom") || strings.Contains(err.Error(), "second line") {
			t.Errorf("%s: err %v", stage, err)
		}
	}
}

func TestCreatePlainHTTPRefused(t *testing.T) {
	cmd := RootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(append([]string{"o/r", "create", "--base", "http://example.com", "--token-file", tokenFile(t, "x")}, good(t)...))
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "must be https") {
		t.Fatalf("err %v", err)
	}
}

func TestCreateTokenFileMustExist(t *testing.T) {
	s := newCreateServer(t)
	cmd := RootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(append([]string{"o/r", "create", "--base", s.URL, "--token-file", "/nonexistent/tok"}, good(t)...))
	if err := cmd.Execute(); err == nil || len(s.methods) != 0 {
		t.Fatalf("err %v, requests %v", err, s.methods)
	}
}

func TestArgumentShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"repo alone":             {[]string{"o/r"}, "want `<owner/repo> create`"},
		"number without a verb":  {[]string{"o/r", "7"}, "want `<owner/repo> create`"},
		"bad repo":               {[]string{"nope", "create"}, "want owner/repo"},
		"create flags on a verb": {[]string{"o/r", "7", "close", "--head", "x"}, "apply to create"},
		"create with a number":   {[]string{"o/r", "7", "create"}, "want edit, close"},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := RootCmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
		})
	}
}

func TestEditIsPrivacyGatedToo(t *testing.T) {
	s := newServer(t)
	_, err := exec(t, s, "o/r", "7", "edit", "--body-file", bodyFile(t, "see `/Users/alicia/Documents/thesis/main.tex`\n"))
	if err == nil || !strings.Contains(err.Error(), "own file system") || s.writes() != 0 {
		t.Fatalf("err %v", err)
	}
}

func TestEscapeRefKeepsSlashes(t *testing.T) {
	if got := escapeRef("feat/a b#c"); got != "feat/a%20b%23c" {
		t.Errorf("got %q", got)
	}
}
