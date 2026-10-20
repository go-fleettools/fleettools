package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	good  = "1111111111111111111111111111111111111111"
	other = "2222222222222222222222222222222222222222"
)

// fakeGitLab answers GET with the merge request it is given and records every
// PUT, so a test can say "this must NOT have merged".
type fakeGitLab struct {
	mr     map[string]any
	puts   []string
	tokens []string
}

func (f *fakeGitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.tokens = append(f.tokens, r.Header.Get("PRIVATE-TOKEN"))
	if r.Method == http.MethodPut {
		f.puts = append(f.puts, r.URL.RequestURI())
		json.NewEncoder(w).Encode(map[string]any{"state": "merged", "merge_commit_sha": "abc"})
		return
	}
	json.NewEncoder(w).Encode(f.mr)
}

func setup(t *testing.T, mr map[string]any) (*fakeGitLab, string) {
	t.Helper()
	f := &fakeGitLab{mr: mr}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	old := apiBase
	apiBase = func(string) string { return srv.URL + "/api/v4" }
	t.Cleanup(func() { apiBase = old })
	sleep = func(time.Duration) {}
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("glpat-secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f, tok
}

func runCmd(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	c := RootCmd()
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	code := execute(c)
	return out.String(), code
}

func open(sha, pipeStatus, pipeSHA string) map[string]any {
	mr := map[string]any{"state": "opened", "detailed_merge_status": "mergeable", "sha": sha, "target_branch": "main"}
	if pipeStatus != "" {
		mr["head_pipeline"] = map[string]any{"status": pipeStatus, "sha": pipeSHA}
	}
	return mr
}

func TestMergesWhenEverythingHolds(t *testing.T) {
	f, tok := setup(t, open(good, "success", good))
	out, code := runCmd(t, "-f", tok, "g/p", "7", good)
	if code != 0 || len(f.puts) != 1 {
		t.Fatalf("exit %d, %d merges:\n%s", code, len(f.puts), out)
	}
	if !strings.Contains(f.puts[0], "sha="+good) || !strings.Contains(f.puts[0], "squash=false") {
		t.Errorf("merge not pinned: %s", f.puts[0])
	}
	if !strings.HasPrefix(f.puts[0], "/api/v4/projects/g%2Fp/merge_requests/7/merge") {
		t.Errorf("project path not escaped: %s", f.puts[0])
	}
	for _, tk := range f.tokens {
		if tk != "glpat-secret-value" {
			t.Errorf("token sent as %q", tk)
		}
	}
	if strings.Contains(out, "secret") {
		t.Error("the token appeared in the output")
	}
}

// Every refusal must leave GitLab unmerged.
func TestRefusesAndDoesNotMerge(t *testing.T) {
	for name, mr := range map[string]map[string]any{
		"another head":       open(other, "success", other),
		"pipeline failed":    open(good, "failed", good),
		"pipeline elsewhere": open(good, "success", other),
		"no pipeline":        open(good, "", ""),
		"closed":             {"state": "closed", "detailed_merge_status": "mergeable", "sha": good},
		"conflict":           {"state": "opened", "detailed_merge_status": "conflict", "sha": good},
	} {
		f, tok := setup(t, mr)
		if _, code := runCmd(t, "-f", tok, "g/p", "7", good); code != 1 {
			t.Errorf("%s: exit %d, want 1", name, code)
		}
		if len(f.puts) != 0 {
			t.Errorf("%s: merged anyway", name)
		}
	}
}

func TestNoPipelineIsExplicit(t *testing.T) {
	f, tok := setup(t, open(good, "", ""))
	out, code := runCmd(t, "-f", tok, "--no-pipeline", "g/p", "7", good)
	if code != 0 || len(f.puts) != 1 || !strings.Contains(out, "no pipeline required") {
		t.Errorf("exit %d, %d merges:\n%s", code, len(f.puts), out)
	}
}

func TestUsageErrors(t *testing.T) {
	_, tok := setup(t, open(good, "success", good))
	for _, args := range [][]string{
		{"g/p", "7", good},             // no token file
		{"-f", tok, "g/p", "7", "abc"}, // short sha
		{"-f", tok, "g/p", "7"},        // missing argument
	} {
		if _, code := runCmd(t, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if _, code := runCmd(t, "-h"); code != 0 {
		t.Errorf("-h: exit %d, want 0", code)
	}
}
