package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-fleettools/fleettools/internal/cli"
)

const (
	secret = "ghs_SECRETSECRET"
	sha    = "aaaaaaa1111111111111111111111111111111aa"
	other  = "bbbbbbb2222222222222222222222222222222bb"
)

// fake is a Pages API. builds is newest first; onPost says what a POST adds.
type fake struct {
	mu     sync.Mutex
	builds []map[string]any
	posts  int
	onPost func(n int) map[string]any
	lists  int
	// afterLists: each list call i (0-based) may reveal a status change.
	onList func(f *fake, i int)
}

func b(commit, status, url string) map[string]any {
	return map[string]any{"commit": commit, "status": status, "url": url,
		"error": map[string]any{"message": nil}}
}

func (f *fake) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(401)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/o/r/pages/builds":
			if f.onList != nil {
				f.onList(f, f.lists)
			}
			f.lists++
			json.NewEncoder(w).Encode(f.builds)
		case r.Method == "POST" && r.URL.Path == "/repos/o/r/pages/builds":
			f.posts++
			if f.onPost != nil {
				f.builds = append([]map[string]any{f.onPost(f.posts)}, f.builds...)
			}
			w.WriteHeader(201)
			w.Write([]byte(`{"url":"x","status":"queued"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	})
}

func drive(t *testing.T, f *fake, extra ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"--api-url", srv.URL, "--token-file", tok, "--poll", "1ms",
		"--grace", "40ms", "--timeout", "80ms"}, extra...)
	args = append(args, "o/r", sha)
	var o, e bytes.Buffer
	code := cli.Drive(RootCmd(), args, &o, &e)
	if strings.Contains(o.String()+e.String(), secret) {
		t.Fatalf("token reached the output:\n%s%s", o.String(), e.String())
	}
	return code, o.String(), e.String()
}

func TestBuiltFirstTime(t *testing.T) {
	f := &fake{builds: []map[string]any{b(sha, "building", "u2")}}
	f.onList = func(f *fake, i int) {
		if i == 2 {
			f.builds[0]["status"] = "built"
		}
	}
	code, out, e := drive(t, f)
	if code != 0 || f.posts != 0 {
		t.Fatalf("code %d posts %d\n%s%s", code, f.posts, out, e)
	}
}

func TestErroredThenBuiltAfterPost(t *testing.T) {
	f := &fake{builds: []map[string]any{b(sha, "errored", "u-old")}}
	f.onPost = func(n int) map[string]any { return b(sha, "built", "u-new") }
	code, out, e := drive(t, f)
	if code != 0 || f.posts != 1 {
		t.Fatalf("code %d posts %d\n%s%s", code, f.posts, out, e)
	}
	if !strings.Contains(out, "requested a new build") {
		t.Fatalf("no request reported:\n%s", out)
	}
}

func TestStillErroredAfterRetriesIsNonZero(t *testing.T) {
	f := &fake{builds: []map[string]any{b(sha, "errored", "u0")}}
	f.onPost = func(n int) map[string]any { return b(sha, "errored", "u"+string(rune('0'+n))) }
	code, out, e := drive(t, f, "--retries", "2")
	if code != 1 || f.posts != 2 {
		t.Fatalf("code %d posts %d\n%s%s", code, f.posts, out, e)
	}
	if !strings.Contains(out, "was not built") {
		t.Fatalf("no reason in the output: %q", out)
	}
}

func TestUnrelatedCommitBuildsAreIgnored(t *testing.T) {
	// Another commit is built; ours never appears. That is NOT a pass.
	f := &fake{builds: []map[string]any{b(other, "built", "u1")}}
	code, out, e := drive(t, f, "--retries", "1")
	if code != 1 || f.posts != 1 {
		t.Fatalf("code %d posts %d\n%s%s", code, f.posts, out, e)
	}
	// And an unrelated errored build must not trigger a POST by itself when
	// ours is building and then built.
	g := &fake{builds: []map[string]any{b(sha, "building", "u3"), b(other, "errored", "u2")}}
	g.onList = func(f *fake, i int) {
		if i == 1 {
			f.builds[0]["status"] = "built"
		}
	}
	if code, out, e := drive(t, g); code != 0 || g.posts != 0 {
		t.Fatalf("code %d posts %d\n%s%s", code, g.posts, out, e)
	}
}

func TestStalledBuildIsRequestedAgain(t *testing.T) {
	f := &fake{builds: []map[string]any{b(sha, "building", "u0")}}
	f.onPost = func(n int) map[string]any { return b(sha, "built", "u1") }
	code, out, e := drive(t, f)
	if code != 0 || f.posts != 1 {
		t.Fatalf("code %d posts %d\n%s%s", code, f.posts, out, e)
	}
}

func TestSetupRefusals(t *testing.T) {
	for _, args := range [][]string{
		{"o/r"}, {"nope", sha}, {"o/r", "XYZ"}, {"o/r", sha}, // last: no token file
	} {
		var o, e bytes.Buffer
		if c := cli.Drive(RootCmd(), args, &o, &e); c != 2 {
			t.Errorf("%v: exit %d, want 2 (%s)", args, c, e.String())
		}
	}
	var o, e bytes.Buffer
	empty := filepath.Join(t.TempDir(), "empty")
	os.WriteFile(empty, nil, 0o600)
	if c := cli.Drive(RootCmd(), []string{"--token-file", empty, "o/r", sha}, &o, &e); c != 2 {
		t.Errorf("empty token file: %d", c)
	}
}

func TestAPIFailureIsNotAPass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer srv.Close()
	tok := filepath.Join(t.TempDir(), "t")
	os.WriteFile(tok, []byte(secret), 0o600)
	var o, e bytes.Buffer
	if c := cli.Drive(RootCmd(), []string{"--api-url", srv.URL, "--token-file", tok, "o/r", sha}, &o, &e); c != 2 {
		t.Errorf("403 on list: exit %d", c)
	}
}
