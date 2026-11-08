package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

func tokenFile(t *testing.T, tok string) string { return writeMode(t, tok, 0o600) }

func writeMode(t *testing.T, s string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

type server struct {
	*httptest.Server
	auth, methods, paths, bodies []string
	pub, priv                    *[32]byte
	fork                         bool
	archived                     bool
	secretStatus                 int
	deleted                      bool
	getStatus                    int
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{fork: true, secretStatus: http.StatusCreated}
	s.pub, s.priv, _ = box.GenerateKey(rand.Reader)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if s.getStatus != 0 && r.Method == http.MethodGet {
			w.Header().Set("Location", "https://api.github.com/repositories/1")
			w.WriteHeader(s.getStatus)
			_, _ = io.WriteString(w, `{"message":"Moved Permanently"}`)
			return
		}
		switch r.Method {
		case http.MethodDelete:
			s.deleted = true
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodPatch:
			var in map[string]any
			_ = json.Unmarshal([]byte(s.bodies[len(s.bodies)-1]), &in)
			if a, ok := in["archived"].(bool); ok {
				s.archived = a
			}
			name := "r"
			if n, ok := in["name"].(string); ok {
				name = n
			}
			desc, _ := in["description"].(string)
			hp, _ := in["homepage"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{"full_name": "o/" + name, "html_url": "https://github.com/o/" + name,
				"archived": s.archived, "description": desc, "homepage": hp})
			return
		}
		if s.deleted {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"full_name":"o/r","html_url":"https://github.com/o/r","archived":`+boolS(s.archived)+`,"fork":`+boolS(s.fork)+`,"default_branch":"main","parent":{"full_name":"up/r"},"topics":["a"]}`)
	})
	mux.HandleFunc("/repos/o/r/forks", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"full_name":"org2/fk","html_url":"https://github.com/org2/fk"}`)
	})
	mux.HandleFunc("/repos/o/r/topics", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		_, _ = io.WriteString(w, s.bodies[len(s.bodies)-1])
	})
	mux.HandleFunc("/repos/o/r/merge-upstream", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		_, _ = io.WriteString(w, `{"message":"Successfully fetched and fast-forwarded","merge_type":"fast-forward","base_branch":"main"}`)
	})
	mux.HandleFunc("/repos/o/r/actions/secrets/public-key", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		_, _ = io.WriteString(w, `{"key_id":"K1","key":"`+base64.StdEncoding.EncodeToString(s.pub[:])+`"}`)
	})
	mux.HandleFunc("/repos/o/r/actions/secrets/MY_SECRET", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(s.secretStatus)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func boolS(b bool) string {
	if b {
		return "true"
	}
	return "false"
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

func exec(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := RootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func (s *server) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return exec(t, "", append([]string{"--base", s.URL, "--token-file", tokenFile(t, "SEKRIT")}, args...)...)
}

func last(s *server) string { return s.bodies[len(s.bodies)-1] }

func TestShowsRepoFirstAndAuthOnlyInHeader(t *testing.T) {
	s := newServer(t)
	out, err := s.run(t, "o/r", "sync")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "o/r  https://github.com/o/r\n  archived=false fork=true default_branch=main parent=up/r") {
		t.Errorf("repo not shown first:\n%s", out)
	}
	for i, a := range s.auth {
		if a != "Bearer SEKRIT" {
			t.Errorf("request %d auth %q", i, a)
		}
		if strings.Contains(s.paths[i]+s.bodies[i], "SEKRIT") {
			t.Error("token leaked into path or body")
		}
	}
}

func TestFork(t *testing.T) {
	s := newServer(t)
	out, err := s.run(t, "o/r", "fork", "--org", "org2", "--name", "fk")
	if err != nil {
		t.Fatal(err)
	}
	if s.methods[1] != "POST" || s.paths[1] != "/repos/o/r/forks" || last(s) != `{"name":"fk","organization":"org2"}` {
		t.Errorf("sent %v %v %q", s.methods, s.paths, last(s))
	}
	if !strings.Contains(out, "org2/fk") {
		t.Errorf("out: %s", out)
	}
}

func TestEditDescriptionHomepageTopics(t *testing.T) {
	s := newServer(t)
	df := writeMode(t, "a \"quoted\" line\n", 0o644)
	out, err := s.run(t, "o/r", "edit", "--description-file", df, "--homepage", "https://x.io", "--topic", "go", "--topic", "cli")
	if err != nil {
		t.Fatal(err)
	}
	if s.methods[1] != "PATCH" || s.bodies[1] != `{"description":"a \"quoted\" line","homepage":"https://x.io"}` {
		t.Errorf("patch: %v %q", s.methods, s.bodies)
	}
	if s.methods[2] != "PUT" || s.paths[2] != "/repos/o/r/topics" || s.bodies[2] != `{"names":["go","cli"]}` {
		t.Errorf("topics: %v %q", s.paths, s.bodies)
	}
	if !strings.Contains(out, "topics: [go cli]") {
		t.Errorf("out: %s", out)
	}
}

func TestEditRefusals(t *testing.T) {
	s := newServer(t)
	for _, a := range [][]string{
		{"o/r", "edit"},
		{"o/r", "edit", "--description", "x", "--description-file", "f"},
		{"o/r", "edit", "--topic", "Bad Topic"},
	} {
		if _, err := s.run(t, a...); err == nil {
			t.Errorf("%v accepted", a)
		}
	}
	if len(s.methods) != 0 {
		t.Errorf("refusals reached the API: %v", s.methods)
	}
}

func TestEditEmptyTopicsClears(t *testing.T) {
	s := newServer(t)
	// --topic "" is refused: clearing is not an accident away.
	if _, err := s.run(t, "o/r", "edit", "--topic", ""); err == nil {
		t.Fatal("empty topic accepted")
	}
}

func TestRenameArchiveDeleteNeedConfirm(t *testing.T) {
	s := newServer(t)
	for _, a := range [][]string{
		{"o/r", "rename", "n2"},
		{"o/r", "rename", "n2", "--confirm", "o/other"},
		{"o/r", "archive"},
		{"o/r", "archive", "--confirm", "O/R"},
		{"o/r", "delete"},
		{"o/r", "delete", "--confirm", "o/r "},
		{"o/r", "sync", "--confirm", "o/r"},
	} {
		if _, err := s.run(t, a...); err == nil {
			t.Errorf("%v accepted", a)
		}
	}
	if len(s.methods) != 0 {
		t.Errorf("refusals reached the API: %v", s.methods)
	}
}

func TestRename(t *testing.T) {
	s := newServer(t)
	out, err := s.run(t, "o/r", "rename", "n2", "--confirm", "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if s.methods[1] != "PATCH" || s.bodies[1] != `{"name":"n2"}` || !strings.Contains(out, "renamed: o/n2") {
		t.Errorf("%v %q\n%s", s.methods, s.bodies, out)
	}
}

func TestArchive(t *testing.T) {
	s := newServer(t)
	out, err := s.run(t, "o/r", "archive", "--confirm", "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if s.bodies[1] != `{"archived":true}` || !strings.Contains(out, "archived: true") {
		t.Errorf("%q\n%s", s.bodies, out)
	}
	if _, err := s.run(t, "o/r", "archive", "--confirm", "o/r"); err == nil || !strings.Contains(err.Error(), "already archived") {
		t.Errorf("second archive: %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := newServer(t)
	out, err := s.run(t, "o/r", "delete", "--confirm", "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if s.methods[1] != "DELETE" || !strings.Contains(out, "now answers 404") {
		t.Errorf("%v\n%s", s.methods, out)
	}
}

func TestSync(t *testing.T) {
	s := newServer(t)
	out, err := s.run(t, "o/r", "sync")
	if err != nil {
		t.Fatal(err)
	}
	if s.paths[1] != "/repos/o/r/merge-upstream" || s.bodies[1] != `{"branch":"main"}` || !strings.Contains(out, "merge_type=fast-forward") {
		t.Errorf("%v %q\n%s", s.paths, s.bodies, out)
	}
	if _, err := s.run(t, "o/r", "sync", "--branch", "dev"); err != nil || last(s) != `{"branch":"dev"}` {
		t.Errorf("branch: %v %q", err, last(s))
	}
	s.fork = false
	if _, err := s.run(t, "o/r", "sync"); err == nil || !strings.Contains(err.Error(), "not a fork") {
		t.Errorf("non-fork: %v", err)
	}
}

func TestSecretSealedAndNeverLeaks(t *testing.T) {
	s := newServer(t)
	const val = "ghp_TOPSECRETVALUE"
	vf := writeMode(t, val+"\n", 0o600)
	out, err := s.run(t, "o/r", "secret-set", "MY_SECRET", "--value-file", vf)
	if err != nil {
		t.Fatal(err)
	}
	n := len(s.methods) - 1
	if s.methods[n] != "PUT" || s.paths[n] != "/repos/o/r/actions/secrets/MY_SECRET" {
		t.Fatalf("%v %v", s.methods, s.paths)
	}
	var in map[string]string
	if err := json.Unmarshal([]byte(s.bodies[n]), &in); err != nil {
		t.Fatal(err)
	}
	if in["key_id"] != "K1" {
		t.Errorf("key_id %q", in["key_id"])
	}
	ct, _ := base64.StdEncoding.DecodeString(in["encrypted_value"])
	pt, ok := box.OpenAnonymous(nil, ct, s.pub, s.priv)
	if !ok || string(pt) != val {
		t.Errorf("sealed box does not open to the value: ok=%v %q", ok, pt)
	}
	if !strings.Contains(out, "created") {
		t.Errorf("out: %s", out)
	}
	for i := range s.paths {
		if strings.Contains(out+s.paths[i]+s.bodies[i], val) {
			t.Errorf("value leaked (request %d)", i)
		}
	}
	s.secretStatus = http.StatusNoContent
	if out, err := s.run(t, "o/r", "secret-set", "MY_SECRET", "--value-file", vf); err != nil || !strings.Contains(out, "updated") {
		t.Errorf("update: %v %s", err, out)
	}
}

func TestSecretStdin(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, "fromstdin\n", "--base", s.URL, "--token-file", tokenFile(t, "T"), "o/r", "secret-set", "MY_SECRET", "--value-stdin")
	if err != nil {
		t.Fatal(err)
	}
	var in map[string]string
	_ = json.Unmarshal([]byte(last(s)), &in)
	ct, _ := base64.StdEncoding.DecodeString(in["encrypted_value"])
	if pt, ok := box.OpenAnonymous(nil, ct, s.pub, s.priv); !ok || string(pt) != "fromstdin" {
		t.Errorf("stdin value: %v %q", ok, pt)
	}
	if strings.Contains(out, "fromstdin") {
		t.Error("value printed")
	}
}

func TestSecretRefusals(t *testing.T) {
	s := newServer(t)
	open := writeMode(t, "v", 0o644)
	good := writeMode(t, "v", 0o600)
	empty := writeMode(t, "\n", 0o600)
	for _, a := range [][]string{
		{"o/r", "secret-set", "MY_SECRET", "--value-file", open},
		{"o/r", "secret-set", "MY_SECRET"},
		{"o/r", "secret-set", "MY_SECRET", "--value-file", good, "--value-stdin"},
		{"o/r", "secret-set", "MY_SECRET", "--value-file", empty},
		{"o/r", "secret-set", "GITHUB_X", "--value-file", good},
		{"o/r", "secret-set", "1BAD", "--value-file", good},
		{"o/r", "secret-set", "MY-SECRET", "--value-file", good},
		{"o/r", "secret-set", "MY_SECRET", "--value-file", filepath.Dir(good)},
	} {
		if _, err := s.run(t, a...); err == nil {
			t.Errorf("%v accepted", a)
		}
	}
	if len(s.methods) != 0 {
		t.Errorf("refusals reached the API: %v", s.methods)
	}
}

func TestDryRunSendsOnlyReads(t *testing.T) {
	s := newServer(t)
	vf := writeMode(t, "v", 0o600)
	df := writeMode(t, "d", 0o600)
	for _, a := range [][]string{
		{"o/r", "fork", "--org", "x"},
		{"o/r", "edit", "--description-file", df, "--topic", "a"},
		{"o/r", "rename", "n", "--confirm", "o/r"},
		{"o/r", "archive", "--confirm", "o/r"},
		{"o/r", "delete", "--confirm", "o/r"},
		{"o/r", "sync"},
		{"o/r", "secret-set", "MY_SECRET", "--value-file", vf},
	} {
		if _, err := s.run(t, append(a, "--dry-run")...); err != nil {
			t.Errorf("%v: %v", a, err)
		}
	}
	if s.writes() != 0 {
		t.Errorf("dry-run wrote: %v", s.methods)
	}
}

func TestRenamedRepoIsReportedNotFollowed(t *testing.T) {
	s := newServer(t)
	s.getStatus = http.StatusMovedPermanently
	_, err := s.run(t, "o/r", "sync")
	if err == nil || !strings.Contains(err.Error(), "301") || !strings.Contains(err.Error(), "repositories/1") {
		t.Errorf("err: %v", err)
	}
	if len(s.methods) != 1 {
		t.Errorf("followed: %v", s.methods)
	}
}

func TestErrorsQuoteTheAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"Must have admin rights"}`)
	}))
	defer srv.Close()
	_, err := exec(t, "", "--base", srv.URL, "--token-file", tokenFile(t, "T"), "o/r", "sync")
	if err == nil || !strings.Contains(err.Error(), "Must have admin rights") {
		t.Errorf("err: %v", err)
	}
}

func TestLocalRefusals(t *testing.T) {
	s := newServer(t)
	for _, a := range [][]string{
		{"o", "sync"},
		{"o/r", "frobnicate"},
		{"o/r", "sync", "extra"},
		{"o/r", "rename", "a/b", "--confirm", "o/r"},
		{"o/r", "sync", "--org", "x"},
		{"o/r", "fork", "--name", "a b"},
	} {
		if _, err := s.run(t, a...); err == nil {
			t.Errorf("%v accepted", a)
		}
	}
	if _, err := exec(t, "", "--base", "http://example.com", "--token-file", tokenFile(t, "T"), "o/r", "sync"); err == nil {
		t.Error("plain http base accepted")
	}
	if _, err := exec(t, "", "--base", s.URL, "--token-file", writeMode(t, "T", 0o644), "o/r", "sync"); err == nil {
		t.Error("world-readable token accepted")
	}
	if len(s.methods) != 0 {
		t.Errorf("refusals reached the API: %v", s.methods)
	}
}
