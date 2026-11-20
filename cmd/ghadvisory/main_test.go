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

const ghsa = "GHSA-c2f3-h4j5-m6pq"

type server struct {
	*httptest.Server
	tags    map[string]bool
	state   string
	patched string
	reqs    []string
	bodies  []string
	auth    []string
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{tags: map[string]bool{"v0.3.4": true}, state: "draft", patched: "0.3.4"}
	mux := http.NewServeMux()
	adv := func() string {
		return `{"ghsa_id":"` + ghsa + `","html_url":"https://github.com/o/r/security/advisories/` + ghsa +
			`","state":"` + s.state + `","summary":"sum","severity":"high","vulnerabilities":[{"package":{"ecosystem":"go","name":"m"},"vulnerable_version_range":"< 0.3.4","patched_versions":"` + s.patched + `"}]}`
	}
	mux.HandleFunc("/repos/o/r/security-advisories", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		}
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "["+adv()+"]")
			return
		}
		_, _ = io.WriteString(w, adv())
	})
	mux.HandleFunc("/repos/o/r/security-advisories/"+ghsa, func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if r.Method == http.MethodPatch {
			s.state = "published"
		}
		_, _ = io.WriteString(w, adv())
	})
	mux.HandleFunc("/repos/o/r/git/ref/tags/", func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		tag := strings.TrimPrefix(r.URL.Path, "/repos/o/r/git/ref/tags/")
		if !s.tags[tag] {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"ref":"refs/tags/`+tag+`"}`)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *server) record(r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	s.reqs = append(s.reqs, r.Method+" "+r.URL.Path)
	s.bodies = append(s.bodies, string(b))
	s.auth = append(s.auth, r.Header.Get("Authorization"))
}

func (s *server) wrote() bool {
	for _, q := range s.reqs {
		if !strings.HasPrefix(q, "GET ") {
			return true
		}
	}
	return false
}

func file(t *testing.T, name, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return p
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

func newArgs(t *testing.T, s *server, extra ...string) []string {
	return append([]string{"o/r", "--new", "--base", s.URL,
		"--token-file", file(t, "tok", "SEKRIT", 0o600),
		"--summary", "acceptor trusts the realm", "--body-file", file(t, "b.md", "The acceptor took the realm from the authenticator.", 0o644),
		"--severity", "high", "--cwe", "CWE-290", "--package", "go:github.com/go-authn/krb5",
		"--affected", "< v0.3.4", "--patched", "v0.3.4", "--credit", "alice"}, extra...)
}

func TestNewCreatesADraftWithTheDocumentedFields(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, newArgs(t, s)...)
	if err != nil {
		t.Fatal(err, out)
	}
	if !strings.Contains(out, "created: "+ghsa+" [draft]") {
		t.Errorf("output: %s", out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(s.bodies[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got["severity"] != "high" || got["summary"] != "acceptor trusts the realm" || got["description"] == "" {
		t.Errorf("payload: %s", s.bodies[0])
	}
	v := got["vulnerabilities"].([]any)[0].(map[string]any)
	if v["vulnerable_version_range"] != "< 0.3.4" || v["patched_versions"] != "0.3.4" {
		t.Errorf("versions: %v", v)
	}
	if p := v["package"].(map[string]any); p["ecosystem"] != "go" || p["name"] != "github.com/go-authn/krb5" {
		t.Errorf("package: %v", p)
	}
	if got["cwe_ids"].([]any)[0] != "CWE-290" || got["credits"].([]any)[0].(map[string]any)["type"] != "reporter" {
		t.Errorf("cwe/credits: %s", s.bodies[0])
	}
	if strings.Contains(s.bodies[0], "state") {
		t.Errorf("a draft is the default; nothing may ask for publication: %s", s.bodies[0])
	}
}

func TestTokenOnlyInTheHeader(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, newArgs(t, s)...); err != nil {
		t.Fatal(err)
	}
	for i := range s.reqs {
		if s.auth[i] != "Bearer SEKRIT" || strings.Contains(s.reqs[i]+s.bodies[i], "SEKRIT") {
			t.Errorf("request %d: auth %q, line %q", i, s.auth[i], s.reqs[i])
		}
	}
}

func TestNewRefusesLocallyBeforeAnyRequest(t *testing.T) {
	cases := map[string][]string{
		"severity": {"--severity", "urgent"},
		"cwe":      {"--cwe", "290"},
		"package":  {"--package", "krb5"},
		"cve":      {"--cve-id", "CVE-1"},
		"credit":   {"--credit", "bob:boss"},
		"summary":  {"--summary", "two\nlines"},
	}
	for name, extra := range cases {
		s := newServer(t)
		if _, err := exec(t, newArgs(t, s, extra...)...); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if len(s.reqs) != 0 {
			t.Errorf("%s: a request left before the refusal: %v", name, s.reqs)
		}
	}
}

func TestBodyGoesThroughThePrivatePathGate(t *testing.T) {
	s := newServer(t)
	// A fixed path, as ghissue's own test uses: the machine's home directory is
	// not the same on CI, which is how this test first went red there.
	private := "## Measurement\n\n`/Users/alicia/Documents/fairshare-b300/fairshare-b300.tex`, 36 pages.\n"
	if _, err := exec(t, newArgs(t, s, "--body-file", file(t, "b.md", private, 0o644))...); err == nil {
		t.Error("a body naming a home directory was accepted")
	}
	if len(s.reqs) != 0 {
		t.Errorf("requests: %v", s.reqs)
	}
	ok := "`/Users/runner/.pkgx` is the runner's home directory.\n"
	if _, err := exec(t, newArgs(t, s, "--body-file", file(t, "b.md", ok, 0o644))...); err != nil {
		t.Errorf("a legitimate path was refused: %v", err)
	}
}

func TestDryRunSendsNothing(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, newArgs(t, s, "--dry-run")...)
	if err != nil || !strings.Contains(out, "NOT sent") || len(s.reqs) != 0 {
		t.Fatalf("%v %s %v", err, out, s.reqs)
	}
}

func pubArgs(t *testing.T, s *server, extra ...string) []string {
	return append([]string{"o/r", "publish", ghsa, "--base", s.URL,
		"--token-file", file(t, "tok", "SEKRIT", 0o600)}, extra...)
}

func TestPublishChecksTheTagThenPublishes(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, pubArgs(t, s)...)
	if err != nil {
		t.Fatal(err, out)
	}
	for _, want := range []string{ghsa + " [draft]", "released tag v0.3.4", "state: published"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	last := len(s.reqs) - 1
	if !strings.HasPrefix(s.reqs[last], "PATCH ") || s.bodies[last] != `{"state":"published"}` {
		t.Errorf("last request %s %s", s.reqs[last], s.bodies[last])
	}
}

func TestPublishRefusesAPatchedVersionThatIsNotATag(t *testing.T) {
	s := newServer(t)
	s.tags = map[string]bool{}
	out, err := exec(t, pubArgs(t, s)...)
	if err == nil || !strings.Contains(err.Error(), "not a released tag") {
		t.Fatalf("err %v\n%s", err, out)
	}
	if s.wrote() {
		t.Errorf("wrote anyway: %v", s.reqs)
	}
	if !strings.Contains(out, ghsa) {
		t.Errorf("the advisory was not printed first:\n%s", out)
	}
}

func TestPublishRefusesWhatIsNotADraft(t *testing.T) {
	s := newServer(t)
	s.state = "published"
	if _, err := exec(t, pubArgs(t, s)...); err == nil || s.wrote() {
		t.Fatalf("err %v reqs %v", err, s.reqs)
	}
}

func TestPublishRefusesNoPatchedVersion(t *testing.T) {
	s := newServer(t)
	s.patched = ""
	if _, err := exec(t, pubArgs(t, s)...); err == nil || s.wrote() {
		t.Fatalf("err %v reqs %v", err, s.reqs)
	}
}

func TestPublishDryRunWritesNothing(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, pubArgs(t, s, "--dry-run")...); err != nil || s.wrote() {
		t.Fatalf("err %v reqs %v", err, s.reqs)
	}
}

func TestPublishRefusesAMalformedId(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, "o/r", "publish", "GHSA-1", "--base", s.URL, "--token-file", file(t, "tok", "x", 0o600)); err == nil {
		t.Fatal("accepted")
	}
}

func TestList(t *testing.T) {
	s := newServer(t)
	out, err := exec(t, "o/r", "list", "--base", s.URL, "--token-file", file(t, "tok", "x", 0o600))
	if err != nil || !strings.Contains(out, ghsa+" [draft]") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestRefusesAWorldReadableTokenAndHTTP(t *testing.T) {
	s := newServer(t)
	if _, err := exec(t, "o/r", "list", "--base", s.URL, "--token-file", file(t, "tok", "x", 0o644)); err == nil {
		t.Error("a world-readable token file was accepted")
	}
	if _, err := exec(t, "o/r", "list", "--base", "http://example.com"); err == nil {
		t.Error("plain http was accepted")
	}
}

func TestNoV(t *testing.T) {
	for in, want := range map[string]string{"< v0.3.4": "< 0.3.4", "v0.3.4": "0.3.4", ">= 1.0, < v2.0.0": ">= 1.0, < 2.0.0", "0.3.4": "0.3.4"} {
		if got := noV(in); got != want {
			t.Errorf("noV(%q) = %q, want %q", in, got, want)
		}
	}
}
