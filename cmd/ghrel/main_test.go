package main

import (
	"encoding/json"
	"fmt"
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

func writeFile(t *testing.T, name, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

type rec struct {
	method, path, query, auth, ctype string
	clen                             int64
	body                             string
}

type server struct {
	*httptest.Server
	reqs     []rec
	deleted  bool
	assets   string // JSON array of existing assets
	uploadTo string // override upload_url
	badSize  bool
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{assets: `[{"id":5,"name":"old.txt","size":3,"state":"uploaded"}]`}
	mux := http.NewServeMux()
	log := func(r *http.Request) rec {
		b, _ := io.ReadAll(r.Body)
		x := rec{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.ContentLength, string(b)}
		s.reqs = append(s.reqs, x)
		return x
	}
	mux.HandleFunc("/repos/o/r/releases/tags/v1.0.0", func(w http.ResponseWriter, r *http.Request) {
		log(r)
		if s.deleted {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		up := s.URL + "/repos/o/r/releases/9/assets{?name,label}"
		if s.uploadTo != "" {
			up = s.uploadTo
		}
		fmt.Fprintf(w, `{"id":9,"tag_name":"v1.0.0","name":"One","body":"old","html_url":"https://github.com/o/r/releases/tag/v1.0.0","upload_url":%q,"assets":%s}`, up, s.assets)
	})
	mux.HandleFunc("/repos/o/r/releases/9", func(w http.ResponseWriter, r *http.Request) {
		x := log(r)
		if r.Method == http.MethodDelete {
			s.deleted = true
			w.WriteHeader(204)
			return
		}
		var in map[string]string
		_ = json.Unmarshal([]byte(x.body), &in)
		b, _ := json.Marshal(map[string]any{"id": 9, "name": in["name"], "body": in["body"]})
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/repos/o/r/releases/assets/5", func(w http.ResponseWriter, r *http.Request) {
		log(r)
		w.WriteHeader(204)
	})
	mux.HandleFunc("/repos/o/r/releases/9/assets", func(w http.ResponseWriter, r *http.Request) {
		x := log(r)
		size := int64(len(x.body))
		if s.badSize {
			size--
		}
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"id":77,"name":%q,"size":%d,"state":"uploaded"}`, r.URL.Query().Get("name"), size)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *server) writes() []string {
	var w []string
	for _, r := range s.reqs {
		if r.method != "GET" {
			w = append(w, r.method+" "+r.path)
		}
	}
	return w
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

func TestEditShowsReleaseThenPatchesAndChecksTheReply(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, "o/r", "v1.0.0", "edit", "--base", s.URL, "--token-file", tokenFile(t, "SEKRIT"),
		"--title", "Two", "--notes-file", writeFile(t, "n.md", "new notes\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `id 9  "One"`) || !strings.Contains(out, "asset 5  old.txt") || !strings.Contains(out, `edited: title "Two"`) {
		t.Errorf("output:\n%s", out)
	}
	if len(s.reqs) != 2 || s.reqs[0].method != "GET" || s.reqs[1].method != "PATCH" {
		t.Fatalf("requests %+v", s.reqs)
	}
	if s.reqs[1].body != `{"body":"new notes\n","name":"Two"}` {
		t.Errorf("PATCH body %q", s.reqs[1].body)
	}
	for _, r := range s.reqs {
		if r.auth != "Bearer SEKRIT" || strings.Contains(r.path+r.query+r.body, "SEKRIT") {
			t.Errorf("token misplaced in %+v", r)
		}
	}
}

func TestDryRunSendsOnlyReads(t *testing.T) {
	s := newServer(t)
	f := writeFile(t, "new.bin", "abcd")
	for _, args := range [][]string{
		{"o/r", "v1.0.0", "edit", "--title", "Two", "--dry-run"},
		{"o/r", "v1.0.0", "upload", f, "--dry-run"},
		{"o/r", "v1.0.0", "delete", "--confirm", "v1.0.0", "--dry-run"},
	} {
		if _, err := exec(t, append(args, "--base", s.URL, "--token-file", tokenFile(t, "x"))...); err != nil {
			t.Fatal(err)
		}
	}
	if w := s.writes(); len(w) != 0 {
		t.Errorf("dry run wrote: %v", w)
	}
}

func TestUploadStreamsWithLengthAndType(t *testing.T) {
	s := newServer(t)
	f := writeFile(t, "pkg.zip", "0123456789")
	out, err := exec(t, "o/r", "v1.0.0", "upload", f, "--base", s.URL, "--token-file", tokenFile(t, "SEKRIT"))
	if err != nil {
		t.Fatal(err)
	}
	up := s.reqs[1]
	if up.method != "POST" || up.path != "/repos/o/r/releases/9/assets" || up.query != "name=pkg.zip" {
		t.Errorf("upload request %+v", up)
	}
	if up.body != "0123456789" || up.clen != 10 || up.ctype != "application/zip" || up.auth != "Bearer SEKRIT" {
		t.Errorf("upload headers/body %+v", up)
	}
	if !strings.Contains(out, "uploaded: asset 77  pkg.zip  10 bytes  [uploaded]") {
		t.Errorf("output:\n%s", out)
	}
}

func TestUploadRefusesAnExistingNameWithoutClobber(t *testing.T) {
	s := newServer(t)
	f := filepath.Join(t.TempDir(), "old.txt")
	_ = os.WriteFile(f, []byte("xyz!"), 0o644)
	_, err := exec(t, "o/r", "v1.0.0", "upload", f, "--base", s.URL, "--token-file", tokenFile(t, "x"))
	if err == nil || !strings.Contains(err.Error(), "--clobber") {
		t.Fatalf("err = %v", err)
	}
	if w := s.writes(); len(w) != 0 {
		t.Errorf("wrote: %v", w)
	}
}

func TestClobberDeletesTheOldAssetFirst(t *testing.T) {
	s := newServer(t)
	f := filepath.Join(t.TempDir(), "old.txt")
	_ = os.WriteFile(f, []byte("xyz!"), 0o644)
	out, err := exec(t, "o/r", "v1.0.0", "upload", f, "--clobber", "--base", s.URL, "--token-file", tokenFile(t, "x"))
	if err != nil {
		t.Fatal(err)
	}
	w := s.writes()
	if len(w) != 2 || w[0] != "DELETE /repos/o/r/releases/assets/5" || w[1] != "POST /repos/o/r/releases/9/assets" {
		t.Errorf("writes %v", w)
	}
	if !strings.Contains(out, "clobber: deleted asset 5") {
		t.Errorf("output:\n%s", out)
	}
}

func TestUploadChecksTheSizeTheAPIReports(t *testing.T) {
	s := newServer(t)
	s.badSize = true
	_, err := exec(t, "o/r", "v1.0.0", "upload", writeFile(t, "a.bin", "abcd"), "--base", s.URL, "--token-file", tokenFile(t, "x"))
	if err == nil || !strings.Contains(err.Error(), "the API reports 3") {
		t.Errorf("err = %v", err)
	}
}

// The token must go only to uploads.github.com, whatever a reply names.
func TestUploadURLOnAnotherHostIsRefused(t *testing.T) {
	s := newServer(t)
	for _, u := range []string{"https://evil.example/x{?name}", "http://uploads.github.com/x", "https://uploads.github.com.evil.example/x"} {
		s.uploadTo = u
		_, err := exec(t, "o/r", "v1.0.0", "upload", writeFile(t, "a.bin", "abcd"), "--base", s.URL, "--token-file", tokenFile(t, "x"))
		if err == nil || !strings.Contains(err.Error(), "the token goes only to") {
			t.Errorf("%s: err = %v", u, err)
		}
	}
	if w := s.writes(); len(w) != 0 {
		t.Errorf("wrote: %v", w)
	}
}

func TestUploadBaseAcceptsTheRealHost(t *testing.T) {
	got, err := uploadBase("https://uploads.github.com/repos/o/r/releases/9/assets{?name,label}", apiBase)
	if err != nil || got != "https://uploads.github.com/repos/o/r/releases/9/assets" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestDeleteNeedsTheTagAndSaysTheTagStays(t *testing.T) {
	s := newServer(t)
	tf := tokenFile(t, "x")
	if _, err := exec(t, "o/r", "v1.0.0", "delete", "--base", s.URL, "--token-file", tf); err == nil {
		t.Error("delete without --confirm succeeded")
	}
	if _, err := exec(t, "o/r", "v1.0.0", "delete", "--confirm", "v1.0.1", "--base", s.URL, "--token-file", tf); err == nil {
		t.Error("delete with the wrong --confirm succeeded")
	}
	if len(s.reqs) != 0 {
		t.Errorf("requests before the confirmation: %+v", s.reqs)
	}
	out, err := exec(t, "o/r", "v1.0.0", "delete", "--confirm", "v1.0.0", "--base", s.URL, "--token-file", tf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "The git tag v1.0.0 was NOT touched") {
		t.Errorf("output:\n%s", out)
	}
	for _, w := range s.writes() {
		if strings.Contains(w, "git/") || strings.Contains(w, "tags") {
			t.Errorf("a tag endpoint was written: %s", w)
		}
	}
}

func TestRefusals(t *testing.T) {
	s := newServer(t)
	tf := tokenFile(t, "x")
	cases := map[string][]string{
		"create verb":     {"o/r", "v1.0.0", "create"},
		"edit nothing":    {"o/r", "v1.0.0", "edit"},
		"upload nothing":  {"o/r", "v1.0.0", "upload"},
		"missing file":    {"o/r", "v1.0.0", "upload", "/nonexistent/x"},
		"empty file":      {"o/r", "v1.0.0", "upload", writeFile(t, "e", "")},
		"clobber on edit": {"o/r", "v1.0.0", "edit", "--title", "t", "--clobber"},
		"confirm on edit": {"o/r", "v1.0.0", "edit", "--title", "t", "--confirm", "v1.0.0"},
		"files on delete": {"o/r", "v1.0.0", "delete", "--confirm", "v1.0.0", "x"},
		"two-line title":  {"o/r", "v1.0.0", "edit", "--title", "a\nb"},
		"bad tag":         {"o/r", "a/b", "edit", "--title", "t"},
		"empty notes":     {"o/r", "v1.0.0", "edit", "--notes-file", writeFile(t, "n", " \n")},
		"http base":       {"o/r", "v1.0.0", "edit", "--title", "t", "--base", "http://example.com"},
	}
	for name, a := range cases {
		if !strings.Contains(name, "base") {
			a = append(a, "--base", s.URL)
		}
		if _, err := exec(t, append(a, "--token-file", tf)...); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	if len(s.reqs) != 0 {
		t.Errorf("a refusal reached the server: %+v", s.reqs)
	}
}

func TestRefusesATokenFileAnyoneCanRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tok")
	_ = os.WriteFile(p, []byte("S"), 0o644)
	if _, err := exec(t, "o/r", "v1.0.0", "edit", "--title", "t", "--token-file", p); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Errorf("err = %v", err)
	}
}
